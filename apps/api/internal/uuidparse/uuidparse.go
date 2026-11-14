// Package uuidparse is Rust's uuid crate parser: the spellings it accepts (simple, hyphenated, braced, urn) and the
// exact error texts it produces ("invalid group length in group 4: expected 12, found 11").
package uuidparse

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func hexv(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func hyphenated(s string) bool { // exactly 36 bytes
	for i := 0; i < 36; i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
		} else if hexv(s[i]) < 0 {
			return false
		}
	}
	return true
}

func simple(s string) bool {
	for i := 0; i < len(s); i++ {
		if hexv(s[i]) < 0 {
			return false
		}
	}
	return true
}

// Parse returns the canonical lowercase hyphenated form, or the message after "UUID parsing failed: ".
func Parse(in string) (string, string) {
	var body string
	switch {
	case len(in) == 32 && simple(in):
		body = in[:8] + "-" + in[8:12] + "-" + in[12:16] + "-" + in[16:20] + "-" + in[20:]
	case len(in) == 36 && hyphenated(in):
		body = in
	case len(in) == 45 && strings.HasPrefix(in, "urn:uuid:") && hyphenated(in[9:]):
		body = in[9:]
	case len(in) == 38 && in[0] == '{' && in[37] == '}' && hyphenated(in[1:37]):
		body = in[1:37]
	default:
		return "", errText(in)
	}
	return strings.ToLower(body), ""
}

func errText(in string) string {
	if !utf8.ValidString(in) {
		return "invalid UTF-8"
	}
	s, offset, isSimple := in, 0, true
	switch {
	case strings.HasPrefix(in, "urn:uuid:"):
		s, offset, isSimple = in[9:], 9, false
	case len(in) >= 2 && in[0] == '{' && in[len(in)-1] == '}':
		s, offset, isSimple = in[1:len(in)-1], 1, false
	}
	hy := 0
	var bounds [4]int
	for i, r := range s {
		switch {
		case r > 0xFF || r > 0x7F && r != rune(byte(r)):
			return fmt.Sprintf("invalid character: found `%c` at %d", r, i+offset)
		case r == '-':
			if hy < 4 {
				bounds[hy] = i
			}
			hy++
		case hexv(byte(r)) < 0:
			return fmt.Sprintf("invalid character: found `%c` at %d", rune(byte(r)), i+offset)
		}
	}
	if hy == 0 && isSimple {
		return fmt.Sprintf("invalid length: found %d", len(in))
	}
	if hy != 4 {
		return fmt.Sprintf("invalid group count: expected 5, found %d", hy+1)
	}
	starts := [5]int{0, 9, 14, 19, 24}
	want := [5]int{8, 4, 4, 4, 12}
	for i := 0; i < 4; i++ {
		if bounds[i] != starts[i+1]-1 {
			return fmt.Sprintf("invalid group length in group %d: expected %d, found %d", i, want[i], bounds[i]-starts[i])
		}
	}
	return fmt.Sprintf("invalid group length in group 4: expected 12, found %d", len(in)-24)
}
