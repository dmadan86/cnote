package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/unsub"
)

// docs-parity-b 13.2.5: the outbox holds references; the mail is built and checked just before SMTP.

type mailFixture struct {
	smtp          *fakeSMTP
	tess, mo      who
	g             *goSide
	note, notifID string
}

func mailSetup(t *testing.T) mailFixture {
	smtp := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+smtp.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "C Note <noreply@example.test>")
	t.Setenv("PUBLIC_WEB_URL", "https://app.example.test/")
	t.Setenv("CNOTE_MASTER_KEY", "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=")
	_, tess, mo, g, note := mentionFixture(t)
	g.cmd("note.update", map[string]any{"id": note, "schema": 2, "expected_epoch": 1, "content_md": "secret body [@Mo](/w/" + tess.ws + "/people/" + mo.userID + ")"})
	g.drain(100)
	var id string
	if err := g.c.Tx.QueryRow(g.c.Context, "select id::text from core.notifications where note_id = $1::uuid and user_id = $2::uuid", note, mo.userID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return mailFixture{smtp, tess, mo, g, note, id}
}

// sweep runs the direct sweep as if four minutes had passed, then plays the worker over the mail job it queued.
func (f mailFixture) sweep(t *testing.T) {
	t.Helper()
	if err := directSweepTx(context.Background(), f.g.c.Tx, f.tess.ws, time.Now().Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.g.drain(100)
}

func (f mailFixture) mailsTo(email string) []smtpMsg {
	f.smtp.mu.Lock()
	defer f.smtp.mu.Unlock()
	var out []smtpMsg
	for _, m := range f.smtp.msgs {
		if m.to == email { // the shared sink also gets other tests' queued jobs
			out = append(out, m)
		}
	}
	return out
}

func (f mailFixture) state(t *testing.T) string {
	t.Helper()
	var s *string
	if err := f.g.c.Tx.QueryRow(f.g.c.Context, "select email_state from core.notifications where id = $1::uuid", f.notifID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s == nil {
		return ""
	}
	return *s
}

func unfold(m smtpMsg) string {
	d := strings.ReplaceAll(m.data, "\r\n", "\n")
	return strings.ReplaceAll(d, "=\n", "")
}

func TestMentionMailIsRebuiltAtSendTime(t *testing.T) {
	f := mailSetup(t)
	f.sweep(t)
	got := f.mailsTo(f.mo.email)
	if len(got) != 1 {
		t.Fatalf("want one mail to %s, got %d", f.mo.email, len(got))
	}
	m := unfold(got[0])
	head := strings.SplitN(m, "\n\n", 2)[0]
	for _, want := range []string{"Subject: Tess mentioned you in Plan", "List-Unsubscribe: <https://app.example.test/api/public/unsubscribe/", "List-Unsubscribe-Post: List-Unsubscribe=One-Click", "Message-ID: <"} {
		if !strings.Contains(head, want) {
			t.Fatalf("header %q missing in:\n%s", want, head)
		}
	}
	if !strings.Contains(m, "https://app.example.test/w/"+f.tess.ws+"/n/"+f.note) {
		t.Fatalf("no link to the page:\n%s", m)
	}
	if strings.Contains(m, "secret body") {
		t.Fatal("a mail never carries page text")
	}
	if f.state(t) != "mailed" {
		t.Fatalf("state %q", f.state(t))
	}
	// The link in the mail is the token for this person and this kind.
	i := strings.Index(m, "/api/public/unsubscribe/")
	tok := strings.FieldsFunc(m[i+len("/api/public/unsubscribe/"):], func(r rune) bool { return r == '>' || r == ' ' || r == '\n' || r == '"' })[0]
	ws, user, kind, err := unsub.Parse(tok)
	if err != nil || ws != f.tess.ws || user != f.mo.userID || kind != unsub.Direct {
		t.Fatalf("token: %v %v %v %v", ws, user, kind, err)
	}
	// A second sweep finds nothing left to send.
	f.sweep(t)
	if n := len(f.mailsTo(f.mo.email)); n != 1 {
		t.Fatalf("a notification is mailed once, got %d mails", n)
	}
}

func TestMailOutboxHoldsOnlyReferences(t *testing.T) {
	f := mailSetup(t)
	if err := directSweepTx(context.Background(), f.g.c.Tx, f.tess.ws, time.Now().Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := f.g.c.Tx.QueryRow(f.g.c.Context, "select payload::text from core.jobs where kind = 'notify.mail' order by id desc limit 1").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"Plan", "Tess", f.mo.email, "mentioned", "secret body", "unsubscribe"} {
		if strings.Contains(payload, leak) {
			t.Fatalf("the outbox entry must hold references only, found %q in %s", leak, payload)
		}
	}
	if !strings.Contains(payload, f.notifID) {
		t.Fatalf("the entry names the notification: %s", payload)
	}
}

func TestMailDroppedWhenPreferencesOrAccessChanged(t *testing.T) {
	cases := map[string]func(f mailFixture, t *testing.T){
		"mode off": func(f mailFixture, t *testing.T) {
			f.exec(t, "insert into core.notify_prefs (workspace_id, user_id, email_mode) values ($1::uuid,$2::uuid,'off')", f.tess.ws, f.mo.userID)
		},
		"digest only": func(f mailFixture, t *testing.T) {
			f.exec(t, "insert into core.notify_prefs (workspace_id, user_id, email_mode) values ($1::uuid,$2::uuid,'digest')", f.tess.ws, f.mo.userID)
		},
		"unsubscribed from direct mail": func(f mailFixture, t *testing.T) {
			f.exec(t, "insert into core.notify_prefs (workspace_id, user_id, direct_off) values ($1::uuid,$2::uuid,true)", f.tess.ws, f.mo.userID)
		},
		"read in the app": func(f mailFixture, t *testing.T) {
			f.exec(t, "update core.notifications set read_at = now() where id = $1::uuid", f.notifID)
		},
		"left the workspace": func(f mailFixture, t *testing.T) {
			f.exec(t, "delete from core.memberships where user_id = $1::uuid and workspace_id = $2::uuid", f.mo.userID, f.tess.ws)
		},
		"page deleted": func(f mailFixture, t *testing.T) {
			f.exec(t, "update core.notes set deleted_at = now() where id = $1::uuid", f.note)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := mailSetup(t)
			change(f, t)
			f.sweep(t)
			if n := len(f.mailsTo(f.mo.email)); n != 0 {
				t.Fatalf("no mail expected, got %d", n)
			}
		})
	}
}

// The change happens after the sweep queued the mail: the job must still drop it.
func TestMailDroppedWhenAccessLostAfterTheSweep(t *testing.T) {
	f := mailSetup(t)
	if err := directSweepTx(context.Background(), f.g.c.Tx, f.tess.ws, time.Now().Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if f.state(t) != "queued" {
		t.Fatalf("state %q", f.state(t))
	}
	f.exec(t, "delete from core.memberships where user_id = $1::uuid and workspace_id = $2::uuid", f.mo.userID, f.tess.ws)
	f.g.drain(100)
	if n := len(f.mailsTo(f.mo.email)); n != 0 {
		t.Fatalf("a person who left must get nothing, got %d", n)
	}
	if f.state(t) != "suppressed" {
		t.Fatalf("state %q", f.state(t))
	}
}

func TestRestrictedPageTitleStaysOutOfTheSubject(t *testing.T) {
	f := mailSetup(t)
	f.exec(t, "update core.notes set restriction = 'view' where id = $1::uuid", f.note)
	f.exec(t, "insert into core.note_permissions (note_id, workspace_id, user_id, level, granted_by) values ($1::uuid,$2::uuid,$3::uuid,'view',$4::uuid)", f.note, f.tess.ws, f.mo.userID, f.tess.userID)
	f.sweep(t)
	got := f.mailsTo(f.mo.email)
	if len(got) != 1 {
		t.Fatalf("want one mail, got %d", len(got))
	}
	head := strings.SplitN(unfold(got[0]), "\n\n", 2)[0]
	if !strings.Contains(head, "Subject: Tess mentioned you in a restricted page") || strings.Contains(head, "Plan") {
		t.Fatalf("the title must not be in the subject:\n%s", head)
	}
}

func TestNoMailWhenNotConfiguredOrNoUnsubscribeKey(t *testing.T) {
	f := mailSetup(t)
	t.Setenv("CNOTE_MASTER_KEY", "")
	f.sweep(t)
	if n := len(f.mailsTo(f.mo.email)); n != 0 {
		t.Fatalf("mail that is on by default must not go out without an unsubscribe link, got %d", n)
	}
	if f.state(t) != "suppressed" {
		t.Fatalf("state %q", f.state(t))
	}
}

func (f mailFixture) exec(t *testing.T, q string, a ...any) {
	t.Helper()
	if _, err := f.g.c.Tx.Exec(f.g.c.Context, q, a...); err != nil {
		t.Fatal(err)
	}
}
