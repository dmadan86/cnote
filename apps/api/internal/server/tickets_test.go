package server_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func (b *bt) ticket(title string, extra map[string]any) map[string]any {
	in := map[string]any{"title": title, "contact_id": b.contact, "description": "It broke"}
	for k, v := range extra {
		in[k] = v
	}
	return b.ok("ticket.create", in)
}

func (b *bt) submitTicket(token, ip string, body map[string]any) resp {
	return b.env.do(b.gosv.URL, "POST", "/api/public/ticket/"+token, who{}, body, map[string]string{"X-Forwarded-For": ip})
}

func TestTicketLifecycleSLAAndReplies(t *testing.T) {
	b := newBT(t, false)
	tk := b.ticket("Printer offline", nil)
	eq(t, tk["status"], "open")
	eq(t, tk["priority"], "normal")
	eq(t, tk["contact_id"], b.contact)
	eq(t, tk["breached"], false)
	id := tk["id"].(string)
	eq(t, b.dbStrings(b.owner.ws, "select (sla_due_at - created_at = interval '24 hours')::text from crm.tickets"), []string{"true"})

	// per-workspace SLA hours apply to new tickets and when the priority changes
	eq(t, b.ok("ticket.sla_get", map[string]any{})["hours"].(map[string]any)["urgent"], float64(4))
	b.ok("ticket.sla_set", map[string]any{"hours": map[string]any{"urgent": 2}})
	up := b.ok("ticket.update", map[string]any{"id": id, "priority": "urgent", "assignee_id": b.owner.userID})
	eq(t, up["priority"], "urgent")
	eq(t, up["assignee_id"], b.owner.userID)
	eq(t, b.dbStrings(b.owner.ws, "select (sla_due_at - created_at = interval '2 hours')::text from crm.tickets"), []string{"true"})

	// replies are note comments
	b.ok("comment.add", map[string]any{"note_id": id, "body": "Looking into it"})
	eq(t, len(b.okList("comment.list", map[string]any{"note_id": id})), 1)

	// breach: past the due time while open or pending, not once resolved
	b.adminSQL("update crm.tickets set sla_due_at = now() - interval '1 hour'")
	eq(t, b.ok("ticket.get", map[string]any{"id": id})["breached"], true)
	eq(t, len(b.okList("ticket.list", map[string]any{"breached": true})), 1)
	eq(t, tile(t, b.home(b.owner), "tickets_breaching")["count"], float64(1))
	b.ok("ticket.update", map[string]any{"id": id, "status": "pending"})
	eq(t, tile(t, b.home(b.owner), "tickets_breaching")["count"], float64(1))
	r := b.ok("ticket.update", map[string]any{"id": id, "status": "resolved"})
	eq(t, r["breached"], false)
	if r["resolved_at"] == nil {
		t.Fatal("resolved_at not set")
	}
	eq(t, tile(t, b.home(b.owner), "tickets_breaching")["count"], float64(0))
	eq(t, len(b.okList("ticket.list", map[string]any{})), 0) // default filter is open + pending
	eq(t, len(b.okList("ticket.list", map[string]any{"status": "resolved"})), 1)
	eq(t, b.ok("ticket.update", map[string]any{"id": id, "status": "open"})["resolved_at"], nil)
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.events where kind in ('ticket.created','ticket.updated','ticket.resolved')"), []string{"6"})
}

