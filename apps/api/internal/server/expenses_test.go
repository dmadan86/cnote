package server_test

import (
	"strings"
	"testing"
)

// Expenses and TDS are Go-only: fixtures come from Rust, every expense.* / tds.* call runs on the Go server.
func (b *bt) claim(w who, amount int64) string {
	b.t.Helper()
	return b.okAs(w, "expense.create", map[string]any{"spent_on": day(-1), "category": "travel", "amount_minor": amount, "vendor_name": "Auto", "description": "Site visit"})["id"].(string)
}

func (b *bt) eid(cmd string, w who, id string) map[string]any {
	return b.okAs(w, cmd, map[string]any{"id": id})
}

func (b *bt) pl(from, to string) map[string]any {
	return b.ok("report.profit_loss", map[string]any{"from": from, "to": to})
}

func TestExpenseClaimFlowPostsBalancedEntries(t *testing.T) {
	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	eq(t, len(b.tb()["accounts"].([]any)), 7) // no expenses: the chart is untouched
	id := b.claim(m, 180_000)
	d := b.eid("expense.get", m, id)
	eq(t, d["status"], "draft")
	eq(t, d["category_label"], "Travel and transport")
	eq(t, d["account_code"], "5200")
	eq(t, d["title"], "Expense: Travel and transport Rs 1800 at Auto")
	eq(t, b.ok("note.get", map[string]any{"id": id})["types"], []any{"expense"})
	eq(t, len(b.tb()["accounts"].([]any)), 8) // only the category account (seeded on use), nothing posted
	b.errAs(m, "expense.approve", map[string]any{"id": id}, 403, "forbidden")
	b.errIs("expense.approve", map[string]any{"id": id}, 409, "conflict") // still a draft
	b.errIs("expense.reimburse", map[string]any{"id": id}, 409, "conflict")

	eq(t, b.eid("expense.submit", m, id)["status"], "submitted")
	b.errAs(m, "expense.submit", map[string]any{"id": id}, 409, "conflict")
	b.errAs(m, "expense.update", map[string]any{"id": id, "amount_minor": 1}, 409, "conflict") // locked once submitted
	b.errIs("expense.reimburse", map[string]any{"id": id}, 409, "conflict")
	b.assertTBZero()
	eq(t, b.acct("5200"), int64(0))

	a := b.eid("expense.approve", b.owner, id)
	eq(t, a["status"], "approved")
	eq(t, b.acct("5200"), int64(180_000))
	eq(t, b.acct("2150"), int64(-180_000))
	b.assertTBZero()
	b.errIs("expense.approve", map[string]any{"id": id}, 409, "conflict")
	b.errIs("expense.reject", map[string]any{"id": id, "reason": "late"}, 409, "conflict")

	queue := b.list(b.owner, "expense.list", map[string]any{"status": "approved"})
	eq(t, len(queue), 1)
	r := b.ok("expense.reimburse", map[string]any{"id": id, "method": "upi", "reference": "UTR9"})
	eq(t, r["status"], "reimbursed")
	eq(t, b.acct("2150"), int64(0))
	eq(t, b.acct("1200"), int64(-180_000))
	b.assertTBZero()
	b.errIs("expense.reimburse", map[string]any{"id": id}, 409, "conflict") // pays once
	k := b.events(id)
	for _, e := range []string{"expense.created", "expense.submitted", "expense.approved", "expense.reimbursed"} {
		eq(t, count(k, e), 1)
	}
	eq(t, b.notifs(m)[0][:17], "approval_decided|")
}

