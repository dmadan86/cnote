package server_test

// OAuth differential tests: the same request goes to Rust and to Go (same Host, so issuer URLs agree). Stateless
// answers must be byte-identical; flows that mint random secrets are compared by shape and by the rows they leave.

import (
	"bytes"
	"encoding/json"
	"fmt"
	mrand "math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// ref is the Rust reference these tests compare against. Rust throttles registration and tokens per address, and
// :4000 is shared by everyone, so set RUST_OAUTH_URL to a private instance (`cnote serve --port 4520` with
// OAUTH_CIMD_ALLOW_PRIVATE=1) for a complete run; without it throttled cases are skipped.
func (o *oc) ref() string {
	if v := strings.TrimRight(os.Getenv("RUST_OAUTH_URL"), "/"); v != "" {
		return v
	}
	return o.rust
}

type diffCase struct {
	name, method, path string
	hdr                map[string]string
	body               []byte
	ct                 string
}

func (o *oc) diff(c diffCase) (hresp, hresp) {
	o.t.Helper()
	// a fresh client address per case: Rust (with TRUST_PROXY=1) and Go both key their throttles on it
	h := map[string]string{"Host": "localhost:4000", "X-Forwarded-For": fmt.Sprintf("10.%d.%d.%d", mrand.Intn(256), mrand.Intn(256), 1+mrand.Intn(250))}
	for k, v := range c.hdr {
		h[k] = v
	}
	rust := o.reqAt(o.ref(), c.method, c.path, h, c.body, c.ct)
	if rust.status == 429 {
		o.t.Logf("%s: Rust throttled the shared test address; case skipped", c.name)
		return rust, rust
	}
	goo := o.reqAt(o.base, c.method, c.path, h, c.body, c.ct)
	mask := func(b []byte) []byte { return secretRe.ReplaceAll(b, []byte("SECRET")) } // random ids and secrets
	if rb, gb := mask(rust.body), mask(goo.body); rust.status != goo.status || !bytes.Equal(rb, gb) {
		o.t.Errorf("%s: differs\nrust %d %.500s\ngo   %d %.500s", c.name, rust.status, rb, goo.status, gb)
	}
	for _, k := range []string{"Content-Type", "Cache-Control", "Pragma", "Access-Control-Allow-Origin", "Allow", "Content-Length"} {
		if rust.hdr.Get(k) != goo.hdr.Get(k) {
			o.t.Errorf("%s: header %s rust %q go %q", c.name, k, rust.hdr.Get(k), goo.hdr.Get(k))
		}
	}
	return rust, goo
}

func jsonBody(v any) []byte { b, _ := json.Marshal(v); return b }

func formBody(kv ...string) []byte {
	f := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		f.Set(kv[i], kv[i+1])
	}
	return []byte(f.Encode())
}

