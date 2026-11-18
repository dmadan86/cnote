package server_test

import (
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"testing"
)

// parseMail splits a raw message from the SMTP sink into its decoded subject, From header, Message-ID, text and html parts.
func parseMail(t *testing.T, raw string) (subject, from, id, text, html string) {
	t.Helper()
	m, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ = new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	from, id = m.Header.Get("From"), m.Header.Get("Message-ID")
	mt, params, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if !strings.HasPrefix(mt, "multipart/") {
		b, _ := io.ReadAll(m.Body)
		return subject, from, id, string(b), ""
	}
	r := multipart.NewReader(m.Body, params["boundary"])
	for i := 0; ; i++ {
		p, err := r.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		if i == 0 {
			text = string(b)
		} else {
			html = string(b)
		}
	}
	return
}

func lastMail(t *testing.T, s *smtpSink) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.msgs) == 0 {
		t.Fatal("no mail")
	}
	return s.msgs[len(s.msgs)-1]
}

func linkCount(p *pl, note string) string {
	return p.dbStrings(p.owner.ws, "select count(*)::text from core.public_links where note_id=$1::uuid", note)[0]
}

func TestShareEmailPrecedenceAndSafety(t *testing.T) {
	p := newPL(t)
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "Acme Billing <billing@acme.example.test>")
	t.Setenv("PUBLIC_WEB_URL", "https://app.example.test")
	send := func(extra map[string]any) (subject, text, html string) {
		in := map[string]any{"note_id": p.invID, "to": "customer@example.test", "origin": "https://evil.example.test"}
		for k, v := range extra {
			in[k] = v
		}
		p.ok("share.email", in)
		raw := lastMail(t, sink)
		subject, from, _, text, html := parseMail(t, raw)
		if !strings.Contains(from, " via Acme Billing") || !strings.Contains(from, "<billing@acme.example.test>") {
			t.Fatalf("from %q", from)
		}
		// origin from the caller is ignored for the emailed link
		if strings.Contains(raw, "evil.example.test") || !strings.Contains(text, "https://app.example.test/p/") {
			t.Fatalf("link origin: %q", text)
		}
		return
	}
	// 4. code default
	subject, text, html := send(nil)
	if !strings.HasPrefix(subject, "Invoice ") || !strings.Contains(text, "Thank you for your business") || html == "" {
		t.Fatalf("default: %q %q", subject, text)
	}
	// 3. legacy settings.share_message beats the code default
	p.ok("workspace.update", map[string]any{"settings": map[string]any{"share_message": "LEGACY words for {number}: {link}"}})
	if _, text, _ = send(nil); !strings.Contains(text, "LEGACY words for ") || strings.Count(text, "/p/") != 1 {
		t.Fatalf("legacy: %q", text)
	}
	// 2. a workspace template beats the legacy wording
	p.sqlArgs(`insert into core.email_templates(workspace_id,event,subject,body) values ($1::uuid,'invoice.sent','TPL {{invoice.number}}','TEMPLATE body {{invoice.number}} {{invoice.link}}')`, p.owner.ws)
	subject, text, _ = send(nil)
	if !strings.HasPrefix(subject, "TPL ") || !strings.Contains(text, "TEMPLATE body") || strings.Contains(text, "LEGACY") {
		t.Fatalf("template: %q %q", subject, text)
	}
	// 1. the per-send message beats the template; the link is appended when missing; an unknown "{{x}}" stays literal
	subject, text, _ = send(map[string]any{"message": "Typed by hand {{oops}}", "subject": "Rs ₹ due"})
	if subject != "Rs ₹ due" || !strings.Contains(text, "Typed by hand") || strings.Contains(text, "TEMPLATE") || !strings.Contains(text, "/p/") {
		t.Fatalf("message: %q %q", subject, text)
	}
	// ₹ survives the round trip in the body too
	if !strings.Contains(text, "{{oops}}") {
		t.Fatalf("%q", text)
	}
}