func TestExpenseRejectResubmitAndEditRules(t *testing.T) {
	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	o := b.addMember(b.owner, b.signup("Other", false), "member")
	id := b.claim(m, 50_000)
	eq(t, n(b.okAs(m, "expense.update", map[string]any{"id": id, "amount_minor": 60_000, "category": "meals"})["amount_minor"]), int64(60_000))
	b.errAs(o, "expense.update", map[string]any{"id": id, "amount_minor": 1}, 404, "not_found")
	b.errIs("expense.update", map[string]any{"id": id, "amount_minor": 1}, 403, "forbidden") // not even the owner edits another's claim
	b.errAs(o, "expense.submit", map[string]any{"id": id}, 404, "not_found")
	b.eid("expense.submit", m, id)
	b.errIs("expense.reject", map[string]any{"id": id}, 400, "invalid") // a reason is required
	rj := b.ok("expense.reject", map[string]any{"id": id, "reason": "No receipt"})
	eq(t, rj["status"], "rejected")
	eq(t, rj["decision_reason"], "No receipt")
	b.assertTBZero()
	eq(t, b.acct("5210"), int64(0)) // a rejected claim never posts
	eq(t, b.notifs(m)[0][:17], "approval_decided|")
	up := b.okAs(m, "expense.update", map[string]any{"id": id, "amount_minor": 55_000, "description": "Receipt attached"})
	eq(t, up["status"], "draft")
	eq(t, up["decision_reason"], nil)
	eq(t, up["description"], "Receipt attached")
	b.eid("expense.submit", m, id)
	b.eid("expense.approve", b.owner, id)
	eq(t, b.acct("5210"), int64(55_000))
	b.assertTBZero()
}

func TestExpenseVisibilityOwnVersusAll(t *testing.T) {
	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	o := b.addMember(b.owner, b.signup("Other", false), "member")
	adm := b.addMember(b.owner, b.signup("Ada", false), "admin")
	id := b.claim(m, 10_000)
	b.claim(o, 20_000)
	eq(t, len(b.list(m, "expense.list", map[string]any{})), 1)
	eq(t, len(b.list(o, "expense.list", map[string]any{})), 1)
	eq(t, len(b.list(b.owner, "expense.list", map[string]any{})), 2)
	eq(t, len(b.list(adm, "expense.list", map[string]any{})), 2)
	b.errAs(o, "expense.get", map[string]any{"id": id}, 404, "not_found")
	// the page itself is private too: another member cannot read it through the notes commands
	b.errAs(o, "note.get", map[string]any{"id": id}, 404, "not_found")
	b.okAs(adm, "expense.get", map[string]any{"id": id})

	// direct expenses are the company's: finance members see them, members without finance access do not
	d := b.ok("expense.record_direct", map[string]any{"spent_on": day(0), "category": "office", "amount_minor": 7_000})
	eq(t, len(b.list(o, "expense.list", map[string]any{"kind": "direct"})), 1)
	b.ok("member.update", map[string]any{"user_id": o.userID, "finance_access": false})
	eq(t, len(b.list(o, "expense.list", map[string]any{"kind": "direct"})), 0)
	b.errAs(o, "expense.get", map[string]any{"id": d["id"]}, 404, "not_found")
	eq(t, len(b.list(o, "expense.list", map[string]any{})), 1) // still sees their own claim
	b.errAs(o, "expense.record_direct", map[string]any{"spent_on": day(0), "category": "office", "amount_minor": 7_000}, 403, "forbidden")
	b.errAs(o, "report.profit_loss", map[string]any{"from": day(-30), "to": day(0)}, 403, "forbidden")
	b.errAs(o, "report.tds_payable", map[string]any{"from": day(-30), "to": day(0)}, 403, "forbidden")
	b.okAs(o, "expense.create", map[string]any{"spent_on": day(0), "category": "meals", "amount_minor": 100}) // own claims need no finance access

	// another workspace sees nothing
	other := b.env.signup("Zed", true)
	b.errAs(other, "expense.get", map[string]any{"id": id}, 403, "forbidden") // not a member of this workspace
	if r := b.cmd(b.gosv.URL, other, other.ws, "expense.get", map[string]any{"id": id}, nil); r.status != 404 {
		t.Fatalf("another workspace's id: %d %s", r.status, r.body)
	}
	if r := b.call(who{ws: b.owner.ws}, "expense.list", map[string]any{}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}
	if r := b.cmd(b.gosv.URL, other, other.ws, "expense.list", map[string]any{}, nil); strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("other workspace list: %s", r.body)
	}
}

