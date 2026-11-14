package server_test

import (
	"fmt"
	"testing"

	"github.com/cnote/api/internal/modules/billing"
)

// Purchase bills are Go-only (Rust has no bill.*): fixtures come from Rust, every bill.* call runs on the Go server.
func (b *bt) vendor(state, name string) string {
	return b.ok("contact.create", map[string]any{"space_id": b.space, "name": name, "kind": "company",
		"gstin": billing.CompleteGSTIN(state + "AABCT1332L1Z"), "is_vendor": true})["id"].(string)
}

func (b *bt) billIn(vendor, number string) map[string]any {
	return map[string]any{"space_id": b.space, "vendor_id": vendor, "vendor_bill_number": number, "bill_date": day(-40),
		"lines": []any{
			map[string]any{"description": "Cloud hosting", "hsn_sac": "998315", "quantity": 2.5, "unit_price_minor": 40_001},
			map[string]any{"description": "Stationery", "unit_price_minor": 10_005, "gst_rate_bp": 500, "account_code": "5100"}}}
}

func (b *bt) bill(vendor, number string) string {
	return b.ok("bill.create_draft", b.billIn(vendor, number))["id"].(string)
}

func (b *bt) bq(cmd, id string) map[string]any { return b.ok(cmd, map[string]any{"id": id}) }

func (b *bt) payBill(id string, amount int64) map[string]any {
	return b.ok("bill.pay", map[string]any{"id": id, "amount_minor": amount})
}

func (b *bt) assertTBZero() {
	b.t.Helper()
	tb := b.tb()
	eq(b.t, n(tb["net_minor"]), int64(0))
	eq(b.t, tb["total_debit_minor"], tb["total_credit_minor"])
}

func TestBillTotalsIntraVsInterStateMatchInvoiceMath(t *testing.T) {
	b := newBT(t, false)
	for _, state := range []string{"29", "27"} {
		inv := b.ok("invoice.preview", map[string]any{"id": b.draft(b.newContact(state), 0)})
		id := b.bill(b.vendor(state, "Vendor "+state), "V-"+state)
		d := b.bq("bill.get", id)
		eq(t, d["status"], "draft")
		eq(t, d["total_minor"], d["subtotal_minor"])
		r := b.bq("bill.record", id)
		eq(t, r["status"], "recorded")
		for _, k := range []string{"subtotal_minor", "cgst_minor", "sgst_minor", "igst_minor", "total_minor"} {
			eq(t, r[k], inv[k])
		}
		eq(t, r["place_of_supply"], "29")
		eq(t, r["supplier_state"], state)
		if state == "29" && n(r["igst_minor"]) != 0 || state == "27" && (n(r["cgst_minor"]) != 0 || n(r["igst_minor"]) == 0) {
			t.Fatalf("wrong split for %s: %v", state, r)
		}
		eq(t, n(r["outstanding_minor"]), n(r["total_minor"]))
	}
	b.assertTBZero()
}

