package server_test

import (
	"strings"
	"testing"
)

// Customer messages: email_template.* commands.

func TestEmailTemplateRolesAndVersions(t *testing.T) {
	b := newBT(t, false)
	member, guest := b.memberAs("Mem", "member"), b.memberAs("Gue", "guest")
	save := map[string]any{"event": "invoice.sent", "subject": "Invoice {{invoice.number}}", "body": "Hi {{contact.name}}", "version": 0}
	for name, w := range map[string]who{"member": member, "guest": guest} {
		for cmd, in := range map[string]map[string]any{"email_template.list": {}, "email_template.save": save,
			"email_template.reset": {"event": "invoice.sent", "version": 0}, "email_template.test": {"event": "invoice.sent"},
			"email_template.preview": {"event": "invoice.sent"}} {
			if r := b.call(w, cmd, in); r.status != 403 {
				t.Errorf("%s %s: want 403, got %d %s", name, cmd, r.status, r.body)
			}
		}
	}
	l := b.ok("email_template.list", map[string]any{})
	evs := l["events"].([]any)
	if len(evs) != 9 || evs[0].(map[string]any)["customised"] != false || evs[0].(map[string]any)["version"].(float64) != 0 {
		t.Fatalf("list %v", l)
	}
	// unknown or half-typed placeholders and sizes are refused
	for _, body := range []string{"Hi {{secret.key}}", "Hi {{ Customer name }}", "", strings.Repeat("x", 10001)} {
		in := map[string]any{"event": "invoice.sent", "subject": "s", "body": body, "version": 0}
		b.errIs("email_template.save", in, 400, "invalid")
	}
	b.errIs("email_template.save", map[string]any{"event": "nope", "subject": "s", "body": "b", "version": 0}, 400, "invalid")
	b.errIs("email_template.save", map[string]any{"event": "quote.sent", "subject": "{{invoice.number}}", "body": "b", "version": 0}, 400, "invalid")

	r := b.ok("email_template.save", save)["template"].(map[string]any)
	eq(t, r["version"], float64(1))
	// a second first-customisation (stale version 0) and a stale update both conflict
	b.errIs("email_template.save", save, 409, "conflict")
	save["version"] = 1
	eq(t, b.ok("email_template.save", save)["template"].(map[string]any)["version"], float64(2))
	b.errIs("email_template.save", save, 409, "conflict")
	b.errIs("email_template.reset", map[string]any{"event": "invoice.sent", "version": 1}, 409, "conflict")
	e0 := b.ok("email_template.list", map[string]any{})["events"].([]any)[0].(map[string]any)
	eq(t, e0["customised"], true)
	eq(t, e0["subject"], "Invoice {{invoice.number}}")
	eq(t, e0["default_subject"], "Invoice {{invoice.number}} from {{workspace.name}}")
	b.ok("email_template.reset", map[string]any{"event": "invoice.sent", "version": 2})
	e0 = b.ok("email_template.list", map[string]any{})["events"].([]any)[0].(map[string]any)
	// the version keeps counting across a reset, and an editor that loaded before the reset is stale
	eq(t, e0["customised"], false)
	eq(t, e0["version"], float64(3))
	eq(t, e0["subject"], e0["default_subject"])
	save["version"] = 2
	b.errIs("email_template.save", save, 409, "conflict")
	save["version"] = 0
	b.errIs("email_template.save", save, 409, "conflict")
	b.errIs("email_template.reset", map[string]any{"event": "invoice.sent", "version": 2}, 409, "conflict")
	save["version"] = 3
	eq(t, b.ok("email_template.save", save)["template"].(map[string]any)["version"], float64(4))
	b.ok("email_template.reset", map[string]any{"event": "invoice.sent", "version": 4})
}

func TestTicketReplyTemplateKeepsReplyText(t *testing.T) {
	b := newBT(t, false)
	b.errIs("email_template.save", map[string]any{"event": "ticket.reply", "subject": "s", "body": "No text here", "version": 0}, 400, "invalid")
	b.ok("email_template.save", map[string]any{"event": "ticket.reply", "subject": "[{{ticket.ref}}] {{ticket.subject}}", "body": "Hi,\n\n{{ticket.message}}", "version": 0})
}

