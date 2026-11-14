package server_test

// OAuth 2.1 authorization server for MCP clients: ports of crates/cnote-server/tests/oauth.rs plus one test per
// security check. Fixtures (users, workspaces) come from the Rust server; the OAuth flows run against Go.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/server"
)

const redirect = "http://127.0.0.1:7777/callback"

type oc struct {
	*env
	t    *testing.T
	base string // what the issuer and resource look like: http://<host of the Go test server>
	xff  string // distinct per test so in-process rate limits never leak between tests
}

type hresp struct {
	status int
	hdr    http.Header
	body   []byte
}

func (h hresp) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(h.body, &m)
	return m
}

func newOC(t *testing.T) *oc {
	return newOCFor(t, newEnv(t))
}

// newOCDiff is newOC without the guard: differential tests send Rust-owned corner cases through the real proxy.
func newOCDiff(t *testing.T) *oc {
	e := newEnv(t)
	t.Setenv("TRUST_PROXY", "1")
	unsetEnv(t, "PUBLIC_BASE_URL")
	return &oc{env: e, t: t, base: e.gosv.URL, xff: fmt.Sprintf("10.%d.%d.%d", mrand.Intn(256), mrand.Intn(256), 1+mrand.Intn(250))}
}

func newOCFor(t *testing.T, e *env) *oc {
	t.Helper()
	t.Setenv("TRUST_PROXY", "1")
	unsetEnv(t, "PUBLIC_BASE_URL")
	// A Go server whose upstream fails the test: everything here must be answered natively, never by Rust.
	guard := httptest.NewServer(server.New(e.pool, command.NewRegistry(modules.Commands()...), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("proxied to Rust: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
	})))
	t.Cleanup(guard.Close)
	e.gosv = guard
	return &oc{env: e, t: t, base: e.gosv.URL, xff: fmt.Sprintf("10.%d.%d.%d", mrand.Intn(256), mrand.Intn(256), 1+mrand.Intn(250))}
}

func (o *oc) req(method, path string, hdr map[string]string, body []byte, ct string) hresp {
	o.t.Helper()
	return o.reqAt(o.base, method, path, hdr, body, ct)
}

