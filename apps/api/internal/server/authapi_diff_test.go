package server_test

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	idNorm  = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	tokNorm = regexp.MustCompile(`(cn_session=|cs_)[A-Za-z0-9_-]{43}`)
)

// shape is everything a client can see of a response, with the per-run ids and tokens masked.
func shape(r hres) string {
	n := func(s string) string {
		return tokNorm.ReplaceAllString(idNorm.ReplaceAllString(s, "<id>"), "${1}<tok>")
	}
	return strings.Join([]string{
		string(rune('0' + r.status/100)), string(rune('0' + r.status/10%10)), string(rune('0' + r.status%10)),
		"ct=" + r.header.Get("Content-Type"), "cookie=" + n(r.cookie()), "allow=" + r.header.Get("Allow"),
		"cache=" + r.header.Get("Cache-Control"), "body=" + n(string(r.body)),
	}, "|")
}

func TestDifferentialAuthSurface(t *testing.T) {
	e := newEnv(t)
	email, pw := "diff-"+rnd()+"@example.test", rnd()+rnd()
	acct := hit(t, e.rust, "POST", "/api/auth/signup", jsonHdr, jb(map[string]any{"email": email, "name": "Dee", "password": pw, "workspace_name": "Dee WS"}))
	tok := sessionOf(t, acct)

	type call struct {
		method, path string
		hdr          map[string]string
		body         string
	}
	cases := map[string]call{}
	add := func(name, method, path string, hdr map[string]string, body string) {
		cases[name] = call{method, path, hdr, body}
	}
	for path, bodies := range map[string][]string{
		"/api/auth/signup": {``, `{`, `[]`, `null`, `{}`, `"x"`, `{"email":"a@b.test"}`, `{"email":"a@b.test","name":"x"}`,
			`{"email":1,"name":"x","password":"12345678"}`, `{"email":null,"name":"x","password":"12345678"}`, `{"Email":"a@b.test","name":"x","password":"12345678"}`,
			`{"email":"a@b.test","email":"c@d.test","name":"x","password":"12345678"}`, `{"email":"nope","name":"x","password":"12345678"}`,
			`{"email":"a@b.test","name":"x","password":"1234567"}`, `{"email":"a@b.test","name":"x","password":"ééé"}`, `{"email":"a@b.test","name":"x","password":"12345678","workspace_name":5}`,
			`{"email":"` + email + `","name":"x","password":"12345678"}`, `{"email":" ` + strings.ToUpper(email) + `","name":"x","password":"12345678"}`,
			`{"email":"a\u0000@b.test","name":"x","password":"12345678"}`, `{"email":"a@b.test","name":"x","password":"12345678"} x`,
			`{"email":"a@b.test","name":"\ud800","password":"12345678"}`, "{\"email\":\"a@b.test\",\"name\":\"\xff\",\"password\":\"12345678\"}"},
		"/api/auth/login": {``, `{`, `[]`, `{}`, `{"email":"a@b.test"}`, `{"email":"a@b.test","password":5}`, `{"email":"` + email + `","password":"wrong-password"}`,
			`{"email":"nobody-` + rnd() + `@b.test","password":"12345678"}`, `{"email":"` + email + `","password":"` + pw + `"}`, `{"email":"  ` + strings.ToUpper(email) + `  ","password":"` + pw + `"}`,
			`{"email":"` + email + `","password":"` + pw + `","extra":[1,{"a":null}]}`, `{"email":"` + email + `","password":"` + pw + `","password":"x"}`},
		"/api/workspaces": {``, `{}`, `{"name":5}`, `{"name":"   "}`, `{"name":""}`, `{"name":"x"}`, `[1]`, `{"name":"x","name":"y"}`},
		"/api/marketplace/token": {``, `{`, `{}`, `{"client_id":1,"client_secret":"x","code":"y"}`, `{"client_id":"nope","client_secret":"x","code":"y"}`,
			`{"client_id":"00000000-0000-4000-8000-000000000001","client_secret":"x","code":"y"}`, `{"client_id":"00000000-0000-4000-8000-00000000000A","client_secret":"x","code":"y"}`,
			`{"client_id":"00000000000040008000000000000001","client_secret":"x","code":"y"}`, `{"client_id":"00000000-0000-4000-8000-000000000001","client_secret":"x","code":"y","extra":1}`,
			`{"client_id":"` + strings.Repeat("0", 5000) + `","client_secret":"x","code":"y"}`},
	} {
		for i, b := range bodies {
			for _, ct := range []string{"application/json", "", "text/plain", "application/vnd.api+json", "application/json; charset=utf-8"} {
				h := map[string]string{}
				if ct != "" {
					h["Content-Type"] = ct
				}
				if i%3 == 0 && ct == "application/json" { // browsers and native apps alike
					h["X-Cnote-Client"] = "native"
				}
				if path == "/api/workspaces" {
					h["Cookie"] = "cn_session=" + tok
					if i%2 == 1 {
						delete(h, "Cookie")
					}
				}
				add("POST "+path+" #"+string(rune('A'+i))+" ct="+ct, "POST", path, h, b)
			}
		}
	}
	for _, p := range []string{"/api/auth/signup", "/api/auth/login", "/api/auth/logout", "/api/auth/me", "/api/workspaces", "/api/commands", "/api/marketplace/token", "/healthz", "/api/hooks/github/x", "/api/auth/me/", "/api/auth/%6Cogin", "//api/auth/me"} {
		for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
			add(m+" "+p, m, p, nil, "")
		}
	}
	for name, h := range map[string]map[string]string{
		"no auth": nil, "valid cookie": cookieHdr(tok), "garbage cookie": cookieHdr("nope"), "empty cookie": {"Cookie": "cn_session="}, "other cookie name": {"Cookie": "session=" + tok},
		"cookie among others": {"Cookie": "a=b; cn_session=" + tok + "; c=d"}, "native bearer": {"Authorization": "Bearer cs_" + tok}, "bad native bearer": {"Authorization": "Bearer cs_nope"},
		"api key bearer": {"Authorization": "Bearer cn_x_y"}, "bearer and cookie": {"Authorization": "Bearer cn_x_y", "Cookie": "cn_session=" + tok}, "basic": {"Authorization": "Basic Zm9vOmJhcg=="},
		"cs_ bearer no space": {"Authorization": "Bearercs_" + tok},
	} {
		add("GET me, "+name, "GET", "/api/auth/me", h, "")
		add("GET commands, "+name, "GET", "/api/commands", h, "")
		add("POST workspaces, "+name, "POST", "/api/workspaces", mergeHdr(h, jsonHdr), `{"name":"  "}`)
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)

	// Cases that change state (signup, workspace creation, token exchange) use fresh emails per server, so the
	// per-run values differ only where masked. Rust's token bucket is shared, so mind the marketplace call count.
	uniq := func(c call, who string) call {
		c.body = strings.NewReplacer(`"a@b.test"`, `"`+who+rnd()+`@b.test"`, `"c@d.test"`, `"`+who+rnd()+`@d.test"`, `a\u0000@b.test`, who+`\u0000@b.test`).Replace(c.body)
		return c
	}
	for _, n := range names {
		c := cases[n]
		if strings.Contains(c.path, "marketplace") && c.method == "POST" && !strings.Contains(n, "application/json") && !strings.HasSuffix(n, "ct=") {
			continue // keep Rust's shared per-IP bucket (burst 20) from running dry; content types are covered by the other routes
		}
		rc, gc := uniq(c, "r"), uniq(c, "g")
		rr := hit(t, e.rust, c.method, c.path, c.hdr, rc.body)
		gr := hit(t, e.gosv.URL, c.method, c.path, gc.hdr, gc.body)
		if a, b := shape(rr), shape(gr); a != b && c.path != "/api/commands" {
			t.Errorf("%s\n  rust %.300s\n  go   %.300s", n, a, b)
		}
	}
}

