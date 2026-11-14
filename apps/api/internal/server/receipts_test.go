package server_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Receipts are Go-only (Rust has no receipt.*): fixtures come from Rust, every receipt.* call runs on the Go server.
func (b *bt) receipt(over map[string]any) map[string]any {
	b.t.Helper()
	in := map[string]any{"contact_id": b.contact, "amount_minor": 118_000, "supply_type": "services", "method": "upi", "reference": "UTR1"}
	for k, v := range over {
		in[k] = v
	}
	return b.ok("receipt.create", in)
}

// issuedInvoice is an issued invoice of one line (price before 18% GST) for the contact.
func (b *bt) issuedInvoice(contact string, price int64) map[string]any {
	d := b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": contact, "invoice_date": day(0),
		"lines": []any{map[string]any{"description": "Work", "hsn_sac": "998314", "unit_price_minor": price}}})["id"].(string)
	return b.issue(d)
}

func (b *bt) apply(w who, receipt, invoice string, amount int64) resp {
	return b.call(w, "receipt.apply", map[string]any{"id": receipt, "invoice_id": invoice, "amount_minor": amount})
}

func (b *bt) acct(code string) int64 { return balance(b.t, b.tb(), code) }

func TestReceiptTaxSplitMatchesInvoiceMath(t *testing.T) {
	b := newBT(t, false)
	for _, state := range []string{"29", "27"} { // intra-state CGST+SGST, inter-state IGST
		contact := b.contact
		if state != "29" {
			contact = b.newContact(state)
		}
		pv := b.ok("invoice.preview", map[string]any{"id": b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": contact,
			"lines": []any{map[string]any{"description": "Work", "unit_price_minor": 1_000_000}}})["id"]})
		r := b.receipt(map[string]any{"contact_id": contact, "amount_minor": 1_180_000})
		eq(t, r["taxable_minor"], pv["subtotal_minor"])
		for _, k := range []string{"cgst_minor", "sgst_minor", "igst_minor"} {
			eq(t, r[k], pv[k])
		}
		eq(t, r["place_of_supply"], state)
		eq(t, r["status"], "issued")
		eq(t, n(r["unapplied_minor"]), int64(1_180_000))
	}
	// rounding: tax = gross x rate / (100 + rate), CGST gets the floor half
	r := b.receipt(map[string]any{"amount_minor": 10_001, "gst_rate_bp": 500})
	eq(t, n(r["cgst_minor"])+n(r["sgst_minor"])+n(r["taxable_minor"]), int64(10_001))
	eq(t, n(r["taxable_minor"]), int64(9_525))
	eq(t, n(r["cgst_minor"]), int64(238))
	eq(t, n(r["sgst_minor"]), int64(238))
	// unknown rate is 18%; unknown place of supply (contact without state) is inter-state
	c := b.must(b.owner, "contact.create", map[string]any{"space_id": b.space, "name": "No state", "kind": "person"})["id"].(string)
	r = b.receipt(map[string]any{"contact_id": c, "amount_minor": 11_800})
	eq(t, r["gst_rate_bp"], float64(1800))
	eq(t, n(r["igst_minor"]), int64(1_800))
	eq(t, r["place_of_supply"], nil)
	// goods: no GST on an advance
	g := b.receipt(map[string]any{"supply_type": "goods", "amount_minor": 50_000})
	eq(t, n(g["taxable_minor"]), int64(50_000))
	eq(t, n(g["cgst_minor"])+n(g["sgst_minor"])+n(g["igst_minor"]), int64(0))
	b.assertTBZero()
}

func TestReceiptNumberingAndEvents(t *testing.T) {
	b := newBT(t, false)
	fy := fyOf(todayIST())
	r1, r2 := b.receipt(nil), b.receipt(nil)
	eq(t, r1["number"], "RV/"+fy+"/0001")
	eq(t, r2["number"], "RV/"+fy+"/0002")
	b.errIs("receipt.create", map[string]any{"contact_id": b.contact, "amount_minor": 0, "supply_type": "services"}, 400, "invalid")
	eq(t, b.receipt(nil)["number"], "RV/"+fy+"/0003") // refused calls burn no number
	eq(t, count(b.events(r1["id"].(string)), "receipt.issued"), 1)
	eq(t, b.ok("note.get", map[string]any{"id": r1["id"]})["types"], []any{"receipt_voucher"})
	eq(t, b.dbStrings(b.owner.ws, "select name from billing.sequences order by name"), []string{"receipt:" + fy})
}

