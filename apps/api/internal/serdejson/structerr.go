package serdejson

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/uuidparse"
)

// Kind of a struct member.
type Kind int

const (
	String  Kind = iota // String
	UUID                // Uuid
	Bool                // bool
	StrList             // Vec<String>
)

// Field is one member of a Rust input struct: Optional is Option<T> (null and absent are fine), Default is
// #[serde(default)] (absent is fine, null is not).
type Field struct {
	Name     string
	Optional bool
	Default  bool
	Flat     bool // member of a #[serde(flatten)] struct: read after the other fields, duplicates not detected
	Kind     Kind
}

// Spec describes a Rust input struct: its name, its fields in declaration order and whether it uses
// #[serde(flatten)] (which makes the sequence form invalid).
type Spec struct {
	Name    string
	Fields  []Field
	Flatten bool
}

// StructError is what axum's Json<T> extractor answers for a body that is not a valid T: 400 with the syntax error,
// or 422 with serde's data error, whose position is where serde stopped reading. The document is read in order, like
// serde does, so a data error before a later syntax error wins and unknown fields are skipped leniently. It returns
// status 0 when the body is a valid T.
func StructError(data []byte, spec Spec) (int, string) {
	name, fields := spec.Name, spec.Fields
	const pre = "Failed to deserialize the JSON body into the target type: "
	p := &parser{b: data, remaining: 128, withPath: true}
	syntax := func(e *Error) (int, string) { return 400, "Failed to parse the request body as JSON: " + e.Error() }
	c, ok := p.ws()
	at := func(msg string) string {
		l, col := p.pos(p.i)
		return fmt.Sprintf("%s%s at line %d column %d", pre, msg, l, col)
	}
	trailing := func() (int, string) {
		if _, more := p.ws(); more {
			return syntax(p.peekErr("trailing characters"))
		}
		return 0, ""
	}
	if !ok {
		return syntax(p.peekErr(eofValue))
	}
	if c != '{' {
		start := p.i
		if e := p.value(); e != nil {
			return syntax(e)
		}
		if c == '[' {
			if spec.Flatten {
				p.i = start
				return 422, at(fmt.Sprintf("invalid type: sequence, expected struct %s", name))
			}
			// positional form: element i is field i
			q := &parser{b: data, i: start + 1, remaining: 128}
			n := 0
			for {
				d, ok := q.ws()
				if !ok || d == ']' {
					break
				}
				if d == ',' {
					q.i++
					q.ws()
				}
				es := q.i
				_ = q.value()
				if n < len(fields) {
					if msg := checkKind(data, es, q, fields[n], fmt.Sprintf("[%d]", n)); msg != "" {
						p.i = q.i
						return 422, at(msg)
					}
				}
				n++
			}
			if n > len(fields) {
				return 422, at(fmt.Sprintf("invalid length %d, expected fewer elements in array", n))
			}
			for _, f := range fields[min(n, len(fields)):] {
				if !f.Default { // a missing positional element of a field without #[serde(default)]
					return 422, at(fmt.Sprintf("invalid length %d, expected struct %s with %d element%s", n, name, len(fields), plural(len(fields))))
				}
			}
			return trailing()
		}
		return 422, at(fmt.Sprintf("invalid type: %s, expected struct %s", scalarName(data[start:p.i]), name))
	}
	p.i++
	known := map[string]Field{}
	for _, f := range fields {
		known[f.Name] = f
	}
	seen := map[string]bool{}
	var deferred []Field // #[serde(flatten)] members are read from a buffer after the other fields
	var deferredAt []int
	for {
		d, ok := p.ws()
		if !ok {
			return syntax(p.peekErr(eofObject))
		}
		if d == '}' {
			p.i++
			break
		}
		if d == ',' {
			p.i++
			if e, ok := p.ws(); ok && e == '}' {
				return syntax(p.peekErr("trailing comma"))
			}
		}
		if d, _ := p.ws(); d != '"' {
			return syntax(p.peekErr("key must be a string"))
		}
		p.i++
		ks := p.i
		if e := p.str(); e != nil {
			return syntax(e)
		}
		key := unquote(data[ks : p.i-1])
		keyEnd := p.i
		if d, ok := p.ws(); !ok || d != ':' {
			if !ok {
				return syntax(p.peekErr(eofObject))
			}
			return syntax(p.peekErr("expected `:`"))
		}
		p.i++
		f, isKnown := known[key]
		flat := isKnown && f.Flat
		if isKnown && !flat && seen[key] {
			p.i = keyEnd // serde reports it right after reading the key
			return 422, at("duplicate field `" + key + "`")
		}
		p.ws()
		start := p.i
		p.path = append(p.path[:0], seg{idx: -1, key: key})
		p.lenient = !isKnown && !spec.Flatten
		e := p.value()
		p.lenient = false
		p.path = p.path[:0]
		if e != nil {
			return syntax(e)
		}
		if !isKnown {
			continue
		}
		if flat {
			deferred = append(deferred, f)
			deferredAt = append(deferredAt, start)
			continue
		}
		seen[key] = true
		if msg := checkKind(data, start, p, f, key); msg != "" {
			return 422, at(msg)
		}
	}
	for _, f := range fields {
		if !f.Flat && !f.Optional && !f.Default && !seen[f.Name] {
			return 422, at("missing field `" + f.Name + "`")
		}
	}
	for i, f := range deferred {
		end := p.i
		q := &parser{b: data, i: deferredAt[i], remaining: 128}
		_ = q.value()
		if msg := checkKind(data, deferredAt[i], q, f, f.Name); msg != "" {
			p.i = end
			return 422, at(msg)
		}
		p.i = end
	}
	return trailing()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// scalarName is serde's Unexpected for a JSON value's text (strings are quoted with Debug escapes).
func scalarName(v []byte) string {
	s := strings.TrimSpace(string(v))
	switch {
	case s == "null":
		return "null"
	case s == "true" || s == "false":
		return "boolean `" + s + "`"
	case strings.HasPrefix(s, "["):
		return "sequence"
	case strings.HasPrefix(s, "{"):
		return "map"
	case strings.HasPrefix(s, `"`):
		u, _ := strconv.Unquote(s)
		return "string " + strconv.Quote(u)
	}
	if !strings.ContainsAny(s, ".eE") {
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return "integer `" + s + "`"
		}
		if _, err := strconv.ParseUint(s, 10, 64); err == nil {
			return "integer `" + s + "`"
		}
	}
	f, _ := strconv.ParseFloat(s, 64)
	return "floating point `" + rustFloat(f) + "`"
}

