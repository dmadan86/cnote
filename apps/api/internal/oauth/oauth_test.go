package oauth

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

// Ports of the unit tests in crates/cnote-server/src/oauth.rs and netguard.rs.

func TestRedirectURIRules(t *testing.T) {
	for _, ok := range []string{
		"https://claude.ai/api/mcp/auth_callback", "http://localhost:6274/oauth/callback", "http://127.0.0.1:8080/cb", "http://[::1]:9/cb",
		"cursor://anysphere.cursor-retrieval/oauth/callback", "https://example.com/cb?x=1&y=2", "https://example.com",
	} {
		if msg, sure := validateRedirectURI(ok); msg != "" || !sure {
			t.Errorf("%s: %q sure=%v", ok, msg, sure)
		}
	}
	for bad, want := range map[string]string{
		"http://example.com/cb":                "http redirect_uri is only allowed for loopback",
		"http://localhost.evil.com/cb":         "http redirect_uri is only allowed for loopback",
		"http://127.0.0.1.evil.com/cb":         "http redirect_uri is only allowed for loopback",
		"https://example.com/cb#frag":          "redirect_uri must not contain a fragment or credentials",
		"https://a@example.com/cb":             "redirect_uri must not contain a fragment or credentials",
		"javascript:alert(1)":                  "redirect_uri scheme not allowed",
		"data:text/html,x":                     "redirect_uri scheme not allowed",
		"ftp://example.com/x":                  "redirect_uri scheme not allowed",
		"wss://example.com/x":                  "redirect_uri scheme not allowed",
		"/relative":                            "redirect_uri is not a valid absolute URI",
		"":                                     "redirect_uri is not a valid absolute URI",
		"https://" + strings.Repeat("a", 2100): "redirect_uri too long",
	} {
		if msg, sure := validateRedirectURI(bad); msg != want || !sure {
			t.Errorf("%.40s: got %q sure=%v want %q", bad, msg, sure, want)
		}
	}
	// URIs Go cannot judge like the url crate are left to Rust (never decided here).
	for _, odd := range []string{"HTTPS://EXAMPLE.com/cb", "https://exa mple.com/", "https://example.com/a/../b", "http://127.1/cb", "https://exämple.com/", "https://example.com:443/cb", "http://[::2]/cb"} {
		if _, sure := validateRedirectURI(odd); sure {
			t.Errorf("%q: should be left to Rust", odd)
		}
	}
}

func TestResourceMustBeThisServer(t *testing.T) {
	base := "https://notes.example.com"
	if r, ok := canonicalResource("https://notes.example.com/mcp/", base); !ok || r != "https://notes.example.com/mcp" {
		t.Fatal(r, ok)
	}
	for _, bad := range []string{"https://notes.example.com", "https://evil.example.com/mcp", "https://notes.example.com.evil.io/mcp", "http://notes.example.com/mcp", ""} {
		if _, ok := canonicalResource(bad, base); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestMetadataDocumentRules(t *testing.T) {
	id := "https://app.example.com/client.json"
	good := map[string]any{"client_id": id, "client_name": "App", "redirect_uris": []any{"https://app.example.com/cb"}}
	if c, msg := parseClientDocument(id, good); msg != "" || c.name != "App" {
		t.Fatal(c, msg)
	}
	for name, doc := range map[string]map[string]any{
		"other client_id": {"client_id": "https://x.example.com/c.json", "redirect_uris": []any{"https://app.example.com/cb"}},
		"secret method":   {"client_id": id, "token_endpoint_auth_method": "client_secret_basic", "redirect_uris": []any{"https://app.example.com/cb"}},
		"secret":          {"client_id": id, "client_secret": nil, "redirect_uris": []any{"https://app.example.com/cb"}},
		"http redirect":   {"client_id": id, "redirect_uris": []any{"http://app.example.com/cb"}},
		"no redirects":    {"client_id": id},
		"empty redirects": {"client_id": id, "redirect_uris": []any{}},
		"non-string":      {"client_id": id, "redirect_uris": []any{5}},
		"eleven":          {"client_id": id, "redirect_uris": make11()},
	} {
		if _, msg := parseClientDocument(id, doc); msg == "" {
			t.Errorf("%s accepted", name)
		}
	}
	if c, _ := parseClientDocument(id, map[string]any{"client_id": id, "redirect_uris": []any{"https://app.example.com/cb"}}); c.name != "app.example.com" {
		t.Errorf("fallback name %q", c.name)
	}
}

func make11() []any {
	l := make([]any, 11)
	for i := range l {
		l[i] = "https://app.example.com/cb"
	}
	return l
}

func TestConsentShowsRedirectHost(t *testing.T) {
	for in, want := range map[string]string{"https://claude.ai/cb": "claude.ai", "http://localhost:6274/cb": "localhost:6274", "cursor://anysphere.x/cb": "cursor://anysphere.x"} {
		u, ok := plainURI(in)
		if !ok || u.hostLabel() != want {
			t.Errorf("%s: %q", in, u.hostLabel())
		}
	}
}

func TestFormEscapeAndQuery(t *testing.T) {
	if got := formEscape("a b&c=d~e*f-._é"); got != "a+b%26c%3Dd%7Ee*f-._%C3%A9" {
		t.Fatal(got)
	}
	u, _ := plainURI("https://a.example/cb?x=1")
	if got := withQuery("https://a.example/cb?x=1", u, [][2]string{{"code", "c"}, {"state", "s t"}}); got != "https://a.example/cb?x=1&code=c&state=s+t" {
		t.Fatal(got)
	}
	u, _ = plainURI("https://a.example/cb")
	if got := withQuery("https://a.example/cb", u, [][2]string{{"code", "c"}}); got != "https://a.example/cb?code=c" {
		t.Fatal(got)
	}
}

func TestClassifiesAddresses(t *testing.T) {
	for _, ok := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !isPublic(netip.MustParseAddr(ok)) {
			t.Errorf("%s should be public", ok)
		}
	}
	for _, bad := range []string{"127.0.0.1", "169.254.169.254", "100.64.0.1", "::ffff:10.0.0.1", "fd00::1", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
		"192.0.0.8", "64:ff9b::808:808", "64:ff9b:1::1", "2002:808:808::1", "2001::1", "::8.8.8.8", "fec0::1", "ff02::1", "10.1.2.3", "172.16.0.1", "192.168.1.1",
		"0.0.0.0", "198.18.0.1", "198.51.100.1", "203.0.113.1", "192.0.2.1", "::1", "::", "fe80::1"} {
		if isPublic(netip.MustParseAddr(bad)) {
			t.Errorf("%s should not be public", bad)
		}
	}
}

func TestFetchRejectsInternalAndInsecureURLs(t *testing.T) {
	t.Setenv("OAUTH_CIMD_ALLOW_PRIVATE", "")
	for _, bad := range []string{"http://example.com/c.json", "https://127.0.0.1/c.json", "https://169.254.169.254/latest", "https://[::1]/c.json", "https://localhost/c.json", "https://u@example.com/c.json"} {
		if _, err := fetchJSON(context.Background(), bad, 1024); err == nil {
			t.Errorf("%s fetched", bad)
		}
	}
}