func TestReceiptLedgerPostingsAndLazyAccounts(t *testing.T) {
	b := newBT(t, false)
	eq(t, len(b.tb()["accounts"].([]any)), 7) // no receipts: the chart is untouched
	r := b.receipt(map[string]any{"amount_minor": 1_180_000})
	eq(t, len(b.tb()["accounts"].([]any)), 9)
	eq(t, b.acct("1200"), int64(1_180_000))
	eq(t, b.acct("2300"), int64(-1_180_000))
	eq(t, b.acct("1310"), int64(180_000))
	eq(t, b.acct("2201"), -n(r["cgst_minor"]))
	eq(t, b.acct("2202"), -n(r["sgst_minor"]))
	eq(t, b.acct("2203"), int64(0))
	b.assertTBZero()
	b.receipt(map[string]any{"supply_type": "goods", "amount_minor": 1_000}) // goods: no GST legs
	eq(t, b.acct("1310"), int64(180_000))
	eq(t, b.acct("2300"), int64(-1_181_000))
	b.assertTBZero()
}

func TestReceiptAgainstAcceptedQuoteApplyToInvoiceKeepsLedgerBalanced(t *testing.T) {
	b := newBT(t, false)
	q := b.quote(b.contact, "")
	b.errIs("receipt.create", map[string]any{"contact_id": b.contact, "quotation_id": q, "amount_minor": 1_000, "supply_type": "services"}, 409, "conflict")
	b.q("quote.send", q)
	b.errIs("receipt.create", map[string]any{"contact_id": b.contact, "quotation_id": q, "amount_minor": 1_000, "supply_type": "services"}, 409, "conflict")
	b.q("quote.accept", q)
	other := b.newContact("27")
	b.errIs("receipt.create", map[string]any{"contact_id": other, "quotation_id": q, "amount_minor": 1_000, "supply_type": "services"}, 400, "invalid")
	r := b.receipt(map[string]any{"quotation_id": q, "amount_minor": 38_550})
	eq(t, r["quotation_id"], q)
	rid := r["id"].(string)

	inv := b.q("quote.convert_to_invoice", q)["invoice"].(map[string]any)
	b.errAs(b.owner, "receipt.apply", map[string]any{"id": rid, "invoice_id": inv["id"], "amount_minor": 1_000}, 409, "conflict") // still a draft
	issued := b.issue(inv["id"].(string))
	iid, total := issued["id"].(string), n(issued["total_minor"])

	cg, sg := n(r["cgst_minor"]), n(r["sgst_minor"])
	a := b.apply(b.owner, rid, iid, 10_000)
	eq(t, a.status, 200)
	res := a.json()
	rc, in := res["receipt"].(map[string]any), res["invoice"].(map[string]any)
	eq(t, rc["status"], "partly_applied")
	eq(t, n(rc["applied_minor"]), int64(10_000))
	eq(t, n(rc["unapplied_minor"]), int64(28_550))
	eq(t, in["status"], "partly_paid")
	eq(t, n(in["outstanding_minor"]), total-10_000)
	got := b.ok("invoice.get", map[string]any{"id": iid})
	eq(t, n(got["outstanding_minor"]), total-10_000)
	pay := got["payments"].([]any)[0].(map[string]any)
	eq(t, pay["method"], "advance")
	eq(t, pay["reference"], r["number"])
	// the share of GST released is proportional; bank is untouched by an application
	eq(t, b.acct("1200"), int64(38_550))
	eq(t, b.acct("2300"), int64(-28_550))
	eq(t, b.acct("1310"), cg+sg-cg*10_000/38_550-sg*10_000/38_550) // each tax part released rounded down
	b.assertTBZero()

	a = b.apply(b.owner, rid, iid, 28_550)
	eq(t, a.status, 200)
	rc = a.json()["receipt"].(map[string]any)
	eq(t, rc["status"], "applied")
	eq(t, len(rc["applications"].([]any)), 2)
	eq(t, b.acct("2300"), int64(0))
	eq(t, b.acct("1310"), int64(0)) // all the advance GST is released into the invoice's output tax
	b.assertTBZero()
	b.errIs("receipt.apply", map[string]any{"id": rid, "invoice_id": iid, "amount_minor": 1}, 400, "invalid")
	eq(t, count(b.events(rid), "receipt.applied"), 2)
	eq(t, count(b.events(iid), "payment.recorded"), 2)

	// an advance big enough to settle the invoice marks it paid and fires invoice.paid
	r2 := b.receipt(map[string]any{"amount_minor": total})
	eq(t, b.apply(b.owner, r2["id"].(string), iid, total-38_550).status, 200)
	eq(t, b.ok("invoice.get", map[string]any{"id": iid})["status"], "paid")
	eq(t, count(b.events(iid), "invoice.paid"), 1)
	b.errIs("receipt.apply", map[string]any{"id": r2["id"], "invoice_id": iid, "amount_minor": 1}, 409, "conflict") // invoice is settled
	b.assertTBZero()
}