func mergeHdr(a, b map[string]string) map[string]string {
	m := map[string]string{}
	for _, h := range []map[string]string{a, b} {
		for k, v := range h {
			m[k] = v
		}
	}
	return m
}

// The catalog is Rust's, entry for entry and byte for byte; Go-only commands come on top, sorted by name.
func TestDifferentialCommandCatalog(t *testing.T) {
	e := newEnv(t)
	rr, gr := hit(t, e.rust, "GET", "/api/commands", nil, ""), hit(t, e.gosv.URL, "GET", "/api/commands", nil, "")
	if rr.status != 200 || gr.status != 200 || gr.header.Get("Content-Type") != rr.header.Get("Content-Type") {
		t.Fatalf("status %d/%d ct %q/%q", rr.status, gr.status, rr.header.Get("Content-Type"), gr.header.Get("Content-Type"))
	}
	byName := func(b []byte) (map[string]string, []string) {
		var rows []json.RawMessage
		if err := json.Unmarshal(b, &rows); err != nil {
			t.Fatal(err)
		}
		m, order := map[string]string{}, []string{}
		for _, row := range rows {
			var n struct{ Name string }
			_ = json.Unmarshal([]byte(strings.Replace(string(row), `"name"`, `"Name"`, 1)), &n)
			m[n.Name] = string(row)
			order = append(order, n.Name)
		}
		return m, order
	}
	rust, _ := byName(rr.body)
	got, order := byName(gr.body)
	for n, row := range rust {
		if got[n] != row {
			t.Errorf("%s differs (snapshot out of date? regenerate internal/mcp/catalog.json from Rust GET /api/commands)\n rust %s\n go   %s", n, row, got[n])
		}
	}
	if !sort.StringsAreSorted(order) {
		t.Error("catalog is not sorted by name")
	}
	if len(got) < len(rust) {
		t.Errorf("go lists %d commands, rust %d", len(got), len(rust))
	}
	if string(rr.body) == string(gr.body) {
		return
	}
	var extra []string
	for n := range got {
		if _, ok := rust[n]; !ok {
			extra = append(extra, n)
		}
	}
	if len(extra) == 0 {
		t.Errorf("same entries but different bytes:\n%.200s\n%.200s", rr.body, gr.body)
	}
}

