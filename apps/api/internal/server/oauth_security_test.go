package server_test

// One test per security check of the authorization server and the MCP endpoint.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestOAuthSecurityRedirectURIExactMatch(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register("https://app.example/cb?keep=1")
	_, challenge := pkce()
	for _, uri := range []string{"https://app.example/cb", "https://app.example/cb?keep=1&x=2", "https://app.example/cb?keep=1/", "https://APP.example/cb?keep=1",
		"https://app.example:444/cb?keep=1", "http://app.example/cb?keep=1", "https://app.example/cb/?keep=1", "https://app.example/cb?keep=1#f", "", " https://app.example/cb?keep=1", "https://evil.example/cb?keep=1"} {
		out := o.authorize(u, o.authzBody(client, uri, challenge), u.ws, true, nil).json()
		if out["ok"] != false || out["redirect_to"] != nil {
			t.Errorf("%q accepted or redirected: %v", uri, out)
		}
	}
	out := o.authorize(u, o.authzBody(client, "https://app.example/cb?keep=1", challenge), u.ws, true, nil).json()
	to := out["redirect_to"].(string)
	if !strings.HasPrefix(to, "https://app.example/cb?keep=1&code=cnx_") || !strings.Contains(to, "&iss=") || !strings.HasSuffix(to, "&state=st-1") {
		t.Fatalf("redirect_to %q", to)
	}
}

func TestOAuthSecurityPKCERequired(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register(redirect)
	verifier, challenge := pkce()
	for _, c := range [][2]string{{"code_challenge_method", ""}, {"code_challenge_method", "plain"}, {"code_challenge_method", "s256"}, {"code_challenge", ""},
		{"code_challenge", verifier}, {"code_challenge", challenge + "A"}, {"code_challenge", strings.Repeat("=", 43)}} {
		body := o.authzBody(client, redirect, challenge)
		body[c[0]] = c[1]
		out := o.authorize(u, body, u.ws, true, nil).json()
		if out["ok"] != false || out["error"] != "invalid_request" {
			t.Errorf("%v accepted: %v", c, out)
		}
	}
	body := o.authzBody(client, redirect, challenge)
	delete(body, "code_challenge")
	delete(body, "code_challenge_method")
	eq(t, o.authorize(u, body, u.ws, true, nil).json()["ok"], false)

	// The token step demands the verifier and checks it against the stored S256 challenge.
	code := query(t, o.authorize(u, o.authzBody(client, redirect, challenge), u.ws, true, nil).json()["redirect_to"], "code")
	noVerifier := []string{"grant_type", "authorization_code", "code", code, "redirect_uri", redirect, "client_id", client}
	eq(t, o.postForm("/oauth/token", noVerifier...).json()["error"], "invalid_request")
	other, _ := pkce()
	eq(t, o.postForm("/oauth/token", tokenForm(client, code, other)...).json()["error"], "invalid_grant")
	eq(t, o.postForm("/oauth/token", tokenForm(client, code, challenge)...).json()["error"], "invalid_grant") // the challenge itself is not the verifier
	eq(t, o.postForm("/oauth/token", tokenForm(client, code, verifier)...).status, 200)
}

