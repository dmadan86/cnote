package server_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Tests for the non-command surface (auth, workspaces, catalog, marketplace token, GitHub webhook). Go and Rust
// share the database, so a session or password created by one must work on the other.

type hres struct {
	status int
	header http.Header
	body   []byte
}

func (h hres) cookie() string { return h.header.Get("Set-Cookie") }

func hit(t *testing.T, base, method, path string, hdr map[string]string, body string) hres {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return hres{res.StatusCode, res.Header, b}
}

var jsonHdr = map[string]string{"Content-Type": "application/json"}

func jb(v any) string { b, _ := json.Marshal(v); return string(b) }

func cookieHdr(tok string) map[string]string { return map[string]string{"Cookie": "cn_session=" + tok} }

var cookieRe = regexp.MustCompile(`^cn_session=([A-Za-z0-9_-]{43}); Path=/; HttpOnly; SameSite=Lax; Max-Age=2592000$`)

func sessionOf(t *testing.T, h hres) string {
	t.Helper()
	m := cookieRe.FindStringSubmatch(h.cookie())
	if m == nil {
		t.Fatalf("session cookie has the wrong shape: %q", h.cookie())
	}
	return m[1]
}

func TestAuthSignupLoginLogoutCookies(t *testing.T) {
	e := newEnv(t)
	email, pw := "go-"+rnd()+"@example.test", rnd()+rnd()

	// signup on Go: cookie flags, body, hash format
	r := hit(t, e.gosv.URL, "POST", "/api/auth/signup", jsonHdr, jb(map[string]any{"email": "  " + strings.ToUpper(email) + " ", "name": "  Gina ", "password": pw, "workspace_name": "Gina Co"}))
	if r.status != 200 || r.header.Get("Content-Type") != "application/json" {
		t.Fatalf("signup: %d %s", r.status, r.body)
	}
	tok := sessionOf(t, r)
	var su struct{ UserID, WorkspaceID string }
	body := r.body
	if !bytes.HasPrefix(body, []byte(`{"user_id":"`)) || !bytes.Contains(body, []byte(`","workspace_id":"`)) {
		t.Fatalf("body %s", body)
	}
	_ = json.Unmarshal([]byte(strings.NewReplacer("user_id", "UserID", "workspace_id", "WorkspaceID").Replace(string(body))), &su)
	var hash, stored string
	if err := e.pool.QueryRow(context.Background(), "select password_hash, email from core.users where id = $1::uuid", su.UserID).Scan(&hash, &stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") || stored != email {
		t.Fatalf("hash %q email %q", hash, stored)
	}
	if got := hit(t, e.gosv.URL, "POST", "/api/auth/signup", jsonHdr, jb(map[string]any{"email": email, "name": "x", "password": pw})); got.status != 409 || !strings.Contains(string(got.body), `"code":"conflict"`) {
		t.Fatalf("duplicate: %d %s", got.status, got.body)
	}
	if got := hit(t, e.gosv.URL, "POST", "/api/auth/signup", jsonHdr, jb(map[string]any{"email": "nope", "name": "x", "password": pw})); got.status != 400 || !strings.Contains(string(got.body), "a valid email is required") {
		t.Fatalf("bad email: %d %s", got.status, got.body)
	}
	if got := hit(t, e.gosv.URL, "POST", "/api/auth/signup", jsonHdr, jb(map[string]any{"email": rnd() + "@x.test", "name": "x", "password": "short"})); got.status != 400 || !strings.Contains(string(got.body), "password must be at least 8 characters") {
		t.Fatalf("short password: %d %s", got.status, got.body)
	}

	// a session made by Go is valid on Rust, and the Go-made hash verifies on Rust
	if m := hit(t, e.rust, "GET", "/api/auth/me", cookieHdr(tok), ""); m.status != 200 || !strings.Contains(string(m.body), `"email":"`+email+`"`) {
		t.Fatalf("rust me with go session: %d %s", m.status, m.body)
	}
	rl := hit(t, e.rust, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": email, "password": pw}))
	if rl.status != 200 {
		t.Fatalf("rust login of go account: %d %s", rl.status, rl.body)
	}
	// login on Go (any email case/whitespace) works; wrong password and unknown email give the same 401
	gl := hit(t, e.gosv.URL, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": " " + strings.ToUpper(email), "password": pw}))
	if gl.status != 200 || string(gl.body) != `{"user_id":"`+su.UserID+`"}` {
		t.Fatalf("go login: %d %s", gl.status, gl.body)
	}
	tok2 := sessionOf(t, gl)
	bad := hit(t, e.gosv.URL, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": email, "password": pw + "x"}))
	unk := hit(t, e.gosv.URL, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": rnd() + "@x.test", "password": pw}))
	if bad.status != 401 || unk.status != 401 || string(bad.body) != string(unk.body) || bad.cookie() != "" ||
		string(bad.body) != `{"error":{"code":"unauthenticated","message":"invalid email or password"}}` {
		t.Fatalf("bad login: %d %s / %d %s", bad.status, bad.body, unk.status, unk.body)
	}

	// the token is stored hashed, with a 30 day life; an expired session is refused on both servers
	var sid string
	var days float64
	if err := e.pool.QueryRow(context.Background(), "select id, extract(epoch from expires_at - now())/86400 from core.sessions where id = encode(sha256($1::bytea), 'hex')", tok2).Scan(&sid, &days); err != nil || sid == tok2 || days < 29.99 || days > 30 {
		t.Fatalf("session row: %q %v days=%v", sid, err, days)
	}
	if _, err := e.pool.Exec(context.Background(), "update core.sessions set expires_at = now() - interval '1 second' where id = encode(sha256($1::bytea), 'hex')", tok); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{e.gosv.URL, e.rust} {
		if got := hit(t, base, "GET", "/api/auth/me", cookieHdr(tok), ""); got.status != 401 || !strings.Contains(string(got.body), "session expired") {
			t.Fatalf("%s me with expired session: %d %s", base, got.status, got.body)
		}
	}
	if _, err := e.pool.Exec(context.Background(), "update core.sessions set expires_at = now() + interval '1 day' where id = encode(sha256($1::bytea), 'hex')", tok); err != nil {
		t.Fatal(err)
	}

	// me
	m := hit(t, e.gosv.URL, "GET", "/api/auth/me", cookieHdr(tok), "")
	var me struct {
		User       map[string]string
		Workspaces []map[string]string
	}
	_ = json.Unmarshal(m.body, &me)
	if m.status != 200 || me.User["id"] != su.UserID || me.User["name"] != "Gina" || len(me.Workspaces) != 1 || me.Workspaces[0]["id"] != su.WorkspaceID ||
		me.Workspaces[0]["role"] != "owner" || !strings.HasPrefix(me.Workspaces[0]["slug"], "gina-co") {
		t.Fatalf("me: %d %s", m.status, m.body)
	}

	// logout on Go destroys the session for both servers and clears the cookie; the other session survives
	lo := hit(t, e.gosv.URL, "POST", "/api/auth/logout", cookieHdr(tok), "")
	if lo.status != 200 || string(lo.body) != `{"ok":true}` || lo.cookie() != "cn_session=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0" {
		t.Fatalf("logout: %d %s %q", lo.status, lo.body, lo.cookie())
	}
	for _, base := range []string{e.gosv.URL, e.rust} {
		if got := hit(t, base, "GET", "/api/auth/me", cookieHdr(tok), ""); got.status != 401 || !strings.Contains(string(got.body), "session expired") {
			t.Fatalf("%s me after logout: %d %s", base, got.status, got.body)
		}
	}
	if got := hit(t, e.gosv.URL, "GET", "/api/auth/me", cookieHdr(tok2), ""); got.status != 200 {
		t.Fatalf("other session killed: %d", got.status)
	}
	// logout without a session still answers ok and clears the cookie; me without one asks to sign in
	if lo := hit(t, e.gosv.URL, "POST", "/api/auth/logout", nil, ""); lo.status != 200 || lo.cookie() == "" {
		t.Fatalf("anonymous logout: %d", lo.status)
	}
	if got := hit(t, e.gosv.URL, "GET", "/api/auth/me", nil, ""); got.status != 401 || !strings.Contains(string(got.body), "sign in required") {
		t.Fatalf("anonymous me: %d %s", got.status, got.body)
	}

	// Secure flag follows COOKIE_SECURE, which the Go server reads per request like Rust reads it
	t.Setenv("COOKIE_SECURE", "true")
	sec := hit(t, e.gosv.URL, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": email, "password": pw}))
	if !strings.HasSuffix(sec.cookie(), "; Max-Age=2592000; Secure") {
		t.Fatalf("secure cookie: %q", sec.cookie())
	}
}

// A hash made by Rust verifies in Go, so existing accounts keep working after the flip.
func TestAuthRustAccountsWorkInGo(t *testing.T) {
	e := newEnv(t)
	email, pw := "go-"+rnd()+"@example.test", rnd()+rnd()
	rs := hit(t, e.rust, "POST", "/api/auth/signup", jsonHdr, jb(map[string]any{"email": email, "name": "Rae", "password": pw, "workspace_name": "Rae WS"}))
	if rs.status != 200 {
		t.Fatalf("%d %s", rs.status, rs.body)
	}
	rustTok := sessionOf(t, rs)
	if got := hit(t, e.gosv.URL, "GET", "/api/auth/me", cookieHdr(rustTok), ""); got.status != 200 {
		t.Fatalf("go me with rust session: %d %s", got.status, got.body)
	}
	gl := hit(t, e.gosv.URL, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": email, "password": pw}))
	if gl.status != 200 {
		t.Fatalf("go login with rust hash: %d %s", gl.status, gl.body)
	}
	if got := hit(t, e.gosv.URL, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": email, "password": pw + " "})); got.status != 401 {
		t.Fatalf("wrong password accepted: %d", got.status)
	}
	// the Go-issued session works on a Rust command route
	var su struct {
		WorkspaceID string `json:"workspace_id"`
	}
	_ = json.Unmarshal(rs.body, &su)
	if got := hit(t, e.rust, "POST", "/api/w/"+su.WorkspaceID+"/commands/space.list", map[string]string{"Cookie": "cn_session=" + sessionOf(t, gl), "Content-Type": "application/json"}, "{}"); got.status != 200 {
		t.Fatalf("rust command with go session: %d %s", got.status, got.body)
	}
}

func TestAuthNativeClientGetsBearerToken(t *testing.T) {
	e := newEnv(t)
	email, pw := "go-"+rnd()+"@example.test", rnd()+rnd()
	h := map[string]string{"Content-Type": "application/json", "X-Cnote-Client": "native"}
	r := hit(t, e.gosv.URL, "POST", "/api/auth/signup", h, jb(map[string]any{"email": email, "name": "Nat", "password": pw, "workspace_name": "N"}))
	var out map[string]any
	_ = json.Unmarshal(r.body, &out)
	tok, _ := out["token"].(string)
	if r.status != 200 || !strings.HasPrefix(tok, "cs_") || tok != "cs_"+sessionOf(t, r) {
		t.Fatalf("native signup: %d %s", r.status, r.body)
	}
	for _, base := range []string{e.gosv.URL, e.rust} {
		if got := hit(t, base, "GET", "/api/auth/me", map[string]string{"Authorization": "Bearer " + tok}, ""); got.status != 200 {
			t.Fatalf("%s bearer me: %d %s", base, got.status, got.body)
		}
	}
	// browsers do not get the token in the body
	if r := hit(t, e.gosv.URL, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": email, "password": pw})); strings.Contains(string(r.body), "token") {
		t.Fatalf("token leaked to a browser: %s", r.body)
	}
	if got := hit(t, e.gosv.URL, "POST", "/api/auth/logout", map[string]string{"Authorization": "Bearer " + tok}, ""); got.status != 200 {
		t.Fatal(got.status)
	}
	if got := hit(t, e.rust, "GET", "/api/auth/me", map[string]string{"Authorization": "Bearer " + tok}, ""); got.status != 401 {
		t.Fatalf("native session survived logout: %d", got.status)
	}
}

func TestAuthCreateWorkspaceSeedsLikeRust(t *testing.T) {
	e := newEnv(t)
	u := e.signup("Wanda", false)
	create := func(base, name string) string {
		r := hit(t, base, "POST", "/api/workspaces", map[string]string{"Content-Type": "application/json", "Cookie": "cn_session=" + u.cookie}, jb(map[string]any{"name": name}))
		var out struct{ ID string }
		_ = json.Unmarshal([]byte(strings.Replace(string(r.body), `"id"`, `"ID"`, 1)), &out)
		if r.status != 200 || out.ID == "" {
			t.Fatalf("create workspace on %s: %d %s", base, r.status, r.body)
		}
		return out.ID
	}
	q := `select (select string_agg(key||':'||name||':'||template, ',' order by key) from core.spaces)||'|'||
	  (select string_agg(name||':'||kind||':'||source_kind||':'||md5(content_md)||':'||md5(title), ',' order by name) from docs.templates)||'|'||
	  (select string_agg(code||name||kind, ',' order by code) from billing.accounts)||'|'||
	  (select string_agg(role, ',') from core.memberships)||'|'`
	r, g := create(e.rust, "Ünï  Co! 2026"), create(e.gosv.URL, "Ünï  Co! 2026")
	if a, b := e.dbStrings(r, q)[0], e.dbStrings(g, q)[0]; a != b || !strings.Contains(a, "GEN:General:blank") || !strings.Contains(a, "1100Accounts receivableasset") {
		t.Fatalf("seed differs:\nrust %s\ngo   %s", a, b)
	}
	// errors: no session, blank name; both are decided by the handler, not by serde
	if got := hit(t, e.gosv.URL, "POST", "/api/workspaces", jsonHdr, jb(map[string]any{"name": "x"})); got.status != 401 {
		t.Fatalf("no session: %d %s", got.status, got.body)
	}
	if got := hit(t, e.gosv.URL, "POST", "/api/workspaces", map[string]string{"Content-Type": "application/json", "Cookie": "cn_session=" + u.cookie}, jb(map[string]any{"name": "  "})); got.status != 400 || !strings.Contains(string(got.body), "name is required") {
		t.Fatalf("blank: %d %s", got.status, got.body)
	}
}

// Security checks of the token endpoint: one answer for every failure, no-store on success, per-IP bucket.
func TestMarketplaceTokenRateLimitAndErrors(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TRUST_PROXY", "1")
	body := jb(map[string]any{"client_id": "00000000-0000-4000-8000-000000000001", "client_secret": "x", "code": "y"})
	hdr := map[string]string{"Content-Type": "application/json", "X-Forwarded-For": "203.0.113." + "7"}
	for i := 0; i < 20; i++ {
		if r := hit(t, e.gosv.URL, "POST", "/api/marketplace/token", hdr, body); r.status != 400 || string(r.body) != `{"error":"invalid_grant"}` {
			t.Fatalf("attempt %d: %d %s", i, r.status, r.body)
		}
	}
	if r := hit(t, e.gosv.URL, "POST", "/api/marketplace/token", hdr, body); r.status != 429 || len(r.body) != 0 {
		t.Fatalf("21st attempt: %d %q", r.status, r.body)
	}
	// another client IP has its own bucket
	hdr["X-Forwarded-For"] = "203.0.113.8"
	if r := hit(t, e.gosv.URL, "POST", "/api/marketplace/token", hdr, body); r.status != 400 {
		t.Fatalf("other ip: %d", r.status)
	}
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Port of webhook_verifies_signature_and_records_evidence (cnote-projects/tests/projects.rs), over HTTP.
func TestGitHubWebhookVerifiesSignatureAndRecordsEvidence(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Hook", true)
	space := e.must(owner, "space.create", map[string]any{"key": "ENG", "name": "ENG", "template": "software"})["id"]
	e.must(owner, "item.create", map[string]any{"space_id": space, "title": "Ship it"})
	e.must(owner, "item.transition", map[string]any{"key": "ENG-1", "to": "In Progress"})
	body := []byte(`{"action":"closed","pull_request":{"merged":true,"title":"ENG-1: ship it","head":{"ref":"feature/eng-1"},"body":"","html_url":"https://example.test/pr/1"}}`)
	post := func(ws, sig string, b []byte) hres {
		h := map[string]string{}
		if sig != "" {
			h["X-Hub-Signature-256"] = sig
		}
		return hit(t, e.gosv.URL, "POST", "/api/hooks/github/"+ws, h, string(b))
	}
	unauth := func(r hres, msg string) {
		t.Helper()
		if r.status != 401 || string(r.body) != `{"error":{"code":"unauthenticated","message":"`+msg+`"}}` {
			t.Fatalf("want 401 %q, got %d %s", msg, r.status, r.body)
		}
	}
	// no secret configured: nothing is accepted
	unauth(post(owner.ws, sign("x", body), body), "github webhook is not configured for this workspace")
	secret := rnd() + rnd()
	tx, _ := dbTenant(e, owner.ws)
	if _, err := tx.Exec(context.Background(), "update core.workspaces set settings = settings || jsonb_build_object('github_webhook_secret', $1::text) where id = $2::uuid", secret, owner.ws); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(context.Background())
	for _, bad := range []string{sign("wrong-secret", body), "", "sha256=00", sign(secret, []byte("{}")), "sha1=" + sign(secret, body)[7:], "sha256=zz", strings.ToUpper(sign(secret, body)[:7]) + sign(secret, body)[7:]} {
		unauth(post(owner.ws, bad, body), "invalid webhook signature")
	}
	if got := e.dbStrings(owner.ws, "select count(*)::text from projects.evidence")[0]; got != "0" {
		t.Fatalf("rejected calls recorded evidence: %s", got)
	}
	r := post(owner.ws, sign(secret, body), body)
	var out struct {
		Recorded    int
		Transitions []map[string]string
	}
	_ = json.Unmarshal([]byte(strings.NewReplacer(`"recorded"`, `"Recorded"`, `"transitions"`, `"Transitions"`).Replace(string(r.body))), &out)
	if r.status != 200 || out.Recorded != 1 || len(out.Transitions) != 1 || out.Transitions[0]["to"] != "Done" {
		t.Fatalf("recorded: %d %s", r.status, r.body)
	}
	ping := []byte(`{"zen":"hi"}`)
	if r := post(owner.ws, sign(secret, ping), ping); r.status != 200 || string(r.body) != `{"ignored":true}` {
		t.Fatalf("ping: %d %s", r.status, r.body)
	}
	nokeys := []byte(`{"action":"opened","pull_request":{"title":"nothing here"}}`)
	if r := post(owner.ws, sign(secret, nokeys), nokeys); r.status != 200 || string(r.body) != `{"ignored":true,"reason":"no item keys found"}` {
		t.Fatalf("no keys: %d %s", r.status, r.body)
	}
	// another tenant cannot use this workspace's secret
	other := e.signup("Other", true)
	unauth(post(other.ws, sign(secret, body), body), "github webhook is not configured for this workspace")
}

// The same signed events on twin fixtures: Go and Rust answer alike and write the same evidence.
func TestDifferentialGitHubWebhook(t *testing.T) {
	e := newEnv(t)
	secret := rnd() + rnd()
	fixture := func() who {
		o := e.signup("Twin", true)
		sp := e.must(o, "space.create", map[string]any{"key": "ENG", "name": "ENG", "template": "software"})["id"]
		for _, title := range []string{"one", "two", "three"} {
			e.must(o, "item.create", map[string]any{"space_id": sp, "title": title})
		}
		tx, _ := dbTenant(e, o.ws)
		_, _ = tx.Exec(context.Background(), "update core.workspaces set settings = settings || jsonb_build_object('github_webhook_secret', $1::text) where id = $2::uuid", secret, o.ws)
		_ = tx.Commit(context.Background())
		return o
	}
	rustWS, goWS := fixture(), fixture()
	bodies := []string{
		`{"action":"closed","pull_request":{"merged":true,"title":"ENG-1 and eng-2","head":{"ref":"x"},"body":"also ENG-99 ŇENG-3 ENG-3x","html_url":"https://example.test/pr/1"}}`,
		`{"action":"opened","pull_request":{"title":"t","head":{"ref":"feature/eng-3-login"}}}`,
		`{"action":"reopened","pull_request":{"title":"ENG-1","merged":"yes"}}`,
		`{"action":"closed","pull_request":{"merged":false,"title":"ENG-1"}}`,
		`{"action":"synchronize","pull_request":{"title":"ENG-1"}}`,
		`{"pull_request":null}`,
		`{"action":"opened","pull_request":{"title":"ſENG-1 ENG-１ ENG-1٣ ab-1 abcdefghijk-2 xabcdefghij-3 ENG–4 (ENG-5) ENG-6_ _ENG-7"}}`,
		`{"deployment_status":{"state":"success","target_url":"https://x.test"},"deployment":{"ref":"release/ENG-2","description":"ENG-3"}}`,
		`{"deployment_status":{"state":"success","log_url":"https://log.test"},"deployment":{"ref":"main","description":"deploy ENG-1"}}`,
		`{"deployment_status":{"state":"failure"},"deployment":{"ref":"ENG-2"}}`,
		`{"deployment_status":{"state":"success"},"deployment":{"ref":"main"}}`,
		`{"zen":"hi"}`, `[]`, `null`, `"ENG-1"`, `{}`, `{"a":1,"a":2}`, `{"pull_request":{"title":"ENG-1","title":"ENG-2"},"action":"opened"}`,
		`not json`, ``, `{"zen":`, `{"x":1} trailing`, "{\"zen\":\"\xff\"}", `{"zen":"\ud800"}`,
	}
	norm := func(s string) string {
		return regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f-]{27}`).ReplaceAllString(s, "<id>")
	}
	for i, b := range bodies {
		for _, sig := range []string{sign(secret, []byte(b)), "x"} {
			h := map[string]string{"X-Hub-Signature-256": sig}
			r := hit(t, e.rust, "POST", "/api/hooks/github/"+rustWS.ws, h, b)
			g := hit(t, e.gosv.URL, "POST", "/api/hooks/github/"+goWS.ws, h, b)
			if r.status != g.status || norm(string(r.body)) != norm(string(g.body)) || r.header.Get("Content-Type") != g.header.Get("Content-Type") {
				t.Errorf("body %d (%.40q) sig %.10s: rust %d %s %s | go %d %s %s", i, b, sig, r.status, r.header.Get("Content-Type"), r.body, g.status, g.header.Get("Content-Type"), g.body)
			}
		}
	}
	q := `select coalesce(string_agg(n.title||'|'||v.source||'|'||v.kind||'|'||coalesce(v.url,''), ';' order by n.title, v.kind, v.url), '') from projects.evidence v join core.notes n on n.id = v.note_id`
	if a, b := e.dbStrings(rustWS.ws, q)[0], e.dbStrings(goWS.ws, q)[0]; a != b || a == "" {
		t.Fatalf("evidence differs:\nrust %s\ngo   %s", a, b)
	}
	ev := `select string_agg(kind||'|'||actor_type||'|'||(payload - 'note_id')::text, ';' order by kind, payload::text) from core.events where kind like 'evidence%' or kind like 'item.%'`
	if a, b := norm(e.dbStrings(rustWS.ws, ev)[0]), norm(e.dbStrings(goWS.ws, ev)[0]); a != b {
		t.Fatalf("events differ:\nrust %s\ngo   %s", a, b)
	}
	// weird paths and methods are decided by Rust (proxied)
	for _, c := range [][2]string{{"POST", "/api/hooks/github/not-a-uuid"}, {"POST", "/api/hooks/github/" + strings.ToUpper(rustWS.ws)}, {"GET", "/api/hooks/github/" + rustWS.ws}, {"POST", "/api/hooks/github/" + rustWS.ws + "/"}} {
		r, g := hit(t, e.rust, c[0], c[1], nil, "{}"), hit(t, e.gosv.URL, c[0], c[1], nil, "{}")
		if r.status != g.status || norm(string(r.body)) != norm(string(g.body)) {
			t.Errorf("%v: rust %d %s | go %d %s", c, r.status, r.body, g.status, g.body)
		}
	}
}
