package server_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Leads and follow-ups are Go-only except deal.* (ported, byte-compatible); fixtures come from Rust.
func (b *bt) form() string {
	f := b.ok("inquiry_form.set", map[string]any{"space_id": b.space, "owner_id": b.owner.userID})
	return f["token"].(string)
}

// submit posts to the public endpoint from a made-up client address (the rate limit is per address).
func (b *bt) submit(token, ip string, body map[string]any) resp {
	return b.env.do(b.gosv.URL, "POST", "/api/public/inquiry/"+token, who{}, body, map[string]string{"X-Forwarded-For": ip})
}

func (b *bt) dealRow(id string) map[string]any {
	for _, r := range b.okList("followup.list", map[string]any{"note_id": id}) {
		return r.(map[string]any)
	}
	b.t.Fatalf("deal %s not in followup.list", id)
	return nil
}

func (b *bt) newDeal(title string) string {
	return b.ok("deal.create", map[string]any{"space_id": b.space, "title": title, "contact_id": b.contact})["id"].(string)
}

func TestInquiryFormCreatesContactAndLeadOnce(t *testing.T) {
	b := newBT(t, false)
	tok := b.form()
	ip := "10.1.0.1"
	r := b.submit(tok, ip, map[string]any{"name": "Asha Rao", "phone": "+91 98765 43210", "email": "Asha@Example.test", "message": "Need a site visit Friday"})
	if r.status != 200 {
		t.Fatalf("submit: %d %s", r.status, r.body)
	}
	// the same person in other spellings matches the same contact, each submission is a new lead
	for _, p := range []string{"09876543210", "9876543210", "91-98765-43210"} {
		if r := b.submit(tok, ip, map[string]any{"name": "Asha", "phone": p, "message": "again"}); r.status != 200 {
			t.Fatalf("submit %s: %d %s", p, r.status, r.body)
		}
	}
	contacts := b.okList("contact.list", map[string]any{"q": "Asha"})
	eq(t, len(contacts), 1)
	c := contacts[0].(map[string]any)
	eq(t, c["phone"], "+919876543210")
	eq(t, c["email"], "asha@example.test")
	inbox := b.okList("followup.list", map[string]any{"due": "inbox"})
	eq(t, len(inbox), 4)
	row := inbox[0].(map[string]any)
	eq(t, row["source"], "form")
	eq(t, row["stage"], "lead")
	eq(t, row["contact_id"], c["id"])
	eq(t, row["owner_id"], b.owner.userID)
	acts := b.okList("activity.list", map[string]any{"contact_id": c["id"]})
	eq(t, len(acts), 4)
	eq(t, acts[len(acts)-1].(map[string]any)["summary"], "Need a site visit Friday")
	// consent is stored once, on the contact; the home tile counts the inquiries
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from crm.contacts where consent_at is not null and phone='+919876543210'"), []string{"1"})
	eq(t, tile(t, b.home(b.owner), "new_inquiries")["count"], float64(4))

	// honeypot and unknown/rotated/disabled tokens
	eq(t, b.submit(tok, ip, map[string]any{"name": "Bot", "phone": "9123456780", "hp": "http://spam"}).status, 200)
	eq(t, len(b.okList("contact.list", map[string]any{"q": "Bot"})), 0)
	eq(t, b.submit("nope", ip, map[string]any{"name": "X", "phone": "9123456780"}).status, 404)
	b.ok("inquiry_form.set", map[string]any{"enabled": false})
	eq(t, b.submit(tok, ip, map[string]any{"name": "Late", "phone": "9123456780"}).status, 404)
	b.ok("inquiry_form.set", map[string]any{"enabled": true, "rotate": true})
	eq(t, b.submit(tok, ip, map[string]any{"name": "Old", "phone": "9123456780"}).status, 404)
	eq(t, len(b.okList("contact.list", map[string]any{"q": "Old"})), 0)
}