func TestShareEmailReminders(t *testing.T) {
	p := newPL(t)
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "billing@acme.example.test")
	in := func(lvl int) map[string]any {
		return map[string]any{"note_id": p.invID, "to": "customer@example.test", "reminder_level": lvl}
	}
	p.ok("share.email", map[string]any{"note_id": p.invID, "to": "customer@example.test", "reminder": true})
	if s, _, _, _, _ := parseMail(t, lastMail(t, sink)); !strings.Contains(s, "Friendly reminder") {
		t.Fatalf("reminder:true is level 1: %q", s)
	}
	p.ok("share.email", in(2))
	if s, _, _, _, _ := parseMail(t, lastMail(t, sink)); !strings.Contains(s, "Second reminder") {
		t.Fatalf("level 2: %q", s)
	}
	p.errIs("share.email", in(4), 400, "invalid")
	p.errIs("share.email", map[string]any{"note_id": p.quoteID, "to": "customer@example.test", "reminder_level": 1}, 400, "invalid")
	// a paid invoice cannot be chased
	p.sqlArgs("update billing.invoices set status='paid', paid_minor=total_minor where note_id=$1::uuid", p.invID)
	before := linkCount(p, p.invID)
	p.errIs("share.email", in(1), 400, "invalid")
	eq(t, linkCount(p, p.invID), before)
}

func TestSharePreviewCreatesNoLink(t *testing.T) {
	p := newPL(t)
	t.Setenv("PUBLIC_WEB_URL", "https://app.example.test")
	r := p.ok("share.preview", map[string]any{"note_id": p.invID, "reminder_level": 3})
	eq(t, r["event"], "invoice.reminder_3")
	if !strings.Contains(r["text"].(string), "(link created when you send)") || r["html"] == "" || r["subject"] == "" {
		t.Fatalf("%v", r)
	}
	eq(t, linkCount(p, p.invID), "0")
	eq(t, len(linkEvents(p.bt, p.invID)), 0)
	q := p.ok("share.preview", map[string]any{"note_id": p.quoteID})
	eq(t, q["event"], "quote.sent")
	p.errIs("share.preview", map[string]any{"note_id": p.quoteID, "reminder_level": 1}, 400, "invalid")
}

func TestSharePreviewBodyAndEditedMessage(t *testing.T) {
	p := newPL(t)
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "billing@acme.example.test")
	t.Setenv("PUBLIC_WEB_URL", "https://app.example.test")
	p.ok("workspace.update", map[string]any{"settings": map[string]any{"email_signature": "UNIQUE-SIGNOFF", "email_footer": "UNIQUE-FOOTER"}})
	pv := p.ok("share.preview", map[string]any{"note_id": p.invID})
	body := pv["body"].(string)
	if !strings.Contains(body, "Thank you for your business") || !strings.Contains(body, "(link created when you send)") || strings.Contains(body, "UNIQUE-") || strings.Contains(body, "{{") {
		t.Fatalf("body %q", body)
	}
	// preview takes unsaved edits
	pv = p.ok("share.preview", map[string]any{"note_id": p.invID, "subject": "EDITED SUBJ", "message": body + " EDITED"})
	if pv["subject"] != "EDITED SUBJ" || !strings.Contains(pv["html"].(string), "EDITED") {
		t.Fatalf("%v", pv)
	}
	// sending the edited body: sign-off and footer exactly once, marker replaced by the real link
	p.ok("share.email", map[string]any{"note_id": p.invID, "to": "customer@example.test", "message": body + " EDITED"})
	_, _, _, text, html := parseMail(t, lastMail(t, sink))
	if strings.Count(text, "UNIQUE-SIGNOFF") != 1 || strings.Count(text, "UNIQUE-FOOTER") != 1 || strings.Count(html, "UNIQUE-SIGNOFF") != 1 || strings.Count(html, "UNIQUE-FOOTER") != 1 {
		t.Fatalf("signature/footer not exactly once: %q", text)
	}
	if strings.Contains(text, "link created when you send") || strings.Count(text, "https://app.example.test/p/") != 1 || !strings.Contains(text, "EDITED") {
		t.Fatalf("link marker: %q", text)
	}
}