func TestBillLedgerRecordPayCancelKeepTrialBalanceZero(t *testing.T) {
	b := newBT(t, false)
	eq(t, len(b.tb()["accounts"].([]any)), 7) // no bills: the chart is untouched
	v := b.vendor("27", "Interstate Supplies")
	id := b.bill(v, "INV-1")
	b.assertTBZero()
	r := b.bq("bill.record", id)
	total, igst, sub := n(r["total_minor"]), n(r["igst_minor"]), n(r["subtotal_minor"])
	tb := b.tb()
	eq(t, balance(t, tb, "2100"), -total)
	eq(t, balance(t, tb, "1303"), igst)
	eq(t, balance(t, tb, "1301"), int64(0))
	eq(t, balance(t, tb, "5100"), int64(10_005)) // the line with its own account
	eq(t, balance(t, tb, "5000"), sub-10_005)
	b.assertTBZero()

	p := b.payBill(id, 1000)
	eq(t, p["status"], "partly_paid")
	eq(t, n(p["outstanding_minor"]), total-1000)
	eq(t, len(p["payments"].([]any)), 1)
	b.errIs("bill.pay", map[string]any{"id": id, "amount_minor": total}, 400, "invalid") // exceeds outstanding
	b.errIs("bill.pay", map[string]any{"id": id, "amount_minor": 0}, 400, "invalid")
	b.errIs("bill.cancel", map[string]any{"id": id}, 409, "conflict") // has a payment
	tb = b.tb()
	eq(t, balance(t, tb, "2100"), -(total - 1000))
	eq(t, balance(t, tb, "1200"), -int64(1000))
	p = b.payBill(id, total-1000)
	eq(t, p["status"], "paid")
	eq(t, n(p["outstanding_minor"]), int64(0))
	b.errIs("bill.pay", map[string]any{"id": id, "amount_minor": 1}, 409, "conflict")
	eq(t, balance(t, b.tb(), "2100"), int64(0))
	b.assertTBZero()
	if k := b.events(id); count(k, "bill.recorded") != 1 || count(k, "bill.payment_recorded") != 2 || count(k, "bill.paid") != 1 {
		t.Fatalf("events %v", k)
	}

	// cancel a recorded, unpaid bill: the reverse entry zeroes every account it touched
	c := b.bill(b.vendor("29", "Local"), "L-1")
	b.bq("bill.record", c)
	eq(t, b.bq("bill.cancel", c)["status"], "cancelled")
	b.errIs("bill.record", map[string]any{"id": c}, 409, "conflict")
	b.errIs("bill.cancel", map[string]any{"id": c}, 409, "conflict")
	tb = b.tb()
	eq(t, balance(t, tb, "1301"), int64(0))
	eq(t, balance(t, tb, "1302"), int64(0))
	eq(t, balance(t, tb, "2100"), int64(0))
	b.assertTBZero()
	eq(t, count(b.events(c), "bill.cancelled"), 1)

	// a draft cancels without touching the ledger
	d := b.bill(v, "INV-2")
	eq(t, b.bq("bill.cancel", d)["status"], "cancelled")
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from billing.journal_entries where source_id = '"+d+"'"), []string{"0"})
}

func TestBillDuplicateVendorBillNumberRejected(t *testing.T) {
	b := newBT(t, false)
	v, w := b.vendor("29", "A"), b.vendor("29", "B")
	first := b.bill(v, "DUP-1")
	b.errIs("bill.create_draft", b.billIn(v, "DUP-1"), 409, "conflict")
	b.errIs("bill.create_draft", b.billIn(v, "  DUP-1 "), 409, "conflict") // trimmed
	b.bill(w, "DUP-1")                                                     // another vendor may reuse the number
	other := b.bill(v, "DUP-2")
	b.errIs("bill.update_draft", map[string]any{"id": other, "vendor_bill_number": "DUP-1"}, 409, "conflict")
	eq(t, b.ok("bill.update_draft", map[string]any{"id": first, "vendor_bill_number": "DUP-1"})["vendor_bill_number"], "DUP-1")
	b.bq("bill.cancel", first) // cancelling frees the number
	b.bill(v, "DUP-1")
	in := b.billIn(v, "")
	b.errIs("bill.create_draft", in, 400, "invalid")
}

