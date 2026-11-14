package server_test

import (
	"fmt"
	"strings"
	"testing"
)

// Bank reconciliation is Go-only: every bank.* call runs on the Go server.
func dmy(offset int) string { return todayIST().AddDate(0, 0, offset).Format("02/01/2006") }

func (b *bt) bankAcct(name string) string {
	return b.ok("bank.account_create", map[string]any{"name": name, "account_hint": "4321"})["id"].(string)
}

// hdfc builds a statement the way HDFC exports it: preamble, separator row, Indian grouping, DD/MM/YY dates.
func hdfc(rows ...string) string {
	return "Account Statement\nCustomer: ACME\n\nDate,Narration,Chq./Ref.No.,Value Dt,Withdrawal Amt.,Deposit Amt.,Closing Balance\n********,********,********,********,********,********,********\n" +
		strings.Join(rows, "\n") + "\n"
}

func (b *bt) imp(acct, csv string) map[string]any {
	return b.ok("bank.import", map[string]any{"account_id": acct, "csv": csv})
}

func (b *bt) lines(acct, status string) []any {
	in := map[string]any{"account_id": acct}
	if status != "" {
		in["status"] = status
	}
	return b.okList("bank.line_list", in)
}

func sugg(l any) []any { return l.(map[string]any)["suggestions"].([]any) }

func TestBankImportMappingAndDedupe(t *testing.T) {
	b := newBT(t, false)
	a := b.bankAcct("HDFC Current")
	csv := hdfc(
		fmt.Sprintf("%s,UPI-ACME CUSTOMER-440123456789,0000440123456789,%s,,\"1,18,000.00\",\"5,18,000.00\"", dmy(-3), dmy(-3)),
		fmt.Sprintf("%s,NEFT DR-OFFICE RENT,N1,%s,\"25,000.00\",,\"4,93,000.00\"", dmy(-2), dmy(-2)),
		fmt.Sprintf("%s,NEFT DR-OFFICE RENT,N1,%s,\"25,000.00\",,\"4,68,000.00\"", dmy(-2), dmy(-2)),
		"STATEMENT SUMMARY,,,,,,")
	r := b.imp(a, csv)
	eq(t, n(r["imported"]), int64(3))
	eq(t, n(r["duplicates"]), int64(0))
	eq(t, r["mapping"].(map[string]any)["debit"], "Withdrawal Amt.")
	acct := b.okList("bank.account_list", map[string]any{})[0].(map[string]any)
	eq(t, acct["csv_mapping"].(map[string]any)["credit"], "Deposit Amt.") // saved for the next import
	eq(t, n(acct["lines"]), int64(3))

	// the same file again: nothing new
	r = b.imp(a, csv)
	eq(t, n(r["imported"]), int64(0))
	eq(t, n(r["duplicates"]), int64(3))
	// an overlapping later statement adds only the new line
	r = b.imp(a, hdfc(fmt.Sprintf("%s,NEFT DR-OFFICE RENT,N1,%s,\"25,000.00\",,\"4,68,000.00\"", dmy(-2), dmy(-2)),
		fmt.Sprintf("%s,ATM,,%s,\"2,000.00\",,\"4,66,000.00\"", dmy(-1), dmy(-1))))
	eq(t, n(r["imported"]), int64(1))
	eq(t, n(r["duplicates"]), int64(1))
	ls := b.lines(a, "")
	eq(t, len(ls), 4)
	first := ls[3].(map[string]any) // oldest last
	eq(t, n(first["amount_minor"]), int64(11_800_000))
	eq(t, first["date"], day(-3))

	// the same lines in another account are not duplicates
	r = b.imp(b.bankAcct("ICICI"), csv)
	eq(t, n(r["imported"]), int64(3))

	// signed-amount, semicolon-delimited export in another bank's layout, with an explicit mapping saved
	k := b.bankAcct("Kotak")
	r = b.ok("bank.import", map[string]any{"account_id": k, "mapping": map[string]any{"date": "When", "narration": "Memo", "amount": "Amt"},
		"csv": fmt.Sprintf("When;Memo;Amt\n%s;Salary;\"1,00,000.00\"\n%s;Fee;-118.00\n", dmy(-1), dmy(-1))})
	eq(t, n(r["imported"]), int64(2))
	eq(t, len(b.lines(k, "")), 2)

	// invalid input
	b.errIs("bank.import", map[string]any{"account_id": a}, 400, "invalid")
	b.errIs("bank.import", map[string]any{"account_id": a, "csv": "foo,bar\n1,2"}, 400, "invalid")
	b.errIs("bank.import", map[string]any{"account_id": a, "csv": "Date,Debit,Credit\n" + dmy(5) + ",1.00,\n"}, 400, "invalid") // future
	b.errIs("bank.import", map[string]any{"account_id": "nope", "csv": csv}, 400, "invalid")
	b.errIs("bank.import", map[string]any{"account_id": "00000000-0000-0000-0000-000000000000", "csv": csv}, 404, "not_found")
	b.errIs("bank.account_create", map[string]any{"name": ""}, 400, "invalid")
	b.errIs("bank.account_create", map[string]any{"name": "HDFC Current"}, 409, "conflict")
	b.ok("bank.account_update", map[string]any{"id": a, "active": false})
	b.errIs("bank.import", map[string]any{"account_id": a, "csv": csv}, 409, "conflict")
	b.assertTBZero()
}

