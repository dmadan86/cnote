package server_test

import (
	"fmt"
	"testing"
)

// Quotations are Go-only (Rust has no quote.*): fixtures come from Rust, every quote.* call runs on the Go server.
func (b *bt) quote(contact string, validUntil string) string {
	in := map[string]any{"space_id": b.space, "contact_id": contact, "quote_date": day(0),
		"lines": []any{
			map[string]any{"description": "Consulting", "hsn_sac": "998314", "quantity": 2.5, "unit_price_minor": 40_001},
			map[string]any{"description": "Hosting", "hsn_sac": "998315", "unit_price_minor": 10_005, "gst_rate_bp": 500}}}
	if validUntil != "" {
		in["valid_until"] = validUntil
	}
	return b.ok("quote.create_draft", in)["id"].(string)
}

func (b *bt) q(cmd, id string) map[string]any { return b.ok(cmd, map[string]any{"id": id}) }

func TestQuoteTotalsGSTMatchInvoiceAndSkipLedger(t *testing.T) {
	b := newBT(t, false)
	for _, state := range []string{"29", "27"} { // intra-state CGST+SGST, inter-state IGST
		contact := b.contact
		if state != "29" {
			contact = b.newContact(state)
		}
		inv := b.ok("invoice.preview", map[string]any{"id": b.draft(contact, 0)})
		q := b.quote(contact, "")
		eq(t, b.ok("quote.get", map[string]any{"id": q})["status"], "draft")
		s := b.q("quote.send", q)
		for _, k := range []string{"subtotal_minor", "cgst_minor", "sgst_minor", "igst_minor", "total_minor"} {
			eq(t, s[k], inv[k])
		}
		eq(t, s["place_of_supply"], state)
		eq(t, len(s["lines"].([]any)), 2)
	}
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from billing.journal_entries"), []string{"0"})
	if k := b.events(b.quote(b.contact, "")); count(k, "quote.created") != 1 {
		t.Fatalf("events %v", k)
	}
	eq(t, len(b.okList("quote.list", map[string]any{"status": "draft"})), 1)
	eq(t, b.ok("quote.get", map[string]any{"id": b.quote(b.contact, "")})["contact_name"], "Buyer 29")
}

func TestQuoteNumberingGapFreeAndSeparateFromInvoices(t *testing.T) {
	b := newBT(t, false)
	fy := fyOf(todayIST())
	q1, q2, q3 := b.quote(b.contact, ""), b.quote(b.contact, ""), b.quote(b.contact, "")
	if n := b.ok("quote.get", map[string]any{"id": q1})["number"]; n != nil {
		t.Fatalf("draft has number %v", n)
	}
	eq(t, b.q("quote.send", q1)["number"], "QT/"+fy+"/0001")
	b.errIs("quote.send", map[string]any{"id": q1}, 409, "conflict") // a refused send burns no number
	eq(t, b.q("quote.send", q3)["number"], "QT/"+fy+"/0002")
	eq(t, b.q("quote.send", q2)["number"], "QT/"+fy+"/0003")
	// invoices keep their own series
	eq(t, b.issue(b.draft(b.contact, 0))["number"], "INV/"+fy+"/0001")
	eq(t, b.dbStrings(b.owner.ws, "select name from billing.sequences order by name"), []string{"invoice:" + fy, "quote:" + fy})
}

func TestQuoteStatusTransitions(t *testing.T) {
	b := newBT(t, false)
	q := b.quote(b.contact, "")
	for _, c := range []string{"quote.accept", "quote.reject", "quote.convert_to_invoice", "quote.convert_to_project"} {
		b.errIs(c, map[string]any{"id": q}, 409, "conflict") // not sent / not accepted yet
	}
	b.q("quote.send", q)
	b.errIs("quote.update_draft", map[string]any{"id": q, "reverse_charge": true}, 409, "conflict")
	b.errIs("quote.convert_to_invoice", map[string]any{"id": q}, 409, "conflict")
	eq(t, b.q("quote.accept", q)["status"], "accepted")
	b.errIs("quote.reject", map[string]any{"id": q}, 409, "conflict")
	b.errIs("quote.send", map[string]any{"id": q}, 409, "conflict")

	r := b.quote(b.contact, "")
	b.q("quote.send", r)
	eq(t, b.q("quote.reject", r)["status"], "rejected")
	b.errIs("quote.accept", map[string]any{"id": r}, 409, "conflict")
	b.errIs("quote.convert_to_invoice", map[string]any{"id": r}, 409, "conflict")

	// a draft is editable
	d := b.quote(b.contact, "")
	u := b.ok("quote.update_draft", map[string]any{"id": d, "lines": []any{map[string]any{"description": "Only", "unit_price_minor": 100_000}}})
	eq(t, n(u["subtotal_minor"]), int64(100_000))
	b.errIs("quote.update_draft", map[string]any{"id": d, "lines": []any{}}, 400, "invalid")
	b.errIs("quote.get", map[string]any{"id": "nope"}, 400, "invalid")

	// past valid_until: reported as expired and cannot be accepted
	e := b.quote(b.contact, day(-1))
	b.q("quote.send", e)
	eq(t, b.ok("quote.get", map[string]any{"id": e})["status"], "expired")
	b.errIs("quote.accept", map[string]any{"id": e}, 409, "conflict")

	kinds := b.events(q)
	for _, k := range []string{"quote.created", "quote.sent", "quote.accepted"} {
		eq(t, count(kinds, k), 1)
	}
	eq(t, count(b.events(r), "quote.rejected"), 1)
}

