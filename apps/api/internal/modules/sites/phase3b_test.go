package sites

import "testing"

func TestFingerprint(t *testing.T) {
	for _, tt := range []struct{ in, want string }{{"", "e3b0c44298fc1c14"}, {"abc", "ba7816bf8f01cfea"}} {
		if got := Fingerprint(tt.in); got != tt.want {
			t.Errorf("Fingerprint(%q) = %q", tt.in, got)
		}
	}
}
func TestSupportedLang(t *testing.T) {
	t.Setenv("SITES_EXTRA_LANGS", "pa, or")
	for _, tt := range []struct {
		code string
		want bool
	}{{"en", true}, {"hi", true}, {"pa", true}, {"or", true}, {"fr", false}, {"EN", false}} {
		if got := SupportedLang(tt.code); got != tt.want {
			t.Errorf("SupportedLang(%q) = %v", tt.code, got)
		}
	}
}
func TestCanonicalURL(t *testing.T) {
	for _, tt := range []struct {
		raw, want string
		bad       bool
	}{{"https://EXAMPLE.com/path?q=1#x", "https://example.com/path", false}, {"https://example.com", "https://example.com/", false}, {"javascript:alert(1)", "", true}} {
		got, err := CanonicalURL(tt.raw)
		if (err != nil) != tt.bad || got != tt.want {
			t.Errorf("CanonicalURL(%q) = %q, %v", tt.raw, got, err)
		}
	}
}