func TestReceiptApplyLimitsAndCustomerMatch(t *testing.T) {
	b := newBT(t, false)
	r := b.receipt(map[string]any{"amount_minor": 50_000})
	rid := r["id"].(string)
	inv := b.issuedInvoice(b.contact, 1_000_000)
	iid := inv["id"].(string)
	for _, amt := range []int64{0, -5, 50_001} {
		eq(t, b.apply(b.owner, rid, iid, amt).status, 400) // zero, negative, more than the receipt
	}
	small := b.issuedInvoice(b.contact, 10_000)                            // total 11,800 with GST
	eq(t, b.apply(b.owner, rid, small["id"].(string), 11_801).status, 400) // more than the invoice
	eq(t, b.apply(b.owner, rid, small["id"].(string), 11_800).status, 200)
	// another customer's invoice
	other := b.issuedInvoice(b.newContact("27"), 10_000)
	eq(t, b.apply(b.owner, rid, other["id"].(string), 100).status, 400)
	// a credit note is not an invoice
	cn := b.ok("credit_note.create", map[string]any{"invoice_id": iid, "reason": "x", "lines": []any{map[string]any{"description": "x", "unit_price_minor": 100}}})
	eq(t, b.apply(b.owner, rid, cn["id"].(string), 10).status, 409)
	b.errIs("receipt.apply", map[string]any{"id": rid, "invoice_id": "nope", "amount_minor": 1}, 400, "invalid")
	b.errIs("receipt.apply", map[string]any{"id": rid}, 400, "invalid")
	eq(t, n(b.ok("receipt.get", map[string]any{"id": rid})["unapplied_minor"]), int64(38_200))
	b.assertTBZero()
}

func TestReceiptConcurrentApplyCannotOverApply(t *testing.T) {
	b := newBT(t, false)
	r := b.receipt(map[string]any{"amount_minor": 100_000})
	rid := r["id"].(string)
	a, c := b.issuedInvoice(b.contact, 1_000_000)["id"].(string), b.issuedInvoice(b.contact, 1_000_000)["id"].(string)
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := a
			if i%2 == 1 {
				target = c
			}
			codes[i] = b.apply(b.owner, rid, target, 60_000).status
		}()
	}
	wg.Wait()
	ok := 0
	for _, s := range codes {
		if s == 200 {
			ok++
		}
	}
	eq(t, ok, 1)
	eq(t, n(b.ok("receipt.get", map[string]any{"id": rid})["applied_minor"]), int64(60_000))
	// two receipts racing for one invoice cannot exceed what it owes
	small := b.issuedInvoice(b.contact, 10_000)["id"].(string) // 11,800
	r1, r2 := b.receipt(map[string]any{"amount_minor": 10_000})["id"].(string), b.receipt(map[string]any{"amount_minor": 10_000})["id"].(string)
	codes = make([]int, 2)
	for i, rr := range []string{r1, r2} {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i] = b.apply(b.owner, rr, small, 10_000).status }()
	}
	wg.Wait()
	eq(t, codes[0]+codes[1] == 200+400, true) // one wins, the other sees the reduced balance
	eq(t, n(b.ok("invoice.get", map[string]any{"id": small})["outstanding_minor"]), int64(1_800))
	b.assertTBZero()
}