func TestQuoteRoleGatesAndAgentConfirmation(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	in := map[string]any{"space_id": b.space, "contact_id": b.contact, "lines": []any{map[string]any{"description": "x", "unit_price_minor": 100}}}
	b.errAs(guest, "quote.create_draft", in, 403, "forbidden")
	id := b.okAs(member, "quote.create_draft", in)["id"].(string)
	b.okAs(member, "quote.send", map[string]any{"id": id})
	b.errAs(guest, "quote.accept", map[string]any{"id": id}, 403, "forbidden")
	b.errAs(guest, "quote.convert_to_invoice", map[string]any{"id": id}, 403, "forbidden")
	eq(t, b.okAs(member, "quote.reject", map[string]any{"id": id})["status"], "rejected")
	if r := b.call(who{}, "quote.get", map[string]any{"id": id}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}

	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_crm", "manage_invoices"}}})
	created := b.must(b.owner, "agent.create", map[string]any{"name": "Quoter", "scopes": []string{"read", "manage_crm", "manage_invoices"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	d := b.okAs(agent, "quote.create_draft", in)["id"].(string) // drafting is not gated
	r := b.okAs(agent, "quote.send", map[string]any{"id": d})
	eq(t, r["confirmation_required"], true)
	eq(t, b.ok("quote.get", map[string]any{"id": d})["status"], "draft")
}

func TestQuoteTenantIsolation(t *testing.T) {
	b := newBT(t, false)
	q := b.quote(b.contact, "")
	b.q("quote.send", q)
	o := b.env.tenant(false)
	if r := o.call(o.owner, "quote.get", map[string]any{"id": q}); r.status != 404 {
		t.Fatalf("other workspace sees quote: %d %s", r.status, r.body)
	}
	if r := o.cmd(o.gosv.URL, o.owner, b.owner.ws, "quote.get", map[string]any{"id": q}, nil); r.status == 200 {
		t.Fatalf("foreign workspace path: %s", r.body)
	}
	eq(t, len(o.okList("quote.list", map[string]any{})), 0)
	o.errIs("quote.accept", map[string]any{"id": q}, 404, "not_found")
	eq(t, len(b.okList("quote.list", map[string]any{})), 1)
}

func TestQuoteConversion(t *testing.T) {
	b := newBT(t, false)
	q := b.quote(b.contact, "")
	b.q("quote.send", q)
	b.q("quote.accept", q)

	c := b.q("quote.convert_to_invoice", q)
	inv := c["invoice"].(map[string]any)
	eq(t, inv["status"], "draft")
	eq(t, inv["contact_id"], b.contact)
	eq(t, len(inv["lines"].([]any)), 2)
	eq(t, inv["lines"].([]any)[0].(map[string]any)["description"], "Consulting")
	eq(t, inv["lines"].([]any)[0].(map[string]any)["quantity"], "2.5")
	eq(t, inv["lines"].([]any)[1].(map[string]any)["gst_rate_bp"], float64(500))
	got := b.ok("quote.get", map[string]any{"id": q})
	eq(t, got["status"], "converted")
	eq(t, got["converted_invoice_id"], inv["id"])
	b.errIs("quote.convert_to_invoice", map[string]any{"id": q}, 409, "conflict")
	// the draft invoice totals what the quote promised
	pv := b.ok("invoice.preview", map[string]any{"id": inv["id"]})
	eq(t, pv["total_minor"], got["total_minor"])
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from billing.journal_entries"), []string{"0"})

	p := b.q("quote.convert_to_project", q) // still allowed once converted to an invoice
	item := p["item"].(map[string]any)
	got = b.ok("quote.get", map[string]any{"id": q})
	eq(t, got["converted_item_id"], item["id"])
	eq(t, b.ok("item.get", map[string]any{"id": item["id"]})["title"], fmt.Sprintf("%s Quotation for Buyer 29", got["number"]))
	b.errIs("quote.convert_to_project", map[string]any{"id": q}, 409, "conflict")
	eq(t, count(b.events(q), "quote.converted"), 2)
}

func TestQuoteIdempotentReplay(t *testing.T) {
	b := newBT(t, false)
	q := b.quote(b.contact, "")
	h := map[string]string{"Idempotency-Key": "send-" + rnd()}
	in := map[string]any{"id": q}
	r1 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "quote.send", in, h)
	r2 := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "quote.send", in, h)
	if r1.status != 200 || string(r1.body) != string(r2.body) {
		t.Fatalf("replay: %d %s / %d %s", r1.status, r1.body, r2.status, r2.body)
	}
	eq(t, b.dbStrings(b.owner.ws, "select next_value::text from billing.sequences where name like 'quote:%'"), []string{"2"})
	eq(t, count(b.events(q), "quote.sent"), 1)
}