// Every password stored by Rust for an email with special casing is found by Go's lowercase, and the other way round.
func TestAuthEmailLowercasingMatchesRust(t *testing.T) {
	e := newEnv(t)
	pw := rnd() + rnd()
	for _, local := range []string{"ΑΣ", "Σ", "ΑΣΑ", "İSTANBUL", "ΌΣΟΣ-", "ǅ", "ẞ", "K"} {
		email := local + "-" + rnd() + "@Example.TEST"
		hit(t, e.rust, "POST", "/api/auth/signup", jsonHdr, jb(map[string]any{"email": email, "name": "u", "password": pw}))
		if r := hit(t, e.gosv.URL, "POST", "/api/auth/login", jsonHdr, jb(map[string]any{"email": email, "password": pw})); r.status != 200 {
			t.Errorf("%q: go cannot log in to the account rust created: %d %s", local, r.status, r.body)
		}
		// and Go signing up the same address is a conflict, i.e. it lowercases to the same string
		if r := hit(t, e.gosv.URL, "POST", "/api/auth/signup", jsonHdr, jb(map[string]any{"email": email, "name": "u", "password": pw})); r.status != 409 {
			t.Errorf("%q: go signup of an existing address: %d %s", local, r.status, r.body)
		}
	}
}

// Bodies over axum's 2 MiB limit are refused with its 413 text before authentication. Rust hangs up mid-upload,
// so only the Go side is called here (its answer is the text asserted in TestDifferentialAuthSurface cases for
// small bodies and in proxy_test for the rule itself).
func TestAuthOversizedBodies(t *testing.T) {
	e := newEnv(t)
	big := `{"email":"` + strings.Repeat("a", 3<<20) + `"}`
	for _, p := range []string{"/api/auth/signup", "/api/auth/login", "/api/workspaces"} {
		if r := hit(t, e.gosv.URL, "POST", p, jsonHdr, big); r.status != 413 || string(r.body) != "Failed to buffer the request body: length limit exceeded" {
			t.Errorf("%s: %d %.80s", p, r.status, r.body)
		}
	}
	if r := hit(t, e.gosv.URL, "POST", "/api/marketplace/token", jsonHdr, strings.Repeat(" ", 5000)+"{}"); r.status != 413 {
		t.Errorf("token: %d %.80s", r.status, r.body)
	}
}