func TestOAuthSecurityCodeIsSingleUse(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register(redirect)
	verifier, challenge := pkce()
	code := query(t, o.authorize(u, o.authzBody(client, redirect, challenge), u.ws, true, nil).json()["redirect_to"], "code")
	form := tokenForm(client, code, verifier)
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 8; i++ { // racing exchanges: the conditional update lets exactly one through
		wg.Add(1)
		go func() {
			defer wg.Done()
			if o.postForm("/oauth/token", form...).status == 200 {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	eq(t, won, 1)
	r := o.postForm("/oauth/token", form...)
	eq(t, r.status, 400)
	eq(t, r.json()["error"], "invalid_grant")
	eq(t, r.json()["error_description"], "code already used")
}

func TestOAuthSecurityRefreshRotationRevokesOld(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client, tok := o.connect(u)
	old := tok["refresh_token"].(string)
	r := o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", old, "client_id", client)
	eq(t, r.status, 200)
	fresh := r.json()
	eq(t, o.mcpStatus(tok["access_token"].(string)), 401) // the access token the old refresh token minted died with it
	eq(t, o.mcpStatus(fresh["access_token"].(string)), 200)
	// reuse of the rotated-out refresh token: refused, and the whole family (the new pair too) is revoked
	r = o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", old, "client_id", client)
	eq(t, r.json()["error_description"], "refresh token reuse detected; grant revoked")
	eq(t, o.mcpStatus(fresh["access_token"].(string)), 401)
	eq(t, o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", fresh["refresh_token"].(string), "client_id", client).status, 400)
	// another client cannot use the refresh token either
	other := o.register(redirect)
	_, tok2 := o.connect(u)
	eq(t, o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", tok2["refresh_token"].(string), "client_id", other).json()["error"], "invalid_grant")
}

func TestOAuthSecurityScopeEnforcement(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	_, tok := o.connect(u)
	access := tok["access_token"].(string)
	post := func(cmd string) int { return o.command(access, u.ws, cmd) }
	// An OAuth session is an agent: capped below owner and held to the workspace's agent tool groups.
	eq(t, post("apikey.create"), 403)
	eq(t, post("agent.create"), 403)
	eq(t, post("workspace.update"), 403)
	eq(t, post("space.list"), 200)
	o.must(u, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read"}}})
	eq(t, o.req("POST", "/api/w/"+u.ws+"/commands/space.create", map[string]string{"Authorization": "Bearer " + access}, []byte(`{"key":"X","name":"X"}`), "application/json").status, 403)
	eq(t, post("space.list"), 200)
	// ...and an API key limited to read cannot write over MCP even where the group is enabled for agents.
	o.must(u, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes"}}})
	reader := o.must(u, "agent.create", map[string]any{"name": "r", "scopes": []string{"read"}})["api_key"].(string)
	body := []byte(rpc(1, "tools/call", call("invoke", map[string]any{"command": "note.create", "arguments": map[string]any{"space_id": o.space(u), "title": "x"}})))
	r := o.req("POST", "/mcp", map[string]string{"Accept": "application/json, text/event-stream", "Authorization": "Bearer " + reader}, body, "application/json")
	if !strings.Contains(string(r.body), `"code":"forbidden"`) {
		t.Fatalf("read-only key wrote: %s", r.body)
	}
	// A token never reaches another workspace.
	b := o.signup("B", true)
	eq(t, o.command(access, b.ws, "space.list"), 403)
}

func TestOAuthSecurityRevokedTokenIsRejected(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	// 1. revoking the access token (RFC 7009) kills it but not the grant
	client, tok := o.connect(u)
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
	eq(t, o.postForm("/oauth/revoke", "token", access, "client_id", client).status, 200)
	eq(t, o.mcpStatus(access), 401)
	eq(t, o.command(access, u.ws, "space.list"), 401)
	eq(t, o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", refresh, "client_id", client).status, 200)
	// 2. revoking a refresh token ends the grant, access token included
	client, tok = o.connect(u)
	eq(t, o.postForm("/oauth/revoke", "token", tok["refresh_token"].(string), "client_id", client).status, 200)
	eq(t, o.mcpStatus(tok["access_token"].(string)), 401)
	// 3. the person disconnecting the app in C Note
	_, tok = o.connect(u)
	var apps []map[string]any
	_ = json.Unmarshal(o.cmd(o.base, u, u.ws, "oauth_app.list", map[string]any{}, nil).body, &apps)
	eq(t, o.cmd(o.base, u, u.ws, "oauth_app.revoke", map[string]any{"id": apps[0]["id"]}, nil).status, 200)
	eq(t, o.mcpStatus(tok["access_token"].(string)), 401)
	// 4. a revoked grant stays revoked even if its token row is still there
	_, tok = o.connect(u)
	adminSQL(o.env, u.ws, "update core.oauth_grants set revoked_at = now() where workspace_id = $1::uuid and revoked_at is null", u.ws)
	eq(t, o.mcpStatus(tok["access_token"].(string)), 401)
	// 5. expired tokens
	_, tok = o.connect(u)
	adminSQL(o.env, u.ws, "update core.oauth_tokens set expires_at = now() - interval '1 second' where token_hash = $1", sha("cno_"+strings.TrimPrefix(tok["access_token"].(string), "cno_")))
	eq(t, o.mcpStatus(tok["access_token"].(string)), 401)
	// 6. made-up and truncated tokens
	for _, junk := range []string{"cno_", "cno_junk", tok["access_token"].(string) + "x", tok["access_token"].(string)[:20], "cnr_" + tok["access_token"].(string)[4:]} {
		eq(t, o.mcpStatus(junk), 401)
	}
}

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestOAuthSecuritySecretsAreStoredHashedAndNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register(redirect)
	verifier, challenge := pkce()
	code := query(t, o.authorize(u, o.authzBody(client, redirect, challenge), u.ws, true, nil).json()["redirect_to"], "code")
	tok := o.postForm("/oauth/token", tokenForm(client, code, verifier)...).json()
	for _, c := range []struct{ table, col, secret string }{{"oauth_codes", "code_hash", code}, {"oauth_tokens", "token_hash", tok["access_token"].(string)}, {"oauth_tokens", "token_hash", tok["refresh_token"].(string)}} {
		n := adminScalar(o.env, u.ws, "select count(*)::text from core."+c.table+" where "+c.col+" = $1", sha(c.secret))
		eq(t, n, "1") // stored as hex SHA-256, the same hashing Rust uses
		eq(t, adminScalar(o.env, u.ws, "select count(*)::text from core."+c.table+" where "+c.col+" = $1", c.secret), "0")
	}
	// provoke errors that carry the secrets: reuse, junk, revoked tokens
	o.postForm("/oauth/token", tokenForm(client, code, verifier)...)
	o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", tok["refresh_token"].(string), "client_id", "other")
	o.mcpStatus(tok["access_token"].(string))
	for name, secret := range map[string]string{"code": code, "verifier": verifier, "access": tok["access_token"].(string), "refresh": tok["refresh_token"].(string)} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("%s leaked into the logs", name)
		}
	}
}

func TestOAuthSecurityStrangersCannotUseConsentEndpoints(t *testing.T) {
	o := newOC(t)
	client := o.register(redirect)
	_, challenge := pkce()
	body := o.authzBody(client, redirect, challenge)
	for _, path := range []string{"/api/oauth/authorize", "/api/oauth/authorize/context"} {
		body["workspace_id"], body["approve"] = "6f1c8f1e-0000-4000-8000-000000000000", true
		eq(t, o.postJSON(path, nil, body).status, 401)
		eq(t, o.postJSON(path, map[string]string{"Cookie": "cn_session=nope"}, body).status, 401)
		eq(t, o.postJSON(path, map[string]string{"Authorization": "Bearer cn_nope_nope"}, body).status, 401) // an API key is not a person's session
		r := o.postJSON(path, nil, body)
		if strings.Contains(r.hdr.Get("Access-Control-Allow-Origin"), "*") {
			t.Errorf("%s answers cross-origin callers", path)
		}
	}
}
