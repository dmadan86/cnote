// Package serdejson reproduces the syntax error texts of Rust's serde_json (1.x) when parsing into a Value, so
// requests and import contents that Rust would reject get the same message here ("key must be a string at line 1
// column 2"). It is a validating recursive-descent parser written against serde_json's de.rs / read.rs; it builds
// no value. With Path set it also adds the serde_path_to_error prefix axum's Json extractor shows ("a[0]: ...").
package serdejson

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Error is a serde_json syntax error.
type Error struct {
	Msg       string
	Line, Col int
	Path      string // non-empty only with WithPath
}

func (e *Error) Error() string {
	s := fmt.Sprintf("%s at line %d column %d", e.Msg, e.Line, e.Col)
	if e.Path == "\x00empty" {
		return ": " + s
	}
	if e.Path != "" {
		return e.Path + ": " + s
	}
	return s
}

const (
	eofList      = "EOF while parsing a list"
	eofObject    = "EOF while parsing an object"
	eofString    = "EOF while parsing a string"
	eofValue     = "EOF while parsing a value"
	expectedByte = "expected ident"
)

type parser struct {
	b         []byte
	i         int
	remaining int
	path      []seg
	withPath  bool
	lenient   bool // inside a value serde skips (unknown struct fields): strings are not validated
}

type seg struct {
	idx     int // >= 0: sequence index
	key     string
	unknown bool // serde_path_to_error's "?": an error while reading an object key
}

// Check parses data as one JSON value (serde_json::from_slice::<Value>) and returns the error serde_json would
// return, or nil. withPath adds the "a.b[0]: " prefix axum shows for request bodies.
func Check(data []byte, withPath bool) *Error {
	p := &parser{b: data, remaining: 128, withPath: withPath}
	if e := p.value(); e != nil {
		return e
	}
	if _, ok := p.ws(); ok {
		return p.peekErr("trailing characters")
	}
	return nil
}

// CheckString is serde_json::from_str::<Value> (no path).
func CheckString(s string) *Error { return Check([]byte(s), false) }