func TestTicketListFiltersAndLinks(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	inv := b.invDue(day(3))["id"].(string)
	a := b.ticket("A", map[string]any{"priority": "high", "assignee_id": member.userID, "invoice_id": inv, "project_id": b.space})
	eq(t, a["invoice_id"], inv)
	eq(t, a["project_id"], b.space)
	b.ticket("B", map[string]any{"priority": "low"})
	eq(t, len(b.okList("ticket.list", map[string]any{"priority": "high"})), 1)
	eq(t, len(b.okList("ticket.list", map[string]any{"assignee_id": member.userID})), 1)
	eq(t, len(b.list(member, "ticket.list", map[string]any{"assignee_id": "me"})), 1)
	eq(t, len(b.okList("ticket.list", map[string]any{"contact_id": b.contact})), 2)
	// clearing a link with null
	eq(t, b.ok("ticket.update", map[string]any{"id": a["id"], "invoice_id": nil})["invoice_id"], nil)

	for _, bad := range []map[string]any{
		{"title": "", "contact_id": b.contact}, {"title": "x"}, {"title": "x", "contact_id": "nope"},
		{"title": "x", "contact_id": b.contact, "priority": "asap"}, {"title": "x", "contact_id": b.contact, "invoice_id": b.contact},
		{"title": "x", "contact_id": b.contact, "assignee_id": b.signup("Out", false).userID},
		{"title": strings.Repeat("t", 201), "contact_id": b.contact}, {"title": "x", "contact_id": a["id"]},
	} {
		if r := b.call(b.owner, "ticket.create", bad); r.status != 400 && r.status != 404 {
			t.Fatalf("%v: %d %s", bad, r.status, r.body)
		}
	}
	b.errIs("ticket.update", map[string]any{"id": a["id"]}, 400, "invalid")
	b.errIs("ticket.update", map[string]any{"id": a["id"], "status": "gone"}, 400, "invalid")
	b.errIs("ticket.update", map[string]any{"id": b.contact, "status": "open"}, 404, "not_found") // a contact is not a ticket
	b.errIs("ticket.list", map[string]any{"status": "x"}, 400, "invalid")
	b.errIs("ticket.sla_set", map[string]any{"hours": map[string]any{"urgent": 0}}, 400, "invalid")
	b.errIs("ticket.sla_set", map[string]any{"hours": map[string]any{"vip": 3}}, 400, "invalid")
}