func TestInquiryFormValidationAndRateLimit(t *testing.T) {
	b := newBT(t, false)
	tok := b.form()
	g := b.env.do(b.gosv.URL, "GET", "/api/public/inquiry/"+tok, who{}, nil, nil)
	eq(t, g.status, 200)
	for _, bad := range []map[string]any{
		{"name": "", "phone": "9876543210"}, {"name": "A", "phone": "12345"}, {"name": "A", "phone": "9876543210", "email": "nope"},
		{"name": strings.Repeat("x", 101), "phone": "9876543210"}, {"name": "A", "phone": "9876543210", "message": strings.Repeat("m", 2001)},
	} {
		if r := b.submit(tok, "10.1.0.2", bad); r.status != 400 {
			t.Fatalf("%v: %d %s", bad, r.status, r.body)
		}
	}
	eq(t, len(b.okList("followup.list", map[string]any{"due": "inbox"})), 0)
	for i := 0; i < 14; i++ { // 10 per address per 10 minutes; the rest are dropped with the same 200
		eq(t, b.submit(tok, "10.1.0.3", map[string]any{"name": fmt.Sprintf("Flood %d", i), "phone": fmt.Sprintf("98765%05d", i)}).status, 200)
	}
	eq(t, len(b.okList("followup.list", map[string]any{"due": "inbox"})), 10)
	other := b.env.tenant(false) // a form never writes into another workspace
	eq(t, len(other.okList("followup.list", map[string]any{"due": "inbox"})), 0)
}

func TestFollowUpPlanningListsAndFirstResponse(t *testing.T) {
	b := newBT(t, false)
	late, today, soon, far, none := b.newDeal("Late"), b.newDeal("Today"), b.newDeal("Soon"), b.newDeal("Far"), b.newDeal("Unplanned")
	b.ok("followup.set", map[string]any{"id": late, "next_follow_up": day(-3), "next_step": "Call back", "source": "referral"})
	b.ok("followup.set", map[string]any{"id": today, "next_follow_up": day(0)})
	b.ok("followup.set", map[string]any{"id": soon, "next_follow_up": day(5)})
	b.ok("followup.set", map[string]any{"id": far, "next_follow_up": day(20)})
	ids := func(due string) []string {
		out := []string{}
		for _, r := range b.okList("followup.list", map[string]any{"due": due}) {
			out = append(out, r.(map[string]any)["id"].(string))
		}
		return out
	}
	eq(t, ids("overdue"), []string{late})
	eq(t, ids("today"), []string{today})
	eq(t, ids("week"), []string{soon})
	l := b.dealRow(late)
	eq(t, l["next_step"], "Call back")
	eq(t, l["source"], "referral")
	eq(t, l["days_overdue"], float64(3))
	eq(t, ids("")[:4], []string{late, today, soon, far}) // by date, unplanned last
	eq(t, len(ids("inbox")), 5)

	// a private note does not answer the lead; a call does, and its next step replaces the plan
	b.ok("activity.log", map[string]any{"note_id": none, "kind": "note", "summary": "internal"})
	eq(t, len(ids("inbox")), 5)
	b.ok("activity.log", map[string]any{"note_id": late, "kind": "call", "summary": "wants site visit Friday", "next_follow_up": day(2), "next_step": "Site visit"})
	eq(t, len(ids("inbox")), 4)
	eq(t, ids("overdue"), []string{})
	r := b.dealRow(late)
	eq(t, r["next_follow_up"], day(2))
	eq(t, r["next_step"], "Site visit")
	if r["first_response_at"] == nil {
		t.Fatal("first_response_at not set")
	}
	// logging without a plan keeps the plan; null clears the date; empty step clears the step
	b.ok("activity.log", map[string]any{"note_id": late, "kind": "whatsapp", "summary": "sent location"})
	eq(t, b.dealRow(late)["next_follow_up"], day(2))
	b.ok("followup.set", map[string]any{"id": late, "next_follow_up": nil, "next_step": ""})
	r = b.dealRow(late)
	eq(t, r["next_follow_up"], nil)
	eq(t, r["next_step"], nil)
	acts := b.okList("activity.list", map[string]any{"note_id": late})
	eq(t, len(acts), 2)
	eq(t, acts[0].(map[string]any)["kind"], "whatsapp") // newest first

	// owner filter, backdated entry, and the events
	eq(t, len(b.okList("followup.list", map[string]any{"owner_id": b.owner.userID})), 5)
	eq(t, len(b.okList("followup.list", map[string]any{"owner_id": "00000000-0000-4000-8000-000000000000"})), 0)
	b.ok("activity.log", map[string]any{"note_id": today, "kind": "visit", "summary": "met on site", "happened_at": "2026-01-02T10:00:00+05:30"})
	eq(t, b.okList("activity.list", map[string]any{"note_id": today})[0].(map[string]any)["happened_at"], "2026-01-02T04:30:00Z")
	kinds := b.dbStrings(b.owner.ws, "select distinct kind from core.events where kind in ('activity.logged','deal.follow_up_set') order by 1")
	eq(t, kinds, []string{"activity.logged", "deal.follow_up_set"})
}

