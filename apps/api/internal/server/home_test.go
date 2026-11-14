package server_test

import (
	"testing"
)

// home.summary is Go-only: fixtures come from Rust (workspace, spaces), every other call runs on the Go server.
func (b *bt) invDue(due string) map[string]any {
	id := b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "invoice_date": day(-40), "due_date": due,
		"lines": []any{map[string]any{"description": "Work", "hsn_sac": "998314", "unit_price_minor": 100_000}}})["id"].(string)
	return b.issue(id)
}

func (b *bt) billDue(vendor, number, due string) map[string]any {
	in := b.billIn(vendor, number)
	in["due_date"] = due
	id := b.ok("bill.create_draft", in)["id"].(string)
	return b.bq("bill.record", id)
}

func tile(t *testing.T, h map[string]any, name string) map[string]any {
	t.Helper()
	m, ok := h["tiles"].(map[string]any)[name].(map[string]any)
	if !ok {
		t.Fatalf("tile %s missing: %v", name, h["tiles"])
	}
	return m
}

func noTile(t *testing.T, h map[string]any, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := h["tiles"].(map[string]any)[name]; ok {
			t.Fatalf("tile %s must be hidden: %v", name, h["tiles"])
		}
	}
}

func kinds(h map[string]any) []string {
	out := []string{}
	for _, a := range h["attention"].([]any) {
		out = append(out, a.(map[string]any)["kind"].(string))
	}
	return out
}

func (b *bt) home(w who) map[string]any { return b.okAs(w, "home.summary", map[string]any{}) }