func TestBankMatchingSuggestionsConfirmAndUnmatch(t *testing.T) {
	b := newBT(t, false)
	a := b.bankAcct("HDFC")
	inv := b.issuedInvoice(b.contact, 100_000)["id"].(string)
	b.ok("payment.record", map[string]any{"invoice_id": inv, "amount_minor": 50_000, "reference": "402512345678", "method": "upi"})
	inv2 := b.issuedInvoice(b.contact, 100_000)["id"].(string)
	b.ok("payment.record", map[string]any{"invoice_id": inv2, "amount_minor": 50_000, "reference": "no-match-ref", "method": "bank"})
	b.ok("payment.record", map[string]any{"invoice_id": inv2, "amount_minor": 7_000, "method": "cash"}) // cash never reaches a statement
	v := b.vendor("29", "Hosting Co")
	bill := b.bill(v, "H-1")
	b.bq("bill.record", bill)
	b.payBill(bill, 33_300)
	b.receipt(map[string]any{"amount_minor": 90_000, "reference": "RCPT-UTR-9"})
	b.ok("expense.record_direct", map[string]any{"spent_on": day(-1), "category": "office", "amount_minor": 4_400, "reference": "CARD1"})

	csv := hdfc(
		fmt.Sprintf("%s,UPI-ACME-402512345678-PAY,x,%s,,\"500.00\",\"1,000.00\"", dmy(-1), dmy(-1)), // two payments of 500: the UTR one ranks first
		fmt.Sprintf("%s,NEFT-ACME-LATE,x,%s,,\"500.00\",\"1,500.00\"", dmy(-2), dmy(-2)),
		fmt.Sprintf("%s,NEFT-TOO-OLD,x,%s,,\"500.00\",\"2,000.00\"", dmy(-5), dmy(-5)),  // 5 days off: no candidate
		fmt.Sprintf("%s,NEFT TO HOSTING,x,%s,\"333.00\",,\"1,667.00\"", dmy(0), dmy(0)), // bill payment (debit)
		fmt.Sprintf("%s,RTGS ADVANCE,x,%s,,\"900.00\",\"2,567.00\"", dmy(0), dmy(0)),    // receipt voucher (credit)
		fmt.Sprintf("%s,POS OFFICE,x,%s,\"44.00\",,\"2,523.00\"", dmy(0), dmy(0)),       // direct expense (debit)
		fmt.Sprintf("%s,CASH DEP,x,%s,,\"70.00\",\"2,593.00\"", dmy(0), dmy(0)))         // only a cash payment has this amount
	eq(t, n(b.imp(a, csv)["imported"]), int64(7))
	by := map[string]map[string]any{}
	for _, l := range b.lines(a, "unmatched") {
		m := l.(map[string]any)
		by[strings.Fields(m["narration"].(string))[0]] = m
	}
	utr := by["UPI-ACME-402512345678-PAY"]
	s := utr["suggestions"].([]any)
	eq(t, len(s), 2)
	top := s[0].(map[string]any)
	eq(t, top["reference_match"], true)
	eq(t, top["kind"], "payment")
	eq(t, len(by["NEFT-ACME-LATE"]["suggestions"].([]any)), 2) // both payments are within 3 days, none has the UTR
	eq(t, len(by["NEFT-TOO-OLD"]["suggestions"].([]any)), 0)
	eq(t, len(by["CASH"]["suggestions"].([]any)), 0)
	var bl, rc, ex map[string]any
	for _, l := range b.lines(a, "unmatched") {
		m := l.(map[string]any)
		switch n(m["amount_minor"]) {
		case -33_300:
			bl = m
		case 90_000:
			rc = m
		case -4_400:
			ex = m
		}
	}
	eq(t, sugg(bl)[0].(map[string]any)["kind"], "bill_payment")
	eq(t, sugg(rc)[0].(map[string]any)["kind"], "receipt")
	eq(t, sugg(ex)[0].(map[string]any)["kind"], "expense")

	// confirm the UTR match; the same payment cannot match a second line
	id := utr["id"].(string)
	m := b.ok("bank.match", map[string]any{"line_id": id, "match_kind": top["kind"], "match_id": top["id"]})
	eq(t, m["status"], "matched")
	b.errIs("bank.match", map[string]any{"line_id": id, "match_kind": top["kind"], "match_id": top["id"]}, 409, "conflict") // already matched
	late := by["NEFT-ACME-LATE"]
	for _, l := range b.lines(a, "unmatched") { // refreshed: the matched payment is gone from the offers
		if l.(map[string]any)["id"] == late["id"] {
			late = l.(map[string]any)
		}
	}
	eq(t, len(sugg(late)), 1)
	other := sugg(late)[0].(map[string]any)
	if other["id"] == top["id"] {
		t.Fatal("a matched payment is still offered")
	}
	b.errIs("bank.match", map[string]any{"line_id": late["id"], "match_kind": "payment", "match_id": top["id"]}, 409, "conflict")
	b.errIs("bank.match", map[string]any{"line_id": by["NEFT-TOO-OLD"]["id"], "match_kind": other["kind"], "match_id": other["id"]}, 409, "conflict")    // outside 3 days
	b.errIs("bank.match", map[string]any{"line_id": bl["id"], "match_kind": "receipt", "match_id": sugg(rc)[0].(map[string]any)["id"]}, 409, "conflict") // wrong direction
	b.errIs("bank.match", map[string]any{"line_id": late["id"]}, 400, "invalid")
	ml := b.lines(a, "matched")
	eq(t, len(ml), 1)
	eq(t, strings.HasPrefix(ml[0].(map[string]any)["match_label"].(string), "Payment on invoice"), true)

	// unmatch frees both; the payment is offered again
	b.errIs("bank.unmatch", map[string]any{"line_id": late["id"]}, 409, "conflict")
	b.ok("bank.unmatch", map[string]any{"line_id": id})
	eq(t, len(b.lines(a, "matched")), 0)
	again := b.lines(a, "unmatched")
	eq(t, len(again), 7)
	for _, kind := range []string{"bill_payment", "receipt", "expense"} {
		var l map[string]any
		switch kind {
		case "bill_payment":
			l = bl
		case "receipt":
			l = rc
		default:
			l = ex
		}
		c := sugg(l)[0].(map[string]any)
		b.ok("bank.match", map[string]any{"line_id": l["id"], "match_kind": c["kind"], "match_id": c["id"]})
	}
	eq(t, len(b.lines(a, "matched")), 3)
	b.assertTBZero() // matching never posts
	eq(t, n(b.ok("bank.summary", map[string]any{"account_id": a})["matched"]), int64(3))
}

