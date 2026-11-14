package leads

import "strings"

// NormalizePhone returns the E.164 form of a phone number as people type it in India (+91, 0-prefix, spaces,
// dashes, 91 without plus, 00 international prefix). shortcut: any other country is accepted only with an explicit
// + or 00 prefix and 8-15 digits, no per-country length check; upgrade with a phone library if non-India volume appears.
func NormalizePhone(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	plus := strings.HasPrefix(raw, "+")
	var d strings.Builder
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			d.WriteRune(r)
		case strings.ContainsRune(" -().+", r):
		default:
			return "", false
		}
	}
	s := d.String()
	switch {
	case !plus && strings.HasPrefix(s, "00"):
		s, plus = s[2:], true
	case plus:
	case len(s) == 10 && s[0] >= '6':
		s = "91" + s
	case len(s) == 11 && s[0] == '0' && s[1] >= '6':
		s = "91" + s[1:]
	case len(s) == 12 && strings.HasPrefix(s, "91") && s[2] >= '6':
	default:
		return "", false
	}
	if len(s) < 8 || len(s) > 15 || s[0] == '0' {
		return "", false
	}
	if strings.HasPrefix(s, "91") && (len(s) != 12 || s[2] < '6') {
		return "", false
	}
	return "+" + s, true
}