func TestHomeTilesCountsAndAmounts(t *testing.T) {
	b := newBT(t, false)
	eng := b.must(b.owner, "space.create", map[string]any{"key": "ENG", "name": "Eng", "template": "software"})["id"].(string)
	item := func(title, due string, to string) string {
		in := map[string]any{"space_id": eng, "title": title, "assignee_id": b.owner.userID}
		if due != "" {
			in["due_date"] = due
		}
		r := b.ok("item.create", in)
		if to != "" { // the software workflow demands evidence for Done; the dashboard only reads the category
			if err := b.raw("update projects.items set status_category = 'done' where key = '" + r["key"].(string) + "'"); err != nil {
				t.Fatal(err)
			}
		}
		return r["key"].(string)
	}
	late := item("Late task", day(-3), "")
	item("Today task", day(0), "")
	item("Future task", day(5), "")
	item("No date", "", "")
	item("Done late", day(-9), "Done")
	b.ok("item.create", map[string]any{"space_id": eng, "title": "Not mine", "due_date": day(-1)})

	i1 := b.invDue(day(-10)) // overdue
	i2 := b.invDue(day(-1))  // overdue
	i3 := b.invDue(day(20))  // not yet due
	b.ok("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "lines": []any{map[string]any{"description": "x", "unit_price_minor": 5}}})
	b.ok("payment.record", map[string]any{"invoice_id": i2["id"], "amount_minor": 30_000, "method": "upi"})
	t1, t2, t3 := n(i1["total_minor"]), n(i2["total_minor"])-30_000, n(i3["total_minor"])

	v := b.vendor("29", "Vend")
	bo := b.billDue(v, "B-1", day(-2)) // overdue
	bs := b.billDue(v, "B-2", day(7))  // due in 7 days
	b.billDue(v, "B-3", day(8))        // outside window
	b.ok("bill.create_draft", b.billIn(v, "B-4"))
	bo1, bs1 := n(bo["total_minor"]), n(bs["total_minor"])

	q1 := b.quote(b.contact, "")
	b.q("quote.send", q1)
	b.quote(b.contact, "") // draft: not awaiting
	qa := b.quote(b.contact, "")
	b.q("quote.send", qa)
	b.q("quote.accept", qa)

	crm := b.must(b.owner, "space.create", map[string]any{"key": "SAL", "name": "Sales"})["id"].(string)
	d1 := b.ok("deal.create", map[string]any{"space_id": crm, "title": "Fresh lead", "amount_minor": 50_000})["id"].(string)
	b.ok("deal.create", map[string]any{"space_id": crm, "title": "Proposal", "stage": "proposal"})
	old := b.ok("deal.create", map[string]any{"space_id": crm, "title": "Old lead"})["id"].(string)
	if err := b.raw("update core.notes set created_at = now() - interval '9 days' where id = '" + old + "'"); err != nil {
		t.Fatal(err)
	}

	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_invoices"}}})
	ag := b.must(b.owner, "agent.create", map[string]any{"name": "Payer", "scopes": []string{"read", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: ag["api_key"].(string)}
	eq(t, b.okAs(agent, "bill.pay", map[string]any{"id": bo["id"], "amount_minor": 1})["confirmation_required"], true)

	h := b.home(b.owner)
	eq(t, h["view"], "owner")
	eq(t, tile(t, h, "tasks_due")["count"], float64(2))
	eq(t, tile(t, h, "tasks_due")["overdue"], float64(1))
	pp := tile(t, h, "payments_pending")
	eq(t, n(pp["count"]), int64(3))
	eq(t, n(pp["total_minor"]), t1+t2+t3)
	eq(t, n(pp["overdue_count"]), int64(2))
	eq(t, n(pp["overdue_minor"]), t1+t2)
	eq(t, tile(t, h, "new_inquiries")["count"], float64(1))
	ap := tile(t, h, "approvals")
	eq(t, ap["confirmations"], float64(1))
	eq(t, ap["quotes_awaiting"], float64(1))
	eq(t, ap["count"], float64(2))
	bd := tile(t, h, "bills_due")
	eq(t, n(bd["count"]), int64(2))
	eq(t, n(bd["total_minor"]), bo1+bs1)
	eq(t, n(bd["overdue_count"]), int64(1))
	ur := tile(t, h, "urgent")
	eq(t, ur["count"], float64(3)) // 1 overdue task + 2 overdue invoices
	eq(t, ur["overdue_tasks"], float64(1))
	eq(t, ur["overdue_invoices"], float64(2))

	// attention: overdue first (longest overdue first: invoice 10d, task 3d, bill 2d, invoice 1d), then due (today before later), then info (bigger amount first); capped at 10
	att := h["attention"].([]any)
	var seq []string
	for _, a := range att {
		m := a.(map[string]any)
		seq = append(seq, m["kind"].(string)+":"+m["severity"].(string))
	}
	eq(t, seq, []string{"invoice:overdue", "task:overdue", "bill:overdue", "invoice:overdue", "task:due", "confirmation:due", "bill:due", "quote:info", "deal:info"})
	first := att[0].(map[string]any)
	eq(t, first["id"], i1["id"])
	eq(t, n(first["amount_minor"]), t1)
	eq(t, first["days_overdue"], float64(10))
	eq(t, att[1].(map[string]any)["key"], late)
	eq(t, att[1].(map[string]any)["reason"], "Overdue by 3 days")
	eq(t, att[8].(map[string]any)["id"], d1)

	// the cap: 12 more overdue tasks keep the list at 10
	for i := 0; i < 12; i++ {
		item("Bulk", day(-20), "")
	}
	eq(t, len(b.home(b.owner)["attention"].([]any)), 10)
}

func TestHomePermissions(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	eng := b.must(b.owner, "space.create", map[string]any{"key": "ENG", "name": "Eng", "template": "software"})["id"].(string)
	b.ok("item.create", map[string]any{"space_id": eng, "title": "Mine", "assignee_id": member.userID, "due_date": day(-1)})
	b.ok("item.create", map[string]any{"space_id": eng, "title": "Guest's", "assignee_id": guest.userID, "due_date": day(-1)})
	inv := b.invDue(day(-5))
	b.billDue(b.vendor("29", "V"), "P-1", day(1))
	b.q("quote.send", b.quote(b.contact, ""))
	deal := b.ok("deal.create", map[string]any{"space_id": b.space, "title": "Lead", "owner_id": b.owner.userID})["id"].(string)

	// a member who can read the finance space sees the money (the report.* gate is member), but only their own deals
	h := b.home(member)
	eq(t, h["view"], "personal")
	eq(t, n(tile(t, h, "payments_pending")["total_minor"]), n(inv["total_minor"]))
	eq(t, tile(t, h, "new_inquiries")["count"], float64(0))
	eq(t, tile(t, h, "tasks_due")["count"], float64(1))
	eq(t, tile(t, h, "approvals")["confirmations"], float64(0))

	// a pending agent confirmation is the admin's to approve; a member sees only their own agents' requests
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_invoices"}}})
	ag := b.must(b.owner, "agent.create", map[string]any{"name": "Issuer", "scopes": []string{"read", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: ag["api_key"].(string)}
	eq(t, b.okAs(agent, "invoice.issue", map[string]any{"id": b.draft(b.contact, 0)})["confirmation_required"], true)
	eq(t, tile(t, b.home(b.owner), "approvals")["confirmations"], float64(1))
	eq(t, tile(t, b.home(member), "approvals")["confirmations"], float64(0))

	// guest: tasks only, never money, inquiries or approvals
	g := b.home(guest)
	noTile(t, g, "payments_pending", "bills_due", "approvals", "new_inquiries")
	eq(t, kinds(g), []string{})
	tile(t, g, "tasks_due")
	// guest is not in the eng space: it cannot read the task either
	eq(t, tile(t, g, "tasks_due")["count"], float64(0))

	// private finance space: a member outside it sees zero money
	prv := b.must(b.owner, "space.create", map[string]any{"key": "PRV", "name": "Private", "visibility": "private"})["id"].(string)
	b.space = prv
	pinv := b.invDue(day(-2))
	h = b.home(member)
	eq(t, n(tile(t, h, "payments_pending")["total_minor"]), n(inv["total_minor"])) // not + pinv
	eq(t, n(tile(t, b.home(b.owner), "payments_pending")["total_minor"]), n(inv["total_minor"])+n(pinv["total_minor"]))

	// restricted page: view-restricted invoice disappears for the member, stays for the owner
	b.must(b.owner, "note.restrict", map[string]any{"note_id": inv["id"], "restriction": "view"})
	h = b.home(member)
	eq(t, n(tile(t, h, "payments_pending")["total_minor"]), int64(0))
	for _, a := range h["attention"].([]any) {
		if a.(map[string]any)["id"] == inv["id"] {
			t.Fatal("restricted invoice leaked into attention")
		}
	}
	eq(t, n(tile(t, b.home(b.owner), "payments_pending")["count"]), int64(2))

	// owner sees the deal as a new inquiry; a member who owns none sees zero
	eq(t, tile(t, b.home(b.owner), "new_inquiries")["count"], float64(1))
	_ = deal

	// other workspace sees nothing of this one
	o := b.env.tenant(false)
	oh := o.home(o.owner)
	eq(t, n(tile(t, oh, "payments_pending")["count"]), int64(0))
	eq(t, kinds(oh), []string{})
	if r := o.cmd(o.gosv.URL, o.owner, b.owner.ws, "home.summary", map[string]any{}, nil); r.status == 200 {
		t.Fatalf("foreign workspace path: %s", r.body)
	}
	if r := b.call(who{}, "home.summary", map[string]any{}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}
}

func TestHomeModulesOffHidesTiles(t *testing.T) {
	b := newBT(t, false)
	eng := b.must(b.owner, "space.create", map[string]any{"key": "ENG", "name": "Eng", "template": "software"})["id"].(string)
	b.ok("item.create", map[string]any{"space_id": eng, "title": "T", "assignee_id": b.owner.userID, "due_date": day(-1)})
	b.invDue(day(-5))
	b.ok("deal.create", map[string]any{"space_id": b.space, "title": "Lead"})
	set := func(mods ...string) {
		b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"modules": append([]string{}, mods...)}})
	}
	set("docs", "projects", "crm") // billing off
	h := b.home(b.owner)
	noTile(t, h, "payments_pending", "bills_due")
	tile(t, h, "tasks_due")
	tile(t, h, "new_inquiries")
	eq(t, tile(t, h, "approvals")["quotes_awaiting"], float64(0))
	eq(t, kinds(h), []string{"task", "deal"})
	set("docs", "billing") // projects and crm off
	h = b.home(b.owner)
	noTile(t, h, "tasks_due", "new_inquiries")
	tile(t, h, "payments_pending")
	eq(t, kinds(h), []string{"invoice"})
	set() // everything off: nothing but an empty approvals-free home
	h = b.home(b.owner)
	noTile(t, h, "tasks_due", "new_inquiries", "payments_pending", "bills_due", "urgent")
	eq(t, kinds(h), []string{})
}
