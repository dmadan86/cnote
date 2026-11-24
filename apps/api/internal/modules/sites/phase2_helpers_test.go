package sites

import "testing"

func TestNormalizeEmbed(t *testing.T) {
	for _, tc := range []struct{ p, in, want string }{{"youtube", "https://youtu.be/abcdefghijk", "abcdefghijk"}, {"google_maps", "https://www.google.com/maps?q=New+Delhi", "New Delhi"}, {"calendly", "https://calendly.com/user/meeting", "user/meeting"}, {"youtube", "https://evil.test/abcdefghijk", ""}} {
		got, err := NormalizeEmbed(tc.p, tc.in)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("%s: %q %v", tc.in, got, err)
		}
	}
}
func TestNormalizeHostname(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"Example.COM.", "example.com"}, {"localhost", ""}, {"127.0.0.1", ""}, {"bad..com", ""}} {
		got, err := NormalizeHostname(tc.in)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("%s: %q %v", tc.in, got, err)
		}
	}
}
