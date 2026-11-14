// Package importers is the Go port of crates/cnote-importers: the `import.run` background job and the
// parsers it needs (Jira, Trello, Notion/Confluence pages, CSV). The commands import.start/status/list stay in Rust.
package importers

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ---------- records (model.rs) ----------

type Rec struct {
	SourceID    string
	SourceURL   *string
	Title       string
	BodyMD      string
	CreatedAt   *time.Time
	Author      *string
	Item        *ItemData // non-nil = an item in projects.items; nil = a page
	Parent      *string
	Links       []Link
	History     []Hist
	Comments    []Comment
	Attachments []Attach
	Warnings    []string
}

type ItemData struct {
	ItemType       string
	Status         string
	StatusCategory string
	Priority       string
	AssigneeEmail  *string
	ReporterEmail  *string
	Labels         []string
	Component      *string
	FixVersion     *Version
	Sprint         *Sprint
	Estimate       *float64
	DueDate        *string // YYYY-MM-DD
	ResolvedAt     *time.Time
	Custom         map[string]any
}

type Version struct {
	Name     string
	Released bool
	Date     *string
}

type Sprint struct {
	Name       string
	State      string
	Start, End *string
}

type Link struct {
	Kind    string // blocks | relates
	Other   string
	Outward bool
}

type Hist struct {
	ID       string
	At       *time.Time
	Author   *string
	Field    string
	From, To *string
}

type Comment struct {
	ID          string
	Author      *string
	AuthorEmail *string
	At          *time.Time
	Body        string
}

type Attach struct {
	Filename, ContentType string
	Size                  int64
	URL                   string
}

type Filter struct{ ID, Name, JQL string }

type Automation struct {
	Name                         string
	Trigger, Conditions, Actions any
}

type Parsed struct {
	Recs        []Rec
	Warnings    []string
	Filters     []Filter
	Automations []Automation
}

// Report is the fidelity report (ADR-012). Field order = Rust struct order.
type Report struct {
	Imported      uint32            `json:"imported"`
	Updated       uint32            `json:"updated"`
	Skipped       uint32            `json:"skipped"`
	Comments      uint32            `json:"comments"`
	HistoryEvents uint32            `json:"history_events"`
	Attachments   uint32            `json:"attachments"`
	Relations     uint32            `json:"relations"`
	Views         uint32            `json:"views"`
	Automations   uint32            `json:"automations"`
	Warnings      map[string]uint32 `json:"warnings"`
}

func (r *Report) warn(msg string) {
	if r.Warnings == nil {
		r.Warnings = map[string]uint32{}
	}
	r.Warnings[msg]++
}

// ---------- JSON values like serde_json::Value (decoded with UseNumber) ----------

func decodeJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) { // serde rejects trailing characters
		return nil, errors.New("trailing characters")
	}
	return v, nil
}

// get is Value's Index<&str>: anything but an object member is Null.
func get(v any, k string) any {
	m, _ := v.(map[string]any)
	return m[k]
}

func has(v any, k string) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m[k]
	return ok
}

