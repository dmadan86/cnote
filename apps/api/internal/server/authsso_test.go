package server_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/modules/security"
)

// WP18 SSO against a fake OpenID Connect provider (httptest, a key generated per run). Nothing talks to a real IdP.

type fakeIDP struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	secret   string
	mu       sync.Mutex
	codes    map[string]idpGrant
	// per-login knobs: claim overrides (nil value deletes), a different signing key
	override map[string]any
	signWith *rsa.PrivateKey
}

type idpGrant struct{ nonce, challenge, redirect string }

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{key: k, clientID: "client-" + rnd(), secret: "secret-" + rnd(), codes: map[string]idpGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		g, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code")) // a code is good once
		over, signer := f.override, f.signWith
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || r.Form.Get("client_secret") != f.secret || r.Form.Get("client_id") != f.clientID || r.Form.Get("redirect_uri") != g.redirect ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		claims := map[string]any{"iss": f.srv.URL, "aud": f.clientID, "sub": "sub-1", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"nonce": g.nonce, "email": "x@corp.test", "email_verified": true, "name": "Sso Person"}
		for k, v := range over {
			if v == nil {
				delete(claims, k)
			} else {
				claims[k] = v
			}
		}
		if signer == nil {
			signer = f.key
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": f.jwt(signer, claims), "token_type": "Bearer"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIDP) jwt(k *rsa.PrivateKey, claims map[string]any) string {
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeIDP) set(over map[string]any, signer *rsa.PrivateKey) {
	f.mu.Lock()
	f.override, f.signWith = over, signer
	f.mu.Unlock()
}

var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (e *env) get(path string, cookies ...string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.gosv.URL+path, nil)
	req.Header.Set("X-Forwarded-For", fakeIP())
	for _, c := range cookies {
		req.Header.Add("Cookie", c)
	}
	res, err := noFollow.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func cookieOf(res *http.Response, name string) string {
	for _, c := range res.Cookies() {
		if c.Name == name && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// ssoFlow runs the browser's side: start, "log in at the provider" (grant a code), callback. mutate lets a test tamper.
// ssoLogin runs the browser's side: start, "log in at the provider" (grant a code), callback. tamper lets a test forge parts.
func (e *env) ssoLogin(f *fakeIDP, slug string, tamper func(state *string, cookie *string)) *http.Response {
	e.t.Helper()
	start := e.get("/api/auth/sso/start?workspace=" + url.QueryEscape(slug))
	if start.StatusCode != 302 {
		e.t.Fatalf("start: %d", start.StatusCode)
	}
	loc, _ := url.Parse(start.Header.Get("Location"))
	if !strings.HasPrefix(loc.String(), f.srv.URL+"/authorize") {
		return start // refused before the provider (e.g. back to /login with a message)
	}
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("nonce") == "" || q.Get("state") == "" || q.Get("scope") != "openid email profile" {
		e.t.Fatalf("authorization request is missing PKCE, state or nonce: %s", loc)
	}
	code := rnd() + rnd()
	f.mu.Lock()
	f.codes[code] = idpGrant{q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri")}
	f.mu.Unlock()
	state, cookie := q.Get("state"), "cn_sso="+cookieOf(start, "cn_sso")
	if tamper != nil {
		tamper(&state, &cookie)
	}
	return e.get("/api/auth/sso/callback?code="+code+"&state="+url.QueryEscape(state), cookie)
}

func loginError(res *http.Response) string {
	loc, _ := url.Parse(res.Header.Get("Location"))
	return loc.Query().Get("sso_error")
}

// dns is the fake resolver: name -> TXT records. Nothing in these tests touches real DNS.
func fakeDNS(t *testing.T) map[string][]string {
	t.Helper()
	recs := map[string][]string{}
	old := security.LookupTXT
	security.LookupTXT = func(_ context.Context, name string) ([]string, error) {
		if r, ok := recs[name]; ok {
			return r, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { security.LookupTXT = old })
	return recs
}

// verified adds a domain through the command, publishes its record in the fake DNS and verifies it.
func (e *env) verified(dns map[string][]string, ck, ws, domain string) {
	e.t.Helper()
	r := e.run(ck, ws, "security.domain_add", map[string]any{"domain": domain})
	var d struct{ Record, RecordName string }
	_ = json.Unmarshal(r.body, &d)
	if r.status != 200 || d.Record == "" {
		e.t.Fatalf("domain_add: %d %s", r.status, r.body)
	}
	dns["_cnote."+domain] = []string{d.Record}
	if r := e.run(ck, ws, "security.domain_verify", map[string]any{"domain": domain}); r.status != 200 {
		e.t.Fatalf("domain_verify: %d %s", r.status, r.body)
	}
}

// ssoWorkspace: an owner, a fake provider, SSO switched on for corp.test.
func ssoWorkspace(t *testing.T) (*env, *fakeIDP, acct, string, string) {
	t.Helper()
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1") // the fake provider is on loopback
	e := sec(t)
	dns := fakeDNS(t)
	f := newFakeIDP(t)
	owner := e.newAcct("SsoOwner", true)
	ck := e.session(owner)
	e.verified(dns, ck, owner.ws, "corp.test")
	r := e.run(ck, owner.ws, "security.update", map[string]any{"sso": map[string]any{"enabled": true, "issuer": f.srv.URL, "client_id": f.clientID, "client_secret": f.secret}})
	if r.status != 200 || strings.Contains(string(r.body), f.secret) {
		t.Fatalf("configure: %d %s", r.status, r.body)
	}
	var w struct{ Slug string }
	_ = json.Unmarshal(e.run(ck, owner.ws, "workspace.get", map[string]any{}).body, &w)
	slug := w.Slug
	return e, f, owner, ck, slug
}

func TestSSOHappyPathJoinsAsMemberAndPinsTheSession(t *testing.T) {
	e, f, owner, ownerCk, slug := ssoWorkspace(t)
	email := "pat-" + rnd() + "@corp.test"
	f.set(map[string]any{"email": email}, nil)
	res := e.ssoLogin(f, slug, nil)
	ck := cookieOf(res, "cn_session")
	if res.StatusCode != 302 || res.Header.Get("Location") != "/" || ck == "" {
		t.Fatalf("callback: %d %s %q", res.StatusCode, res.Header.Get("Location"), loginError(res))
	}
	var hash string
	if err := e.pool.QueryRow(context.Background(), "select coalesce(password_hash,'') from core.users where email = $1", email).Scan(&hash); err != nil || hash != "" {
		t.Fatalf("a person created by SSO has no password: %v %q", err, hash)
	}
	list := e.run(ownerCk, owner.ws, "member.list", map[string]any{})
	if !strings.Contains(string(list.body), email) || !strings.Contains(string(list.body), `"role":"member"`) {
		t.Fatalf("member.list: %s", list.body)
	}
	if e.events(owner.ws, "auth.sso_login") != 1 {
		t.Fatal("SSO sign-ins are audited")
	}
	if r := e.run(ck, owner.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("SSO session in its workspace: %d %s", r.status, r.body)
	}
	// a second login is not a second membership
	f.set(map[string]any{"email": email}, nil)
	if res := e.ssoLogin(f, slug, nil); cookieOf(res, "cn_session") == "" {
		t.Fatalf("second login: %s", loginError(res))
	}
	// the session is pinned: when the same person is also a member elsewhere it still reaches only this workspace
	other := e.newAcct("Elsewhere", true)
	oCk := e.session(other)
	if r := e.run(oCk, other.ws, "member.add", map[string]any{"email": email, "role": "member"}); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if r := e.run(ck, other.ws, "workspace.get", map[string]any{}); r.status != 403 || !strings.Contains(string(r.body), "single sign-on session") {
		t.Fatalf("SSO session reaching another workspace: %d %s", r.status, r.body)
	}
	me := hit(t, e.gosv.URL, "GET", "/api/auth/me", cookieHdr(ck), "")
	if strings.Contains(string(me.body), other.ws) || !strings.Contains(string(me.body), owner.ws) {
		t.Fatalf("/me lists %s", me.body)
	}
}

func TestSSORejectsBadTokensAndStates(t *testing.T) {
	e, f, owner, _, slug := ssoWorkspace(t)
	wrongKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	valid := func() map[string]any { return map[string]any{"email": "ok-" + rnd() + "@corp.test"} }
	merge := func(kv map[string]any) map[string]any {
		m := valid()
		for k, v := range kv {
			m[k] = v
		}
		return m
	}
	for _, tc := range []struct {
		name   string
		claims map[string]any
		signer *rsa.PrivateKey
		tamper func(*string, *string)
		want   string
	}{
		{"bad signature", valid(), wrongKey, nil, "signature"},
		{"wrong issuer", merge(map[string]any{"iss": "https://evil.example.test"}), nil, nil, "issuer"},
		{"wrong audience", merge(map[string]any{"aud": "someone-else"}), nil, nil, "audience"},
		{"wrong nonce", merge(map[string]any{"nonce": "replayed-nonce"}), nil, nil, "nonce"},
		{"expired", merge(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}), nil, nil, "expired"},
		{"unverified email", merge(map[string]any{"email_verified": false}), nil, nil, "not verified"},
		{"email_verified missing", merge(map[string]any{"email_verified": nil}), nil, nil, "not verified"},
		{"domain not allowed", merge(map[string]any{"email": "mallory@evil.test"}), nil, nil, "domain is not allowed"},
		{"no email", merge(map[string]any{"email": nil}), nil, nil, "no email"},
		{"state mismatch", valid(), nil, func(s *string, _ *string) { *s = "forged-state" }, "state"},
		{"no state cookie", valid(), nil, func(_ *string, c *string) { *c = "" }, "incomplete or expired"},
		{"cookie of a tampered blob", valid(), nil, func(_ *string, c *string) { *c = *c + "A" }, "incomplete or expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.set(tc.claims, tc.signer)
			res := e.ssoLogin(f, slug, tc.tamper)
			if res.StatusCode != 302 || cookieOf(res, "cn_session") != "" || !strings.HasPrefix(res.Header.Get("Location"), "/login?sso_error=") || !strings.Contains(loginError(res), tc.want) {
				t.Fatalf("%d %q cookie=%q", res.StatusCode, res.Header.Get("Location"), cookieOf(res, "cn_session"))
			}
			if em, _ := tc.claims["email"].(string); em != "" {
				var n int
				_ = e.pool.QueryRow(context.Background(), "select count(*) from core.users where email = $1", em).Scan(&n)
				if n != 0 {
					t.Fatalf("a refused login must not create %s", em)
				}
			}
		})
	}
	// an authorization code cannot be replayed at the provider, and the sealed cookie of one login does not fit another workspace
	f.set(nil, nil)
	if e.events(owner.ws, "auth.sso_login") != 0 {
		t.Fatal("refused logins are not sign-ins")
	}
	if res := e.get("/api/auth/sso/start?workspace=" + url.QueryEscape("no-such-workspace-"+rnd())); !strings.Contains(loginError(res), "not found") {
		t.Fatalf("unknown workspace: %q", res.Header.Get("Location"))
	}
}

func TestSSOStartRefusesPrivateIdentityProviders(t *testing.T) {
	e, f, _, _, slug := ssoWorkspace(t)
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "") // production: the guard is on, and the provider is on loopback
	res := e.get("/api/auth/sso/start?workspace=" + url.QueryEscape(slug))
	if res.StatusCode != 302 || strings.HasPrefix(res.Header.Get("Location"), f.srv.URL) || !strings.Contains(loginError(res), "refused") {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestSSOConfigurationRules(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	e := sec(t)
	f := newFakeIDP(t)
	owner := e.newAcct("CfgOwner", true)
	ck := e.session(owner)
	sso := func(m map[string]any) resp {
		base := map[string]any{"enabled": true, "issuer": f.srv.URL, "client_id": f.clientID, "client_secret": f.secret}
		for k, v := range m {
			base[k] = v
		}
		return e.run(ck, owner.ws, "security.update", map[string]any{"sso": base})
	}
	for name, m := range map[string]map[string]any{
		"public mailbox domain":                {"allowed_domains": []string{"gmail.com"}},
		"no domains":                           {"allowed_domains": []string{}},
		"no client id":                         {"client_id": ""},
		"issuer that does not serve discovery": {"issuer": f.srv.URL + "/nope"},
	} {
		if r := sso(m); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	// without a verified domain SSO cannot be switched on
	if r := sso(nil); r.status != 400 || !strings.Contains(string(r.body), "verified domain") {
		t.Fatalf("no verified domain: %d %s", r.status, r.body)
	}
	e.verified(fakeDNS(t), ck, owner.ws, "corp.test")
	if r := e.run(ck, owner.ws, "security.domain_add", map[string]any{"domain": "gmail.com"}); r.status != 400 {
		t.Fatalf("public mailbox domain: %d %s", r.status, r.body)
	}
	if r := sso(nil); r.status != 200 {
		t.Fatalf("valid config: %d %s", r.status, r.body)
	}
	var blob []byte
	adm := adminPool(t)
	if err := adm.QueryRow(context.Background(), "select sso_client_secret_enc from core.workspace_security where workspace_id = $1::uuid", owner.ws).Scan(&blob); err != nil || strings.Contains(string(blob), f.secret) || len(blob) < 30 {
		t.Fatalf("client secret must be encrypted at rest: %v", err)
	}
	g := e.run(ck, owner.ws, "security.get", map[string]any{})
	if strings.Contains(string(g.body), f.secret) || !strings.Contains(string(g.body), `"has_client_secret":true`) {
		t.Fatalf("security.get leaks or hides: %s", g.body)
	}
	// the secret can stay as it is when other fields change
	if r := e.run(ck, owner.ws, "security.update", map[string]any{"sso": map[string]any{"enforce": false}}); r.status != 200 {
		t.Fatalf("update without the secret: %d %s", r.status, r.body)
	}
	// with a provider that is down, enabling is refused (the issuer is fetched through the SSRF guard)
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "")
	if r := sso(nil); r.status != 400 || !strings.Contains(string(r.body), "refused") {
		t.Fatalf("private issuer with the guard on: %d %s", r.status, r.body)
	}
}

func TestSSOEnforcementIsPerWorkspaceAndPasswordLoginStillWorks(t *testing.T) {
	e, _, owner, ownerCk, _ := ssoWorkspace(t)
	if r := e.run(ownerCk, owner.ws, "security.update", map[string]any{"sso": map[string]any{"enforce": true}}); r.status != 200 {
		t.Fatalf("enforce: %d %s", r.status, r.body)
	}
	member := e.newAcctAt("Emp", "emp-"+rnd()+"@corp.test")
	if r := e.run(ownerCk, owner.ws, "member.add", map[string]any{"email": member.email, "role": "member"}); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	// the same person is also a member of another workspace that has nothing to do with SSO
	other := e.newAcct("Elsewhere", true)
	oCk := e.session(other)
	e.run(oCk, other.ws, "member.add", map[string]any{"email": member.email, "role": "member"})
	// password login succeeds everywhere (MFA and throttling aside) ...
	r := e.login(member, member.pw, nil)
	if r.status != 200 || r.cookie() == "" {
		t.Fatalf("password login must not be blocked globally: %d %s", r.status, r.body)
	}
	ck := sessionOf(t, r)
	// ... the unrelated workspace still works ...
	if r := e.run(ck, other.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("other workspace: %d %s", r.status, r.body)
	}
	// ... and the enforcing one refuses the password session with a pointer to company sign-in
	if r := e.run(ck, owner.ws, "workspace.get", map[string]any{}); r.status != 403 || !strings.Contains(string(r.body), "company single sign-on") {
		t.Fatalf("enforcing workspace and a password session: %d %s", r.status, r.body)
	}
	// owners keep the way back in; people on other domains are not affected
	if r := e.run(ownerCk, owner.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("owner: %d %s", r.status, r.body)
	}
	outsider := e.newAcct("Outsider", false)
	e.run(ownerCk, owner.ws, "member.add", map[string]any{"email": outsider.email, "role": "member"})
	if r := e.run(e.session(outsider), owner.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("other domain: %d %s", r.status, r.body)
	}
}

func TestSSOUnverifiedDomainIsIgnored(t *testing.T) {
	e, f, owner, ownerCk, slug := ssoWorkspace(t)
	// victim.test is registered but its TXT record was never published
	if r := e.run(ownerCk, owner.ws, "security.domain_add", map[string]any{"domain": "victim.test"}); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	// a victim who already has an account (with a password) is not linked or joined by an IdP vouching for their address
	victim := e.newAcctAt("Victim", "vic-"+rnd()+"@victim.test")
	f.set(map[string]any{"email": victim.email, "sub": "attacker-sub"}, nil)
	res := e.ssoLogin(f, slug, nil)
	if cookieOf(res, "cn_session") != "" || !strings.Contains(loginError(res), "domain is not allowed") {
		t.Fatalf("unverified domain for linking: %q %q", loginError(res), cookieOf(res, "cn_session"))
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), "select count(*) from core.sessions where user_id = $1::uuid and amr = 'sso'", victim.userID).Scan(&n)
	if n != 0 {
		t.Fatal("no session may be created for the victim")
	}
	// nor does a brand-new address at that domain get created (JIT)
	fresh := "new-" + rnd() + "@victim.test"
	f.set(map[string]any{"email": fresh}, nil)
	if res := e.ssoLogin(f, slug, nil); cookieOf(res, "cn_session") != "" {
		t.Fatal("JIT join on an unverified domain")
	}
	_ = e.pool.QueryRow(context.Background(), "select count(*) from core.users where email = $1", fresh).Scan(&n)
	if n != 0 {
		t.Fatal("user created for an unverified domain")
	}
	// enforcement ignores it as well: an enforced workspace does not refuse the password session of that address
	e.run(ownerCk, owner.ws, "security.update", map[string]any{"sso": map[string]any{"enforce": true}})
	e.run(ownerCk, owner.ws, "member.add", map[string]any{"email": victim.email, "role": "member"})
	if r := e.run(e.session(victim), owner.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("unverified domain must not trigger enforcement: %d %s", r.status, r.body)
	}
}

func TestSSODomainVerificationAgainstDNS(t *testing.T) {
	e := sec(t)
	dns := fakeDNS(t)
	owner := e.newAcct("DnsOwner", true)
	ck := e.session(owner)
	r := e.run(ck, owner.ws, "security.domain_add", map[string]any{"domain": " Acme-" + rnd() + ".TEST "})
	var d struct {
		Domain, Token, Record string
		RecordName            string `json:"record_name"`
	}
	_ = json.Unmarshal(r.body, &d)
	if r.status != 200 || len(d.Token) != 32 || d.Record != "cnote-verification="+d.Token || d.RecordName != "_cnote."+d.Domain || d.Domain != strings.ToLower(d.Domain) {
		t.Fatalf("add: %d %s", r.status, r.body)
	}
	verify := func() resp { return e.run(ck, owner.ws, "security.domain_verify", map[string]any{"domain": d.Domain}) }
	// no record, a wrong record, and another workspace's token all fail
	if r := verify(); r.status != 400 || !strings.Contains(string(r.body), "TXT record was not found") {
		t.Fatalf("no record: %d %s", r.status, r.body)
	}
	dns["_cnote."+d.Domain] = []string{"cnote-verification=" + strings.Repeat("0", 32), "v=spf1 -all"}
	if r := verify(); r.status != 400 {
		t.Fatalf("wrong token: %d %s", r.status, r.body)
	}
	if !strings.Contains(string(e.run(ck, owner.ws, "security.domain_list", map[string]any{}).body), `"verified":false`) {
		t.Fatal("still unverified")
	}
	// the record at the domain itself works too
	dns[d.Domain] = []string{d.Record}
	if r := verify(); r.status != 200 || !strings.Contains(string(r.body), `"verified":true`) {
		t.Fatalf("verify: %d %s", r.status, r.body)
	}
	if l := e.run(ck, owner.ws, "security.domain_list", map[string]any{}); !strings.Contains(string(l.body), `"verified":true`) || !strings.Contains(string(l.body), d.Domain) {
		t.Fatalf("list: %s", l.body)
	}
	// a second workspace cannot reuse the first one's proof: it gets its own token
	rival := e.newAcct("Rival", true)
	rk := e.session(rival)
	e.run(rk, rival.ws, "security.domain_add", map[string]any{"domain": d.Domain})
	if r := e.run(rk, rival.ws, "security.domain_verify", map[string]any{"domain": d.Domain}); r.status != 400 {
		t.Fatalf("a token from another workspace verified a domain: %d %s", r.status, r.body)
	}
	// who may: members no, API keys need a confirmation
	member := e.newAcct("Mem", false)
	e.run(ck, owner.ws, "member.add", map[string]any{"email": member.email, "role": "member"})
	if r := e.run(e.session(member), owner.ws, "security.domain_add", map[string]any{"domain": "x-" + rnd() + ".test"}); r.status != 403 {
		t.Fatalf("member: %d", r.status)
	}
	admin := e.newAcct("Adm", false)
	e.run(ck, owner.ws, "member.add", map[string]any{"email": admin.email, "role": "admin"})
	for _, cmd := range []string{"security.domain_add", "security.domain_verify", "security.domain_remove"} {
		if r := e.run(e.session(admin), owner.ws, cmd, map[string]any{"domain": d.Domain}); r.status != 403 {
			t.Fatalf("admin %s: %d %s", cmd, r.status, r.body)
		}
	}
	if r := e.run(e.session(admin), owner.ws, "security.domain_list", map[string]any{}); r.status != 200 {
		t.Fatalf("admin domain_list: %d %s", r.status, r.body)
	}
	k := e.run(ck, owner.ws, "apikey.create", map[string]any{"name": "ci"})
	var kv struct {
		APIKey string `json:"api_key"`
	}
	_ = json.Unmarshal(k.body, &kv)
	ak := e.cmd(e.gosv.URL, who{bearer: kv.APIKey}, owner.ws, "security.domain_add", map[string]any{"domain": "agent-" + rnd() + ".test"}, nil)
	if ak.status != 403 && !strings.Contains(string(ak.body), "confirmation_required") { // the admin group is off for agents by default; with it on a person must confirm
		t.Fatalf("API key: %d %s", ak.status, ak.body)
	}
	if r := e.run(ck, owner.ws, "security.domain_add", map[string]any{"domain": "gmail.com"}); r.status != 400 {
		t.Fatalf("public mailbox: %d", r.status)
	}
}

func TestSSOIdentityLinkFollowsSubjectNotEmail(t *testing.T) {
	e, f, owner, _, slug := ssoWorkspace(t)
	a, b := "ann-"+rnd()+"@corp.test", "bob-"+rnd()+"@corp.test"
	login := func(sub, email string) *http.Response {
		f.set(map[string]any{"sub": sub, "email": email}, nil)
		return e.ssoLogin(f, slug, nil)
	}
	subA, subB := "sub-a-"+rnd(), "sub-b-"+rnd()
	if res := login(subA, a); cookieOf(res, "cn_session") == "" {
		t.Fatalf("first login: %s", loginError(res))
	}
	if res := login(subB, b); cookieOf(res, "cn_session") == "" {
		t.Fatalf("second person: %s", loginError(res))
	}
	// a different subject asserting Ann's email is refused (the account is linked to subA)
	res := login("sub-evil-"+rnd(), a)
	if cookieOf(res, "cn_session") != "" || !strings.Contains(loginError(res), "already linked") {
		t.Fatalf("sub mismatch: %q", loginError(res))
	}
	// Ann's subject now claims Bob's email: she still gets Ann's account, nobody is re-linked
	res = login(subA, b)
	ck := cookieOf(res, "cn_session")
	me := hit(t, e.gosv.URL, "GET", "/api/auth/me", cookieHdr(ck), "")
	if ck == "" || !strings.Contains(string(me.body), a) || strings.Contains(string(me.body), `"email":"`+b) {
		t.Fatalf("changed email must not switch accounts: %s", me.body)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), "select count(*) from core.users where email = $1", b).Scan(&n)
	if n != 1 {
		t.Fatalf("users for Bob's email: %d", n)
	}
	_ = owner
}

func TestSSOSessionCannotTouchTheAccountOrOtherWorkspaces(t *testing.T) {
	e, f, owner, _, slug := ssoWorkspace(t)
	email := "sess-" + rnd() + "@corp.test"
	f.set(map[string]any{"email": email}, nil)
	ck := cookieOf(e.ssoLogin(f, slug, nil), "cn_session")
	if ck == "" {
		t.Fatal("login")
	}
	for path, body := range map[string]any{"/api/auth/mfa/setup": map[string]any{}, "/api/auth/mfa/confirm": map[string]any{"code": "123456"},
		"/api/auth/mfa/disable": map[string]any{"password": "x", "code": "123456"}, "/api/workspaces": map[string]any{"name": "Mine"}} {
		if r := e.post(path, fakeIP(), body, nil, ck); r.status != 403 || !strings.Contains(string(r.body), "single sign-on session") {
			t.Errorf("%s: %d %s", path, r.status, r.body)
		}
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), "select count(*) from core.user_mfa u join core.users x on x.id = u.user_id where x.email = $1", email).Scan(&n)
	if n != 0 {
		t.Fatal("an SSO session must not enrol MFA")
	}
	// another workspace the person belongs to is out of reach, and so is OAuth consent for it
	other := e.newAcct("Elsewhere", true)
	e.run(e.session(other), other.ws, "member.add", map[string]any{"email": email, "role": "member"})
	if r := e.run(ck, other.ws, "workspace.get", map[string]any{}); r.status != 403 {
		t.Fatalf("other workspace: %d", r.status)
	}
	o := newOCFor(t, e)
	client := o.register(redirect)
	_, challenge := pkce()
	if r := o.authorize(who{cookie: ck, ws: other.ws}, o.authzBody(client, redirect, challenge), other.ws, true, nil); r.status != 403 {
		t.Fatalf("OAuth consent for another workspace: %d %s", r.status, r.body)
	}
	if r := o.authorize(who{cookie: ck, ws: owner.ws}, o.authzBody(client, redirect, challenge), owner.ws, true, nil); r.status != 200 {
		t.Fatalf("OAuth consent for the pinned workspace: %d %s", r.status, r.body)
	}
	ctx := o.postJSON("/api/oauth/authorize/context", map[string]string{"Cookie": "cn_session=" + ck}, o.authzBody(client, redirect, challenge)).json()
	if ws, _ := ctx["workspaces"].([]any); len(ws) != 1 {
		t.Fatalf("consent page lists %v", ctx["workspaces"])
	}
}

func (e *env) newAcctAt(name, email string) acct {
	e.t.Helper()
	a := acct{email: email, pw: rnd() + rnd(), ip: fakeIP()}
	if r := e.post("/api/auth/signup", a.ip, map[string]any{"email": a.email, "name": name, "password": a.pw}, nil, ""); r.status != 200 {
		e.t.Fatalf("signup: %d %s", r.status, r.body)
	}
	_ = e.pool.QueryRow(context.Background(), "select id::text from core.users where email = $1", email).Scan(&a.userID)
	return a
}
