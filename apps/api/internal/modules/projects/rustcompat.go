package projects

import (
	"strings"
	"time"
	"unicode"
)

// Helpers that reproduce Rust std / chrono / uuid behaviour the CNQL port depends on.

func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func isCaseIgnorable(r rune) bool {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return false
}

// rustLower is `str::to_lowercase`: full lowercase mapping (U+0130 expands) and the final-sigma rule.
func rustLower(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch r {
		case 0x130:
			b.WriteString("i̇")
		case 'Σ':
			final := false
			for j := i - 1; j >= 0; j-- {
				if isCaseIgnorable(rs[j]) {
					continue
				}
				final = isCased(rs[j])
				break
			}
			if final {
				for j := i + 1; j < len(rs); j++ {
					if isCaseIgnorable(rs[j]) {
						continue
					}
					final = !isCased(rs[j])
					break
				}
			}
			if final {
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

func isRustAlnum(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsNumber(c) || unicode.Is(unicode.Other_Alphabetic, c)
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func eqFold(a, b string) bool { return asciiLower(a) == asciiLower(b) }

// parseUUID is uuid::Uuid::parse_str: simple, hyphenated, {braced} and urn:uuid: forms; returns the canonical form.
func parseUUID(s string) (string, bool) {
	var h string
	switch len(s) {
	case 32:
		h = s
	case 36:
		h = strings.ReplaceAll(s, "-", "")
		if !hyphenOK(s) {
			return "", false
		}
	case 38:
		if s[0] != '{' || s[37] != '}' || !hyphenOK(s[1:37]) {
			return "", false
		}
		h = strings.ReplaceAll(s[1:37], "-", "")
	case 45:
		if !strings.EqualFold(s[:9], "urn:uuid:") || !hyphenOK(s[9:]) {
			return "", false
		}
		h = strings.ReplaceAll(s[9:], "-", "")
	default:
		return "", false
	}
	if len(h) != 32 {
		return "", false
	}
	for i := 0; i < 32; i++ {
		c := h[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return "", false
		}
	}
	h = strings.ToLower(h)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], true
}

func hyphenOK(s string) bool {
	return len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}

// ---- chrono ----

const (
	chronoMinYear = -262143
	chronoMaxYear = 262142
)

var (
	chronoMinUnix = time.Date(chronoMinYear, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	chronoMaxUnix = time.Date(chronoMaxYear, 12, 31, 23, 59, 59, 0, time.UTC).Unix()
)

func daysIn(y, m int) int {
	switch m {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	}
	return 31
}

func validDate(y, m, d int) bool {
	return y >= chronoMinYear && y <= chronoMaxYear && m >= 1 && m <= 12 && d >= 1 && d <= daysIn(y, m)
}

// addSecs is DateTime::checked_add_signed for whole seconds.
func addSecs(t time.Time, n int64) (time.Time, bool) {
	if n > 1<<40 || n < -(1<<40) {
		return t, false
	}
	u := t.Unix() + n
	if u < chronoMinUnix || u > chronoMaxUnix {
		return t, false
	}
	return time.Unix(u, int64(t.Nanosecond())).UTC(), true
}

// addMonths is checked_add_months / checked_sub_months: the day clamps to the end of the target month.
func addMonths(t time.Time, n int64) (time.Time, bool) {
	if n > 1<<32 || n < -(1<<32) {
		return t, false
	}
	total := int64(t.Year())*12 + int64(t.Month()-1) + n
	y, m := total/12, total%12
	if m < 0 {
		m += 12
		y--
	}
	if y < chronoMinYear || y > chronoMaxYear {
		return t, false
	}
	d := t.Day()
	if dm := daysIn(int(y), int(m)+1); d > dm {
		d = dm
	}
	return time.Date(int(y), time.Month(m+1), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC), true
}

type numItem struct {
	kind  byte // 'Y' 'm' 'd' 'H' 'M' 'S', ' ' space, 'l' literal
	width int
	lit   string
}

func scanNumber(s string, min, max int) (string, int64, bool) {
	if len(s) < min {
		return "", 0, false
	}
	var n int64
	for i := 0; i < len(s) && i < max; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			if i < min {
				return "", 0, false
			}
			return s[i:], n, true
		}
		if n > (1<<63-1-int64(c-'0'))/10 {
			return "", 0, false
		}
		n = n*10 + int64(c-'0')
	}
	end := max
	if len(s) < end {
		end = len(s)
	}
	return s[end:], n, true
}

func trimStart(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }

type chronoParsed struct{ y, mo, d, h, mi, s int }

// chronoParse is chrono's strftime parser for the fixed formats CNQL uses.
func chronoParse(s string, items []numItem) (chronoParsed, bool) {
	var p chronoParsed
	for _, it := range items {
		switch it.kind {
		case 'l':
			if !strings.HasPrefix(s, it.lit) {
				return p, false
			}
			s = s[len(it.lit):]
		case ' ':
			s = trimStart(s)
		default:
			s = trimStart(s)
			var v int64
			var ok bool
			if it.kind == 'Y' {
				switch {
				case strings.HasPrefix(s, "-"):
					if s, v, ok = scanNumber(s[1:], 1, 1<<62); !ok {
						return p, false
					}
					v = -v
				case strings.HasPrefix(s, "+"):
					if s, v, ok = scanNumber(s[1:], 1, 1<<62); !ok {
						return p, false
					}
				default:
					if s, v, ok = scanNumber(s, 1, 4); !ok {
						return p, false
					}
				}
				if v < -(1<<31) || v > 1<<31-1 {
					return p, false
				}
				p.y = int(v)
				continue
			}
			if s, v, ok = scanNumber(s, 1, 2); !ok {
				return p, false
			}
			switch it.kind {
			case 'm':
				if v < 1 || v > 12 {
					return p, false
				}
				p.mo = int(v)
			case 'd':
				if v < 1 || v > 31 {
					return p, false
				}
				p.d = int(v)
			case 'H':
				if v > 23 {
					return p, false
				}
				p.h = int(v)
			case 'M':
				if v > 59 {
					return p, false
				}
				p.mi = int(v)
			case 'S':
				if v > 60 {
					return p, false
				}
				p.s = int(v)
			}
		}
	}
	return p, s == "" && validDate(p.y, p.mo, p.d)
}

func fmtItems(sep string, rest ...numItem) []numItem {
	l := func(s string) numItem { return numItem{kind: 'l', lit: s} }
	out := []numItem{{kind: 'Y'}, l(sep), {kind: 'm'}, l(sep), {kind: 'd'}}
	return append(out, rest...)
}

func dateFormats() [][]numItem {
	return [][]numItem{fmtItems("-"), fmtItems("/")}
}

func dateTimeFormats() [][]numItem {
	l := func(s string) numItem { return numItem{kind: 'l', lit: s} }
	hm := []numItem{{kind: 'H'}, l(":"), {kind: 'M'}}
	hms := append(append([]numItem{}, hm...), l(":"), numItem{kind: 'S'})
	sp := numItem{kind: ' '}
	return [][]numItem{
		fmtItems("-", append([]numItem{sp}, hm...)...),
		fmtItems("/", append([]numItem{sp}, hm...)...),
		fmtItems("-", append([]numItem{l("T")}, hms...)...),
		fmtItems("-", append([]numItem{sp}, hms...)...),
	}
}

// chronoDate is NaiveDate::parse_from_str for the two date formats; ok is false when none matches.
func chronoDate(s string) (time.Time, bool) {
	for _, f := range dateFormats() {
		if p, ok := chronoParse(s, f); ok {
			return time.Date(p.y, time.Month(p.mo), p.d, 0, 0, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}

func chronoDateTime(s string) (time.Time, bool, bool) {
	for _, f := range dateTimeFormats() {
		if p, ok := chronoParse(s, f); ok {
			sec, leap := p.s, 0
			if sec == 60 {
				sec, leap = 59, 1
			}
			t := time.Date(p.y, time.Month(p.mo), p.d, p.h, p.mi, sec, 0, time.UTC)
			if leap == 1 {
				t, ok := addSecs(t, 1)
				return t, ok, true
			}
			return t, true, false
		}
	}
	return time.Time{}, false, false
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// chronoRFC3339 is DateTime::parse_from_rfc3339 (chrono 0.4.45), converted to UTC.
func chronoRFC3339(s string) (time.Time, bool, bool) {
	if len(s) < 19 {
		return time.Time{}, false, false
	}
	dg := func(i int) (int, bool) {
		if !isDigit(s[i]) {
			return 0, false
		}
		return int(s[i] - '0'), true
	}
	two := func(i int) (int, bool) {
		a, ok1 := dg(i)
		b, ok2 := dg(i + 1)
		return a*10 + b, ok1 && ok2
	}
	var y int
	for i := 0; i < 4; i++ {
		d, ok := dg(i)
		if !ok {
			return time.Time{}, false, false
		}
		y = y*10 + d
	}
	if s[4] != '-' {
		return time.Time{}, false, false
	}
	mo, ok := two(5)
	if !ok || s[7] != '-' {
		return time.Time{}, false, false
	}
	d, ok := two(8)
	if !ok || !validDate(y, mo, d) {
		return time.Time{}, false, false
	}
	if s[10] != 't' && s[10] != 'T' && s[10] != ' ' {
		return time.Time{}, false, false
	}
	h, ok := two(11)
	if !ok || s[13] != ':' {
		return time.Time{}, false, false
	}
	mi, ok := two(14)
	if !ok || s[16] != ':' {
		return time.Time{}, false, false
	}
	sec, ok := two(17)
	if !ok {
		return time.Time{}, false, false
	}
	extra := int64(0)
	if sec == 60 {
		sec, extra = 59, 1_000_000_000
	}
	rest := s[19:]
	nano := extra
	if len(s) > 19 && s[19] == '.' {
		r, v, ok := scanNumber(s[20:], 1, 9)
		if !ok {
			return time.Time{}, false, false
		}
		consumed := len(s[20:]) - len(r)
		for i := consumed; i < 9; i++ {
			v *= 10
		}
		rest = strings.TrimLeft(r, "0123456789")
		nano += v
	}
	if h > 23 || mi > 59 || sec > 59 || nano >= 2_000_000_000 || (nano >= 1_000_000_000 && sec != 59) {
		return time.Time{}, false, false
	}
	// offset: Z | +hh:mm | -hh:mm | U+2212 hh:mm; the colon is required, 00-23 hours, 00-59 minutes
	var off int
	switch {
	case rest != "" && (rest[0] == 'Z' || rest[0] == 'z'):
		rest = rest[1:]
	default:
		neg := false
		switch {
		case strings.HasPrefix(rest, "+"):
			rest = rest[1:]
		case strings.HasPrefix(rest, "-"):
			rest, neg = rest[1:], true
		case strings.HasPrefix(rest, "−"):
			rest, neg = rest[len("−"):], true
		default:
			return time.Time{}, false, false
		}
		if len(rest) < 2 || !isDigit(rest[0]) || !isDigit(rest[1]) {
			return time.Time{}, false, false
		}
		hh := int(rest[0]-'0')*10 + int(rest[1]-'0')
		rest = rest[2:]
		if !strings.HasPrefix(rest, ":") {
			return time.Time{}, false, false
		}
		rest = rest[1:]
		if len(rest) < 2 || !isDigit(rest[0]) || !isDigit(rest[1]) {
			return time.Time{}, false, false
		}
		mm := int(rest[0]-'0')*10 + int(rest[1]-'0')
		if mm > 59 {
			return time.Time{}, false, false
		}
		rest = rest[2:]
		off = hh*3600 + mm*60
		if off >= 86400 {
			return time.Time{}, false, false
		}
		if neg {
			off = -off
		}
	}
	if rest != "" {
		return time.Time{}, false, false
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, 0, time.UTC)
	t, ok = addSecs(t, int64(-off)+nano/1_000_000_000)
	if !ok {
		return time.Time{}, false, false
	}
	return time.Unix(t.Unix(), nano%1_000_000_000).UTC(), true, nano >= 1_000_000_000
}

// chronoMicros truncates like sqlx's chrono encoding: microseconds since 2000-01-01, truncated toward zero.
func chronoMicros(t time.Time) time.Time {
	ns := int64(t.Nanosecond())
	pg := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	secs := t.Unix() - pg
	if secs >= 0 || ns == 0 {
		return time.Unix(t.Unix(), ns/1000*1000).UTC()
	}
	// negative total: floor division produced (secs, ns); truncation toward zero keeps ceil(total/1000)
	return time.Unix(t.Unix(), (ns+999)/1000*1000).UTC()
}