// checkKind validates the member value that starts at data[start] (the parser has consumed all of it) and returns
// the serde message, leaving p.i where serde stops reading.
func checkKind(data []byte, start int, p *parser, f Field, key string) string {
	end := p.i
	first := data[start]
	bad := func(i int, expected string) string {
		p.i = i
		return key + ": invalid type: " + scalarName(data[start:max(i, start+1)]) + ", expected " + expected
	}
	opener := func() int { // arrays and objects are refused before they are read
		if first == '[' || first == '{' {
			return start
		}
		return end
	}
	if first == 'n' && f.Optional {
		return ""
	}
	switch f.Kind {
	case String:
		if first != '"' {
			return bad(opener(), "a string")
		}
	case UUID:
		if first != '"' {
			return bad(opener(), "a formatted UUID string")
		}
		u, _ := strconv.Unquote(string(data[start:end]))
		if _, msg := uuidparse.Parse(u); msg != "" {
			p.i = end
			return key + ": UUID parsing failed: " + msg
		}
	case Bool:
		if first != 't' && first != 'f' {
			return bad(opener(), "a boolean")
		}
	case StrList:
		if first != '[' {
			return bad(opener(), "a sequence")
		}
		q := &parser{b: data, remaining: 128, i: start + 1}
		for idx := 0; ; idx++ {
			c, _ := q.ws()
			if c == ']' {
				break
			}
			if c == ',' {
				q.i++
				q.ws()
			}
			es := q.i
			_ = q.value()
			if data[es] != '"' {
				p.i = max(opener(), q.i)
				if data[es] == '[' || data[es] == '{' {
					p.i = es
				} else {
					p.i = q.i
				}
				return fmt.Sprintf("%s[%d]: invalid type: %s, expected a string", key, idx, scalarName(data[es:max(p.i, es+1)]))
			}
		}
		p.i = end
	}
	return ""
}

// RustFloat is how serde prints Unexpected::Float: shortest digits, ".0" on integral values, exponent form
// ("1e+20", "1.5e-6") from 1e16 up and below 1e-5.
func RustFloat(f float64) string {
	a := math.Abs(f)
	if a != 0 && (a >= 1e16 || a < 1e-5) {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		sign := exp[:1]
		exp = strings.TrimLeft(exp[1:], "0")
		if exp == "" {
			exp = "0"
		}
		return mant + "e" + sign + exp
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".IN") {
		s += ".0"
	}
	return s
}

func rustFloat(f float64) string { return RustFloat(f) }