func (o *oc) reqAt(base, method, path string, hdr map[string]string, body []byte, ct string) hresp {
	o.t.Helper()
	req, _ := http.NewRequest(method, base+path, bytes.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("X-Forwarded-For", o.xff)
	for k, v := range hdr {
		if strings.EqualFold(k, "host") {
			req.Host = v
		}
		req.Header.Set(k, v)
	}
	res, err := noRedirect.Do(req)
	if err != nil {
		o.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return hresp{res.StatusCode, res.Header, b}
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (o *oc) postJSON(path string, hdr map[string]string, v any) hresp {
	b, _ := json.Marshal(v)
	return o.req("POST", path, hdr, b, "application/json")
}

func (o *oc) postForm(path string, kv ...string) hresp {
	f := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		f.Set(kv[i], kv[i+1])
	}
	return o.req("POST", path, nil, []byte(f.Encode()), "application/x-www-form-urlencoded")
}

func (o *oc) register(redir string) string {
	o.t.Helper()
	r := o.postJSON("/oauth/register", nil, map[string]any{"client_name": "Test Agent", "redirect_uris": []string{redir}, "token_endpoint_auth_method": "none"})
	if r.status != 201 {
		o.t.Fatalf("register: %d %s", r.status, r.body)
	}
	return r.json()["client_id"].(string)
}

func pkce() (verifier, challenge string) {
	verifier = rnd() + rnd() + rnd()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (o *oc) authzBody(client, redir, challenge string) map[string]any {
	return map[string]any{"client_id": client, "redirect_uri": redir, "response_type": "code", "code_challenge": challenge,
		"code_challenge_method": "S256", "resource": o.base + "/mcp", "state": "st-1"}
}

func (o *oc) authorize(u who, body map[string]any, ws string, approve bool, hdr map[string]string) hresp {
	body["workspace_id"], body["approve"] = ws, approve
	h := map[string]string{"Cookie": "cn_session=" + u.cookie}
	for k, v := range hdr {
		h[k] = v
	}
	return o.postJSON("/api/oauth/authorize", h, body)
}

func query(t *testing.T, redirectTo any, key string) string {
	t.Helper()
	s, _ := redirectTo.(string)
	u, err := url.Parse(s)
	if err != nil || !u.Query().Has(key) {
		t.Fatalf("%s missing in %v", key, redirectTo)
	}
	return u.Query().Get(key)
}

// connect registers, approves and exchanges: returns the client id and the token response.
func (o *oc) connect(u who) (string, map[string]any) {
	o.t.Helper()
	client := o.register(redirect)
	verifier, challenge := pkce()
	out := o.authorize(u, o.authzBody(client, redirect, challenge), u.ws, true, nil).json()
	code := query(o.t, out["redirect_to"], "code")
	r := o.postForm("/oauth/token", "grant_type", "authorization_code", "code", code, "redirect_uri", redirect, "client_id", client, "code_verifier", verifier)
	if r.status != 200 {
		o.t.Fatalf("token: %d %s", r.status, r.body)
	}
	return client, r.json()
}

func (o *oc) mcpStatus(bearer string) int {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{}})
	return o.req("POST", "/mcp", map[string]string{"Accept": "application/json, text/event-stream", "Authorization": "Bearer " + bearer}, body, "application/json").status
}

func (o *oc) command(bearer, ws, name string) int {
	return o.req("POST", "/api/w/"+ws+"/commands/"+name, map[string]string{"Authorization": "Bearer " + bearer}, []byte(`{}`), "application/json").status
}

func tokenForm(client, code, verifier string) []string {
	return []string{"grant_type", "authorization_code", "code", code, "redirect_uri", redirect, "client_id", client, "code_verifier", verifier}
}

func TestOAuthDiscoveryDocumentsAndChallenge(t *testing.T) {
	o := newOC(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		m := o.req("GET", path, nil, nil, "").json()
		eq(t, m["resource"], o.base+"/mcp")
		eq(t, m["authorization_servers"], []any{o.base})
	}
	r := o.req("GET", "/.well-known/oauth-authorization-server", map[string]string{"Origin": "https://inspector.example"}, nil, "")
	eq(t, r.hdr.Get("Access-Control-Allow-Origin"), "*")
	m := r.json()
	eq(t, m["issuer"], o.base)
	eq(t, m["code_challenge_methods_supported"], []any{"S256"})
	eq(t, m["token_endpoint"], o.base+"/oauth/token")
	eq(t, m["client_id_metadata_document_supported"], true)

	r = o.req("POST", "/mcp", nil, []byte(`{}`), "application/json")
	eq(t, r.status, 401)
	want := `resource_metadata="` + o.base + `/.well-known/oauth-protected-resource/mcp"`
	if !strings.Contains(r.hdr.Get("WWW-Authenticate"), want) {
		t.Fatalf("challenge %q lacks %s", r.hdr.Get("WWW-Authenticate"), want)
	}
}

func TestOAuthFullFlowRefreshRotationReuseAndRevocation(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client, tok := o.connect(u)
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
	if !strings.HasPrefix(access, "cno_") || !strings.HasPrefix(refresh, "cnr_") {
		t.Fatalf("token shapes: %v", tok)
	}
	eq(t, tok["token_type"], "Bearer")
	eq(t, tok["expires_in"], float64(3600))

	// Works on the MCP endpoint and the command API, bound to the approved workspace.
	eq(t, o.mcpStatus(access), 200)
	eq(t, o.command(access, u.ws, "space.list"), 200)

	// Refresh rotates both tokens; the old access token stops working.
	r := o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", refresh, "client_id", client)
	eq(t, r.status, 200)
	access2, refresh2 := r.json()["access_token"].(string), r.json()["refresh_token"].(string)
	if refresh2 == refresh {
		t.Fatal("refresh token was not rotated")
	}
	eq(t, o.mcpStatus(access), 401)
	eq(t, o.mcpStatus(access2), 200)

	// Replaying the rotated-out refresh token is theft: the whole family dies.
	r = o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", refresh, "client_id", client)
	eq(t, r.status, 400)
	eq(t, r.json()["error"], "invalid_grant")
	eq(t, o.mcpStatus(access2), 401)
	eq(t, o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", refresh2, "client_id", client).status, 400)

	// RFC 7009 revocation: refresh token ends the grant; wrong client changes nothing.
	client, tok = o.connect(u)
	access, refresh = tok["access_token"].(string), tok["refresh_token"].(string)
	eq(t, o.postForm("/oauth/revoke", "token", refresh, "client_id", "cnc_someone_else").status, 200)
	eq(t, o.mcpStatus(access), 200)
	eq(t, o.postForm("/oauth/revoke", "token", refresh, "client_id", client).status, 200)
	eq(t, o.mcpStatus(access), 401)
}

func TestOAuthPrincipalIsAnAgentCappedBelowOwner(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true) // the signing-up user is the workspace owner
	_, tok := o.connect(u)
	access := tok["access_token"].(string)
	// Agents cannot mint keys or manage connected apps.
	eq(t, o.command(access, u.ws, "apikey.create"), 403)
	eq(t, o.command(access, u.ws, "oauth_app.list"), 403)
	// ...but are admins otherwise: an admin-only command that agents may run is allowed in the default groups.
	eq(t, o.command(access, u.ws, "space.list"), 200)
}

func TestOAuthTokenIsBoundToItsWorkspace(t *testing.T) {
	o := newOC(t)
	a, b := o.signup("A", true), o.signup("B", true)
	_, tok := o.connect(a)
	eq(t, o.command(tok["access_token"].(string), b.ws, "space.list"), 403)

	// A user cannot approve into a workspace they do not belong to.
	client := o.register(redirect)
	_, challenge := pkce()
	eq(t, o.authorize(a, o.authzBody(client, redirect, challenge), b.ws, true, nil).status, 403)
	// Authorization requires a session.
	eq(t, o.postJSON("/api/oauth/authorize", nil, map[string]any{"workspace_id": a.ws, "approve": true}).status, 401)
}

func TestOAuthPKCEIsMandatoryAndVerified(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register(redirect)
	verifier, challenge := pkce()

	// plain, missing challenge: bounced back to the (verified) redirect with an error and the state.
	for _, c := range [][2]string{{"code_challenge_method", "plain"}, {"code_challenge", ""}, {"code_challenge_method", ""}} {
		body := o.authzBody(client, redirect, challenge)
		body[c[0]] = c[1]
		out := o.authorize(u, body, u.ws, true, nil).json()
		eq(t, out["ok"], false)
		eq(t, out["error"], "invalid_request")
		eq(t, query(t, out["redirect_to"], "error"), "invalid_request")
		eq(t, query(t, out["redirect_to"], "state"), "st-1")
		eq(t, query(t, out["redirect_to"], "iss"), o.base)
	}

	out := o.authorize(u, o.authzBody(client, redirect, challenge), u.ws, true, nil).json()
	eq(t, query(t, out["redirect_to"], "state"), "st-1")
	code := query(t, out["redirect_to"], "code")
	exchange := func(verifier, code string) hresp {
		return o.postForm("/oauth/token", tokenForm(client, code, verifier)...)
	}
	// Wrong verifier and malformed verifier both fail (and do not mint tokens).
	wrong, _ := pkce()
	eq(t, exchange(wrong, code).json()["error"], "invalid_grant")
	eq(t, exchange("short", code).json()["error"], "invalid_request")
	// The right verifier still works afterwards: a failed attempt did not burn the code.
	eq(t, exchange(verifier, code).status, 200)
	// Replay of a used code is refused.
	eq(t, exchange(verifier, code).json()["error"], "invalid_grant")
}

func TestOAuthCodeReplayRevokesIssuedTokens(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register(redirect)
	verifier, challenge := pkce()
	out := o.authorize(u, o.authzBody(client, redirect, challenge), u.ws, true, nil).json()
	code := query(t, out["redirect_to"], "code")
	form := tokenForm(client, code, verifier)
	r := o.postForm("/oauth/token", form...)
	eq(t, r.status, 200)
	access := r.json()["access_token"].(string)
	eq(t, o.mcpStatus(access), 200)
	eq(t, o.postForm("/oauth/token", form...).status, 400)
	eq(t, o.mcpStatus(access), 401)
}

func TestOAuthResourceIndicatorIsRequiredAndBound(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register(redirect)
	verifier, challenge := pkce()
	for _, resource := range []string{"", "https://evil.example/mcp", "http://127.0.0.1:1/mcp", o.base} {
		body := o.authzBody(client, redirect, challenge)
		body["resource"] = resource
		eq(t, o.authorize(u, body, u.ws, true, nil).json()["error"], "invalid_target")
	}
	// A trailing slash is canonicalized, not refused.
	body := o.authzBody(client, redirect, challenge)
	body["resource"] = o.base + "/mcp/"
	eq(t, o.authorize(u, body, u.ws, true, nil).json()["ok"], true)

	out := o.authorize(u, o.authzBody(client, redirect, challenge), u.ws, true, nil).json()
	code := query(t, out["redirect_to"], "code")
	r := o.postForm("/oauth/token", append(tokenForm(client, code, verifier), "resource", "https://evil.example/mcp")...)
	eq(t, r.status, 400)
	eq(t, r.json()["error"], "invalid_target")
}

func TestOAuthRedirectURIMustMatchExactly(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register(redirect)
	verifier, challenge := pkce()

	// Never redirect to an unregistered URI: the response carries no redirect_to at all, on both consent endpoints.
	for _, bad := range []string{"http://127.0.0.1:7777/callback/", "http://127.0.0.1:7778/callback", "https://evil.example/callback", "HTTP://127.0.0.1:7777/callback"} {
		for _, path := range []string{"/api/oauth/authorize/context", "/api/oauth/authorize"} {
			body := o.authzBody(client, bad, challenge)
			body["workspace_id"], body["approve"] = u.ws, true
			out := o.postJSON(path, map[string]string{"Cookie": "cn_session=" + u.cookie}, body).json()
			eq(t, out["ok"], false)
			eq(t, out["redirect_to"], nil)
		}
	}

	// Registration rejects insecure or dangerous redirects.
	for _, bad := range []string{"http://example.com/cb", "javascript:alert(1)", "https://x.example/cb#f", "https://u@x.example/cb", "/relative", ""} {
		r := o.postJSON("/oauth/register", nil, map[string]any{"client_name": "x", "redirect_uris": []string{bad}})
		eq(t, r.status, 400)
		eq(t, r.json()["error"], "invalid_redirect_uri")
	}

	// The token step re-checks the URI the code was issued for.
	out := o.authorize(u, o.authzBody(client, redirect, challenge), u.ws, true, nil).json()
	code := query(t, out["redirect_to"], "code")
	r := o.postForm("/oauth/token", "grant_type", "authorization_code", "code", code, "redirect_uri", "http://127.0.0.1:7777/other", "client_id", client, "code_verifier", verifier)
	eq(t, r.status, 400)
	eq(t, r.json()["error"], "invalid_grant")
	// A different client cannot redeem it either.
	other := o.register(redirect)
	eq(t, o.postForm("/oauth/token", tokenForm(other, code, verifier)...).status, 400)
}

func TestOAuthConsentContextDenyAndCrossSite(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client := o.register(redirect)
	_, challenge := pkce()
	body := o.authzBody(client, redirect, challenge)
	ck := map[string]string{"Cookie": "cn_session=" + u.cookie}

	ctx := o.postJSON("/api/oauth/authorize/context", ck, body).json()
	eq(t, ctx["ok"], true)
	eq(t, ctx["client_name"], "Test Agent")
	eq(t, ctx["redirect_host"], "127.0.0.1:7777")
	eq(t, ctx["workspaces"].([]any)[0].(map[string]any)["id"], u.ws)

	out := o.authorize(u, body, u.ws, false, nil).json()
	eq(t, query(t, out["redirect_to"], "error"), "access_denied")
	eq(t, query(t, out["redirect_to"], "state"), "st-1")

	b2 := map[string]any{}
	for k, v := range body {
		b2[k] = v
	}
	eq(t, o.authorize(u, b2, u.ws, true, map[string]string{"Sec-Fetch-Site": "cross-site"}).status, 403)
}

func TestOAuthConnectedAppsListAndRevoke(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	_, tok := o.connect(u)
	access := tok["access_token"].(string)
	run := func(w who, name string, body any) resp { return o.cmd(o.base, w, w.ws, name, body, nil) }

	apps := run(u, "oauth_app.list", map[string]any{})
	var list []map[string]any
	_ = json.Unmarshal(apps.body, &list)
	eq(t, len(list), 1)
	eq(t, list[0]["name"], "Test Agent")
	// Another user does not see it.
	other := o.signup("Other", true)
	eq(t, string(run(other, "oauth_app.list", map[string]any{}).body), "[]")

	eq(t, o.mcpStatus(access), 200)
	out := run(u, "oauth_app.revoke", map[string]any{"id": list[0]["id"]})
	eq(t, out.json()["revoked"], true)
	eq(t, o.mcpStatus(access), 401)
	eq(t, string(run(u, "oauth_app.list", map[string]any{}).body), "[]")
	eq(t, run(u, "oauth_app.revoke", map[string]any{"id": list[0]["id"]}).json()["revoked"], false)
}

func TestOAuthClientIDMetadataDocumentFlow(t *testing.T) {
	o := newOC(t)
	t.Setenv("OAUTH_CIMD_ALLOW_PRIVATE", "1") // the throwaway metadata host is on loopback
	u := o.signup("Olive", true)
	var clientID string
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": clientID, "client_name": "Doc Client", "redirect_uris": []string{redirect}})
	}))
	defer meta.Close()
	clientID = meta.URL + "/client.json"

	verifier, challenge := pkce()
	out := o.authorize(u, o.authzBody(clientID, redirect, challenge), u.ws, true, nil).json()
	code := query(t, out["redirect_to"], "code")
	r := o.postForm("/oauth/token", tokenForm(clientID, code, verifier)...)
	eq(t, r.status, 200)
	eq(t, o.mcpStatus(r.json()["access_token"].(string)), 200)
	ctx := o.postJSON("/api/oauth/authorize/context", map[string]string{"Cookie": "cn_session=" + u.cookie}, o.authzBody(clientID, redirect, challenge)).json()
	eq(t, ctx["client_name"], "Doc Client")

	// A document that names another client_id, or asks for a secret, is refused.
	for _, doc := range []map[string]any{
		{"client_id": "https://x.example/c.json", "redirect_uris": []string{redirect}},
		{"client_id": "SELF", "token_endpoint_auth_method": "client_secret_basic", "redirect_uris": []string{redirect}},
		{"client_id": "SELF", "client_secret": "x", "redirect_uris": []string{redirect}},
		{"client_id": "SELF", "redirect_uris": []string{"http://example.com/cb"}},
		{"client_id": "SELF", "redirect_uris": []string{}},
	} {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d := map[string]any{}
			for k, v := range doc {
				d[k] = v
			}
			if d["client_id"] == "SELF" {
				d["client_id"] = "http://" + r.Host + "/c.json"
			}
			_ = json.NewEncoder(w).Encode(d)
		}))
		id := bad.URL + "/c.json"
		got := o.postJSON("/api/oauth/authorize/context", map[string]string{"Cookie": "cn_session=" + u.cookie}, o.authzBody(id, redirect, challenge)).json()
		eq(t, got["ok"], false)
		eq(t, got["redirect_to"], nil)
		bad.Close()
	}
}