func TestOAuthDifferentialMetadataAndErrors(t *testing.T) {
	o := newOCDiff(t)
	t.Setenv("PUBLIC_WEB_URL", "")
	unsetEnv(t, "PUBLIC_WEB_URL")
	const form = "application/x-www-form-urlencoded"
	cases := []diffCase{
		{name: "protected resource", method: "GET", path: "/.well-known/oauth-protected-resource"},
		{name: "protected resource /mcp", method: "GET", path: "/.well-known/oauth-protected-resource/mcp"},
		{name: "authorization server", method: "GET", path: "/.well-known/oauth-authorization-server"},
		{name: "authorization server with origin", method: "GET", path: "/.well-known/oauth-authorization-server", hdr: map[string]string{"Origin": "https://i.example"}},
		{name: "metadata evil host", method: "GET", path: "/.well-known/oauth-authorization-server", hdr: map[string]string{"Host": "evil.example"}},
		{name: "resource evil host", method: "GET", path: "/.well-known/oauth-protected-resource", hdr: map[string]string{"Host": "evil.example"}},
		{name: "metadata ipv6 host", method: "GET", path: "/.well-known/oauth-authorization-server", hdr: map[string]string{"Host": "[::1]:4000"}},
		{name: "options preflight", method: "OPTIONS", path: "/oauth/token", hdr: map[string]string{"Origin": "https://x.example", "Access-Control-Request-Method": "POST"}},
		{name: "get on token", method: "GET", path: "/oauth/token"},
		{name: "revoke no fields", method: "POST", path: "/oauth/revoke", ct: form, body: formBody()},
		{name: "revoke unknown token", method: "POST", path: "/oauth/revoke", ct: form, body: formBody("token", "x", "client_id", "y")},
		{name: "revoke wrong content type", method: "POST", path: "/oauth/revoke", ct: "application/json", body: []byte(`{}`)},
		{name: "token no client", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "authorization_code")},
		{name: "token empty client", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "authorization_code", "client_id", "")},
		{name: "token bad grant", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "password", "client_id", "c")},
		{name: "token no grant", method: "POST", path: "/oauth/token", ct: form, body: formBody("client_id", "c")},
		{name: "token no code", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "authorization_code", "client_id", "c")},
		{name: "token no verifier", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "authorization_code", "client_id", "c", "code", "x")},
		{name: "token short verifier", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "authorization_code", "client_id", "c", "code", "x", "code_verifier", "short")},
		{name: "token bad verifier chars", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "authorization_code", "client_id", "c", "code", "x", "code_verifier", strings.Repeat("a", 42)+"!")},
		{name: "token unknown code", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "authorization_code", "client_id", "c", "code", "x", "code_verifier", strings.Repeat("a", 43))},
		{name: "token refresh missing", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "refresh_token", "client_id", "c")},
		{name: "token refresh wrong prefix", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "refresh_token", "client_id", "c", "refresh_token", "abc")},
		{name: "token refresh unknown", method: "POST", path: "/oauth/token", ct: form, body: formBody("grant_type", "refresh_token", "client_id", "c", "refresh_token", "cnr_unknown")},
		{name: "token json body", method: "POST", path: "/oauth/token", ct: "application/json", body: []byte(`{"grant_type":"x"}`)},
		{name: "token evil host", method: "POST", path: "/oauth/token", ct: form, hdr: map[string]string{"Host": "evil.example"}, body: formBody("grant_type", "authorization_code", "client_id", "c")},
		{name: "token duplicate key", method: "POST", path: "/oauth/token", ct: form, body: []byte("grant_type=a&grant_type=b&client_id=c")},
		{name: "token plus and percent", method: "POST", path: "/oauth/token", ct: form, body: []byte("grant_type=refresh%5Ftoken&client_id=a+b&refresh_token=cnr_x%20y")},
		{name: "register not json", method: "POST", path: "/oauth/register", ct: "text/plain", body: []byte(`{}`)},
		{name: "register bad type", method: "POST", path: "/oauth/register", ct: "application/json", body: []byte(`{"client_name":5}`)},
		{name: "register null name", method: "POST", path: "/oauth/register", ct: "application/json", body: []byte(`{"client_name":null,"redirect_uris":["https://a.example/cb"]}`)},
		{name: "register null uris", method: "POST", path: "/oauth/register", ct: "application/json", body: []byte(`{"redirect_uris":null}`)},
		{name: "register duplicate key", method: "POST", path: "/oauth/register", ct: "application/json", body: []byte(`{"client_name":"a","client_name":"b","redirect_uris":["https://a.example/cb"]}`)},
		{name: "register array body", method: "POST", path: "/oauth/register", ct: "application/json", body: []byte(`[]`)},
		{name: "register no body", method: "POST", path: "/oauth/register", ct: "application/json", body: []byte(``)},
		{name: "register empty object", method: "POST", path: "/oauth/register", ct: "application/json", body: []byte(`{}`)},
		{name: "register secret method", method: "POST", path: "/oauth/register", ct: "application/json", body: jsonBody(map[string]any{"redirect_uris": []string{"https://a.example/cb"}, "token_endpoint_auth_method": "client_secret_basic"})},
		{name: "register bad grants", method: "POST", path: "/oauth/register", ct: "application/json", body: jsonBody(map[string]any{"redirect_uris": []string{"https://a.example/cb"}, "grant_types": []string{"password"}})},
		{name: "register 11 uris", method: "POST", path: "/oauth/register", ct: "application/json", body: jsonBody(map[string]any{"redirect_uris": make([]string, 11)})},
		{name: "register evil host", method: "POST", path: "/oauth/register", ct: "application/json", hdr: map[string]string{"Host": "evil.example"}, body: jsonBody(map[string]any{"redirect_uris": []string{"https://a.example/cb"}})},
	}
	// Rust's registration throttle (10 per address, shared by everyone testing against :4000) limits how many of these
	// can run against it; the full table is in TestOAuthRedirectURIRules.
	for _, bad := range []string{"http://example.com/cb", "https://example.com/cb#frag", "javascript:alert(1)", "/relative", "https://example.com", "http://localhost:3000/cb"} {
		cases = append(cases, diffCase{name: "register redirect " + bad, method: "POST", path: "/oauth/register", ct: "application/json",
			body: jsonBody(map[string]any{"redirect_uris": []string{bad}})})
	}
	for _, c := range cases {
		o.diff(c)
	}
}