func TestReceiptRefundOnlyUnappliedAndAdminOnly(t *testing.T) {
	b := newBT(t, false)
	admin := b.addMember(b.owner, b.signup("Ada", false), "admin")
	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	r := b.receipt(map[string]any{"amount_minor": 118_000})
	rid := r["id"].(string)
	tax := n(r["cgst_minor"]) + n(r["sgst_minor"])
	b.errAs(member, "receipt.refund", map[string]any{"id": rid}, 403, "forbidden")
	inv := b.issuedInvoice(b.contact, 1_000_000)
	eq(t, b.apply(b.owner, rid, inv["id"].(string), 59_000).status, 200)
	b.errAs(admin, "receipt.refund", map[string]any{"id": rid, "amount_minor": 59_001}, 400, "invalid") // applied part is not refundable
	b.errAs(admin, "receipt.refund", map[string]any{"id": rid, "amount_minor": 0}, 400, "invalid")

	rf := b.okAs(admin, "receipt.refund", map[string]any{"id": rid, "amount_minor": 20_000, "reference": "REFUND-1"})
	fy := fyOf(todayIST())
	eq(t, rf["kind"], "refund")
	eq(t, rf["number"], "RFV/"+fy+"/0001")
	eq(t, rf["original_receipt_id"], rid)
	eq(t, n(rf["amount_minor"]), int64(20_000))
	eq(t, n(rf["taxable_minor"])+n(rf["cgst_minor"])+n(rf["sgst_minor"])+n(rf["igst_minor"]), int64(20_000))
	got := b.ok("receipt.get", map[string]any{"id": rid})
	eq(t, got["status"], "partly_applied")
	eq(t, n(got["unapplied_minor"]), int64(39_000))
	eq(t, got["refunds"].([]any)[0].(map[string]any)["number"], rf["number"])
	eq(t, b.acct("1200"), int64(118_000-20_000))
	b.errAs(admin, "receipt.refund", map[string]any{"id": rf["id"]}, 409, "conflict") // a refund voucher is not refundable
	eq(t, b.apply(b.owner, rf["id"].(string), inv["id"].(string), 1).status, 409)
	b.assertTBZero()

	// the rest: default amount is everything unapplied; the GST of the advance is fully released
	b.okAs(admin, "receipt.refund", map[string]any{"id": rid})
	got = b.ok("receipt.get", map[string]any{"id": rid})
	eq(t, got["status"], "applied") // some applied, nothing left
	eq(t, n(got["unapplied_minor"]), int64(0))
	eq(t, b.acct("2300"), int64(0))
	eq(t, b.acct("1310"), int64(0))
	_ = tax
	b.errAs(admin, "receipt.refund", map[string]any{"id": rid}, 400, "invalid")
	eq(t, count(b.events(rid), "receipt.refunded"), 2)
	b.assertTBZero()

	whole := b.receipt(nil)
	b.okAs(admin, "receipt.refund", map[string]any{"id": whole["id"]})
	eq(t, b.ok("receipt.get", map[string]any{"id": whole["id"]})["status"], "refunded")
	b.assertTBZero()
}

func TestReceiptVouchersAreImmutable(t *testing.T) {
	b := newBT(t, false)
	r := b.receipt(nil)
	id := r["id"].(string)
	inv := b.issuedInvoice(b.contact, 10_000)
	b.apply(b.owner, id, inv["id"].(string), 100)
	for _, sql := range []string{
		fmt.Sprintf("update billing.receipts set amount_minor = 5 where note_id = '%s'", id),
		fmt.Sprintf("update billing.receipts set number = 'X' where note_id = '%s'", id),
		fmt.Sprintf("update billing.receipts set cgst_minor = 0, taxable_minor = taxable_minor + cgst_minor where note_id = '%s'", id),
		fmt.Sprintf("delete from billing.receipts where note_id = '%s'", id),
		"update billing.receipt_applications set amount_minor = 1",
		"delete from billing.receipt_applications",
		fmt.Sprintf("update billing.receipts set applied_minor = amount_minor + 1 where note_id = '%s'", id),
	} {
		if err := b.raw(sql); err == nil {
			t.Fatalf("allowed: %s", sql)
		}
	}
}

