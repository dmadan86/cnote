package server

import (
	"net/http/httptest"
	"testing"
)

// X-Forwarded-For is honoured only behind a trusted proxy (TRUST_PROXY=1|true), and only when it holds an IP;
// otherwise a caller could pick a fresh rate-limit bucket per request.
func TestOauthClientIPTrustsForwardedForOnlyWhenConfigured(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/marketplace/token", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	for env, want := range map[string]string{"": "192.0.2.1", "0": "192.0.2.1", "yes": "192.0.2.1", "1": "203.0.113.9", "true": "203.0.113.9"} {
		t.Setenv("TRUST_PROXY", env)
		if got := oauthClientIP(r); got != want {
			t.Errorf("TRUST_PROXY=%q: %s, want %s", env, got, want)
		}
	}
	t.Setenv("TRUST_PROXY", "1")
	for _, bad := range []string{"", "not-an-ip", "203.0.113.9:80"} {
		r.Header.Set("X-Forwarded-For", bad)
		if got := oauthClientIP(r); got != "192.0.2.1" {
			t.Errorf("XFF %q: %s", bad, got)
		}
	}
	r.Header.Set("X-Forwarded-For", "2001:db8::1")
	if got := oauthClientIP(r); got != "2001:db8::1" {
		t.Errorf("ipv6: %s", got)
	}
}

func TestExtractKeysMatchesRustRegex(t *testing.T) {
	for in, want := range map[string]string{
		"Fix ENG-12 and eng-12, also OPS-3": "ENG-12 OPS-3",
		"feature/eng-7-login-page":          "ENG-7",
		"no keys here v1.2":                 "",
		"éENG-1 ENG-1é":                     "", // Unicode word boundaries
		"ENG-1,OPS-2;X-3":                   "ENG-1 OPS-2 X-3",
		"abcdefghij-1 abcdefghijk-2":        "ABCDEFGHIJ-1",
	} {
		if got := join(extractKeys(in)); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}