func TestBillVendorRulesAndDraftEditing(t *testing.T) {
	b := newBT(t, false)
	b.errIs("bill.create_draft", b.billIn(b.contact, "X-1"), 400, "invalid") // not flagged as a vendor
	v := b.vendor("29", "Flagged")
	bad := b.billIn(v, "X-2")
	bad["lines"] = []any{map[string]any{"description": "x", "unit_price_minor": 100, "account_code": "1100"}}
	b.errIs("bill.create_draft", bad, 400, "invalid") // receivables is not an expense account
	id := b.bill(v, "X-3")
	u := b.ok("bill.update_draft", map[string]any{"id": id, "lines": []any{map[string]any{"description": "Only", "unit_price_minor": 100_000}}})
	eq(t, n(u["subtotal_minor"]), int64(100_000))
	b.errIs("bill.update_draft", map[string]any{"id": id, "lines": []any{}}, 400, "invalid")
	b.bq("bill.record", id)
	b.errIs("bill.update_draft", map[string]any{"id": id, "reverse": true}, 409, "conflict")
	b.errIs("bill.get", map[string]any{"id": "nope"}, 400, "invalid")

	// contact output is unchanged; the flag is reachable through vendor.list and the contact.list filter
	c := b.ok("contact.get", map[string]any{"id": v})
	if _, has := c["is_vendor"]; has {
		t.Fatalf("contact output gained is_vendor: %v", c)
	}
	vs := b.okList("vendor.list", map[string]any{})
	eq(t, len(vs), 1)
	eq(t, vs[0].(map[string]any)["name"], "Flagged")
	eq(t, n(vs[0].(map[string]any)["outstanding_minor"]), n(b.bq("bill.get", id)["total_minor"]))
	eq(t, len(b.okList("contact.list", map[string]any{})), 2)
	eq(t, len(b.okList("contact.list", map[string]any{"vendor": true})), 1)
	eq(t, len(b.okList("contact.list", map[string]any{"vendor": false})), 1)
	b.ok("contact.update", map[string]any{"id": v, "is_vendor": false})
	eq(t, len(b.okList("vendor.list", map[string]any{})), 0)
	eq(t, len(b.okList("bill.list", map[string]any{"status": "recorded"})), 1)
	eq(t, len(b.okList("bill.list", map[string]any{"status": "draft"})), 0)
}

func TestBillPayablesAgeingAndITCSummary(t *testing.T) {
	b := newBT(t, false)
	v := b.vendor("29", "Ageing Co")
	old := b.bill(v, "OLD") // dated 40 days ago: 31-60 bucket
	b.bq("bill.record", old)
	fresh := b.ok("bill.create_draft", map[string]any{"space_id": b.space, "vendor_id": v, "vendor_bill_number": "NEW",
		"lines": []any{map[string]any{"description": "x", "unit_price_minor": 100_000}}})["id"].(string)
	fr := b.bq("bill.record", fresh)
	paid := b.bill(v, "PAID")
	b.bq("bill.record", paid)
	b.payBill(paid, n(b.bq("bill.get", paid)["total_minor"]))
	b.bq("bill.cancel", b.bill(v, "DRAFT-ONLY"))

	rep := b.ok("report.payables_ageing", map[string]any{})
	eq(t, len(rep["bills"].([]any)), 2)
	oldTotal := n(b.bq("bill.get", old)["total_minor"])
	bk := map[string]int64{}
	for _, x := range rep["buckets"].([]any) {
		m := x.(map[string]any)
		bk[m["bucket"].(string)] = n(m["outstanding_minor"])
	}
	eq(t, bk["31-60"], oldTotal)
	eq(t, bk["0-30"], n(fr["total_minor"]))
	eq(t, n(rep["total_minor"]), oldTotal+n(fr["total_minor"]))

	itc := b.ok("report.itc_summary", map[string]any{"from": day(-60), "to": day(0)})
	tot := itc["totals"].(map[string]any)
	oldD := b.bq("bill.get", old)
	paidD := b.bq("bill.get", paid)
	eq(t, n(tot["cgst_minor"]), n(oldD["cgst_minor"])+n(fr["cgst_minor"])+n(paidD["cgst_minor"]))
	eq(t, n(tot["itc_minor"]), n(tot["cgst_minor"])+n(tot["sgst_minor"])+n(tot["igst_minor"]))
	b.errIs("report.itc_summary", map[string]any{}, 400, "invalid")
	// output tax report is untouched by bills
	eq(t, len(b.ok("report.gst_summary", map[string]any{"from": day(-60), "to": day(0)})["rows"].([]any)), 0)
}

