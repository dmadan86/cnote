package server_test

import "testing"

func TestContactLifecycle(t *testing.T) {
	b := newBT(t, false)
	lc := func(w who) map[string]any { return b.okAs(w, "contact.lifecycle", map[string]any{"id": b.contact}) }
	empty := lc(b.owner)
	eq(t, len(empty["deals"].([]any)), 0)
	eq(t, empty["outstanding_minor"], float64(0))

	b.ok("deal.create", map[string]any{"space_id": b.space, "title": "Big deal", "contact_id": b.contact, "amount_minor": 5000})
	q := b.quote(b.contact, "")
	b.q("quote.send", q)
	b.q("quote.accept", q)
	inv := b.q("quote.convert_to_invoice", q)["invoice"].(map[string]any)["id"].(string)
	item := b.q("quote.convert_to_project", q)["item"].(map[string]any)["id"].(string)
	got := b.ok("invoice.issue", map[string]any{"id": inv})
	total := got["total_minor"].(float64)
	b.ok("payment.record", map[string]any{"invoice_id": inv, "amount_minor": 1000, "reference": "UTR9"})
	// a second contact's invoice must not leak in
	b.issue(b.draft(b.newContact("27"), 0))

	r := lc(b.owner)
	eq(t, len(r["deals"].([]any)), 1)
	eq(t, len(r["quotes"].([]any)), 1)
	if r["deals"].([]any)[0].(map[string]any)["created_at"] == nil || r["quotes"].([]any)[0].(map[string]any)["created_at"] == nil {
		t.Fatal("created_at missing")
	}
	eq(t, len(r["invoices"].([]any)), 1)
	eq(t, r["outstanding_minor"], total-1000)
	pay := r["payments"].([]any)
	eq(t, len(pay), 1)
	eq(t, pay[0].(map[string]any)["invoice_id"], inv)
	eq(t, pay[0].(map[string]any)["amount_minor"], float64(1000))
	pr := r["projects"].([]any)
	eq(t, len(pr), 1)
	eq(t, pr[0].(map[string]any)["id"], item)

	// a private space the member cannot read: its invoice for the same contact stays hidden from them
	member := b.addMember(b.owner, b.signup("M", false), "member")
	priv := b.ok("space.create", map[string]any{"key": "PRV", "name": "Private", "visibility": "private"})["id"].(string)
	b.ok("invoice.create_draft", map[string]any{"space_id": priv, "contact_id": b.contact, "invoice_date": day(0),
		"lines": []any{map[string]any{"description": "Secret", "unit_price_minor": 777}}})
	eq(t, len(lc(b.owner)["invoices"].([]any)), 2)
	eq(t, len(lc(member)["invoices"].([]any)), 1)

	// invalid input, no auth, other tenant
	b.errIs("contact.lifecycle", map[string]any{}, 400, "invalid")
	if x := b.call(who{}, "contact.lifecycle", map[string]any{"id": b.contact}); x.status != 401 {
		t.Fatalf("no auth: %d", x.status)
	}
	o := b.env.tenant(false)
	if x := o.call(o.owner, "contact.lifecycle", map[string]any{"id": b.contact}); x.status != 404 {
		t.Fatalf("other workspace: %d %s", x.status, x.body)
	}
}