func (p *parser) pos(idx int) (line, col int) {
	line = 1
	for _, c := range p.b[:idx] {
		if c == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return
}

// errAt: error() = position after the bytes consumed so far.
func (p *parser) errAt(msg string) *Error {
	l, c := p.pos(min(p.i, len(p.b)))
	return p.mk(msg, l, c)
}

// peekErr: peek_error() = position of the next byte.
func (p *parser) peekErr(msg string) *Error {
	l, c := p.pos(min(p.i+1, len(p.b)))
	return p.mk(msg, l, c)
}

func (p *parser) mk(msg string, l, c int) *Error {
	e := &Error{Msg: msg, Line: l, Col: c}
	if p.withPath {
		e.Path = p.pathString()
	}
	return e
}

// pathString is serde_path_to_error's Display; a path of only unknown segments shows no prefix at all.
func (p *parser) pathString() string {
	only := true
	for _, s := range p.path {
		only = only && s.unknown
	}
	if only {
		return ""
	}
	var sb strings.Builder
	for n, s := range p.path {
		switch {
		case s.idx >= 0:
			sb.WriteString("[" + strconv.Itoa(s.idx) + "]")
		default:
			if n > 0 {
				sb.WriteByte('.')
			}
			if s.unknown {
				sb.WriteByte('?')
			} else {
				sb.WriteString(s.key)
			}
		}
	}
	if sb.Len() == 0 {
		return "\x00empty" // a path made of one empty key still prints ": "
	}
	return sb.String()
}

func (p *parser) ws() (byte, bool) {
	for p.i < len(p.b) {
		switch c := p.b[p.i]; c {
		case ' ', '\n', '\t', '\r':
			p.i++
		default:
			return c, true
		}
	}
	return 0, false
}

func (p *parser) value() *Error {
	c, ok := p.ws()
	if !ok {
		return p.peekErrNoPeek(eofValue)
	}
	switch {
	case c == 'n':
		p.i++
		return p.ident("ull")
	case c == 't':
		p.i++
		return p.ident("rue")
	case c == 'f':
		p.i++
		return p.ident("alse")
	case c == '-':
		p.i++
		return p.integer()
	case c >= '0' && c <= '9':
		return p.integer()
	case c == '"':
		p.i++
		return p.str()
	case c == '[':
		if p.remaining--; p.remaining == 0 {
			return p.peekErr("recursion limit exceeded")
		}
		p.i++
		e := p.seq()
		p.remaining++
		return e
	case c == '{':
		if p.remaining--; p.remaining == 0 {
			return p.peekErr("recursion limit exceeded")
		}
		p.i++
		e := p.obj()
		p.remaining++
		return e
	}
	return p.peekErr("expected value")
}

// peekErrNoPeek: peek_error at EOF (position = end of input).
func (p *parser) peekErrNoPeek(msg string) *Error { return p.peekErr(msg) }

func (p *parser) next() (byte, bool) {
	if p.i < len(p.b) {
		c := p.b[p.i]
		p.i++
		return c, true
	}
	return 0, false
}

func (p *parser) peekOrNull() byte {
	if p.i < len(p.b) {
		return p.b[p.i]
	}
	return 0
}

func (p *parser) ident(rest string) *Error {
	for k := 0; k < len(rest); k++ {
		c, ok := p.next()
		if !ok {
			return p.errAt(eofValue)
		}
		if c != rest[k] {
			return p.errAt(expectedByte)
		}
	}
	return nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *parser) integer() *Error {
	c, ok := p.next()
	if !ok {
		return p.errAt(eofValue)
	}
	switch {
	case c == '0':
		if isDigit(p.peekOrNull()) {
			return p.peekErr("invalid number")
		}
		return p.afterInt()
	case c >= '1' && c <= '9':
		for isDigit(p.peekOrNull()) {
			p.i++
		}
		return p.afterInt()
	}
	return p.errAt("invalid number")
}

func (p *parser) afterInt() *Error {
	switch p.peekOrNull() {
	case '.':
		return p.decimal()
	case 'e', 'E':
		return p.exponent()
	}
	return p.checkRange()
}

func (p *parser) decimal() *Error {
	p.i++ // '.'
	digits := false
	for isDigit(p.peekOrNull()) {
		p.i++
		digits = true
	}
	if !digits {
		if p.i < len(p.b) {
			return p.peekErr("invalid number")
		}
		return p.errAt(eofValue)
	}
	if c := p.peekOrNull(); c == 'e' || c == 'E' {
		return p.exponent()
	}
	return p.checkRange()
}

func (p *parser) exponent() *Error {
	p.i++ // 'e'
	if c := p.peekOrNull(); c == '+' || c == '-' {
		p.i++
	}
	c, ok := p.next()
	if !ok {
		return p.errAt(eofValue)
	}
	if !isDigit(c) {
		return p.errAt("invalid number")
	}
	for isDigit(p.peekOrNull()) {
		p.i++
	}
	return p.checkRange()
}

// checkRange: a literal that does not fit a finite f64 is "number out of range" (integers that overflow u64/i64
// are parsed as f64 as well).
func (p *parser) checkRange() *Error {
	j := p.i
	for j > 0 && (isDigit(p.b[j-1]) || strings.IndexByte("+-.eE", p.b[j-1]) >= 0) {
		j--
	}
	lit := string(p.b[j:p.i])
	if _, err := strconv.ParseInt(lit, 10, 64); err == nil {
		return nil
	}
	if _, err := strconv.ParseUint(lit, 10, 64); err == nil {
		return nil
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil && (f > 1 || f < -1) { // ±Inf
		return p.errAt("number out of range")
	}
	return nil
}

func (p *parser) hex4() (uint16, *Error) {
	if p.i+4 > len(p.b) {
		p.i = len(p.b)
		return 0, p.errAt(eofString)
	}
	var n uint16
	bad := false
	for _, c := range p.b[p.i : p.i+4] {
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			bad = true
		}
		n = n<<4 | uint16(d)
	}
	p.i += 4
	if bad {
		return 0, p.errAt("invalid escape")
	}
	return n, nil
}

func (p *parser) str() *Error {
	badCol := -1 // first invalid UTF-8 chunk; serde_json reports it once the whole string was read
	for {
		start := p.i
		for p.i < len(p.b) && p.b[p.i] != '"' && p.b[p.i] != '\\' && (p.lenient || p.b[p.i] >= 0x20) {
			p.i++
		}
		if p.i == len(p.b) {
			return p.errAt(eofString)
		}
		term := p.b[p.i]
		if term < 0x20 && !p.lenient {
			p.i++
			return p.errAt("control character (\\u0000-\\u001F) found while parsing a string")
		}
		// Position of the error: just after the first invalid byte, one further when the chunk ended at a
		// backslash (measured against Rust).
		if n := validUpTo(p.b[start:p.i]); start+n < p.i && badCol < 0 && !p.lenient {
			badCol = start + n + 1
			if term == '\\' {
				badCol++
			}
		}
		p.i++
		if term == '"' {
			if badCol >= 0 {
				p.i = badCol
				return p.errAt("invalid unicode code point")
			}
			return nil
		}
		if e := p.escape(); e != nil {
			return e
		}
	}
}

// validUpTo is str::from_utf8's valid_up_to.
func validUpTo(b []byte) int {
	n := 0
	for n < len(b) {
		r, size := utf8.DecodeRune(b[n:])
		if r == utf8.RuneError && size <= 1 {
			return n
		}
		n += size
	}
	return n
}

func (p *parser) escape() *Error {
	c, ok := p.next()
	if !ok {
		return p.errAt(eofString)
	}
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return nil
	case 'u':
		n, e := p.hex4()
		if e != nil {
			return e
		}
		switch {
		case p.lenient:
			return nil
		case n >= 0xDC00 && n <= 0xDFFF:
			return p.errAt("lone leading surrogate in hex escape")
		case n >= 0xD800 && n <= 0xDBFF:
			if p.i >= len(p.b) {
				return p.errAt(eofString)
			}
			if p.b[p.i] != '\\' {
				p.i++
				return p.errAt("unexpected end of hex escape")
			}
			p.i++
			if p.i >= len(p.b) {
				return p.errAt(eofString)
			}
			if p.b[p.i] != 'u' {
				p.i++
				return p.errAt("unexpected end of hex escape")
			}
			p.i++
			n2, e := p.hex4()
			if e != nil {
				return e
			}
			if n2 < 0xDC00 || n2 > 0xDFFF {
				return p.errAt("lone leading surrogate in hex escape")
			}
		}
		return nil
	}
	return p.errAt("invalid escape")
}

