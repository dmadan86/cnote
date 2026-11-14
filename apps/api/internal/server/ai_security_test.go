package server_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/cnote/api/internal/modules/ai"
)

// Security properties of the AI and hook commands, asserted on the Go server (the Rust equivalents are the unit and
// integration tests in crates/cnote-ai): the private-endpoint guard, encrypted keys, key reuse rules, metering.

func (e *env) goCmd(w who, cmd string, in any) resp { return e.cmd(e.gosv.URL, w, w.ws, cmd, in, nil) }

func errMsg(r resp) string {
	m, _ := r.json()["error"].(map[string]any)
	s, _ := m["message"].(string)
	return s
}

func TestAIPrivateEndpointGuard(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "") // production setting
	e := newEnv(t)
	o := e.signup("Guard", true)
	note := e.note(o, "guard page")
	for url, want := range map[string]string{
		"http://example.com/v1":          "base_url must use https",
		"https://127.0.0.1/v1":           "base_url must resolve to public addresses only",
		"https://10.0.0.5":               "base_url must resolve to public addresses only",
		"https://169.254.169.254/latest": "base_url must resolve to public addresses only",
		"https://localhost:8080":         "base_url must resolve to public addresses only",
		"https://[::1]/v1":               "base_url host does not resolve",
		"https://user:pw@api.openai.com": "base_url must not contain credentials",
		"ftp://example.com":              "base_url must use https",
		"nonsense":                       "invalid base_url: relative URL without a base",
	} {
		r := e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": url, "api_key": "k"})
		if r.status != 400 || errMsg(r) != want {
			t.Errorf("%s: %d %s (want 400 %q)", url, r.status, r.body, want)
		}
	}
	if n := adminScalar(e, o.ws, "select count(*)::text from ai.provider_config"); n != "0" {
		t.Fatalf("a rejected endpoint was stored (%s rows)", n)
	}
	// DNS may change after saving: the guard runs again at call time, as a refusal
	adminSQL(e, o.ws, `insert into ai.provider_config (workspace_id, provider, base_url) values ($1::uuid, 'openai', 'https://127.0.0.1/v1')`, o.ws)
	r := e.goCmd(o, "ai.summarize", map[string]any{"note_id": note})
	if r.status != 403 || errMsg(r) != "base_url must resolve to public addresses only" {
		t.Errorf("call-time guard: %d %s", r.status, r.body)
	}
	r = e.goCmd(o, "search.hybrid", map[string]any{"q": "guard"})
	if r.status != 403 {
		t.Errorf("search.hybrid must run the guard too: %d %s", r.status, r.body)
	}
	// hooks use the same guard, worded for `url`
	for url, want := range map[string]string{"http://example.com/h": "url must use https", "https://127.0.0.1/h": "url must resolve to public addresses only"} {
		r := e.goCmd(o, "hook.create", map[string]any{"name": "x", "target": "http", "url": url, "events": []string{"note."}})
		if r.status != 400 || errMsg(r) != want {
			t.Errorf("hook %s: %d %s", url, r.status, r.body)
		}
	}
	adminSQL(e, o.ws, `insert into core.hooks (workspace_id, name, target, url, events) values ($1::uuid, 'sneaky', 'http', 'https://127.0.0.1/h', '{note.}')`, o.ws)
	id := adminScalar(e, o.ws, "select id::text from core.hooks where name = 'sneaky'")
	r = e.goCmd(o, "hook.test", map[string]any{"id": id})
	if r.status != 200 || r.json()["ok"] != false || r.json()["error"] != "url must resolve to public addresses only" {
		t.Errorf("hook.test must not reach a private address: %d %s", r.status, r.body)
	}
}