func TestBankRecordUnmatchedLines(t *testing.T) {
	b := newBT(t, false)
	a := b.bankAcct("HDFC")
	b.imp(a, hdfc(
		fmt.Sprintf("%s,BANK CHARGES,,%s,\"590.00\",,\"9,410.00\"", dmy(-1), dmy(-1)),
		fmt.Sprintf("%s,UPI-NEW CUSTOMER-77,,%s,,\"11,800.00\",\"21,210.00\"", dmy(-1), dmy(-1))))
	var debit, credit map[string]any
	for _, l := range b.lines(a, "unmatched") {
		if m := l.(map[string]any); n(m["amount_minor"]) < 0 {
			debit = m
		} else {
			credit = m
		}
	}
	before := b.acct("1200")
	b.errIs("bank.record_receipt", map[string]any{"line_id": debit["id"], "contact_id": b.contact, "supply_type": "services"}, 400, "invalid")
	b.errIs("bank.record_expense", map[string]any{"line_id": credit["id"], "category": "other"}, 400, "invalid")
	b.errIs("bank.record_expense", map[string]any{"line_id": debit["id"]}, 400, "invalid")
	b.errIs("bank.record_expense", map[string]any{"line_id": debit["id"], "category": "yachts"}, 400, "invalid")
	b.errIs("bank.record_receipt", map[string]any{"line_id": credit["id"], "supply_type": "services"}, 400, "invalid")
	eq(t, b.acct("1200"), before) // refused calls post nothing

	hdr := map[string]string{"Idempotency-Key": rnd()}
	in := map[string]any{"line_id": debit["id"], "category": "other", "vendor_name": "HDFC Bank"}
	b.cmd(b.gosv.URL, b.owner, b.owner.ws, "bank.record_expense", in, hdr)
	b.cmd(b.gosv.URL, b.owner, b.owner.ws, "bank.record_expense", in, hdr) // replay: one expense
	eq(t, b.acct("5290"), int64(59_000))
	eq(t, b.acct("1200"), before-59_000)
	b.errIs("bank.record_expense", in, 409, "conflict") // already matched
	eq(t, len(b.list(b.owner, "expense.list", map[string]any{"kind": "direct"})), 1)
	e := b.lines(a, "matched")[0].(map[string]any)
	eq(t, e["match_kind"], "expense")

	r := b.ok("bank.record_receipt", map[string]any{"line_id": credit["id"], "contact_id": b.contact, "supply_type": "services"})
	eq(t, r["line"].(map[string]any)["status"], "matched")
	eq(t, b.acct("1200"), before-59_000+1_180_000)
	b.errIs("bank.record_receipt", map[string]any{"line_id": credit["id"], "contact_id": b.contact, "supply_type": "services"}, 409, "conflict")
	eq(t, len(b.lines(a, "unmatched")), 0)
	b.assertTBZero()

	// unmatching leaves the recorded expense in the books; it is not offered back to the same line only because the line is free again
	b.ok("bank.unmatch", map[string]any{"line_id": debit["id"]})
	eq(t, b.acct("5290"), int64(59_000))
	eq(t, len(sugg(b.lines(a, "unmatched")[0])), 1)
}