func TestFollowUpInvalidInput(t *testing.T) {
	b := newBT(t, false)
	d := b.newDeal("D")
	for cmd, in := range map[string]map[string]any{
		"followup.set": {"id": d, "next_follow_up": "31/12/2026"},
		"activity.log": {"note_id": d, "kind": "telepathy", "summary": "x"},
	} {
		b.errIs(cmd, in, 400, "invalid")
	}
	b.errIs("followup.set", map[string]any{"id": d}, 400, "invalid")
	b.errIs("followup.set", map[string]any{"id": d, "next_follow_up": "2026-02-30"}, 400, "invalid")
	b.errIs("followup.set", map[string]any{"id": "nope", "next_follow_up": day(1)}, 400, "invalid")
	b.errIs("activity.log", map[string]any{"note_id": d, "kind": "call", "summary": "  "}, 400, "invalid")
	b.errIs("activity.log", map[string]any{"note_id": d, "kind": "call", "summary": "x", "happened_at": "2099-01-01T00:00:00Z"}, 400, "invalid")
	b.errIs("activity.log", map[string]any{"note_id": b.contact, "kind": "call", "summary": "x", "next_follow_up": day(1)}, 400, "invalid")
	plain := b.note(b.owner, "just a page")
	b.errIs("activity.log", map[string]any{"note_id": plain, "kind": "call", "summary": "x"}, 400, "invalid")
	b.errIs("activity.list", map[string]any{}, 400, "invalid")
	b.errIs("activity.list", map[string]any{"note_id": d, "contact_id": b.contact}, 400, "invalid")
	b.errIs("followup.list", map[string]any{"due": "someday"}, 400, "invalid")
	b.errIs("inquiry_form.set", map[string]any{"enabled": true}, 400, "invalid") // first call needs a space
	b.errIs("inquiry_form.set", map[string]any{"space_id": b.space, "owner_id": b.env.signup("Stranger", false).userID}, 400, "invalid")
	// a closed deal has no follow-up
	b.ok("deal.move_stage", map[string]any{"id": d, "stage": "won"})
	b.errIs("followup.set", map[string]any{"id": d, "next_follow_up": day(1)}, 400, "invalid")
	eq(t, len(b.okList("followup.list", map[string]any{})), 0)
	b.ok("activity.log", map[string]any{"note_id": d, "kind": "note", "summary": "thank you sent"}) // history still works
}

func TestLostReasonRequiredByDealLoseOnly(t *testing.T) {
	b := newBT(t, false)
	d, plain := b.newDeal("Lose me"), b.newDeal("Old way")
	b.ok("followup.set", map[string]any{"id": d, "next_follow_up": day(1), "next_step": "Chase"})
	b.errIs("deal.lose", map[string]any{"id": d}, 400, "invalid")
	b.errIs("deal.lose", map[string]any{"id": d, "lost_reason": "  "}, 400, "invalid")
	out := b.ok("deal.lose", map[string]any{"id": d, "lost_reason": "Price too high"})
	eq(t, out["stage"], "lost")
	eq(t, out["probability"], float64(0))
	eq(t, b.dbStrings(b.owner.ws, "select lost_reason||'|'||coalesce(next_follow_up::text,'-') from crm.deals where note_id='"+d+"'"), []string{"Price too high|-"})
	eq(t, len(b.okList("followup.list", map[string]any{})), 1) // only the other deal is open
	// the Rust-compatible path is unchanged: moving to lost without a reason still works and the output shape is the same
	eq(t, b.ok("deal.move_stage", map[string]any{"id": plain, "stage": "lost"})["stage"], "lost")
	if _, ok := out["lost_reason"]; ok {
		t.Fatalf("deal output must stay byte-compatible: %v", out)
	}
}

func (b *bt) list(w who, cmd string, in any) []any {
	b.t.Helper()
	r := b.call(w, cmd, in)
	if r.status != 200 {
		b.t.Fatalf("%s: %d %s", cmd, r.status, r.body)
	}
	var v []any
	if err := json.Unmarshal(r.body, &v); err != nil {
		b.t.Fatal(err)
	}
	return v
}