func TestOAuthRegistrationIsRateLimited(t *testing.T) {
	o := newOC(t)
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		limited = o.postJSON("/oauth/register", nil, map[string]any{"redirect_uris": []string{redirect}}).status == 429
	}
	if !limited {
		t.Fatal("registration never throttled")
	}
}

func TestOAuthTokenResourceIsCheckedOnEveryUse(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	_, tok := o.connect(u)
	access := tok["access_token"].(string)
	eq(t, o.mcpStatus(access), 200)
	eq(t, o.command(access, u.ws, "space.list"), 200) // the same protected resource also serves the command API
	adminSQL(o.env, u.ws, "update core.oauth_grants set resource = 'https://other.example/mcp' where workspace_id = $1::uuid", u.ws)
	eq(t, o.mcpStatus(access), 401)
	eq(t, o.command(access, u.ws, "space.list"), 401)
}

func TestOAuthRemovedMemberGrantsStayDeadAfterReAdding(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client, tok := o.connect(u)
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
	adminSQL(o.env, u.ws, "delete from core.memberships where user_id = $1::uuid", u.userID)
	eq(t, o.mcpStatus(access), 401)
	adminSQL(o.env, u.ws, "insert into core.memberships (workspace_id, user_id, role) values ($1::uuid,$2::uuid,'member')", u.ws, u.userID)
	eq(t, o.mcpStatus(access), 401) // re-adding must not resurrect the grant
	r := o.postForm("/oauth/token", "grant_type", "refresh_token", "refresh_token", refresh, "client_id", client)
	eq(t, r.status, 400)
	eq(t, r.json()["error"], "invalid_grant")
}

