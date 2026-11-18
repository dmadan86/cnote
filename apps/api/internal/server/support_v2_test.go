package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/modules/leads"
)

func masterKey(t *testing.T) {
	t.Setenv("CNOTE_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

// inbound turns email-in on and returns the signing secret.
func (b *bt) inbound() string {
	masterKey(b.t)
	return b.ok("ticket_inbound.rotate", map[string]any{"space_id": b.space})["secret"].(string)
}

// postMail sends one signed inbound email; ts and sig override the valid ones when non-empty.
func (b *bt) postMail(secret string, m map[string]any, ts, sig string) resp {
	raw, _ := json.Marshal(m)
	if ts == "" {
		ts = fmt.Sprint(time.Now().Unix())
	}
	if sig == "" {
		sig = leads.Sign(secret, ts, raw)
	}
	req, _ := http.NewRequest("POST", b.gosv.URL+"/api/public/ticket-mail/"+b.owner.ws, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if ts != "-" {
		req.Header.Set("X-Cnote-Timestamp", ts)
	}
	if sig != "-" {
		req.Header.Set("X-Cnote-Signature", sig)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	return resp{status: r.StatusCode, body: body}
}

func mail(from, subject, text, id string, extra map[string]any) map[string]any {
	m := map[string]any{"from": from, "to": "support@acme.example.test", "subject": subject, "text": text, "message_id": "<" + id + ">"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestTicketRefPerWorkspace(t *testing.T) {
	b := newBT(t, false)
	eq(t, b.ticket("A", nil)["ref"], "TKT-000001")
	eq(t, b.ticket("B", nil)["ref"], "TKT-000002")
	o := b.env.tenant(false)
	eq(t, o.ticket("C", nil)["ref"], "TKT-000001")
	eq(t, b.ok("ticket.get", map[string]any{"id": b.okList("ticket.list", map[string]any{})[0].(map[string]any)["id"]})["ref"], "TKT-000001")
}

func TestInboundSecretAndSignature(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	b.errAs(member, "ticket_inbound.rotate", map[string]any{"space_id": b.space}, 403, "forbidden")
	b.errAs(member, "ticket_channels.get", map[string]any{}, 403, "forbidden")
	masterKey(t)
	b.errIs("ticket_inbound.rotate", map[string]any{}, 400, "invalid") // first time needs a space
	b.errIs("ticket_inbound.rotate", map[string]any{"space_id": "00000000-0000-0000-0000-000000000000"}, 404, "not_found")
	sec := b.inbound()
	if len(sec) != 64 {
		t.Fatalf("secret %q", sec)
	}
	cfg := b.ok("ticket_channels.get", map[string]any{})
	eq(t, cfg["inbound_enabled"], true)
	if strings.Contains(fmt.Sprint(cfg), sec) {
		t.Fatal("the secret must not be readable again")
	}
	// encrypted at rest: the stored blob is not the secret
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from crm.ticket_channels where position(convert_to($1,'UTF8') in inbound_secret) > 0", sec), []string{"0"})

	m := mail("Ravi <ravi@cust.example.test>", "Cannot log in", "Help", "m1@cust", nil)
	now := time.Now().Unix()
	for name, c := range map[string][2]string{
		"missing timestamp": {"-", ""}, "missing signature": {"", "-"}, "bad signature": {"", strings.Repeat("0", 64)}, "not hex": {"", "zz"},
		"old timestamp": {fmt.Sprint(now - 600), ""}, "future timestamp": {fmt.Sprint(now + 600), ""}, "not a number": {"abc", "deadbeef"},
	} {
		if r := b.postMail(sec, m, c[0], c[1]); r.status != 401 {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
	}
	eq(t, len(b.okList("ticket.list", map[string]any{})), 0)
	// a replayed old request: the signature is valid for its own timestamp, but the timestamp is stale
	raw, _ := json.Marshal(m)
	old := fmt.Sprint(now - 3600)
	if r := b.postMail(sec, m, old, leads.Sign(sec, old, raw)); r.status != 401 {
		t.Fatalf("replay: %d", r.status)
	}
	// a body tampered after signing
	ts := fmt.Sprint(now)
	sig := leads.Sign(sec, ts, raw)
	if r := b.postMail(sec, mail("Ravi <ravi@cust.example.test>", "Other", "x", "m1@cust", nil), ts, sig); r.status != 401 {
		t.Fatalf("tamper: %d", r.status)
	}
	if r := b.postMail(sec, m, "", ""); r.status != 200 {
		t.Fatalf("good: %d %s", r.status, r.body)
	}
	// another workspace cannot be reached with this secret
	o := b.env.tenant(false)
	osec := o.inbound()
	raw2, _ := json.Marshal(mail("a@b.test", "s", "t", "m2@x", nil))
	ts2 := fmt.Sprint(time.Now().Unix())
	req, _ := http.NewRequest("POST", b.gosv.URL+"/api/public/ticket-mail/"+o.owner.ws, bytes.NewReader(raw2))
	req.Header.Set("X-Cnote-Timestamp", ts2)
	req.Header.Set("X-Cnote-Signature", leads.Sign(sec, ts2, raw2))
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 401 {
		t.Fatalf("cross-workspace secret: %d", r.StatusCode)
	}
	_ = osec
	eq(t, len(o.okList("ticket.list", map[string]any{})), 0)

	// rotation kills the old secret at once
	sec2 := b.ok("ticket_inbound.rotate", map[string]any{})["secret"].(string)
	if sec2 == sec {
		t.Fatal("rotate returns a new secret")
	}
	eq(t, b.postMail(sec, mail("Ravi <ravi@cust.example.test>", "Again", "x", "m3@cust", nil), "", "").status, 401)
	eq(t, b.postMail(sec2, mail("Ravi <ravi@cust.example.test>", "Again", "x", "m3@cust", nil), "", "").status, 200)
	// bad input after a good signature
	eq(t, b.postMail(sec2, mail("not-an-address", "s", "x", "m4@cust", nil), "", "").status, 400)
	eq(t, b.postMail(sec2, mail("a@b.test", "s", "x", "", nil), "", "").status, 400)
	eq(t, b.postMail(sec2, mail("a@b.test", "s", strings.Repeat("x", 300<<10), "m5@cust", nil), "", "").status, 400) // over 256 KB
	// switching it off
	b.ok("ticket_channels.set", map[string]any{"inbound_enabled": false})
	eq(t, b.postMail(sec2, mail("a@b.test", "s", "x", "m6@cust", nil), "", "").status, 401)
	b.errIs("ticket_channels.set", map[string]any{"inbound_enabled": true}, 400, "invalid")
	b.errIs("ticket_channels.set", map[string]any{}, 400, "invalid")
	eq(t, b.env.do(b.gosv.URL, "GET", "/api/public/ticket-mail/"+b.owner.ws, who{}, nil, nil).status, 405)
	eq(t, b.env.do(b.gosv.URL, "POST", "/api/public/ticket-mail/not-a-uuid", who{}, map[string]any{}, nil).status, 401)
}

func TestInboundThreadingDedupeAndContacts(t *testing.T) {
	b := newBT(t, false)
	sec := b.inbound()
	send := func(m map[string]any) map[string]any {
		r := b.postMail(sec, m, "", "")
		if r.status != 200 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		return r.json()
	}
	// a new customer: contact created, ticket assigned to whoever set email-in up, text only
	first := send(mail("Ravi Kumar <Ravi@Cust.example.test>", "Cannot log in", "Hello,\nI cannot log in.\n\nOn Mon, 3 Nov 2026 at 10:00, Support <s@acme.example.test> wrote:\n> old stuff\n> more old stuff", "m1@cust",
		map[string]any{"html": "<script>alert(1)</script><b>bold</b>"}))
	eq(t, first["created"], true)
	eq(t, first["ref"], "TKT-000001")
	tk := b.ok("ticket.get", map[string]any{"id": first["ticket_id"]})
	eq(t, tk["source"], "email")
	eq(t, tk["title"], "Cannot log in")
	eq(t, tk["contact_name"], "Ravi Kumar")
	eq(t, tk["assignee_id"], b.owner.userID)
	eq(t, len(b.okList("contact.list", map[string]any{"q": "Ravi"})), 1)
	eq(t, b.dbStrings(b.owner.ws, "select email from crm.contacts where kind='person' and email is not null"), []string{"ravi@cust.example.test"})
	msgs := b.okList("ticket.messages", map[string]any{"id": first["ticket_id"]})
	eq(t, len(msgs), 1)
	eq(t, msgs[0].(map[string]any)["body"], "Hello,\nI cannot log in.") // quoted history stripped, html never stored
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from crm.ticket_messages where body like '%script%' or body like '%old stuff%'"), []string{"0"})

	// dedupe: the same Message-ID again changes nothing
	again := send(mail("Ravi Kumar <ravi@cust.example.test>", "Cannot log in", "Hello", "m1@cust", nil))
	eq(t, again["duplicate"], true)
	eq(t, again["ticket_id"], first["ticket_id"])
	eq(t, len(b.okList("ticket.messages", map[string]any{"id": first["ticket_id"]})), 1)
	eq(t, len(b.okList("ticket.list", map[string]any{})), 1)

	// threading by In-Reply-To and by References (list and string)
	r1 := send(mail("ravi@cust.example.test", "Re: Cannot log in", "Still failing", "m2@cust", map[string]any{"in_reply_to": "<m1@cust>"}))
	eq(t, r1["created"], false)
	eq(t, r1["ticket_id"], first["ticket_id"])
	r2 := send(mail("ravi@cust.example.test", "Re: Cannot log in", "Third", "m3@cust", map[string]any{"references": []string{"<zzz@x>", "<m2@cust>"}}))
	eq(t, r2["ticket_id"], first["ticket_id"])
	r3 := send(mail("ravi@cust.example.test", "Re: Cannot log in", "Fourth", "m4@cust", map[string]any{"references": "<zzz@x> <m3@cust>"}))
	eq(t, r3["ticket_id"], first["ticket_id"])
	r4 := send(mail("ravi@cust.example.test", "Re: Cannot log in", "Fifth", "m5@cust", map[string]any{"in_reply_to": "<m4@cust>"}))
	eq(t, r4["ticket_id"], first["ticket_id"])
	eq(t, len(b.okList("ticket.messages", map[string]any{"id": first["ticket_id"]})), 5)
	eq(t, len(b.okList("ticket.list", map[string]any{})), 1)

	// somebody else quoting the ticket's number or Message-ID does not write into it
	s1 := send(mail("stranger@elsewhere.example.test", "Re: [TKT-000001] hi", "I am not the customer", "x1@el", map[string]any{"in_reply_to": "<m1@cust>"}))
	eq(t, s1["created"], true)
	if s1["ticket_id"] == first["ticket_id"] {
		t.Fatal("a stranger threaded onto someone else's ticket")
	}
	eq(t, len(b.okList("ticket.messages", map[string]any{"id": first["ticket_id"]})), 5)

	// forged From + the TKT tag, no thread headers: a new ticket, the victim's is untouched
	f := send(mail("ravi@cust.example.test", "Re: [TKT-000001] Cannot log in", "forged", "f1@evil", nil))
	eq(t, f["created"], true)
	if f["ticket_id"] == first["ticket_id"] {
		t.Fatal("subject tag threaded a message onto an existing ticket")
	}
	eq(t, len(b.okList("ticket.messages", map[string]any{"id": first["ticket_id"]})), 5)

	// a resolved ticket reopens when the customer writes again
	b.ok("ticket.update", map[string]any{"id": first["ticket_id"], "status": "resolved"})
	send(mail("ravi@cust.example.test", "Re: Cannot log in", "It broke again", "m6@cust", map[string]any{"in_reply_to": "m5@cust"}))
	eq(t, b.ok("ticket.get", map[string]any{"id": first["ticket_id"]})["status"], "open")

	// an address that is already a CRM contact is matched, not duplicated
	b.sqlArgs("update crm.contacts set email='Buyer@Corp.example.test' where note_id=$1::uuid", b.contact)
	c := send(mail("Someone <buyer@corp.example.test>", "Invoice question", "Hi", "c1@corp", nil))
	eq(t, b.ok("ticket.get", map[string]any{"id": c["ticket_id"]})["contact_id"], b.contact)
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from crm.contacts"), []string{"3"}) // seeded buyer, Ravi, stranger

	// a ticket's reference means nothing in another workspace, which has its own secret and tickets
	o := b.env.tenant(false)
	osec := o.inbound()
	other := o.postMail(osec, mail("ravi@cust.example.test", "Re: [TKT-000001] Cannot log in", "hello from another tenant", "m1@cust", map[string]any{"in_reply_to": "<m1@cust>"}), "", "")
	if other.status != 200 || other.json()["created"] != true {
		t.Fatalf("other tenant: %d %s", other.status, other.body)
	}
	eq(t, len(b.okList("ticket.messages", map[string]any{"id": first["ticket_id"]})), 6)

	// limits: a long subject and text are cut, never rejected; no subject gets a title
	long := send(mail("long@cust.example.test", strings.Repeat("s", 500), strings.Repeat("é", 30000), "l1@cust", nil))
	eq(t, len([]rune(b.ok("ticket.get", map[string]any{"id": long["ticket_id"]})["title"].(string))), 200)
	eq(t, b.dbStrings(b.owner.ws, "select length(body)::text from crm.ticket_messages where message_id='l1@cust'"), []string{"20000"})
	none := send(mail("none@cust.example.test", "", "", "n1@cust", nil))
	eq(t, b.ok("ticket.get", map[string]any{"id": none["ticket_id"]})["title"], "(no subject)")
}

func (b *bt) waitMails(s *smtpSink, n int) []string {
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		if len(s.msgs) >= n {
			out := append([]string(nil), s.msgs...)
			s.mu.Unlock()
			return out
		}
		s.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	b.t.Fatalf("expected %d mails", n)
	return nil
}

func TestTicketReplyEmailsCustomerNotes(t *testing.T) {
	b := newBT(t, false)
	tk := b.ticket("Printer offline", nil)["id"].(string)
	in := map[string]any{"id": tk, "body": "We are on it."}
	b.errIs("ticket.reply", in, 400, "invalid") // the contact has no email yet
	b.sqlArgs("update crm.contacts set email='cust@example.test' where note_id=$1::uuid", b.contact)
	b.errIs("ticket.reply", map[string]any{"id": tk, "body": " "}, 400, "invalid")
	b.errIs("ticket.reply", map[string]any{"id": b.contact, "body": "x"}, 404, "not_found")

	// SMTP not set up: the reply is stored and shown, flagged as not emailed
	r := b.ok("ticket.reply", in)
	eq(t, r["emailed"], false)
	eq(t, r["ref"], "TKT-000001")

	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "support@acme.example.test")
	b.ok("comment.add", map[string]any{"note_id": tk, "body": "INTERNAL: customer is rude"})
	r = b.ok("ticket.reply", map[string]any{"id": tk, "body": "Fixed now."})
	eq(t, r["emailed"], true)
	m := b.waitMails(sink, 1)[0]
	for _, w := range []string{"To: cust@example.test", "<support@acme.example.test>", "Subject: [TKT-000001] Printer offline", "Fixed now.", "TKT-000001"} {
		if !strings.Contains(m, w) {
			t.Fatalf("%q missing in %q", w, m)
		}
	}
	if strings.Contains(m, "INTERNAL") {
		t.Fatal("an internal note was emailed")
	}
	msgs := b.okList("ticket.messages", map[string]any{"id": tk})
	eq(t, len(msgs), 2)
	if strings.Contains(fmt.Sprint(msgs), "INTERNAL") {
		t.Fatal("an internal note is in the customer conversation")
	}
	eq(t, len(b.okList("comment.list", map[string]any{"note_id": tk})), 1)

	// the customer's reply quotes the agent mail's Message-ID and lands in the same ticket
	_, _, mid, _, html := parseMail(t, m)
	if html == "" || mid == "" {
		t.Fatalf("ticket reply should be branded multipart with a Message-ID: %q", m)
	}
	sec := b.inbound()
	rr := b.postMail(sec, mail("cust@example.test", "Re: [TKT-000001] Printer offline", "Thanks, works.", "cust-reply-1@cust", map[string]any{"in_reply_to": mid}), "", "")
	if rr.status != 200 || rr.json()["created"] != false || rr.json()["ticket_id"] != tk {
		t.Fatalf("reply did not thread: %d %s", rr.status, rr.body)
	}

	// a failing mail server saves nothing
	n := len(b.okList("ticket.messages", map[string]any{"id": tk}))
	sink.ln.Close()
	b.errIs("ticket.reply", map[string]any{"id": tk, "body": "lost"}, 400, "invalid")
	eq(t, len(b.okList("ticket.messages", map[string]any{"id": tk})), n)

	// roles, other workspace, agents and API keys
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	b.errAs(guest, "ticket.reply", in, 403, "forbidden")
	b.errAs(guest, "ticket.messages", map[string]any{"id": tk}, 404, "not_found")
	if r := b.call(who{}, "ticket.reply", in); r.status != 401 {
		t.Fatalf("no auth: %d", r.status)
	}
	b.okAs(member, "ticket.messages", map[string]any{"id": tk})
	o := b.env.tenant(false)
	o.errIs("ticket.reply", in, 404, "not_found")
	o.errIs("ticket.messages", map[string]any{"id": tk}, 404, "not_found")

	created := b.must(b.owner, "agent.create", map[string]any{"name": "Desk", "scopes": []string{"read", "manage_crm", "send_messages", "admin"}})
	agent := who{ws: b.owner.ws, bearer: created["api_key"].(string)}
	b.errAs(agent, "ticket.reply", in, 403, "forbidden") // send_messages is off for agents by default
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_crm", "send_messages", "admin"}}})
	before := b.dbStrings(b.owner.ws, "select count(*)::text from crm.ticket_messages")
	for _, w := range []who{agent, b.env.apiKey(b.owner)} {
		eq(t, b.okAs(w, "ticket.reply", in)["confirmation_required"], true)
		eq(t, b.okAs(w, "ticket_inbound.rotate", map[string]any{"space_id": b.space})["confirmation_required"], true)
	}
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from crm.ticket_messages"), before)
}

// portal drives the public portal over HTTP with an explicit cookie jar value.
type portal struct {
	b *bt
}

func (p portal) do(ws, method, action string, body any, cookie string) (resp, *http.Response) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, p.b.gosv.URL+"/api/public/portal/"+ws+"/"+action, rd)
	req.Header.Set("X-Forwarded-For", "10.9.9."+fmt.Sprint(time.Now().UnixNano()%250))
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		p.b.t.Fatal(err)
	}
	defer r.Body.Close()
	out, _ := io.ReadAll(r.Body)
	return resp{status: r.StatusCode, body: out}, r
}

// login asks for a link, reads it from the mail sink and trades it in; returns the cookie header.
func (p portal) login(ws, email string, sink *smtpSink, nth int) (token, cookie string) {
	r, _ := p.do(ws, "POST", "request", map[string]any{"email": email}, "")
	eq(p.b.t, r.status, 200)
	m := p.b.waitMails(sink, nth)[nth-1]
	i := strings.Index(m, "/verify#")
	if i < 0 {
		p.b.t.Fatalf("no link in %q", m)
	}
	token = strings.Fields(m[i+len("/verify#"):])[0]
	_, hr := p.do(ws, "POST", "verify", map[string]any{"token": token}, "")
	for _, c := range hr.Cookies() {
		if c.Name == "cn_portal_"+ws {
			return token, c.Name + "=" + c.Value
		}
	}
	return token, ""
}

func TestPortalMagicLinkTicketsAndIsolation(t *testing.T) {
	b := newBT(t, false)
	p := portal{b}
	ws := b.owner.ws
	b.sqlArgs("update crm.contacts set email='cust@example.test' where note_id=$1::uuid", b.contact)
	mine := b.ticket("Mine", nil)
	other := b.newContact("29")
	b.sqlArgs("update crm.contacts set email='other@example.test' where note_id=$1::uuid", other)
	theirs := b.ok("ticket.create", map[string]any{"title": "Not yours", "contact_id": other})
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "support@acme.example.test")
	b.ok("comment.add", map[string]any{"note_id": mine["id"], "body": "INTERNAL: do not show"})
	b.sqlArgs("update crm.tickets set description='STAFFNOTE: margin is thin' where note_id=$1::uuid", mine["id"]) // staff-written, source internal

	// off by default: nothing is sent, nothing is revealed
	r, _ := p.do(ws, "POST", "request", map[string]any{"email": "cust@example.test"}, "")
	eq(t, r.status, 200)
	r, _ = p.do(ws, "GET", "info", nil, "")
	eq(t, r.status, 404)
	b.errAs(b.addMember(b.owner, b.signup("M", false), "member"), "ticket_channels.set", map[string]any{"portal_enabled": true}, 403, "forbidden")
	b.ok("ticket_channels.set", map[string]any{"portal_enabled": true})
	r, _ = p.do(ws, "GET", "info", nil, "")
	eq(t, r.status, 200)
	time.Sleep(100 * time.Millisecond)
	sink.mu.Lock()
	eq(t, len(sink.msgs), 0)
	sink.mu.Unlock()

	// unknown address: the same 200, no mail
	r, _ = p.do(ws, "POST", "request", map[string]any{"email": "nobody@example.test"}, "")
	eq(t, r.status, 200)
	time.Sleep(100 * time.Millisecond)
	sink.mu.Lock()
	eq(t, len(sink.msgs), 0)
	sink.mu.Unlock()

	b.ok("ticket.reply", map[string]any{"id": mine["id"], "body": "We are looking."})
	token, cookie := p.login(ws, "Cust@Example.test", sink, 2) // mail 1 was the reply
	if cookie == "" {
		t.Fatal("no session cookie")
	}
	// hashed at rest, 15-minute expiry
	eq(t, b.dbStrings(ws, "select count(*)::text from crm.portal_tokens where token_hash=$1", token), []string{"0"})
	eq(t, b.dbStrings(ws, "select (expires_at - created_at = interval '15 minutes')::text from crm.portal_tokens where kind='login'"), []string{"true"})
	// single use
	r, _ = p.do(ws, "POST", "verify", map[string]any{"token": token}, "")
	eq(t, r.status, 401)

	// the portal shows this contact's ticket and public messages only
	r, _ = p.do(ws, "GET", "tickets", nil, cookie)
	eq(t, r.status, 200)
	body := string(r.body)
	for _, want := range []string{"TKT-000001", "Mine", "We are looking."} {
		if !strings.Contains(body, want) {
			t.Fatalf("%q missing in %s", want, body)
		}
	}
	for _, bad := range []string{"INTERNAL", "STAFFNOTE", "Not yours", "TKT-000002", "sla_due_at", "assignee"} {
		if strings.Contains(body, bad) {
			t.Fatalf("portal leaks %q: %s", bad, body)
		}
	}
	// reply from the portal; replies to somebody else's ticket number are 404
	r, _ = p.do(ws, "POST", "reply", map[string]any{"ref": 1, "body": "Thanks, still broken"}, cookie)
	eq(t, r.status, 200)
	r, _ = p.do(ws, "POST", "reply", map[string]any{"ref": 2, "body": "intrude"}, cookie)
	eq(t, r.status, 404)
	r, _ = p.do(ws, "POST", "reply", map[string]any{"ref": 1, "body": "  "}, cookie)
	eq(t, r.status, 400)
	eq(t, len(b.okList("ticket.messages", map[string]any{"id": mine["id"]})), 2)
	eq(t, len(b.okList("ticket.messages", map[string]any{"id": theirs["id"]})), 0)
	eq(t, b.dbStrings(ws, "select origin from crm.ticket_messages order by created_at desc limit 1"), []string{"customer_portal"})
	// resolved tickets reopen on a reply
	b.ok("ticket.update", map[string]any{"id": mine["id"], "status": "resolved"})
	r, _ = p.do(ws, "POST", "reply", map[string]any{"ref": 1, "body": "Not fixed"}, cookie)
	eq(t, r.status, 200)
	eq(t, b.ok("ticket.get", map[string]any{"id": mine["id"]})["status"], "open")

	// no cookie, a garbage cookie, a cookie from another workspace
	r, _ = p.do(ws, "GET", "tickets", nil, "")
	eq(t, r.status, 401)
	r, _ = p.do(ws, "GET", "tickets", nil, "cn_portal_"+ws+"=garbage")
	eq(t, r.status, 401)
	o := b.env.tenant(false)
	o.sqlArgs("update crm.contacts set email='cust@example.test' where note_id=$1::uuid", o.contact)
	o.ticket("Other tenant", nil)
	o.ok("ticket_channels.set", map[string]any{"portal_enabled": true})
	r, _ = p.do(o.owner.ws, "GET", "tickets", nil, strings.Replace(cookie, "cn_portal_"+ws, "cn_portal_"+o.owner.ws, 1))
	eq(t, r.status, 401)
	// and a login link from this workspace does not work against the other
	r, _ = p.do(ws, "POST", "request", map[string]any{"email": "cust@example.test"}, "")
	tok2 := func() string {
		m := b.waitMails(sink, 3)[2]
		return strings.Fields(m[strings.Index(m, "/verify#")+8:])[0]
	}()
	r, _ = p.do(o.owner.ws, "POST", "verify", map[string]any{"token": tok2}, "")
	eq(t, r.status, 401)

	// expiry: an unused link past 15 minutes and a session past 12 hours are dead
	b.adminSQL("update crm.portal_tokens set expires_at = now() - interval '1 second' where used_at is null")
	r, _ = p.do(ws, "POST", "verify", map[string]any{"token": tok2}, "")
	eq(t, r.status, 401)
	b.adminSQL("update crm.portal_tokens set expires_at = now() - interval '1 second' where kind='session'")
	r, _ = p.do(ws, "GET", "tickets", nil, cookie)
	eq(t, r.status, 401)

	// rate limit: three links per contact per 15 minutes, then silence (still a 200)
	b.adminSQL("delete from crm.portal_tokens")
	for i := 0; i < 5; i++ {
		r, _ = p.do(ws, "POST", "request", map[string]any{"email": "cust@example.test"}, "")
		eq(t, r.status, 200)
	}
	time.Sleep(200 * time.Millisecond)
	eq(t, b.dbStrings(ws, "select count(*)::text from crm.portal_tokens where kind='login'"), []string{"3"})

	// switching the portal off ends sessions
	b.adminSQL("delete from crm.portal_tokens")
	_, cookie = p.login(ws, "cust@example.test", sink, 7)
	r, _ = p.do(ws, "GET", "tickets", nil, cookie)
	eq(t, r.status, 200)
	b.ok("ticket_channels.set", map[string]any{"portal_enabled": false})
	r, _ = p.do(ws, "GET", "tickets", nil, cookie)
	eq(t, r.status, 401)
	r, _ = p.do(ws, "GET", "nope", nil, cookie)
	eq(t, r.status, 404)
}

func (b *bt) sqlArgs(sql string, args ...any) {
	b.t.Helper()
	adminSQL(b.env, b.owner.ws, sql, args...)
}

func TestTicketMessagesFollowSubjectExportAndErase(t *testing.T) {
	b := newBT(t, false)
	b.sqlArgs("update crm.contacts set email='cust@example.test' where note_id=$1::uuid", b.contact)
	id := b.ticket("Printer jam", nil)["id"].(string)
	b.ok("ticket.reply", map[string]any{"id": id, "body": "Public secret reply"})
	out, _ := json.Marshal(b.ok("data.export_subject", map[string]any{"contact_id": b.contact}))
	if !strings.Contains(string(out), "Public secret reply") {
		t.Fatal("export misses the customer-visible conversation")
	}
	b.ok("data.erase_subject", map[string]any{"contact_id": b.contact, "reason": "customer request"})
	eq(t, b.dbStrings(b.owner.ws, "select body from crm.ticket_messages"), []string{"[erased]"})
}

func TestPortalSignInIgnoresTicketReplyTemplate(t *testing.T) {
	b := newBT(t, false)
	ws := b.owner.ws
	b.sqlArgs("update crm.contacts set email='cust@example.test' where note_id=$1::uuid", b.contact)
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "support@acme.example.test")
	b.ticket("Mine", nil)
	b.ok("ticket_channels.set", map[string]any{"portal_enabled": true})
	// a stored body that lacks the reply text (the save command refuses this, so write it directly)
	b.sqlArgs(`insert into core.email_templates(workspace_id,event,subject,body) values ($1::uuid,'ticket.reply','CUSTOM SUBJECT','CUSTOM BODY ONLY')`, ws)
	// direct call: the HTTP route shares a process-wide per-IP limiter with the other portal test
	if err := leads.PortalRequestLink(context.Background(), b.pool, ws, "cust@example.test"); err != nil {
		t.Fatal(err)
	}
	m := b.waitMails(sink, 1)[0]
	if !strings.Contains(m, "/verify#") || strings.Contains(m, "CUSTOM BODY ONLY") || strings.Contains(m, "CUSTOM SUBJECT") {
		t.Fatalf("portal mail used the editable ticket.reply template: %q", m)
	}
}