func TestReceiptGSTR1Tables11AAnd11B(t *testing.T) {
	b := newBT(t, false)
	prev, cur := day(-40)[:7], day(0)[:7]
	if g := b.ok("report.gstr1", map[string]any{"period": cur}); g["advances_11a"] != nil || g["advances_adjusted_11b"] != nil {
		t.Fatalf("no receipts yet: %v", g)
	}
	r := b.receipt(map[string]any{"amount_minor": 1_180_000, "received_at": day(-40)})
	b.receipt(map[string]any{"supply_type": "goods", "amount_minor": 77_000, "received_at": day(-40)}) // goods: not in 11A
	g := b.ok("report.gstr1", map[string]any{"period": prev})
	at := g["advances_11a"].([]any)
	eq(t, len(at), 1)
	pos := at[0].(map[string]any)
	eq(t, pos["pos"], "29")
	eq(t, pos["sply_ty"], "INTRA")
	it := pos["itms"].([]any)[0].(map[string]any)
	eq(t, it["rt"], float64(18))
	eq(t, it["ad_amt"], float64(10_000)) // rupees
	eq(t, it["camt"], float64(900))
	eq(t, it["samt"], float64(900))
	eq(t, it["iamt"], float64(0))
	if g["advances_adjusted_11b"] != nil {
		t.Fatalf("nothing adjusted yet: %v", g)
	}
	if g := b.ok("report.gstr1", map[string]any{"period": cur}); g["advances_11a"] != nil {
		t.Fatalf("11A is the month received: %v", g["advances_11a"])
	}
	// invoiced and applied today: 11B in this month, 11A stays in the earlier one
	inv := b.issuedInvoice(b.contact, 1_000_000)
	eq(t, b.apply(b.owner, r["id"].(string), inv["id"].(string), 590_000).status, 200)
	g = b.ok("report.gstr1", map[string]any{"period": cur})
	tx := g["advances_adjusted_11b"].([]any)[0].(map[string]any)["itms"].([]any)[0].(map[string]any)
	eq(t, tx["ad_amt"], float64(5_000))
	eq(t, tx["camt"], float64(450))
	eq(t, tx["samt"], float64(450))
	if g["advances_11a"] != nil {
		t.Fatalf("11A leaked into %s", cur)
	}
	// a refund voucher nets out of 11A in the month it is issued
	b.ok("receipt.refund", map[string]any{"id": r["id"], "amount_minor": 118_000})
	g = b.ok("report.gstr1", map[string]any{"period": cur})
	rf := g["advances_11a"].([]any)[0].(map[string]any)["itms"].([]any)[0].(map[string]any)
	eq(t, rf["ad_amt"], float64(-1_000))
	// inter-state advances report IGST under their own place of supply
	b.receipt(map[string]any{"contact_id": b.newContact("27"), "amount_minor": 11_800})
	for _, p := range b.ok("report.gstr1", map[string]any{"period": cur})["advances_11a"].([]any) {
		if p.(map[string]any)["pos"] == "27" {
			eq(t, p.(map[string]any)["sply_ty"], "INTER")
			eq(t, p.(map[string]any)["itms"].([]any)[0].(map[string]any)["iamt"], float64(18))
		}
	}
}