func TestLeadsRolesAgentsAndTenants(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	d := b.newDeal("Shared")
	in := map[string]any{"note_id": d, "kind": "call", "summary": "hello"}
	b.errAs(guest, "activity.log", in, 403, "forbidden")
	b.errAs(guest, "followup.set", map[string]any{"id": d, "next_follow_up": day(1)}, 403, "forbidden")
	b.errAs(guest, "followup.list", map[string]any{}, 403, "forbidden")
	b.errAs(guest, "deal.lose", map[string]any{"id": d, "lost_reason": "x"}, 403, "forbidden")
	eq(t, len(b.list(guest, "activity.list", map[string]any{"note_id": d})), 0) // not shared with this guest
	if r := b.call(who{}, "followup.list", map[string]any{}); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}
	b.okAs(member, "activity.log", in)
	eq(t, len(b.list(member, "activity.list", map[string]any{"note_id": d})), 1)
	eq(t, len(b.list(member, "followup.list", map[string]any{})), 1)
	b.errAs(member, "inquiry_form.set", map[string]any{"space_id": b.space}, 403, "forbidden")
	b.errAs(member, "inquiry_form.get", map[string]any{}, 403, "forbidden")

	// agents and API keys: manage_crm is on by default, the admin group (form settings) is not
	created := b.must(b.owner, "agent.create", map[string]any{"name": "Sales", "scopes": []string{"read", "manage_crm", "admin"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	key := b.env.apiKey(b.owner)
	for _, w := range []who{agent, key} {
		r := b.okAs(w, "activity.log", map[string]any{"note_id": d, "kind": "email", "summary": "quote sent"})
		if r["confirmation_required"] != nil {
			t.Fatalf("logging an activity must not need a human: %v", r)
		}
		b.errAs(w, "inquiry_form.set", map[string]any{"space_id": b.space}, 403, "forbidden")
	}
	eq(t, len(b.okList("activity.list", map[string]any{"note_id": d})), 3)

	// idempotent replay writes once
	h := map[string]string{"Idempotency-Key": "log-1"}
	for i := 0; i < 2; i++ {
		if r := b.env.cmd(b.gosv.URL, b.owner, b.owner.ws, "activity.log", map[string]any{"note_id": d, "kind": "visit", "summary": "once"}, h); r.status != 200 {
			t.Fatalf("replay %d: %d %s", i, r.status, r.body)
		}
	}
	eq(t, len(b.okList("activity.list", map[string]any{"note_id": d})), 4)

	// another workspace can neither see nor write to these rows
	o := b.env.tenant(false)
	o.errIs("activity.log", in, 404, "not_found")
	o.errIs("followup.set", map[string]any{"id": d, "next_follow_up": day(1)}, 404, "not_found")
	o.errIs("deal.lose", map[string]any{"id": d, "lost_reason": "x"}, 404, "not_found")
	eq(t, len(o.okList("activity.list", map[string]any{"note_id": d})), 0)
	eq(t, len(o.okList("followup.list", map[string]any{})), 0)
	eq(t, len(o.okList("followup.list", map[string]any{"note_id": d})), 0)
}

func TestFollowUpsOnHomeAndCustomer360(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	late, today, later := b.newDeal("Late"), b.newDeal("Today"), b.newDeal("Later")
	b.ok("followup.set", map[string]any{"id": late, "next_follow_up": day(-2), "next_step": "Send revised quote"})
	b.ok("followup.set", map[string]any{"id": today, "next_follow_up": day(0)})
	b.ok("followup.set", map[string]any{"id": later, "next_follow_up": day(9)})
	h := b.home(b.owner)
	eq(t, tile(t, h, "follow_ups")["count"], float64(2))
	eq(t, tile(t, h, "follow_ups")["overdue"], float64(1))
	var reasons []string
	for _, a := range h["attention"].([]any) {
		if m := a.(map[string]any); m["id"] == late {
			reasons = append(reasons, m["reason"].(string), m["severity"].(string))
		}
	}
	eq(t, reasons, []string{"Follow-up overdue by 2 days: Send revised quote", "overdue"})
	// a member sees only deals they own; a guest sees no CRM tiles at all
	eq(t, tile(t, b.home(member), "follow_ups")["count"], float64(0))
	noTile(t, b.home(guest), "follow_ups")
	// closing a deal takes it off the list
	b.ok("deal.move_stage", map[string]any{"id": late, "stage": "won"})
	eq(t, tile(t, b.home(b.owner), "follow_ups")["overdue"], float64(0))

	// customer 360 carries the contact's activities (its deals' too) and open follow-ups
	b.ok("activity.log", map[string]any{"note_id": today, "kind": "call", "summary": "wants Friday visit"})
	b.ok("activity.log", map[string]any{"note_id": b.contact, "kind": "whatsapp", "summary": "shared brochure"})
	l := b.ok("contact.lifecycle", map[string]any{"id": b.contact})
	eq(t, len(l["activities"].([]any)), 2)
	eq(t, len(l["follow_ups"].([]any)), 2) // today + later; late is won
}