func TestBankSummaryPerAccountAndPeriod(t *testing.T) {
	b := newBT(t, false)
	a := b.bankAcct("HDFC")
	inv := b.issuedInvoice(b.contact, 100_000)["id"].(string)
	b.ok("payment.record", map[string]any{"invoice_id": inv, "amount_minor": 50_000, "method": "bank"})
	b.imp(a, hdfc(
		fmt.Sprintf("%s,OLD,,%s,\"100.00\",,\"900.00\"", dmy(-20), dmy(-20)),
		fmt.Sprintf("%s,CUSTOMER,,%s,,\"500.00\",\"1,400.00\"", dmy(0), dmy(0)),
		fmt.Sprintf("%s,CHARGES,,%s,\"20.00\",,\"1,380.00\"", dmy(0), dmy(0))))
	s := b.ok("bank.summary", map[string]any{"account_id": a, "from": day(-5), "to": day(0)})
	eq(t, n(s["lines"]), int64(2))
	eq(t, n(s["credits_minor"]), int64(50_000))
	eq(t, n(s["debits_minor"]), int64(2_000))
	eq(t, n(s["unmatched"]), int64(2))
	eq(t, n(s["closing_balance_minor"]), int64(138_000))
	eq(t, s["reconciled"], false)
	for _, l := range b.lines(a, "unmatched") {
		if x := sugg(l); len(x) > 0 {
			b.ok("bank.match", map[string]any{"line_id": l.(map[string]any)["id"], "match_kind": x[0].(map[string]any)["kind"], "match_id": x[0].(map[string]any)["id"]})
		}
	}
	s = b.ok("bank.summary", map[string]any{"account_id": a, "from": day(-5), "to": day(0)})
	eq(t, n(s["matched"]), int64(1))
	eq(t, n(s["matched_credits_minor"]), int64(50_000))
	eq(t, n(s["unmatched_debits_minor"]), int64(2_000))
	all := b.ok("bank.summary", map[string]any{"account_id": a})
	eq(t, n(all["lines"]), int64(3))
	b.errIs("bank.summary", map[string]any{}, 400, "invalid")
}

