package dataimport

import (
	"strings"
	"testing"
)

func TestParseMoney(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
		bad  bool
	}{
		{"1,18,000.00", 11800000, false}, {"1180", 118000, false}, {"1180 Dr", 118000, false}, {"1180Dr.", 118000, false},
		{"250 Cr", -25000, false}, {"-500", -50000, false}, {"(500.50)", -50050, false}, {"500-", -50000, false},
		{"₹ 1,000", 100000, false}, {"Rs. 99.5", 9950, false}, {"INR 10", 1000, false}, {"0.10", 10, false}, {".5", 50, false},
		{"12.340", 1234, false}, {"12.345", 0, true}, {"abc", 0, true}, {"", 0, true}, {"-5 Dr", 0, true}, {"1.2.3", 0, true},
		{"9999999999999999", 0, true},
	} {
		got, err := ParseMoney(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("ParseMoney(%q) = %d, %v; want %d bad=%v", c.in, got, err, c.want, c.bad)
		}
	}
}

func TestParseDate(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"01-04-2025", "2025-04-01"}, {"1-4-2025", "2025-04-01"}, {"15/03/2025", "2025-03-15"}, {"2025-04-01", "2025-04-01"},
		{"2025/4/1", "2025-04-01"}, {"1-Apr-2025", "2025-04-01"}, {"01-apr-25", "2025-04-01"}, {"1 April 2025", "2025-04-01"},
		{"2025-04-01 00:00:00", "2025-04-01"}, {"01/04/2025 10:30", "2025-04-01"}, {"29-02-2024", "2024-02-29"},
		{"29-02-2025", ""}, {"32-01-2025", ""}, {"01-13-2025", ""}, {"", ""}, {"yesterday", ""}, {"01-04-1850", ""},
	} {
		got, err := ParseDate(c.in)
		if got != c.want || (err != nil) != (c.want == "") {
			t.Errorf("ParseDate(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestParseQtyAndRate(t *testing.T) {
	for in, want := range map[string]int64{"100": 100000, "1,000.5": 1000500, "0.001": 1, "2.500": 2500} {
		if got, err := ParseQty(in); err != nil || got != want {
			t.Errorf("ParseQty(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"1.2345", "x", "-1", ""} {
		if _, err := ParseQty(in); err == nil {
			t.Errorf("ParseQty(%q) should fail", in)
		}
	}
	for in, want := range map[string]int{"0": 0, "0.1": 10, "0.25": 25, "1.5": 150, "3": 300, "5%": 500, "12": 1200, "18.00": 1800, "28 %": 2800, "40": 4000, "Exempt": 0, "NIL": 0} {
		if got, err := ParseGSTRate(in); err != nil || got != want {
			t.Errorf("ParseGSTRate(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"7", "2", "18.5", "abc", "", "-5"} {
		if _, err := ParseGSTRate(in); err == nil {
			t.Errorf("ParseGSTRate(%q) should fail", in)
		}
	}
}

func TestGSTINPANHSN(t *testing.T) {
	for _, g := range []string{"27AAPFU0939F1ZV", "29abcde1234f1zw", " 07AABCS1234D1ZX ", "27ABCDE1234F1Z0"} {
		if _, err := CheckGSTIN(g); err != nil {
			t.Errorf("CheckGSTIN(%q): %v", g, err)
		}
	}
	for g, frag := range map[string]string{"27AAPFU0939F1ZX": "check character", "27AAPFU0939F1": "15 characters", "XXAAPFU0939F1ZV": "format", "99AAPFU0939F1ZV": "check", "50AAPFU0939F1ZV": "unknown state"} {
		_, err := CheckGSTIN(g)
		if err == nil || !strings.Contains(err.Error(), frag) {
			t.Errorf("CheckGSTIN(%q) = %v; want %q", g, err, frag)
		}
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "verif") {
			t.Errorf("message must not claim verification: %v", err)
		}
	}
	if p, err := CheckPAN("aapfu0939f"); err != nil || p != "AAPFU0939F" {
		t.Error(p, err)
	}
	for _, p := range []string{"AAPFU0939", "1APFU0939F", "AAPFU09390"} {
		if _, err := CheckPAN(p); err == nil {
			t.Errorf("CheckPAN(%q) should fail", p)
		}
	}
	for h, ok := range map[string]bool{"7318": true, "731800": true, "73180000": true, "73": false, "73180": false, "73A8": false, "": false} {
		if (ValidateHSN(h) == nil) != ok {
			t.Errorf("ValidateHSN(%q) want ok=%v", h, ok)
		}
	}
}

func TestStateCode(t *testing.T) {
	for in, want := range map[string]string{"Maharashtra": "27", "maharashtra ": "27", "Orissa": "21", "Odisha": "21", "27": "27", "27-Maharashtra": "27", "MH": "27",
		"Jammu & Kashmir": "01", "Andhra Pradesh": "37", "Tamil Nadu": "33", "New Delhi": "07", "Uttaranchal": "05", "West Bengal": "19"} {
		if got, ok := StateCode(in); !ok || got != want {
			t.Errorf("StateCode(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"Narnia", "", "50", "123"} {
		if _, ok := StateCode(in); ok {
			t.Errorf("StateCode(%q) should fail", in)
		}
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "+91": "'+91", "-5": "'-5", "@sum": "'@sum", "\tx": "'\tx", "\rx": "'\rx", "ok": "ok", "": "", "a=b": "a=b"} {
		if got := CSVSafe(in); got != want {
			t.Errorf("CSVSafe(%q) = %q", in, got)
		}
	}
	if got := SafeCSV([][]string{{"a", "=HYPERLINK(\"x\")"}}); got != "a,\"'=HYPERLINK(\"\"x\"\")\"\n" {
		t.Errorf("SafeCSV = %q", got)
	}
}