// Registration: both servers answer 201 with the same shape; the stored rows match.
func TestOAuthDifferentialRegistrationRows(t *testing.T) {
	o := newOCDiff(t)
	body := jsonBody(map[string]any{"client_name": "  Név\u0007 " + strings.Repeat("x", 90), "redirect_uris": []string{"https://a.example/cb?x=1", "http://localhost:3000/cb"},
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code"}, "response_types": []string{"code"}})
	rust, goo := o.diff(diffCase{name: "register ok", method: "POST", path: "/oauth/register", ct: "application/json", body: body})
	if rust.status == 429 {
		return
	}
	eq(t, goo.status, 201)
	// the stored row equals what Rust stores for the same input
	row := func(r hresp) string {
		return adminScalar(o.env, "00000000-0000-0000-0000-000000000000", "select kind || '|' || name || '|' || array_to_string(redirect_uris, ',') from core.oauth_clients where client_id = $1", regexp.MustCompile(`"client_id":"([^"]+)"`).FindStringSubmatch(string(r.body))[1])
	}
	eq(t, row(goo), row(rust))
	if len(regexp.MustCompile(`"client_id":"cnc_[A-Za-z0-9_-]{43}"`).FindString(string(goo.body))) == 0 {
		t.Fatalf("client_id shape: %s", goo.body)
	}
}

// The consent endpoints answer the same bytes on both servers for the same session.
func TestOAuthDifferentialConsent(t *testing.T) {
	o := newOCDiff(t)
	reg := o.reqAt(o.ref(), "POST", "/oauth/register", map[string]string{"Host": "localhost:4000"}, jsonBody(map[string]any{"client_name": "Test Agent", "redirect_uris": []string{redirect, "https://app.example/cb?a=b"}}), "application/json")
	if reg.status != 201 {
		t.Skipf("Rust registration throttled: %d", reg.status)
	}
	client := reg.json()["client_id"].(string)
	_, challenge := pkce()
	// every case gets its own user: both servers throttle consent calls per user
	var u who
	ck := map[string]string{}
	fresh := func() { u = o.signup("Olive", true); ck = map[string]string{"Cookie": "cn_session=" + u.cookie} }
	fresh()
	base := func(mod func(map[string]any)) []byte {
		b := map[string]any{"client_id": client, "redirect_uri": redirect, "response_type": "code", "code_challenge": challenge, "code_challenge_method": "S256",
			"resource": "http://localhost:4000/mcp", "state": "st &=?é 1", "workspace_id": u.ws, "approve": false}
		if mod != nil {
			mod(b)
		}
		return jsonBody(b)
	}
	type mod = func(map[string]any)
	set := func(k string, v any) mod { return func(m map[string]any) { m[k] = v } }
	for name, m := range map[string]mod{
		"valid":             nil,
		"approve false":     set("approve", false),
		"https redirect":    func(m map[string]any) { m["redirect_uri"] = "https://app.example/cb?a=b" },
		"unregistered":      set("redirect_uri", "https://evil.example/cb"),
		"trailing slash":    set("redirect_uri", redirect+"/"),
		"unknown client":    set("client_id", "cnc_unknown"),
		"empty client":      set("client_id", ""),
		"response_type":     set("response_type", "token"),
		"plain":             set("code_challenge_method", "plain"),
		"short challenge":   set("code_challenge", "abc"),
		"bad challenge":     set("code_challenge", strings.Repeat("!", 43)),
		"no resource":       set("resource", ""),
		"foreign resource":  set("resource", "https://evil.example/mcp"),
		"slash resource":    set("resource", "http://localhost:4000/mcp/"),
		"no state":          func(m map[string]any) { delete(m, "state") },
		"null state":        set("state", nil),
		"scope":             set("scope", "read write"),
		"unicode state":     set("state", "ünï \"<>"),
		"wrong type":        set("client_id", 5),
		"missing workspace": func(m map[string]any) { delete(m, "workspace_id") },
		"null approve":      set("approve", nil),
		"string approve":    set("approve", "true"),
		"null client":       set("client_id", nil),
		"upper workspace":   func(m map[string]any) { m["workspace_id"] = strings.ToUpper(m["workspace_id"].(string)) },
		"bad workspace":     set("workspace_id", "nope"),
		"foreign workspace": set("workspace_id", "6f1c8f1e-0000-4000-8000-000000000000"),
	} {
		for _, path := range []string{"/api/oauth/authorize/context", "/api/oauth/authorize"} {
			fresh()
			o.diff(diffCase{name: "consent " + name + " " + path, method: "POST", path: path, hdr: ck, ct: "application/json", body: base(m)})
		}
	}
	fresh()
	o.diff(diffCase{name: "consent no session", method: "POST", path: "/api/oauth/authorize", ct: "application/json", body: base(nil)})
	fresh()
	o.diff(diffCase{name: "consent bad session", method: "POST", path: "/api/oauth/authorize/context", hdr: map[string]string{"Cookie": "cn_session=nope"}, ct: "application/json", body: base(nil)})
	fresh()
	o.diff(diffCase{name: "consent cross-site", method: "POST", path: "/api/oauth/authorize", hdr: map[string]string{"Cookie": "cn_session=" + u.cookie, "Sec-Fetch-Site": "cross-site"}, ct: "application/json", body: base(nil)})
	fresh()
	o.diff(diffCase{name: "consent evil host", method: "POST", path: "/api/oauth/authorize/context", hdr: map[string]string{"Cookie": "cn_session=" + u.cookie, "Host": "evil.example"}, ct: "application/json", body: base(nil)})
	fresh()
	o.diff(diffCase{name: "consent not json", method: "POST", path: "/api/oauth/authorize/context", hdr: ck, ct: "text/plain", body: base(nil)})
	fresh()
	o.diff(diffCase{name: "consent native bearer", method: "POST", path: "/api/oauth/authorize/context", hdr: map[string]string{"Authorization": "Bearer cs_" + u.cookie}, ct: "application/json", body: base(nil)})
}

var secretRe = regexp.MustCompile(`(cn[xor]_|cnc_)[A-Za-z0-9_-]{43}`)

// Twin flows: the same connect on Go and on Rust leaves the same rows (secrets and ids aside) and the same shapes, and a
// token from either server works on both.
func TestOAuthTokensAreValidOnBothServers(t *testing.T) {
	o := newOCDiff(t)
	host := map[string]string{"Host": "localhost:4000"}
	flow := func(base string, u who) (client string, tok map[string]any, code string) {
		reg := o.reqAt(base, "POST", "/oauth/register", host, jsonBody(map[string]any{"client_name": "Twin", "redirect_uris": []string{redirect}}), "application/json")
		if reg.status != 201 {
			t.Skipf("registration throttled on %s: %d", base, reg.status)
		}
		client = reg.json()["client_id"].(string)
		verifier, challenge := pkce()
		body := map[string]any{"client_id": client, "redirect_uri": redirect, "response_type": "code", "code_challenge": challenge, "code_challenge_method": "S256",
			"resource": "http://localhost:4000/mcp", "state": "s", "workspace_id": u.ws, "approve": true, "scope": "read"}
		h := map[string]string{"Host": "localhost:4000", "Cookie": "cn_session=" + u.cookie}
		a := o.reqAt(base, "POST", "/api/oauth/authorize", h, jsonBody(body), "application/json")
		eq(t, a.status, 200)
		code = query(t, a.json()["redirect_to"], "code")
		eq(t, query(t, a.json()["redirect_to"], "iss"), "http://localhost:4000")
		r := o.reqAt(base, "POST", "/oauth/token", host, formBody(tokenForm(client, code, verifier)...), "application/x-www-form-urlencoded")
		eq(t, r.status, 200)
		eq(t, r.hdr.Get("Cache-Control"), "no-store")
		return client, r.json(), code
	}
	rows := func(u who, client string) string {
		return adminScalar(o.env, u.ws, `select concat_ws('|', g.resource, g.scope, g.revoked_at is null,
			(select string_agg(kind || ':' || (used_at is not null) || ':' || (expires_at - created_at > interval '59 minutes'), ',' order by kind) from core.oauth_tokens t where t.grant_id = g.id),
			(select count(*) from core.oauth_codes c where c.grant_id = g.id and c.used_at is not null and c.client_id = g.client_id))
			from core.oauth_grants g where g.client_id = $1`, client)
	}
	uR, uG := o.signup("Rust", true), o.signup("Go", true)
	cR, tokR, _ := flow(o.ref(), uR)
	cG, tokG, _ := flow(o.base, uG)
	shape := func(m map[string]any) string {
		return secretRe.ReplaceAllString(fmt.Sprint(m), "SECRET")
	}
	eq(t, shape(tokG), shape(tokR))
	eq(t, rows(uG, cG), rows(uR, cR))

	mcp := func(base, access string) int {
		return o.reqAt(base, "POST", "/mcp", map[string]string{"Host": "localhost:4000", "Accept": "application/json, text/event-stream", "Authorization": "Bearer " + access},
			[]byte(rpc(1, "tools/list", nil)), "application/json").status
	}
	rest := func(base, access, ws string) int {
		return o.reqAt(base, "POST", "/api/w/"+ws+"/commands/space.list", map[string]string{"Host": "localhost:4000", "Authorization": "Bearer " + access}, []byte(`{}`), "application/json").status
	}
	for _, c := range []struct {
		tok map[string]any
		u   who
	}{{tokR, uR}, {tokG, uG}} {
		a := c.tok["access_token"].(string)
		for _, base := range []string{o.ref(), o.base} {
			eq(t, mcp(base, a), 200)
			eq(t, rest(base, a, c.u.ws), 200)
		}
	}
	// Refresh on the other server than the one that issued it; the old tokens die everywhere.
	for _, c := range []struct {
		tok    map[string]any
		client string
		base   string
	}{{tokR, cR, o.base}, {tokG, cG, o.ref()}} {
		r := o.reqAt(c.base, "POST", "/oauth/token", host, formBody("grant_type", "refresh_token", "refresh_token", c.tok["refresh_token"].(string), "client_id", c.client), "application/x-www-form-urlencoded")
		eq(t, r.status, 200)
		for _, base := range []string{o.ref(), o.base} {
			eq(t, mcp(base, c.tok["access_token"].(string)), 401)
			eq(t, mcp(base, r.json()["access_token"].(string)), 200)
		}
	}
}

// Client ID metadata documents: Go and Rust fetch the same document (served on loopback, which both allow through
// OAUTH_CIMD_ALLOW_PRIVATE) and give the same consent answer, including every refusal message.
func TestOAuthDifferentialClientMetadataDocuments(t *testing.T) {
	if os.Getenv("RUST_OAUTH_URL") == "" {
		t.Skip("needs a private Rust reference (RUST_OAUTH_URL, started with OAUTH_CIMD_ALLOW_PRIVATE=1): the shared one cannot fetch loopback documents")
	}
	o := newOCDiff(t)
	t.Setenv("OAUTH_CIMD_ALLOW_PRIVATE", "1")
	docs := map[string]func(id string) (int, any){
		"good": func(id string) (int, any) {
			return 200, map[string]any{"client_id": id, "client_name": "Doc ü", "redirect_uris": []string{redirect}}
		},
		"no name": func(id string) (int, any) {
			return 200, map[string]any{"client_id": id, "redirect_uris": []string{redirect}}
		},
		"long name": func(id string) (int, any) {
			return 200, map[string]any{"client_id": id, "client_name": " \x07" + strings.Repeat("é", 100), "redirect_uris": []string{redirect}}
		},
		"other id": func(string) (int, any) {
			return 200, map[string]any{"client_id": "https://x.example/c.json", "redirect_uris": []string{redirect}}
		},
		"secret method": func(id string) (int, any) {
			return 200, map[string]any{"client_id": id, "token_endpoint_auth_method": "client_secret_post", "redirect_uris": []string{redirect}}
		},
		"secret": func(id string) (int, any) {
			return 200, map[string]any{"client_id": id, "client_secret": "x", "redirect_uris": []string{redirect}}
		},
		"http redirect": func(id string) (int, any) {
			return 200, map[string]any{"client_id": id, "redirect_uris": []string{"http://example.com/cb"}}
		},
		"no redirects": func(id string) (int, any) { return 200, map[string]any{"client_id": id} },
		"empty":        func(id string) (int, any) { return 200, map[string]any{"client_id": id, "redirect_uris": []string{}} },
		"non string":   func(id string) (int, any) { return 200, map[string]any{"client_id": id, "redirect_uris": []any{1}} },
		"not found":    func(string) (int, any) { return 404, "nope" },
		"not json":     func(string) (int, any) { return 200, "plain" },
		"redirect":     func(string) (int, any) { return 302, "" },
		"huge": func(id string) (int, any) {
			return 200, map[string]any{"client_id": id, "client_name": strings.Repeat("x", 20000), "redirect_uris": []string{redirect}}
		},
	}
	var name string
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := "http://" + r.Host + r.URL.Path
		status, doc := docs[name](id)
		if s, ok := doc.(string); ok && status == 200 {
			_, _ = w.Write([]byte(s)) // not JSON
			return
		}
		switch status {
		case 404:
			http.NotFound(w, r)
			return
		case 302:
			http.Redirect(w, r, "/elsewhere", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	defer meta.Close()
	_, challenge := pkce()
	results := map[string]hresp{}
	for name = range docs {
		u := o.signup("Olive", true)
		for _, side := range []string{"r", "g"} { // two document URLs so each server fetches its own copy rather than the other's cache row
			id := meta.URL + "/" + side + "/" + strings.ReplaceAll(name, " ", "-") + ".json"
			body := jsonBody(map[string]any{"client_id": id, "redirect_uri": redirect, "response_type": "code", "code_challenge": challenge, "code_challenge_method": "S256",
				"resource": "http://localhost:4000/mcp", "workspace_id": u.ws, "approve": false})
			base := o.ref()
			if side == "g" {
				base = o.base
			}
			res := o.reqAt(base, "POST", "/api/oauth/authorize/context", map[string]string{"Host": "localhost:4000", "Cookie": "cn_session=" + u.cookie}, body, "application/json")
			results[name+side] = res
		}
		r, g := results[name+"r"], results[name+"g"]
		if r.status != g.status || !bytes.Equal(r.body, g.body) {
			t.Errorf("%s: differs\nrust %d %.300s\ngo   %d %.300s", name, r.status, r.body, g.status, g.body)
		}
	}
}