func TestOAuthUnconfiguredServerServesOnlyLoopbackHosts(t *testing.T) {
	o := newOC(t)
	m := o.req("GET", "/.well-known/oauth-authorization-server", nil, nil, "").json()
	eq(t, m["issuer"], o.base) // loopback Host: the issuer follows it
	evil := map[string]string{"Host": "evil.example"}
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource"} {
		eq(t, o.req("GET", path, evil, nil, "").status, 503)
	}
	b, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirect}})
	eq(t, o.req("POST", "/oauth/register", evil, b, "application/json").status, 503)
	r := o.req("POST", "/mcp", evil, []byte(`{}`), "application/json")
	eq(t, r.status, 401)
	if strings.Contains(r.hdr.Get("WWW-Authenticate"), "evil.example") {
		t.Fatal("challenge echoes a spoofed Host")
	}
}

func TestOAuthConsentContextIsThrottledPerUser(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	_, challenge := pkce()
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		limited = o.postJSON("/api/oauth/authorize/context", map[string]string{"Cookie": "cn_session=" + u.cookie}, o.authzBody("cnc_unknown", redirect, challenge)).status == 429
	}
	if !limited {
		t.Fatal("consent context never throttled")
	}
}

func TestOAuthJunkTokenRequestsDoNotChargeAClientsOwnBucket(t *testing.T) {
	o := newOC(t)
	u := o.signup("Olive", true)
	client, _ := o.connect(u)
	// 50 junk exchanges naming the client stay under the per-address limit and must not drain the client bucket.
	for i := 0; i < 50; i++ {
		r := o.postForm("/oauth/token", "grant_type", "authorization_code", "code", "cnx_junk", "redirect_uri", redirect, "client_id", client, "code_verifier", strings.Repeat("a", 43))
		eq(t, r.status, 400)
	}
	// ...and the client can still redeem a real code afterwards.
	_, tok := o.connect(u)
	eq(t, o.mcpStatus(tok["access_token"].(string)), 200)
}