func TestAIKeysAreEncryptedAndNeverReturned(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	e := newEnv(t)
	fk := newFake(t, "unused")
	o := e.signup("Keys", true)
	m := e.addMember(o, e.signup("Mem", false), "member")
	other := e.signup("Other", true)
	note := e.note(o, "keys page")
	key := "sk-" + rnd() + rnd()
	mk, err := ai.MasterKey()
	if err != nil {
		t.Fatal(err)
	}

	if r := e.goCmd(m, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL, "api_key": key}); r.status != 403 {
		t.Errorf("member must not set a provider: %d %s", r.status, r.body)
	}
	r := e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/v1", "api_key": key})
	if r.status != 200 || strings.Contains(string(r.body), key) || r.json()["has_key"] != true || r.json()["metered"] != false {
		t.Fatalf("set_provider: %d %s", r.status, r.body)
	}
	if g := e.goCmd(o, "ai.get_provider", map[string]any{}); strings.Contains(string(g.body), key) {
		t.Fatalf("get_provider leaks the key: %s", g.body)
	}
	blob := adminScalar(e, o.ws, "select encode(key_enc, 'base64') from ai.provider_config")
	raw, _ := base64.StdEncoding.DecodeString(blob)
	if strings.Contains(string(raw), key) {
		t.Fatal("the key is stored in plaintext")
	}
	if got, err := ai.Decrypt(mk, o.ws, raw); err != nil || got != key {
		t.Fatalf("stored key does not decrypt with the master key and workspace: %q %v", got, err)
	}
	if _, err := ai.Decrypt(mk, other.ws, raw); err == nil {
		t.Fatal("the blob decrypts for another workspace")
	}

	// both servers read what the other wrote: Go encrypted it, Rust decrypts it and sends it as the bearer token
	for _, base := range []string{e.gosv.URL, e.rust} {
		if r := e.cmd(base, o, o.ws, "ai.summarize", map[string]any{"note_id": note}, nil); r.status != 200 || r.json()["provider"] != "openai" {
			t.Fatalf("summarize via %s: %d %s", base, r.status, r.body)
		}
	}
	// and the other way round: Rust encrypts, Go decrypts
	e.must(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/v1", "api_key": key + "-rust"})
	if r := e.goCmd(o, "ai.summarize", map[string]any{"note_id": note}); r.status != 200 {
		t.Fatalf("Go cannot use a key Rust encrypted: %d %s", r.status, r.body)
	}
	fk.mu.Lock()
	var auths []string
	for _, q := range fk.reqs {
		auths = append(auths, q.Auth)
	}
	fk.mu.Unlock()
	if strings.Join(auths, ",") != "Bearer "+key+",Bearer "+key+",Bearer "+key+"-rust" {
		t.Errorf("provider saw %v", auths)
	}

	// a key is reused only for the endpoint it was given for
	e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/v1", "api_key": key})
	if r := e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/v1", "model": "m"}); r.json()["has_key"] != true {
		t.Errorf("same endpoint must keep the key: %s", r.body)
	}
	if r := e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/other"}); r.json()["has_key"] != false {
		t.Errorf("a changed URL must not inherit the key: %s", r.body)
	}
	e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/v1", "api_key": key})
	if r := e.goCmd(o, "ai.set_provider", map[string]any{"provider": "anthropic", "base_url": fk.srv.URL + "/v1"}); r.json()["has_key"] != false {
		t.Errorf("a changed provider must not inherit the key: %s", r.body)
	}

	// a blob moved to another workspace's row does not decrypt there (AAD binds the workspace)
	adminSQL(e, other.ws, `insert into ai.provider_config (workspace_id, provider, base_url, key_enc) values ($1::uuid, 'openai', $2, decode($3, 'base64'))`, other.ws, fk.srv.URL+"/v1", blob)
	onote := e.note(other, "other page")
	if r := e.goCmd(other, "ai.summarize", map[string]any{"note_id": onote}); r.status != 500 || errMsg(r) != "cannot decrypt the workspace provider key (wrong master key?)" {
		t.Errorf("moved blob: %d %s", r.status, r.body)
	}
	// a wrong master key fails the same way, and a missing one refuses to store anything
	e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/v1", "api_key": key})
	t.Setenv("CNOTE_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if r := e.goCmd(o, "ai.summarize", map[string]any{"note_id": note}); r.status != 500 {
		t.Errorf("wrong master key: %d %s", r.status, r.body)
	}
	t.Setenv("CNOTE_MASTER_KEY", "")
	r = e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/v1", "api_key": "new-key"})
	if r.status != 400 || errMsg(r) != "CNOTE_MASTER_KEY is not set; refusing to store provider keys" {
		t.Errorf("missing master key: %d %s", r.status, r.body)
	}
	t.Setenv("CNOTE_MASTER_KEY", "short")
	if r = e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "api_key": "new-key"}); r.status != 400 || errMsg(r) != "CNOTE_MASTER_KEY must be 32 bytes, base64 encoded" {
		t.Errorf("malformed master key: %d %s", r.status, r.body)
	}
}

