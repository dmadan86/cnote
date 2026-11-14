package company

import (
	"regexp"
	"strings"
)

// Format checks for the company profile. They catch typos; they do not prove the number exists.
var (
	panRe   = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)
	ifscRe  = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
	pinRe   = regexp.MustCompile(`^[1-9][0-9]{5}$`)
	phoneRe = regexp.MustCompile(`^\+?[0-9]{7,15}$`)
	upiRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{2,64}@[A-Za-z][A-Za-z0-9]{1,31}$`)
	acctRe  = regexp.MustCompile(`^[0-9]{9,18}$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

func upperTrim(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// cleanPhone drops spaces, dashes and brackets; the result is digits with an optional leading +.
func cleanPhone(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(s))
}