// ptr is Value::pointer for object keys: (value, exists).
func ptr(v any, path string) (any, bool) {
	for _, seg := range strings.Split(path, "/")[1:] {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return v, true
}

func asStr(v any) (string, bool) { s, ok := v.(string); return s, ok }

func asArr(v any) []any { a, _ := v.([]any); return a }

func asBool(v any) (bool, bool) { b, ok := v.(bool); return b, ok }

func asI64(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	return i, err == nil
}

func asU64(v any) (uint64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseUint(string(n), 10, 64)
	return i, err == nil
}

func asF64(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	return f, err == nil
}

func sp(s string) *string { return &s }

// strAt is str_at: a string at a pointer that is not empty.
func strAt(v any, path string) *string {
	x, ok := ptr(v, path)
	if !ok {
		return nil
	}
	if s, ok := x.(string); ok && s != "" {
		return &s
	}
	return nil
}

// sKey is `s(v, k)` of others.rs: a non-empty string member.
func sKey(v any, k string) *string {
	if s, ok := get(v, k).(string); ok && s != "" {
		return &s
	}
	return nil
}

// strPtr is pointer(...).as_str() without the empty filter.
func strPtr(v any, path string) *string {
	x, _ := ptr(v, path)
	if s, ok := x.(string); ok {
		return &s
	}
	return nil
}

func orStr(p *string, d string) string {
	if p == nil {
		return d
	}
	return *p
}

// ---------- text semantics of Rust's str ----------

// rustLower is str::to_lowercase: full mapping (U+0130 -> i + U+0307) and the final-sigma rule.
func rustLower(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case r == 0x130:
			b.WriteString("i̇")
		case r == 'Σ':
			if isFinalSigma(rs, i) {
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

func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func isCaseIgnorable(r rune) bool {
	if strings.ContainsRune("'.:·‘’․‧︓﹒﹕＇．：­·՟״", r) {
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

func isFinalSigma(rs []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if isCaseIgnorable(rs[j]) {
			continue
		}
		before = isCased(rs[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(rs); j++ {
		if isCaseIgnorable(rs[j]) {
			continue
		}
		return !isCased(rs[j])
	}
	return true
}

// isAlphanumeric is char::is_alphanumeric (Alphabetic or Numeric).
func isAlphanumeric(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// isWordRune is the regex crate's Unicode \w.
func isWordRune(r rune) bool {
	return isAlphanumeric(r) || unicode.IsMark(r) || unicode.Is(unicode.Pc, r) || r == 0x200C || r == 0x200D
}

// rustLines is str::lines.
func rustLines(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, strings.TrimSuffix(s[:i], "\r"))
		s = s[i+1:]
	}
	return out
}

func slug(s string) string {
	var out strings.Builder
	last := byte(0)
	for _, c := range rustLower(s) {
		if isAlphanumeric(c) {
			out.WriteRune(c)
			last = 'a'
		} else if last != '-' {
			out.WriteByte('-')
			last = '-'
		}
	}
	return strings.Trim(out.String(), "-")
}

func categoryFromName(name string) string {
	n := rustLower(name)
	for _, w := range []string{"done", "complete", "closed", "resolved", "shipped", "released"} {
		if strings.Contains(n, w) {
			return "done"
		}
	}
	for _, w := range []string{"progress", "doing", "review", "testing", "active", "started"} {
		if strings.Contains(n, w) {
			return "in_progress"
		}
	}
	return "todo"
}

func mapPriority(name string) (string, bool) {
	switch rustLower(strings.TrimSpace(name)) {
	case "highest", "blocker", "critical", "urgent", "p0":
		return "urgent", true
	case "high", "major", "p1":
		return "high", true
	case "medium", "normal", "p2":
		return "medium", true
	case "low", "lowest", "minor", "trivial", "p3":
		return "low", true
	}
	return "", false
}

// ---------- chrono parsing (0.4.45) ----------

type scanner struct{ s string }

func (x *scanner) trimStart() {
	x.s = strings.TrimLeftFunc(x.s, unicode.IsSpace)
}

// number is chrono scan::number: min..max ASCII digits.
func (x *scanner) number(min, max int) (int64, bool) {
	if len(x.s) < min {
		return 0, false
	}
	var n int64
	i := 0
	for ; i < len(x.s) && i < max; i++ {
		c := x.s[i]
		if c < '0' || c > '9' {
			if i < min {
				return 0, false
			}
			break
		}
		if n > (1<<62)/10 {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	x.s = x.s[i:]
	return n, true
}

func (x *scanner) literal(l string) bool {
	if !strings.HasPrefix(x.s, l) {
		return false
	}
	x.s = x.s[len(l):]
	return true
}

func (x *scanner) numeric(max int) (int64, bool) {
	x.trimStart()
	return x.number(1, max)
}

func (x *scanner) year() (int64, bool) {
	x.trimStart()
	if strings.HasPrefix(x.s, "-") {
		x.s = x.s[1:]
		n, ok := x.number(1, 1<<30)
		return -n, ok
	}
	if strings.HasPrefix(x.s, "+") {
		x.s = x.s[1:]
		return x.number(1, 1<<30)
	}
	return x.number(1, 4)
}

// nanosecond is scan::nanosecond: 1..9 digits scaled, further digits skipped.
func (x *scanner) nanosecond() (int, bool) {
	before := len(x.s)
	v, ok := x.number(1, 9)
	if !ok {
		return 0, false
	}
	consumed := before - len(x.s)
	for i := consumed; i < 9; i++ {
		v *= 10
	}
	x.s = strings.TrimLeft(x.s, "0123456789")
	return int(v), true
}

// tzOffset is scan::timezone_offset(allow_zulu, colonOnly): seconds east of UTC.
func (x *scanner) tzOffset(zulu, colonRequired bool) (int, bool) {
	if zulu && len(x.s) > 0 && (x.s[0] == 'Z' || x.s[0] == 'z') {
		x.s = x.s[1:]
		return 0, true
	}
	neg := false
	switch {
	case strings.HasPrefix(x.s, "+"):
		x.s = x.s[1:]
	case strings.HasPrefix(x.s, "-"):
		x.s, neg = x.s[1:], true
	case strings.HasPrefix(x.s, "−"):
		x.s, neg = x.s[len("−"):], true
	default:
		return 0, false
	}
	if len(x.s) < 2 || !isDigit(x.s[0]) || !isDigit(x.s[1]) {
		return 0, false
	}
	hours := int(x.s[0]-'0')*10 + int(x.s[1]-'0')
	x.s = x.s[2:]
	if colonRequired {
		if !strings.HasPrefix(x.s, ":") {
			return 0, false
		}
		x.s = x.s[1:]
	} else {
		x.s = strings.TrimLeftFunc(x.s, func(r rune) bool { return r == ':' || unicode.IsSpace(r) })
	}
	if len(x.s) < 2 || x.s[0] < '0' || x.s[0] > '5' || !isDigit(x.s[1]) {
		return 0, false
	}
	minutes := int(x.s[0]-'0')*10 + int(x.s[1]-'0')
	x.s = x.s[2:]
	sec := hours*3600 + minutes*60
	if sec >= 86400 {
		return 0, false
	}
	if neg {
		sec = -sec
	}
	return sec, true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func mkTime(y, mo, d, h, mi, s int64, nano, off int) (time.Time, bool) {
	if mo < 1 || mo > 12 || d < 1 || h > 23 || mi > 59 || s > 60 || y < 0 || y > 9999 {
		return time.Time{}, false
	}
	t := time.Date(int(y), time.Month(mo), 1, 0, 0, 0, 0, time.UTC)
	if d > int64(t.AddDate(0, 1, -1).Day()) {
		return time.Time{}, false
	}
	t = time.Date(int(y), time.Month(mo), int(d), int(h), int(mi), int(s), nano, time.UTC)
	return t.Add(-time.Duration(off) * time.Second), true
}

// parseTSFormat is DateTime::parse_from_str(s, "%Y-%m-%dT%H:%M:%S%.f%z").
func parseTSFormat(s string) (time.Time, bool) {
	x := &scanner{s}
	var v [6]int64
	lits := []string{"-", "-", "T", ":", ":", ""}
	for i := range v {
		var ok bool
		if i == 0 {
			v[i], ok = x.year()
		} else {
			v[i], ok = x.numeric(2)
		}
		if !ok || !x.literal(lits[i]) {
			return time.Time{}, false
		}
	}
	nano := 0
	if strings.HasPrefix(x.s, ".") {
		x.s = x.s[1:]
		var ok bool
		if nano, ok = x.nanosecond(); !ok {
			return time.Time{}, false
		}
	}
	x.trimStart()
	off, ok := x.tzOffset(false, false)
	if !ok || x.s != "" {
		return time.Time{}, false
	}
	return mkTime(v[0], v[1], v[2], v[3], v[4], v[5], nano, off)
}

// parseTSRFC3339 is DateTime::parse_from_rfc3339.
func parseTSRFC3339(s string) (time.Time, bool) {
	if len(s) < 19 {
		return time.Time{}, false
	}
	dg := func(i int) (int64, bool) { return int64(s[i] - '0'), isDigit(s[i]) }
	var n [14]int64
	pos := []int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18}
	for i, p := range pos {
		d, ok := dg(p)
		if !ok {
			return time.Time{}, false
		}
		n[i] = d
	}
	if s[4] != '-' || s[7] != '-' || (s[10] != 't' && s[10] != 'T' && s[10] != ' ') || s[13] != ':' || s[16] != ':' {
		return time.Time{}, false
	}
	year := n[0]*1000 + n[1]*100 + n[2]*10 + n[3]
	x := &scanner{s[19:]}
	nano := 0
	if strings.HasPrefix(x.s, ".") {
		x.s = x.s[1:]
		var ok bool
		if nano, ok = x.nanosecond(); !ok {
			return time.Time{}, false
		}
	}
	off, ok := x.tzOffset(true, true)
	if !ok || x.s != "" {
		return time.Time{}, false
	}
	return mkTime(year, n[4]*10+n[5], n[6]*10+n[7], n[8]*10+n[9], n[10]*10+n[11], n[12]*10+n[13], nano, off)
}

func parseTS(s string) *time.Time {
	t, ok := parseTSFormat(s)
	if !ok {
		t, ok = parseTSRFC3339(s)
	}
	if !ok {
		return nil
	}
	t = t.UTC()
	return &t
}

// parseDate is NaiveDate::parse_from_str(s.get(..10)?, "%Y-%m-%d"); the result is "YYYY-MM-DD".
func parseDate(s string) *string {
	if len(s) < 10 || (len(s) > 10 && !utf8.RuneStart(s[10])) {
		return nil
	}
	x := &scanner{s[:10]}
	y, ok := x.year()
	if !ok || !x.literal("-") {
		return nil
	}
	m, ok := x.numeric(2)
	if !ok || !x.literal("-") {
		return nil
	}
	d, ok := x.numeric(2)
	if !ok || x.s != "" || m < 1 || m > 12 || d < 1 || y < 0 || y > 9999 {
		return nil
	}
	if d > int64(time.Date(int(y), time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()) {
		return nil
	}
	r := time.Date(int(y), time.Month(m), int(d), 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	return &r
}
