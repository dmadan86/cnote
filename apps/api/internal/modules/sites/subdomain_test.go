package sites

import (
	"testing"
	"time"
)

func TestCheckSubdomain(t *testing.T) {
	for in, want := range map[string]string{
		"sharma-dental": "", " Sharma-Dental ": "", "ab": "invalid", "-ab": "invalid", "a--b": "invalid", "xn--abc": "invalid", "a_b": "invalid",
		"www": "reserved", "w-w-w": "reserved", "ns1": "reserved", "g00gle-shop": "reserved", "my-cnote": "reserved", "pay-paytm": "reserved",
		"f-u-c-k": "reserved", "madarchod-shop": "reserved", "b3hench0d": "reserved", "testing-center": "",
	} {
		if _, reason := CheckSubdomain(in); reason != want {
			t.Errorf("%q: got %q want %q", in, reason, want)
		}
	}
}

func TestMonthWindowIST(t *testing.T) {
	// 31 Dec 20:00 UTC is already 1 Jan in India
	s, n := monthWindow(time.Date(2026, 12, 31, 20, 0, 0, 0, time.UTC))
	if s.Month() != time.January || s.Year() != 2027 || n.Month() != time.February {
		t.Fatalf("%v %v", s, n)
	}
	if editLimit(false) != 100 || editLimit(true) != 500 {
		t.Fatal("limits")
	}
}