func TestTicketRolesAgentsAndTenants(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	tk := b.ticket("Shared", nil)["id"]
	b.errAs(guest, "ticket.create", map[string]any{"title": "x", "contact_id": b.contact}, 403, "forbidden")
	b.errAs(guest, "ticket.update", map[string]any{"id": tk, "status": "closed"}, 403, "forbidden")
	eq(t, len(b.list(guest, "ticket.list", map[string]any{})), 0) // not shared with this guest
	if r := b.call(who{}, "ticket.list", map[string]any{}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}
	b.okAs(member, "ticket.update", map[string]any{"id": tk, "status": "pending"})
	b.errAs(member, "ticket.sla_set", map[string]any{"hours": map[string]any{"low": 10}}, 403, "forbidden")
	noTile(t, b.home(guest), "tickets_breaching")

	created := b.must(b.owner, "agent.create", map[string]any{"name": "Desk", "scopes": []string{"read", "manage_crm", "admin"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	key := b.env.apiKey(b.owner)
	for _, w := range []who{agent, key} {
		r := b.okAs(w, "ticket.create", map[string]any{"title": "From agent", "contact_id": b.contact})
		if r["confirmation_required"] != nil {
			t.Fatalf("a ticket must not need a human: %v", r)
		}
		b.errAs(w, "ticket.sla_set", map[string]any{"hours": map[string]any{"low": 10}}, 403, "forbidden")
	}
	eq(t, len(b.okList("ticket.list", map[string]any{})), 3)

	h := map[string]string{"Idempotency-Key": "tk-1"}
	for i := 0; i < 2; i++ {
		if r := b.env.cmd(b.gosv.URL, b.owner, b.owner.ws, "ticket.create", map[string]any{"title": "Once", "contact_id": b.contact}, h); r.status != 200 {
			t.Fatalf("replay %d: %d %s", i, r.status, r.body)
		}
	}
	eq(t, len(b.okList("ticket.list", map[string]any{})), 4)

	o := b.env.tenant(false)
	o.errIs("ticket.update", map[string]any{"id": tk, "status": "closed"}, 404, "not_found")
	o.errIs("ticket.get", map[string]any{"id": tk}, 404, "not_found")
	o.errIs("ticket.create", map[string]any{"title": "x", "contact_id": b.contact}, 404, "not_found")
	eq(t, len(o.okList("ticket.list", map[string]any{})), 0)
	eq(t, tile(t, o.home(o.owner), "tickets_breaching")["count"], float64(0))
}

func TestPublicTicketForm(t *testing.T) {
	b := newBT(t, false)
	tok := b.form()
	ip := "10.2.0.1"
	r := b.submitTicket(tok, ip, map[string]any{"name": "Ravi K", "phone": "98765 43210", "subject": "Wrong invoice", "message": "GST number is wrong"})
	if r.status != 200 {
		t.Fatalf("submit: %d %s", r.status, r.body)
	}
	l := b.okList("ticket.list", map[string]any{})
	eq(t, len(l), 1)
	tk := l[0].(map[string]any)
	eq(t, tk["title"], "Wrong invoice")
	eq(t, tk["source"], "form")
	eq(t, tk["description"], "GST number is wrong")
	eq(t, tk["assignee_id"], b.owner.userID)
	// the same person raising a second ticket reuses their contact
	b.submitTicket(tok, ip, map[string]any{"name": "Ravi", "phone": "09876543210", "subject": "Again", "message": "still wrong"})
	eq(t, len(b.okList("contact.list", map[string]any{"q": "Ravi"})), 1)
	eq(t, len(b.okList("ticket.list", map[string]any{})), 2)

	eq(t, b.submitTicket(tok, ip, map[string]any{"name": "Bot", "phone": "9123456780", "subject": "s", "message": "m", "hp": "x"}).status, 200)
	eq(t, len(b.okList("contact.list", map[string]any{"q": "Bot"})), 0)
	eq(t, b.env.do(b.gosv.URL, "GET", "/api/public/ticket/"+tok, who{}, nil, nil).status, 200)
	for _, bad := range []map[string]any{
		{"name": "A", "phone": "9876543210", "message": "m"}, {"name": "A", "phone": "9876543210", "subject": "s"},
		{"name": "A", "phone": "123", "subject": "s", "message": "m"}, {"name": "", "phone": "9876543210", "subject": "s", "message": "m"},
		{"name": "A", "phone": "9876543210", "subject": strings.Repeat("s", 201), "message": "m"},
	} {
		if r := b.submitTicket(tok, "10.2.0.2", bad); r.status != 400 {
			t.Fatalf("%v: %d %s", bad, r.status, r.body)
		}
	}
	for i := 0; i < 12; i++ { // 10 per address per 10 minutes, the rest silently dropped
		eq(t, b.submitTicket(tok, "10.2.0.3", map[string]any{"name": "F", "phone": "9876500001", "subject": "s", "message": "m"}).status, 200)
	}
	eq(t, len(b.okList("ticket.list", map[string]any{"contact_id": tk["contact_id"]})), 2)
	eq(t, b.submitTicket("nope", ip, map[string]any{}).status, 404)
	b.ok("inquiry_form.set", map[string]any{"enabled": false})
	eq(t, b.submitTicket(tok, ip, map[string]any{"name": "Late", "phone": "9123456780", "subject": "s", "message": "m"}).status, 404)
}

// DPDP: a contact's tickets and their replies are exported and scrubbed with the contact.
func TestTicketsFollowSubjectExportAndErase(t *testing.T) {
	b := newBT(t, false)
	id := b.ticket("Printer jam", map[string]any{"description": "Private detail"})["id"].(string)
	b.ok("comment.add", map[string]any{"note_id": id, "body": "Private reply"})
	out, _ := json.Marshal(b.ok("data.export_subject", map[string]any{"contact_id": b.contact}))
	for _, want := range []string{"Printer jam", "Private detail", "Private reply"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("export misses %q", want)
		}
	}
	r := b.ok("data.erase_subject", map[string]any{"contact_id": b.contact, "reason": "customer request"})
	eq(t, r["status"], "done")
	eq(t, b.dbStrings(b.owner.ws, "select description from crm.tickets where note_id = $1::uuid", id), []string{"[erased]"})
	eq(t, b.dbStrings(b.owner.ws, "select body from core.comments where note_id = $1::uuid", id), []string{"[erased]"})
}
