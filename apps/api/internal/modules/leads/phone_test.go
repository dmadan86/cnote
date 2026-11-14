package leads

import "testing"

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"9876543210": "+919876543210", "+91 98765 43210": "+919876543210", "098765-43210": "+919876543210",
		"91 98765 43210": "+919876543210", "0091 98765 43210": "+919876543210", "(+91) 98765-43210": "+919876543210",
		"+44 20 7946 0958": "+442079460958", "0044 20 7946 0958": "+442079460958",
	} {
		if got, ok := NormalizePhone(in); !ok || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "12345", "5876543210", "98765 4321", "+91 12345 67890", "98765abc10", "+0 123 456 7890", "+1234567890123456"} {
		if got, ok := NormalizePhone(in); ok {
			t.Errorf("%q should be rejected, got %q", in, got)
		}
	}
}