func TestReceiptRoleGatesAgentsAPIKeysAndValidation(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	in := map[string]any{"contact_id": b.contact, "amount_minor": 5_000, "supply_type": "services"}
	b.errAs(guest, "receipt.create", in, 403, "forbidden")
	rid := b.okAs(member, "receipt.create", in)["id"].(string)
	b.errAs(guest, "receipt.get", map[string]any{"id": rid}, 404, "not_found") // a guest outside the space cannot read it
	eq(t, len(b.list(guest, "receipt.list", map[string]any{"contact_id": b.contact})), 0)
	b.errAs(guest, "receipt.apply", map[string]any{"id": rid, "invoice_id": rid, "amount_minor": 1}, 403, "forbidden")
	b.errAs(guest, "receipt.refund", map[string]any{"id": rid}, 403, "forbidden")
	if r := b.call(who{}, "receipt.get", map[string]any{"id": rid}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}

	// agents and API keys: every money-moving command waits for a human; reads and nothing else go straight through
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_crm", "manage_invoices"}}})
	created := b.must(b.owner, "agent.create", map[string]any{"name": "Biller", "scopes": []string{"read", "manage_crm", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	inv := b.issuedInvoice(b.contact, 10_000)["id"].(string)
	for name, w := range map[string]who{"agent": agent, "key": b.apiKey(b.owner)} {
		before := len(b.list(b.owner, "receipt.list", map[string]any{}))
		r := b.okAs(w, "receipt.create", in)
		eq(t, r["confirmation_required"], true)
		eq(t, len(b.list(b.owner, "receipt.list", map[string]any{})), before) // nothing was issued
		for cmd, args := range map[string]map[string]any{
			"receipt.apply":  {"id": rid, "invoice_id": inv, "amount_minor": 10},
			"receipt.refund": {"id": rid},
		} {
			if x := b.call(w, cmd, args); x.status != 403 && !strings.Contains(string(x.body), "confirmation_required") {
				t.Fatalf("%s %s not gated: %d %s", name, cmd, x.status, x.body)
			}
		}
		eq(t, n(b.ok("receipt.get", map[string]any{"id": rid})["unapplied_minor"]), int64(5_000))
		b.okAs(w, "receipt.list", map[string]any{})
		if name == "agent" { // a confirmed replay issues exactly one receipt
			cid := r["confirmation_id"].(string)
			b.ok("confirmation.decide", map[string]any{"id": cid, "approve": true})
			x := b.cmd(b.gosv.URL, w, b.owner.ws, "receipt.create", in, map[string]string{"X-Confirmation-Id": cid})
			eq(t, x.status, 200)
			eq(t, len(b.list(b.owner, "receipt.list", map[string]any{})), before+1)
			if x := b.cmd(b.gosv.URL, w, b.owner.ws, "receipt.create", in, map[string]string{"X-Confirmation-Id": cid}); x.status < 400 {
				t.Fatalf("reused confirmation: %s", x.body)
			}
		}
	}

	// invalid input
	for _, bad := range []map[string]any{
		{"amount_minor": -1}, {"amount_minor": 0}, {"supply_type": "barter"}, {"supply_type": nil}, {"contact_id": nil},
		{"gst_rate_bp": 3000}, {"gst_rate_bp": -1}, {"received_at": day(1)}, {"received_at": "yesterday"}, {"place_of_supply": "00"},
		{"contact_id": "nope"}, {"quotation_id": "nope"}, {"method": strings.Repeat("x", 41)},
	} {
		x := map[string]any{}
		for k, v := range in {
			x[k] = v
		}
		for k, v := range bad {
			if v == nil {
				delete(x, k)
			} else {
				x[k] = v
			}
		}
		b.errIs("receipt.create", x, 400, "invalid")
	}
	b.errIs("receipt.get", map[string]any{"id": "nope"}, 400, "invalid")
	b.errIs("receipt.get", map[string]any{}, 400, "invalid")
	b.errIs("receipt.get", map[string]any{"id": "00000000-0000-0000-0000-000000000000"}, 404, "not_found")
	b.errIs("receipt.create", map[string]any{"contact_id": "00000000-0000-0000-0000-000000000000", "amount_minor": 1, "supply_type": "goods"}, 404, "not_found")
	// the workspace needs a GSTIN before any voucher
	w := b.env.signup("NoGST", true)
	sp := b.must(w, "space.create", map[string]any{"key": "FIN", "name": "Finance"})["id"].(string)
	c := b.must(w, "contact.create", map[string]any{"space_id": sp, "name": "C", "kind": "person"})["id"].(string)
	r := b.cmd(b.gosv.URL, w, w.ws, "receipt.create", map[string]any{"contact_id": c, "amount_minor": 100, "supply_type": "goods"}, nil)
	eq(t, r.status, 400)
}

func TestReceiptTenantIsolation(t *testing.T) {
	b := newBT(t, false)
	r := b.receipt(nil)
	rid := r["id"].(string)
	o := b.env.tenant(false)
	o.errIs("receipt.get", map[string]any{"id": rid}, 404, "not_found")
	o.errIs("receipt.apply", map[string]any{"id": rid, "invoice_id": rid, "amount_minor": 1}, 404, "not_found")
	o.errIs("receipt.refund", map[string]any{"id": rid}, 404, "not_found")
	eq(t, len(o.okList("receipt.list", map[string]any{})), 0)
	// another workspace's contact, quotation and invoice cannot be used
	o.errIs("receipt.create", map[string]any{"contact_id": b.contact, "amount_minor": 100, "supply_type": "goods"}, 404, "not_found")
	q := b.quote(b.contact, "")
	b.q("quote.send", q)
	b.q("quote.accept", q)
	o.errIs("receipt.create", map[string]any{"contact_id": o.contact, "quotation_id": q, "amount_minor": 100, "supply_type": "goods"}, 404, "not_found")
	foreignInv := o.issuedInvoice(o.contact, 10_000)["id"].(string)
	b.errIs("receipt.apply", map[string]any{"id": rid, "invoice_id": foreignInv, "amount_minor": 100}, 404, "not_found")
	if x := o.cmd(o.gosv.URL, o.owner, b.owner.ws, "receipt.get", map[string]any{"id": rid}, nil); x.status == 200 {
		t.Fatalf("foreign workspace path: %s", x.body)
	}
	eq(t, len(b.okList("receipt.list", map[string]any{})), 1)
	// RLS in the database itself
	eq(t, o.dbStrings(o.owner.ws, "select count(*)::text from billing.receipts"), []string{"0"})
}

func TestReceiptIdempotentReplay(t *testing.T) {
	b := newBT(t, false)
	h := map[string]string{"Idempotency-Key": "rv-" + rnd()}
	in := map[string]any{"contact_id": b.contact, "amount_minor": 5_000, "supply_type": "services"}
	r1 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "receipt.create", in, h)
	r2 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "receipt.create", in, h)
	if r1.status != 200 || string(r1.body) != string(r2.body) {
		t.Fatalf("replay: %d %s / %d %s", r1.status, r1.body, r2.status, r2.body)
	}
	eq(t, len(b.okList("receipt.list", map[string]any{})), 1)
	id := r1.json()["id"].(string)
	inv := b.issuedInvoice(b.contact, 10_000)["id"].(string)
	h = map[string]string{"Idempotency-Key": "ap-" + rnd()}
	ap := map[string]any{"id": id, "invoice_id": inv, "amount_minor": 1_000}
	a1 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "receipt.apply", ap, h)
	a2 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "receipt.apply", ap, h)
	if a1.status != 200 || string(a1.body) != string(a2.body) {
		t.Fatalf("replay: %d %s / %d %s", a1.status, a1.body, a2.status, a2.body)
	}
	eq(t, n(b.ok("receipt.get", map[string]any{"id": id})["applied_minor"]), int64(1_000))
	b.assertTBZero()
}