func (p *parser) seq() *Error {
	first := true
	idx := 0
	for {
		c, ok := p.ws()
		switch {
		case ok && c == ']':
			p.i++
			return nil
		case ok && c == ',' && !first:
			p.i++
			c, ok = p.ws()
			if ok && c == ']' {
				return p.peekErr("trailing comma")
			}
			if !ok {
				return p.peekErr(eofValue)
			}
		case ok:
			if !first {
				return p.peekErr("expected `,` or `]`")
			}
			first = false
		default:
			return p.peekErr(eofList)
		}
		p.path = append(p.path, seg{idx: idx})
		e := p.value()
		p.path = p.path[:len(p.path)-1]
		if e != nil {
			return e
		}
		idx++
	}
}

func (p *parser) obj() *Error {
	first := true
	for {
		p.path = append(p.path, seg{idx: -1, unknown: true}) // key phase: errors here show as "?"
		c, ok := p.ws()
		switch {
		case ok && c == '}':
			p.path = p.path[:len(p.path)-1]
			p.i++
			return nil
		case ok && c == ',' && !first:
			p.i++
			c, ok = p.ws()
			if ok && c == '}' {
				return p.peekErr("trailing comma")
			}
			if !ok {
				return p.peekErr(eofValue)
			}
		case ok:
			if !first {
				return p.peekErr("expected `,` or `}`")
			}
			first = false
		default:
			return p.peekErr(eofObject)
		}
		if p.b[p.i] != '"' {
			return p.peekErr("key must be a string")
		}
		p.i++
		ks := p.i
		if e := p.str(); e != nil {
			return e
		}
		key := unquote(p.b[ks : p.i-1])
		p.path = p.path[:len(p.path)-1]
		c, ok = p.ws()
		if !ok {
			return p.peekErr(eofObject)
		}
		if c != ':' {
			return p.peekErr("expected `:`")
		}
		p.i++
		p.path = append(p.path, seg{idx: -1, key: key})
		e := p.value()
		p.path = p.path[:len(p.path)-1]
		if e != nil {
			return e
		}
	}
}

// unquote decodes a validated JSON string body (escapes included) for the path display.
func unquote(raw []byte) string {
	s, err := strconv.Unquote(`"` + strings.ReplaceAll(string(raw), `\/`, `/`) + `"`)
	if err != nil {
		return string(raw)
	}
	return s
}