func TestBankFinanceGateRolesKeysAndOtherWorkspace(t *testing.T) {
	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	g := b.addMember(b.owner, b.signup("Gus", false), "guest")
	adm := b.addMember(b.owner, b.signup("Ada", false), "admin")
	a := b.bankAcct("HDFC")
	b.imp(a, hdfc(fmt.Sprintf("%s,CHARGES,,%s,\"20.00\",,\"980.00\"", dmy(0), dmy(0))))
	line := b.lines(a, "")[0].(map[string]any)["id"].(string)
	reads := map[string]map[string]any{"bank.account_list": {}, "bank.line_list": {}, "bank.summary": {"account_id": a}}
	writes := map[string]map[string]any{"bank.account_create": {"name": "X"}, "bank.import": {"account_id": a, "csv": "Date,Debit\n"}, "bank.unmatch": {"line_id": line},
		"bank.match": {"line_id": line, "match_kind": "payment", "match_id": line}, "bank.record_expense": {"line_id": line, "category": "other"},
		"bank.record_receipt": {"line_id": line, "contact_id": b.contact, "supply_type": "services"}, "bank.account_update": {"id": a, "name": "Y"}}
	for cmd, in := range reads {
		b.errAs(g, cmd, in, 403, "forbidden") // guests have no finance
		b.okAs(m, cmd, in)
		b.okAs(adm, cmd, in)
	}
	for cmd, in := range writes {
		b.errAs(g, cmd, in, 403, "forbidden")
	}
	// finance access off: a member is shut out of every bank command, an admin is not
	b.ok("member.update", map[string]any{"user_id": m.userID, "finance_access": false})
	for _, set := range []map[string]map[string]any{reads, writes} {
		for cmd, in := range set {
			b.errAs(m, cmd, in, 403, "forbidden")
		}
	}
	b.okAs(adm, "bank.line_list", map[string]any{})
	b.okAs(m, "note.list", map[string]any{"space_id": b.space}) // other commands are untouched

	// agents and API keys: recording money waits for a human
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes", "manage_invoices"}}})
	created := b.must(b.owner, "agent.create", map[string]any{"name": "Biller", "scopes": []string{"read", "write_notes", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	for name, w := range map[string]who{"agent": agent, "key": b.apiKey(b.owner)} {
		for _, cmd := range []string{"bank.record_expense", "bank.record_receipt"} {
			r := b.call(w, cmd, writes[cmd])
			if r.status != 403 && !strings.Contains(string(r.body), "confirmation_required") {
				t.Fatalf("%s %s not gated: %d %s", name, cmd, r.status, r.body)
			}
		}
	}
	eq(t, b.lines(a, "unmatched")[0].(map[string]any)["status"], "unmatched")
	b.assertTBZero()

	// another workspace sees nothing of this one
	other := b.env.signup("Zed", true)
	for cmd, in := range map[string]map[string]any{"bank.summary": {"account_id": a}, "bank.import": {"account_id": a, "csv": "Date,Debit\n"},
		"bank.unmatch": {"line_id": line}, "bank.account_update": {"id": a, "name": "Z"}} {
		if r := b.cmd(b.gosv.URL, other, other.ws, cmd, in, nil); r.status != 404 {
			t.Fatalf("%s from another workspace: %d %s", cmd, r.status, r.body)
		}
	}
	if r := b.cmd(b.gosv.URL, other, other.ws, "bank.line_list", map[string]any{}, nil); strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("other workspace lines: %s", r.body)
	}
	if r := b.cmd(b.gosv.URL, other, other.ws, "bank.account_list", map[string]any{}, nil); strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("other workspace accounts: %s", r.body)
	}
	// the same account name is free in another workspace
	if r := b.cmd(b.gosv.URL, other, other.ws, "bank.account_create", map[string]any{"name": "HDFC"}, nil); r.status != 200 {
		t.Fatalf("other workspace create: %d %s", r.status, r.body)
	}
	if r := b.call(who{ws: b.owner.ws}, "bank.account_list", map[string]any{}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}
}
