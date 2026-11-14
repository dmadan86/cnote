package worker

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// wsClass is Rust regex's \s (Unicode White_Space); Go's \s is ASCII only and lacks \v.
const wsClass = `\t\n\x0B\f\r \x{85}\x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}`

// isWordRune is Rust regex's \w (alphabetic, marks, decimal digits, connector punctuation, joiners).
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) ||
		unicode.Is(unicode.Nl, r) || r == 0x200C || r == 0x200D
}

// boundaryBefore and boundaryAfter reproduce \b at byte offset i of s for a match that starts (or ends) with a
// word character.
func boundaryBefore(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !isWordRune(r)
}

func boundaryAfter(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return !isWordRune(r)
}

func isCased(r rune) bool { return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) }

func isCaseIgnorable(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) || strings.ContainsRune("'.:·’‘", r)
}

// rustLower is str::to_lowercase: U+0130 expands to "i̇" and a final capital sigma becomes the final form.
func rustLower(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case r == 0x130:
			b.WriteString("i̇")
		case r == 0x3A3:
			before := false
			for j := i - 1; j >= 0; j-- {
				if isCaseIgnorable(rs[j]) {
					continue
				}
				before = isCased(rs[j])
				break
			}
			after := false
			for j := i + 1; j < len(rs); j++ {
				if isCaseIgnorable(rs[j]) {
					continue
				}
				after = isCased(rs[j])
				break
			}
			if before && !after {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