func TestAIMeteringAndAudit(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	e := newEnv(t)
	fk := newFake(t, "unused")
	o := e.signup("Meter", true)
	note := e.note(o, "meter page")
	count := func(q string) string { return adminScalar(e, o.ws, q) }
	// platform provider: tokens are metered and audited
	if r := e.goCmd(o, "ai.summarize", map[string]any{"note_id": note}); r.status != 200 || r.json()["provider"] != "local" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if n := count("select count(*)::text from core.usage where kind in ('ai.tokens_in','ai.tokens_out') and meta->>'purpose' <> 'index'"); n != "2" {
		t.Errorf("platform call must record 2 usage rows, got %s", n)
	}
	if n := count("select count(*)::text from ai.audit where purpose = 'summarize'"); n != "1" {
		t.Errorf("audit rows: %s", n)
	}
	// bring your own key: audited, never metered
	e.goCmd(o, "ai.set_provider", map[string]any{"provider": "openai", "base_url": fk.srv.URL + "/v1", "api_key": "k-" + rnd()})
	for i := 0; i < 2; i++ {
		if r := e.goCmd(o, "ai.draft", map[string]any{"prompt": "hi", "note_id": note}); r.status != 200 || r.json()["provider"] != "openai" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	}
	if n := count("select count(*)::text from core.usage where kind in ('ai.tokens_in','ai.tokens_out') and meta->>'purpose' <> 'index'"); n != "2" {
		t.Errorf("BYO calls must not be metered, usage rows: %s", n)
	}
	if n := count("select count(*)::text from ai.audit where purpose = 'draft' and provider = 'openai'"); n != "2" {
		t.Errorf("BYO calls must still be audited: %s", n)
	}
	// a failing provider leaves no audit row and no usage, and its body never reaches the caller
	e.goCmd(o, "note.update", map[string]any{"id": note, "content_md": "FAIL500"})
	r := e.goCmd(o, "ai.summarize", map[string]any{"note_id": note})
	if r.status != 500 || errMsg(r) != "AI provider error: model API returned 500 Internal Server Error" {
		t.Errorf("provider failure: %d %s", r.status, r.body)
	}
	if n := count("select count(*)::text from ai.audit where purpose = 'summarize'"); n != "1" {
		t.Errorf("failed call must not be audited: %s", n)
	}
	// agents cannot manage providers
	k := e.must(o, "agent.create", map[string]any{"name": "ai-bot"})["api_key"].(string)
	if r := e.cmd(e.gosv.URL, who{ws: o.ws, bearer: k}, o.ws, "ai.get_provider", map[string]any{}, nil); r.status != 403 {
		t.Errorf("agent: %d %s", r.status, r.body)
	}
}

func TestHookSecretsAreEncryptedAndNeverListed(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	e := newEnv(t)
	fk := newFake(t, "tok")
	o := e.signup("Hooks", true)
	m := e.addMember(o, e.signup("Mem", false), "member")
	mk, _ := ai.MasterKey()
	r := e.goCmd(o, "hook.create", map[string]any{"name": "h", "target": "http", "url": fk.srv.URL + "/hook", "events": []string{"zz."}})
	secret, _ := r.json()["signing_secret"].(string)
	if r.status != 200 || !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if l := e.goCmd(o, "hook.list", map[string]any{}); strings.Contains(string(l.body), secret) || strings.Contains(string(l.body), "secret") {
		t.Fatalf("hook.list leaks the secret: %s", l.body)
	}
	blob := adminScalar(e, o.ws, "select encode(secret_enc, 'base64') from core.hooks")
	raw, _ := base64.StdEncoding.DecodeString(blob)
	if got, err := ai.Decrypt(mk, o.ws, raw); err != nil || got != secret || strings.Contains(string(raw), secret) {
		t.Fatalf("hook secret storage: %q %v", got, err)
	}
	id := r.json()["id"].(string)
	if tr := e.goCmd(o, "hook.test", map[string]any{"id": id}); tr.json()["ok"] != true {
		t.Fatalf("%s", tr.body)
	}
	fk.mu.Lock()
	h := fk.hooks[0]
	fk.mu.Unlock()
	var ts int64
	for _, c := range h.TS {
		ts = ts*10 + int64(c-'0')
	}
	if h.Sig != ai.Signature(secret, ts, h.Body) || !strings.HasPrefix(h.Sig, "sha256=") {
		t.Errorf("delivery is not signed with the returned secret: %s", h.Sig)
	}
	// the MCP bearer token is stored encrypted too and the response has no secret
	r = e.goCmd(o, "hook.create", map[string]any{"name": "m", "target": "mcp", "url": fk.srv.URL + "/mcp", "tool": "ok", "token": "tok", "events": []string{"zz."}})
	if r.json()["signing_secret"] != nil || strings.Contains(string(r.body), "tok\"") {
		t.Errorf("mcp create: %s", r.body)
	}
	for _, cmd := range []string{"hook.list", "hook.create", "hook.delete", "hook.test", "hook.set_enabled"} {
		if x := e.goCmd(m, cmd, map[string]any{"id": id, "enabled": true, "name": "x", "target": "http", "url": "http://x", "events": []string{"a"}}); x.status != 403 {
			t.Errorf("%s as member: %d", cmd, x.status)
		}
	}
	// hook.create returns a secret: its result must never be replayed from the idempotency store
	idem := map[string]string{"Idempotency-Key": "k-" + rnd()}
	in := map[string]any{"name": "idem", "target": "http", "url": fk.srv.URL + "/hook", "events": []string{"zz."}}
	a, b := e.cmd(e.gosv.URL, o, o.ws, "hook.create", in, idem), e.cmd(e.gosv.URL, o, o.ws, "hook.create", in, idem)
	if a.json()["id"] == b.json()["id"] || a.json()["signing_secret"] == b.json()["signing_secret"] {
		t.Errorf("a secret-returning command was replayed: %s %s", a.body, b.body)
	}
}
