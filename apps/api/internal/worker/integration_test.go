package worker

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cnote/api/internal/hooks"
	"github.com/cnote/api/internal/secretbox"
)

// fakeSMTP records the envelope and DATA of every message it receives.
type fakeSMTP struct {
	mu   sync.Mutex
	msgs []smtpMsg
	ln   net.Listener
}

type smtpMsg struct{ from, to, data string }

func startSMTP(t *testing.T) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	say("220 fake ESMTP")
	var m smtpMsg
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			m.from = strings.Trim(strings.TrimSpace(line[10:]), "<>")
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			m.to = strings.Trim(strings.TrimSpace(line[8:]), "<>")
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			m.data = b.String()
			f.mu.Lock()
			f.msgs = append(f.msgs, m)
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

// mention.created sends an email through SMTP_URL, with the same recipient, subject and body as Rust.
func TestMentionEmail(t *testing.T) {
	smtp := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+smtp.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "C Note <noreply@example.test>")
	t.Setenv("PUBLIC_WEB_URL", "https://app.example.test/")
	e := newEnv(t)
	w := e.signup("Tess")
	mo := e.signup("Mo")
	e.rustCmd(w, "member.add", map[string]any{"email": mo.email, "role": "member"})
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "DOC", "name": "Docs", "visibility": "open"}), "id")
	title := "Plan über\nalles"
	note := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": title, "content_md": "hi"}), "id")
	g.cmd("note.update", map[string]any{"id": note, "content_md": "hi [@Mo](/w/" + w.ws + "/people/" + mo.userID + ")"})
	g.drain(100)
	smtp.mu.Lock()
	defer smtp.mu.Unlock()
	if len(smtp.msgs) != 1 {
		t.Fatalf("want one email, got %d", len(smtp.msgs))
	}
	m := smtp.msgs[0]
	if m.from != "noreply@example.test" || m.to != mo.email {
		t.Fatalf("envelope %q -> %q", m.from, m.to)
	}
	body := strings.ReplaceAll(m.data, "\r\n", "\n")
	if !strings.Contains(body, "Subject: ") || strings.Contains(strings.SplitN(body, "\n\n", 2)[0], "alles\n") {
		t.Fatalf("subject must be one encoded line: %q", body)
	}
	if !strings.Contains(body, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatal(body)
	}
	if !strings.Contains(body, "Message-ID: <cnote-mention-") {
		t.Fatal("missing per-event Message-ID")
	}
	// decoded text part
	raw := strings.SplitN(body, "\n\n", 2)[1]
	raw = strings.ReplaceAll(raw, "=\n", "")
	if !strings.Contains(raw, "Tess mentioned you in Plan =C3=BCber") || !strings.Contains(raw, "https://app.example.test/w/"+w.ws+"/n/"+note) {
		t.Fatalf("body %q", raw)
	}
}

// Unset SMTP_URL skips the email; a dead SMTP server is logged, never an error (the job must not retry).
func TestMentionEmailSkippedAndFailureNeverRetried(t *testing.T) {
	e := newEnv(t)
	w := e.signup("Tess")
	mo := e.signup("Mo")
	e.rustCmd(w, "member.add", map[string]any{"email": mo.email, "role": "member"})
	e.settle(w.ws)
	g := e.newGoSide(w)
	sp := str(g.cmd("space.create", map[string]any{"key": "DOC", "name": "Docs", "visibility": "open"}), "id")
	for _, smtpURL := range []string{"", "smtp://127.0.0.1:1"} {
		if smtpURL == "" {
			t.Setenv("SMTP_URL", "")
			_ = unsetenv("SMTP_URL")
		} else {
			t.Setenv("SMTP_URL", smtpURL)
		}
		n := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "N", "content_md": "x"}), "id")
		g.cmd("note.update", map[string]any{"id": n, "content_md": "[@Mo](/w/" + w.ws + "/people/" + mo.userID + ")"})
		g.drain(100) // fails the test if any job (the dispatch) returns an error
	}
}

// The whole chain through the Go worker: event -> hooks.OnEvent -> hook.deliver -> signed POST to the receiver.
func TestHookDeliveryThroughTheWorker(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1") // the receiver listens on loopback
	var mu sync.Mutex
	var got []struct {
		body []byte
		hdr  http.Header
	}
	rx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, struct {
			body []byte
			hdr  http.Header
		}{b, r.Header.Clone()})
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer rx.Close()
	e := newEnv(t)
	w := e.signup("Tess")
	e.settle(w.ws)
	g := e.newGoSide(w)
	key, err := secretbox.MasterKey()
	if err != nil {
		t.Fatal(err)
	}
	secret := "whsec_" + rnd()
	enc, err := secretbox.Encrypt(key, w.ws, secret)
	if err != nil {
		t.Fatal(err)
	}
	var hook string
	if err := g.c.Tx.QueryRow(g.c.Context,
		"insert into core.hooks (workspace_id, name, target, url, secret_enc, events) values ($1::uuid,'rx','http',$2,$3,array['note.created']) returning id::text",
		w.ws, rx.URL, enc).Scan(&hook); err != nil {
		t.Fatal(err)
	}
	sp := str(g.cmd("space.create", map[string]any{"key": "DOC", "name": "Docs"}), "id")
	g.cmd("note.create", map[string]any{"space_id": sp, "title": "Hello"})
	g.drain(100)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("receiver got %d requests", len(got))
	}
	r := got[0]
	var body struct {
		Type string `json:"type"`
		Hook string `json:"hook_id"`
		Ev   struct {
			Kind string `json:"kind"`
		} `json:"event"`
	}
	_ = json.Unmarshal(r.body, &body)
	if body.Type != "event" || body.Hook != hook || body.Ev.Kind != "note.created" {
		t.Fatalf("body %s", r.body)
	}
	ts := r.hdr.Get("x-cnote-timestamp")
	var tsn int64
	_ = json.Unmarshal([]byte(ts), &tsn)
	if r.hdr.Get("x-cnote-signature") != hooks.Signature(secret, tsn, string(r.body)) || r.hdr.Get("content-type") != "application/json" {
		t.Fatalf("bad signature or content type: %v", r.hdr)
	}
	if d := r.hdr.Get("x-cnote-delivery-id"); !strings.HasPrefix(d, "job-") {
		t.Fatalf("missing per-job idempotency key: %q", d)
	}
	var st string
	_ = g.c.Tx.QueryRow(g.c.Context, "select last_status from core.hooks where id = $1::uuid", hook).Scan(&st)
	if st != "ok" {
		t.Fatal("last_status", st)
	}
}