func TestEmailTemplateCrossTenantIsolation(t *testing.T) {
	a := newBT(t, false)
	other := a.env.signup("Zed", true)
	a.ok("email_template.save", map[string]any{"event": "quote.sent", "subject": "A only", "body": "A only body", "version": 0})
	l := a.env.must(other, "email_template.list", map[string]any{})["events"].([]any)
	for _, e := range l {
		if e.(map[string]any)["customised"] != false || strings.Contains(e.(map[string]any)["subject"].(string), "A only") {
			t.Fatalf("leak %v", e)
		}
	}
	// the other workspace can save the same event independently
	a.env.must(other, "email_template.save", map[string]any{"event": "quote.sent", "subject": "Z", "body": "Z", "version": 0})
	eq(t, a.ok("email_template.list", map[string]any{})["events"].([]any)[6].(map[string]any)["subject"], "A only")
}

func TestEmailTemplatePreviewUsesRealRecordAndCreatesNoLink(t *testing.T) {
	b := newBT(t, false)
	p := b.ok("email_template.preview", map[string]any{"event": "invoice.sent"})
	eq(t, p["sample"], true)
	inv := b.draft(b.contact, 0)
	num := b.issue(inv)["number"].(string)
	p = b.ok("email_template.preview", map[string]any{"event": "invoice.sent", "subject": "Pay {{invoice.number}} {{contact.name}}"})
	eq(t, p["sample"], false)
	if !strings.HasPrefix(p["subject"].(string), "Pay "+num+" Buyer 29") || !strings.Contains(p["text"].(string), num) || !strings.Contains(p["html"].(string), "Content-Security-Policy") {
		t.Fatalf("%v", p)
	}
	eq(t, p["link_note"], "(link created when you send)")
	// explicit note, unsaved draft body, and a wrong kind
	p = b.ok("email_template.preview", map[string]any{"event": "invoice.reminder_2", "note_id": inv, "body": "Owed {{invoice.balance}} for {{invoice.days_overdue}} days"})
	if !strings.Contains(p["text"].(string), "Owed ₹") {
		t.Fatalf("%v", p["text"])
	}
	b.errIs("email_template.preview", map[string]any{"event": "quote.sent", "note_id": inv}, 400, "invalid")
	b.errIs("email_template.preview", map[string]any{"event": "invoice.sent", "body": "{{nope}}"}, 400, "invalid")
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.public_links"), []string{"0"})
	// a member who can read the page may preview it
	m := b.memberAs("Mem", "member")
	b.okAs(m, "email_template.preview", map[string]any{"event": "invoice.sent", "note_id": inv})
	// sender look: validated on company.update and used in the render
	b.errIs("company.update", map[string]any{"email_accent": "red"}, 400, "invalid")
	b.errIs("company.update", map[string]any{"email_reply_to": "a@b.test, c@d.test"}, 400, "invalid")
	b.ok("company.update", map[string]any{"email_from_name": "Acme Billing", "email_reply_to": "ar@acme.example.test", "email_accent": "#12ab34", "email_signature": "Warm regards\nTeam Acme"})
	p = b.ok("email_template.preview", map[string]any{"event": "invoice.sent"})
	if p["from_name"] != "Acme Billing" || p["reply_to"] != "ar@acme.example.test" || !strings.Contains(p["html"].(string), "#12ab34") || !strings.Contains(p["text"].(string), "Team Acme") {
		t.Fatalf("%v", p)
	}
	tk := b.ok("email_template.preview", map[string]any{"event": "ticket.reply"})
	eq(t, tk["reply_to"], "")
}

func TestEmailTemplateTestSendGoesOnlyToCallerAndIsRateLimited(t *testing.T) {
	b := newBT(t, false)
	b.errIs("email_template.test", map[string]any{"event": "invoice.sent"}, 400, "invalid") // SMTP not configured
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "billing@acme.example.test")
	key := b.env.apiKey(b.owner)
	b.errAs(key, "email_template.test", map[string]any{"event": "invoice.sent"}, 403, "forbidden")
	for i := 0; i < 4; i++ { // the failed attempt above used one of the 5
		r := b.ok("email_template.test", map[string]any{"event": "invoice.sent", "to": "victim@example.test", "subject": "Hello {{contact.name}}"})
		eq(t, r["sent_to"], b.owner.email)
	}
	b.errIs("email_template.test", map[string]any{"event": "invoice.sent"}, 429, "rate_limited")
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.msgs) != 4 {
		t.Fatalf("messages %d", len(sink.msgs))
	}
	for _, m := range sink.msgs {
		if !strings.Contains(m, "To: "+b.owner.email) || strings.Contains(m, "victim@example.test") || !strings.Contains(m, "Subject: [Test] Hello") {
			t.Fatalf("%s", m)
		}
	}
}