func TestReceiptRefundCanBeHeldByApprovalRule(t *testing.T) {
	b := newBT(t, false)
	admin := b.addMember(b.owner, b.signup("Ada", false), "admin")
	b.rule(map[string]any{"command": "receipt.refund", "min_amount_minor": 10_000})
	rid := b.receipt(map[string]any{"amount_minor": 50_000})["id"].(string)
	small := b.okAs(admin, "receipt.refund", map[string]any{"id": rid, "amount_minor": 9_999})
	eq(t, small["kind"], "refund")
	held := b.okAs(admin, "receipt.refund", map[string]any{"id": rid, "amount_minor": 20_000})
	eq(t, held["approval_required"], true)
	eq(t, n(held["amount_minor"]), int64(20_000))
	eq(t, n(b.ok("receipt.get", map[string]any{"id": rid})["refunded_minor"]), int64(9_999))
	b.ok("approval.decide", map[string]any{"id": held["confirmation_id"], "decision": "approve"})
	eq(t, n(b.ok("receipt.get", map[string]any{"id": rid})["refunded_minor"]), int64(29_999))
	b.assertTBZero()
}

func TestReceiptSharePublicLink(t *testing.T) {
	b := newBT(t, false)
	r := b.receipt(map[string]any{"amount_minor": 118_000, "description": "30% advance on order"})
	l := b.ok("share.create", map[string]any{"note_id": r["id"], "origin": "https://app.example.test"})
	eq(t, l["purpose"], "view")
	g := b.pubGet(l["token"].(string))
	eq(t, g.status, 200)
	d := g.json()
	eq(t, d["kind"], "receipt")
	eq(t, d["number"], r["number"])
	eq(t, d["total_minor"], float64(118_000))
	eq(t, d["subtotal_minor"], float64(100_000))
	eq(t, d["buyer"].(map[string]any)["name"], "Buyer 29")
	eq(t, d["lines"].([]any)[0].(map[string]any)["description"], "30% advance on order")
	eq(t, strings.Contains(string(g.body), "UTR1"), false) // the payment reference stays internal
	// a refund voucher is not shareable
	rf := b.ok("receipt.refund", map[string]any{"id": r["id"], "amount_minor": 1_000})
	b.errIs("share.create", map[string]any{"note_id": rf["id"]}, 400, "invalid")
}
