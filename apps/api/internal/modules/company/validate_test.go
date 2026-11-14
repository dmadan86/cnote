package company

import "testing"

func TestFormats(t *testing.T) {
	for _, c := range []struct {
		name string
		ok   bool
		s    string
	}{
		{"pan", true, "ABCDE1234F"}, {"pan", false, "ABCDE1234"}, {"pan", false, "abcde1234f"},
		{"ifsc", true, "HDFC0001234"}, {"ifsc", false, "HDFC1001234"}, {"ifsc", false, "HDFC000123"},
		{"pin", true, "560001"}, {"pin", false, "060001"}, {"pin", false, "56001"},
		{"phone", true, "+919876543210"}, {"phone", true, "9876543210"}, {"phone", false, "98765"}, {"phone", false, "98765abc10"},
		{"upi", true, "acme.shop@okbank"}, {"upi", false, "acme shop@okbank"}, {"upi", false, "nohandle"},
		{"acct", true, "123456789012"}, {"acct", false, "12345"},
		{"email", true, "a@b.co"}, {"email", false, "a@b"},
	} {
		re := map[string]interface{ MatchString(string) bool }{"pan": panRe, "ifsc": ifscRe, "pin": pinRe, "phone": phoneRe, "upi": upiRe, "acct": acctRe, "email": emailRe}[c.name]
		if re.MatchString(c.s) != c.ok {
			t.Errorf("%s %q: want %v", c.name, c.s, c.ok)
		}
	}
	if cleanPhone(" +91 (98765)-43210 ") != "+919876543210" {
		t.Error("cleanPhone")
	}
}