func TestBillRoleGatesAndAgentConfirmation(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	v := b.vendor("29", "Gated")
	in := b.billIn(v, "G-1")
	b.errAs(guest, "bill.create_draft", in, 403, "forbidden")
	id := b.okAs(member, "bill.create_draft", in)["id"].(string)
	b.errAs(guest, "bill.record", map[string]any{"id": id}, 403, "forbidden")
	b.errAs(guest, "report.payables_ageing", map[string]any{}, 403, "forbidden")
	b.okAs(member, "bill.record", map[string]any{"id": id})
	b.errAs(guest, "bill.pay", map[string]any{"id": id, "amount_minor": 1}, 403, "forbidden")
	b.okAs(member, "report.payables_ageing", map[string]any{})
	b.okAs(member, "bill.get", map[string]any{"id": id})
	if r := b.call(who{}, "bill.get", map[string]any{"id": id}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}

	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_crm", "manage_invoices"}}})
	created := b.must(b.owner, "agent.create", map[string]any{"name": "Payer", "scopes": []string{"read", "manage_crm", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	d := b.okAs(agent, "bill.create_draft", b.billIn(v, "G-2"))["id"].(string) // drafting and recording are not gated
	b.okAs(agent, "bill.record", map[string]any{"id": d})
	r := b.okAs(agent, "bill.pay", map[string]any{"id": d, "amount_minor": 100})
	eq(t, r["confirmation_required"], true)
	r = b.okAs(agent, "bill.cancel", map[string]any{"id": d})
	eq(t, r["confirmation_required"], true)
	eq(t, n(b.bq("bill.get", d)["paid_minor"]), int64(0))
	eq(t, b.bq("bill.get", d)["status"], "recorded")
}

func TestBillTenantIsolation(t *testing.T) {
	b := newBT(t, false)
	id := b.bill(b.vendor("29", "Mine"), "T-1")
	b.bq("bill.record", id)
	o := b.env.tenant(false)
	if r := o.call(o.owner, "bill.get", map[string]any{"id": id}); r.status != 404 {
		t.Fatalf("other workspace sees bill: %d %s", r.status, r.body)
	}
	if r := o.cmd(o.gosv.URL, o.owner, b.owner.ws, "bill.get", map[string]any{"id": id}, nil); r.status == 200 {
		t.Fatalf("foreign workspace path: %s", r.body)
	}
	o.errIs("bill.pay", map[string]any{"id": id, "amount_minor": 1}, 404, "not_found")
	o.errIs("bill.create_draft", b.billIn(b.vendor("29", "Mine2"), "T-2"), 404, "not_found")
	eq(t, len(o.okList("bill.list", map[string]any{})), 0)
	eq(t, len(o.okList("vendor.list", map[string]any{})), 0)
	eq(t, len(o.tb()["accounts"].([]any)), 7)
	eq(t, len(b.okList("bill.list", map[string]any{})), 1)
}

func TestBillIdempotentPayReplay(t *testing.T) {
	b := newBT(t, false)
	id := b.bill(b.vendor("29", "Replay"), "R-1")
	b.bq("bill.record", id)
	h := map[string]string{"Idempotency-Key": "billpay-" + rnd()}
	in := map[string]any{"id": id, "amount_minor": 500}
	r1 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "bill.pay", in, h)
	r2 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "bill.pay", in, h)
	if r1.status != 200 || string(r1.body) != string(r2.body) {
		t.Fatalf("replay: %d %s / %d %s", r1.status, r1.body, r2.status, r2.body)
	}
	eq(t, len(b.bq("bill.get", id)["payments"].([]any)), 1)
	eq(t, fmt.Sprint(n(b.bq("bill.get", id)["paid_minor"])), "500")
	b.assertTBZero()
}