func TestExpenseRolesAgentsAndKeys(t *testing.T) {
	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	g := b.addMember(b.owner, b.signup("Gus", false), "guest")
	adm := b.addMember(b.owner, b.signup("Ada", false), "admin")
	in := map[string]any{"spent_on": day(0), "category": "travel", "amount_minor": 5_000}
	b.errAs(g, "expense.create", in, 403, "forbidden")
	id := b.claim(m, 5_000)
	b.eid("expense.submit", m, id)
	for _, cmd := range []string{"expense.approve", "expense.reject", "expense.reimburse"} {
		b.errAs(m, cmd, map[string]any{"id": id, "reason": "x"}, 403, "forbidden")
	}
	// an admin cannot approve their own claim; the owner can
	own := b.claim(adm, 4_000)
	b.eid("expense.submit", adm, own)
	b.errAs(adm, "expense.approve", map[string]any{"id": own}, 403, "forbidden")
	b.errAs(adm, "expense.reject", map[string]any{"id": own, "reason": "x"}, 403, "forbidden")
	b.eid("expense.approve", b.owner, own)
	b.eid("expense.approve", adm, id)

	// agents and API keys: reimbursing and booking direct expenses wait for a human
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes", "manage_invoices"}}})
	created := b.must(b.owner, "agent.create", map[string]any{"name": "Biller", "scopes": []string{"read", "write_notes", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	for name, w := range map[string]who{"agent": agent, "key": b.apiKey(b.owner)} {
		for cmd, args := range map[string]map[string]any{"expense.reimburse": {"id": id}, "expense.record_direct": in} {
			r := b.call(w, cmd, args)
			if r.status != 403 && !strings.Contains(string(r.body), "confirmation_required") {
				t.Fatalf("%s %s not gated: %d %s", name, cmd, r.status, r.body)
			}
		}
		b.okAs(w, "expense.list", map[string]any{})
	}
	eq(t, b.eid("expense.get", b.owner, id)["status"], "approved") // nothing was reimbursed
	eq(t, len(b.list(b.owner, "expense.list", map[string]any{"kind": "direct"})), 0)
	b.assertTBZero()
}

func TestExpenseValidation(t *testing.T) {
	b := newBT(t, false)
	good := map[string]any{"spent_on": day(0), "category": "travel", "amount_minor": 1_000}
	for _, bad := range []map[string]any{
		{"amount_minor": 0}, {"amount_minor": -5}, {"amount_minor": nil}, {"category": "yachts"}, {"category": nil}, {"spent_on": day(1)},
		{"spent_on": "yesterday"}, {"spent_on": nil}, {"gst_minor": 1_001}, {"gst_minor": -1}, {"account_code": "1100"}, {"account_code": "9999"},
		{"space_id": "nope"}, {"vendor_name": strings.Repeat("x", 201)},
	} {
		x := map[string]any{}
		for k, v := range good {
			x[k] = v
		}
		for k, v := range bad {
			if v == nil {
				delete(x, k)
			} else {
				x[k] = v
			}
		}
		b.errIs("expense.create", x, 400, "invalid")
		b.errIs("expense.record_direct", x, 400, "invalid")
	}
	b.errIs("expense.get", map[string]any{}, 400, "invalid")
	b.errIs("expense.get", map[string]any{"id": "00000000-0000-0000-0000-000000000000"}, 404, "not_found")
	b.errIs("report.profit_loss", map[string]any{"from": day(-1)}, 400, "invalid")
	eq(t, len(b.list(b.owner, "expense.categories", map[string]any{})), 7)
	b.assertTBZero()
	eq(t, len(b.tb()["accounts"].([]any)), 7) // refused calls seed nothing
}

func TestExpenseDirectPostsAtOnceAndAccountOverride(t *testing.T) {
	b := newBT(t, false)
	d := b.ok("expense.record_direct", map[string]any{"spent_on": day(0), "category": "software", "amount_minor": 99_900, "gst_minor": 15_240, "vendor_name": "Hosting Co"})
	eq(t, d["status"], "posted")
	eq(t, d["kind"], "direct")
	eq(t, b.acct("5230"), int64(99_900))
	eq(t, b.acct("1200"), int64(-99_900))
	b.assertTBZero()
	id := d["id"].(string)
	b.errIs("expense.submit", map[string]any{"id": id}, 409, "conflict") // posted, not a draft
	b.errIs("expense.approve", map[string]any{"id": id}, 409, "conflict")
	eq(t, count(b.events(id), "expense.recorded"), 1)
	// a workspace expense account can be chosen
	b.ok("expense.record_direct", map[string]any{"spent_on": day(0), "category": "other", "amount_minor": 1_000, "account_code": "5100"})
	eq(t, b.acct("5100"), int64(1_000))
	b.assertTBZero()
	// replay with the same idempotency key posts once
	hdr := map[string]string{"Idempotency-Key": rnd()}
	in := map[string]any{"spent_on": day(0), "category": "office", "amount_minor": 2_222}
	b.cmd(b.gosv.URL, b.owner, b.owner.ws, "expense.record_direct", in, hdr)
	b.cmd(b.gosv.URL, b.owner, b.owner.ws, "expense.record_direct", in, hdr)
	eq(t, b.acct("5220"), int64(2_222))
	b.assertTBZero()
}

func TestExpenseApprovalRuleHoldsLargeClaimsForTheOwner(t *testing.T) {
	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	adm := b.addMember(b.owner, b.signup("Ada", false), "admin")
	b.rule(map[string]any{"command": "expense.approve", "min_amount_minor": 100_000, "approver_role": "owner"})
	b.rule(map[string]any{"command": "expense.reimburse", "min_amount_minor": 100_000, "approver_role": "owner"})
	small, big := b.claim(m, 99_999), b.claim(m, 250_000)
	b.eid("expense.submit", m, small)
	b.eid("expense.submit", m, big)
	eq(t, b.eid("expense.approve", adm, small)["status"], "approved") // below the threshold: straight through
	held := b.eid("expense.approve", adm, big)
	eq(t, held["approval_required"], true)
	eq(t, b.eid("expense.get", b.owner, big)["status"], "submitted")
	eq(t, b.acct("5200"), int64(99_999))
	req := b.approvals(b.owner, map[string]any{"status": "pending"})
	eq(t, len(req), 1)
	eq(t, req[0]["command"], "expense.approve")
	eq(t, n(req[0]["amount_minor"]), int64(250_000))
	d := b.ok("approval.decide", map[string]any{"id": req[0]["id"], "decision": "approve"})
	eq(t, d["status"], "approved")
	eq(t, b.eid("expense.get", b.owner, big)["status"], "approved")
	eq(t, b.acct("5200"), int64(349_999))
	b.assertTBZero()
	eq(t, b.sendReimburse(adm, big)["approval_required"], true)
	b.assertTBZero()
}

func (b *bt) sendReimburse(w who, id string) map[string]any {
	return b.okAs(w, "expense.reimburse", map[string]any{"id": id})
}

func TestProfitAndLossFromTheJournal(t *testing.T) {
	b := newBT(t, false)
	b.issue(b.draft(b.contact, 0))
	inv := b.tb()
	revenue := -balance(t, inv, "4000")
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	id := b.claim(m, 120_000)
	b.eid("expense.submit", m, id)
	b.eid("expense.approve", b.owner, id)
	b.ok("expense.record_direct", map[string]any{"spent_on": day(0), "category": "meals", "amount_minor": 30_000})
	p := b.pl(day(-1), day(0))
	eq(t, n(p["total_income_minor"]), revenue)
	eq(t, n(p["total_expenses_minor"]), int64(150_000))
	eq(t, n(p["net_profit_minor"]), revenue-150_000)
	codes := []string{}
	for _, l := range p["expenses"].([]any) {
		codes = append(codes, l.(map[string]any)["code"].(string))
	}
	eq(t, codes, []string{"5200", "5210"})
	q := b.pl(day(-60), day(-30)) // an earlier period is empty
	eq(t, n(q["net_profit_minor"]), int64(0))
	eq(t, len(q["income"].([]any)), 0)
	// a member with finance access reads it; a guest cannot
	eq(t, n(b.okAs(m, "report.profit_loss", map[string]any{"from": day(-1), "to": day(0)})["total_expenses_minor"]), int64(150_000))
	g := b.addMember(b.owner, b.signup("Gus", false), "guest")
	b.errAs(g, "report.profit_loss", map[string]any{"from": day(-1), "to": day(0)}, 403, "forbidden")
}
