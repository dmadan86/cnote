package sites

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	nets := TrustedProxies()
	t.Setenv("PUBLIC_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.1, junk")
	nets = TrustedProxies()
	if len(nets) != 2 {
		t.Fatalf("parsed %v", nets)
	}
	for _, c := range []struct{ peer, xff, want string }{
		{"203.0.113.9:1234", "1.1.1.1", "203.0.113.9"},               // peer not trusted: the header is a forgery
		{"10.1.2.3:80", "", "10.1.2.3"},                              // trusted peer, no header
		{"10.1.2.3:80", "198.51.100.7", "198.51.100.7"},              // the client the proxy saw
		{"10.1.2.3:80", "6.6.6.6, 198.51.100.7", "198.51.100.7"},     // a forged left entry is ignored
		{"10.1.2.3:80", "198.51.100.7, 10.9.9.9", "198.51.100.7"},    // a second trusted hop is skipped
		{"10.1.2.3:80", "198.51.100.7, 192.168.1.1", "198.51.100.7"}, // single-address entry in the list
		{"10.1.2.3:80", "10.5.5.5", "10.1.2.3"},                      // every hop trusted
		{"10.1.2.3:80", "not-an-ip", "10.1.2.3"},                     // garbage on the trusted path
		{"[::ffff:10.1.2.3]:80", "2001:db8::1", "2001:db8::1"},
	} {
		r := &http.Request{RemoteAddr: c.peer, Header: http.Header{}}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r, nets); got != c.want {
			t.Errorf("%s xff=%q: got %s want %s", c.peer, c.xff, got, c.want)
		}
	}
}

func TestSubdomainOfAndURL(t *testing.T) {
	t.Setenv("SITES_DOMAIN", "Sites.Example.test")
	for host, want := range map[string]string{"acme.sites.example.test": "acme", "ACME.sites.example.test.": "acme", "a.b.sites.example.test": "", "sites.example.test": "",
		"acme.sites.example.test:3000": "", "xn--a.sites.example.test": "", "ab.sites.example.test": ""} {
		if got := SubdomainOf(host); got != want {
			t.Errorf("%q: got %q want %q", host, got, want)
		}
	}
	if SiteURL("acme") != "https://acme.sites.example.test" {
		t.Error(SiteURL("acme"))
	}
	t.Setenv("SITES_DOMAIN", "localhost:3000")
	if SiteURL("acme") != "http://acme.localhost:3000" {
		t.Error(SiteURL("acme"))
	}
}

func TestCheckEnv(t *testing.T) {
	for _, c := range []struct {
		domain, base, skip string
		bad                bool
	}{
		{"cnote.site", "https://app.cnote.in", "", false}, {"cnote.in", "https://app.cnote.in", "", true}, {"app.cnote.in", "https://cnote.in", "", true},
		{"cnote.in", "https://cnote.in", "", true}, {"localhost:3000", "http://localhost:3000", "", false}, {"cnote.site", "https://app.cnote.in", "1", true},
		{"cnote.site", "http://localhost:3000", "1", false},
	} {
		t.Setenv("SITES_DOMAIN", c.domain)
		t.Setenv("PUBLIC_BASE_URL", c.base)
		t.Setenv("SITES_TEST_SKIP_VERIFY", c.skip)
		if err := CheckEnv(); (err != nil) != c.bad {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestSubmissionFingerprint(t *testing.T) {
	a := Submission{Email: " User@Example.com ", Phone: "123", Message: "hello"}
	b := Submission{Email: "user@example.com", Phone: "456", Message: "hello"}
	if submissionFingerprint(a) != submissionFingerprint(b) {
		t.Fatal("phone changed email fingerprint")
	}
	a.Email, b.Email = "", ""
	if submissionFingerprint(a) == submissionFingerprint(b) {
		t.Fatal("phone fallback ignored")
	}
	b.Phone = "1-2-3"
	if submissionFingerprint(a) != submissionFingerprint(b) {
		t.Fatal("phone not normalized")
	}
}
