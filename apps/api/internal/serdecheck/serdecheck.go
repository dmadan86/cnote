// Package serdecheck answers, for a command input the Go handler cannot decode, what serde's deserialization of the
// Rust input struct would say: the same "invalid input: ..." message in most cases. It is driven by the command's
// JSON Schema (the catalog snapshot of the Rust registry, or the Go-only command's own schema) and a table of the
// Rust input struct names (structs.json, probed from the Rust server).
//
// Fidelity: missing fields, wrong scalar/array/object types, unknown enum variants, integer ranges, UUID text,
// first-error order (serde_json's Value keeps keys sorted, so errors surface in key order, then missing fields in
// declaration order). Not reproduced: custom deserializers (dates, decimals, ...); those report the generic message.
package serdecheck

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cnote/api/internal/serdejson"
	"github.com/cnote/api/internal/uuidparse"
)

//go:embed structs.json
var structsJSON []byte

var (
	once    sync.Once
	structs map[string][]any // command -> [struct name, field count|nil, accepts `[]`]
)

func table() map[string][]any {
	once.Do(func() {
		structs = map[string][]any{}
		_ = json.Unmarshal(structsJSON, &structs)
	})
	return structs
}

// EmptySequence: serde reads `[]` as the positional form of a struct. For the structs whose fields all have
// defaults (the probe in structs.json found Rust accepting it) the command runs as if the input were `{}`.
func EmptySequence(name string, input json.RawMessage) json.RawMessage {
	if e, ok := table()[name]; ok && len(e) > 2 && e[2] == true && strings.TrimSpace(string(input)) == "[]" {
		return json.RawMessage(`{}`)
	}
	return input
}

// Generic is what Describe returns when it cannot tell.
const Generic = "invalid input"

type walker struct {
	defs map[string]any
}

// Describe returns the message after "invalid input: " for the input of command `name`.
func Describe(name string, schema json.RawMessage, input json.RawMessage) string {
	var sc map[string]any
	_ = json.Unmarshal(schema, &sc)
	w := &walker{defs: map[string]any{}}
	for _, k := range []string{"$defs", "definitions"} {
		if d, ok := sc[k].(map[string]any); ok {
			for n, v := range d {
				w.defs[n] = v
			}
		}
	}
	structName, nfields := "", -1
	if e, ok := table()[name]; ok {
		structName, _ = e[0].(string)
		if f, ok := e[1].(float64); ok {
			nfields = int(f)
		}
	}
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return Generic
	}
	obj, ok := v.(map[string]any)
	if !ok {
		if arr, isArr := v.([]any); isArr {
			return w.sequence(sc, structName, nfields, arr)
		}
		if structName == "" {
			return Generic
		}
		return fmt.Sprintf("invalid type: %s, expected struct %s", unexpected(v), structName)
	}
	if msg := w.object(sc, obj); msg != "" {
		return msg
	}
	return Generic
}

// sequence is serde's positional form of a struct: element i is field i in declaration order. The required fields
// are known in that order (the schema's `required`); optional ones follow alphabetically, which is a guess.
func (w *walker) sequence(sc map[string]any, structName string, nfields int, arr []any) string {
	props, _ := sc["properties"].(map[string]any)
	var order []string
	seen := map[string]bool{}
	req, _ := sc["required"].([]any)
	for _, r := range req {
		if n, _ := r.(string); n != "" {
			order = append(order, n)
			seen[n] = true
		}
	}
	var rest []string
	for k := range props {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	required := len(order)
	order = append(order, rest...)
	if nfields < 0 {
		nfields = len(order)
	}
	for i, e := range arr {
		if i < len(order) {
			if msg := w.value(props[order[i]], e); msg != "" {
				return msg
			}
		}
	}
	switch {
	case len(arr) > nfields:
		return fmt.Sprintf("invalid length %d, expected fewer elements in array", len(arr))
	case len(arr) < required && structName != "":
		return fmt.Sprintf("invalid length %d, expected struct %s with %d element%s", len(arr), structName, nfields, plural(nfields))
	}
	return Generic
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (w *walker) resolve(s any) map[string]any {
	m, _ := s.(map[string]any)
	for i := 0; i < 8 && m != nil; i++ {
		ref, ok := m["$ref"].(string)
		if !ok {
			break
		}
		m, _ = w.defs[ref[strings.LastIndex(ref, "/")+1:]].(map[string]any)
	}
	return m
}

// object checks a struct's fields: errors in sorted key order, then the first missing required field.
func (w *walker) object(sc map[string]any, obj map[string]any) string {
	props, _ := sc["properties"].(map[string]any)
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if p, ok := props[k]; ok {
			if msg := w.value(p, obj[k]); msg != "" {
				return msg
			}
		}
	}
	req, _ := sc["required"].([]any)
	for _, r := range req {
		if name, _ := r.(string); name != "" {
			if _, ok := obj[name]; !ok {
				return fmt.Sprintf("missing field `%s`", name)
			}
		}
	}
	return ""
}

func (w *walker) value(s any, v any) string {
	if b, ok := s.(bool); ok && b {
		return ""
	}
	m := w.resolve(s)
	if m == nil {
		return ""
	}
	for _, k := range []string{"anyOf", "oneOf"} {
		alts, ok := m[k].([]any)
		if !ok {
			continue
		}
		var consts []string
		var nonNull []any
		for _, a := range alts {
			am := w.resolve(a)
			switch {
			case am == nil:
			case am["type"] == "null":
				if v == nil {
					return ""
				}
			case am["const"] != nil:
				c, _ := am["const"].(string)
				consts = append(consts, c)
			default:
				nonNull = append(nonNull, a)
			}
		}
		if len(consts) > 0 {
			return enumCheck(consts, v)
		}
		if len(nonNull) == 1 { // Option<T>
			return w.value(nonNull[0], v)
		}
		return ""
	}
	if en, ok := m["enum"].([]any); ok {
		var names []string
		for _, e := range en {
			if s, ok := e.(string); ok {
				names = append(names, s)
			}
		}
		if len(names) == len(en) {
			if v == nil && nullable(m) {
				return ""
			}
			return enumCheck(names, v)
		}
		return ""
	}
	typ := ""
	switch t := m["type"].(type) {
	case string:
		typ = t
	case []any:
		for _, x := range t {
			if xs, _ := x.(string); xs == "null" {
				if v == nil {
					return ""
				}
			} else {
				typ = xs
			}
		}
	}
	format, _ := m["format"].(string)
	switch typ {
	case "string":
		str, ok := v.(string)
		if !ok {
			switch format {
			case "uuid":
				return fmt.Sprintf("invalid type: %s, expected a formatted UUID string", unexpected(v))
			case "date":
				return fmt.Sprintf("invalid type: %s, expected a formatted date string", unexpected(v))
			case "date-time":
				return fmt.Sprintf("invalid type: %s, expected a formatted date and time string", unexpected(v))
			}
			return fmt.Sprintf("invalid type: %s, expected a string", unexpected(v))
		}
		if format == "date" {
			return chronoDate(str)
		}
		if format == "uuid" {
			if _, msg := uuidparse.Parse(str); msg != "" {
				return "UUID parsing failed: " + msg
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("invalid type: %s, expected a boolean", unexpected(v))
		}
	case "integer":
		return intCheck(format, v)
	case "number":
		if _, ok := v.(json.Number); !ok {
			exp := "f64"
			if format == "float" {
				exp = "f32"
			}
			return fmt.Sprintf("invalid type: %s, expected %s", unexpected(v), exp)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Sprintf("invalid type: %s, expected a sequence", unexpected(v))
		}
		for _, e := range arr {
			if msg := w.value(m["items"], e); msg != "" {
				return msg
			}
		}
	case "object":
		o, ok := v.(map[string]any)
		if _, isStruct := m["properties"]; isStruct {
			if !ok {
				name := ""
				if ref, _ := s.(map[string]any)["$ref"].(string); ref != "" {
					name = ref[strings.LastIndex(ref, "/")+1:]
				}
				if name == "" {
					return ""
				}
				return fmt.Sprintf("invalid type: %s, expected struct %s", unexpected(v), name)
			}
			return w.object(m, o)
		}
		if ap, has := m["additionalProperties"]; has {
			if !ok {
				return fmt.Sprintf("invalid type: %s, expected a map", unexpected(v))
			}
			keys := make([]string, 0, len(o))
			for k := range o {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if msg := w.value(ap, o[k]); msg != "" {
					return msg
				}
			}
		}
	}
	return ""
}

func nullable(m map[string]any) bool {
	if t, ok := m["type"].([]any); ok {
		for _, x := range t {
			if x == "null" {
				return true
			}
		}
	}
	return false
}

func enumCheck(names []string, v any) string {
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("invalid type: %s, expected string or map", unexpected(v))
	}
	for _, n := range names {
		if n == s {
			return ""
		}
	}
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	var exp string
	switch len(q) {
	case 1:
		exp = q[0]
	case 2:
		exp = q[0] + " or " + q[1]
	default:
		exp = "one of " + strings.Join(q, ", ")
	}
	return fmt.Sprintf("unknown variant `%s`, expected %s", s, exp)
}

var intTypes = map[string]struct {
	name   string
	signed bool
	bits   uint
}{
	"int8": {"i8", true, 8}, "int16": {"i16", true, 16}, "int32": {"i32", true, 32}, "int64": {"i64", true, 64}, "int": {"isize", true, 64},
	"uint8": {"u8", false, 8}, "uint16": {"u16", false, 16}, "uint32": {"u32", false, 32}, "uint64": {"u64", false, 64}, "uint": {"usize", false, 64},
}

func intCheck(format string, v any) string {
	t, known := intTypes[format]
	if !known {
		t = struct {
			name   string
			signed bool
			bits   uint
		}{"i64", true, 64}
	}
	n, ok := v.(json.Number)
	if !ok {
		return fmt.Sprintf("invalid type: %s, expected %s", unexpected(v), t.name)
	}
	if strings.ContainsAny(string(n), ".eE") {
		return fmt.Sprintf("invalid type: %s, expected %s", unexpected(v), t.name)
	}
	var err error
	if t.signed {
		_, err = strconv.ParseInt(string(n), 10, int(t.bits))
	} else {
		_, err = strconv.ParseUint(string(n), 10, int(t.bits))
	}
	if err == nil {
		return ""
	}
	_, e1 := strconv.ParseInt(string(n), 10, 64)
	_, e2 := strconv.ParseUint(string(n), 10, 64)
	if e1 == nil || e2 == nil { // a real integer, out of this type's range
		return fmt.Sprintf("invalid value: integer `%s`, expected %s", n, t.name)
	}
	return fmt.Sprintf("invalid type: %s, expected %s", unexpected(v), t.name) // beyond 64 bits serde sees a float
}

// unexpected is serde's Unexpected Display for a JSON value.
func unexpected(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return fmt.Sprintf("boolean `%t`", x)
	case json.Number:
		s := string(x)
		if !strings.ContainsAny(s, ".eE") {
			if _, err := strconv.ParseInt(s, 10, 64); err == nil {
				return "integer `" + s + "`"
			}
			if _, err := strconv.ParseUint(s, 10, 64); err == nil {
				return "integer `" + s + "`"
			}
		}
		f, _ := strconv.ParseFloat(s, 64)
		return "floating point `" + serdejson.RustFloat(f) + "`"
	case string:
		return "string " + debugStr(x)
	case []any:
		return "sequence"
	}
	return "map"
}

// debugStr is Rust's Debug for str.
func debugStr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case 0:
			b.WriteString(`\0`)
		default:
			if unicode.IsPrint(r) || r > 0x7f && unicode.IsGraphic(r) {
				b.WriteRune(r)
			} else {
				fmt.Fprintf(&b, `\u{%x}`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// NormalizeUUIDs rewrites every uuid-format string of the input that is a valid but non-canonical spelling (upper
// case, simple, braced, urn) to the canonical lowercase hyphenated one, as serde's Uuid does while deserializing, so
// handlers can match one spelling. Input that is not an object or has nothing to change is returned untouched.
func NormalizeUUIDs(schema, input json.RawMessage) json.RawMessage {
	var sc map[string]any
	if json.Unmarshal(schema, &sc) != nil {
		return input
	}
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return input
	}
	w := &walker{defs: map[string]any{}}
	for _, k := range []string{"$defs", "definitions"} {
		if d, ok := sc[k].(map[string]any); ok {
			for n, x := range d {
				w.defs[n] = x
			}
		}
	}
	if !w.norm(sc, &v, 0) {
		return input
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return input
	}
	return json.RawMessage(strings.TrimRight(b.String(), "\n"))
}

func (w *walker) norm(s any, v *any, depth int) bool {
	m := w.resolve(s)
	if m == nil || depth > 12 {
		return false
	}
	changed := false
	for _, k := range []string{"anyOf", "oneOf"} {
		if alts, ok := m[k].([]any); ok {
			for _, a := range alts {
				if am := w.resolve(a); am != nil && am["type"] != "null" && am["const"] == nil {
					changed = w.norm(a, v, depth+1) || changed
				}
			}
		}
	}
	switch x := (*v).(type) {
	case string:
		if m["format"] == "uuid" {
			if c, bad := uuidparse.Parse(x); bad == "" && c != x {
				*v = c
				return true
			}
		}
	case []any:
		for i := range x {
			changed = w.norm(m["items"], &x[i], depth+1) || changed
		}
	case map[string]any:
		props, _ := m["properties"].(map[string]any)
		for k, val := range x {
			if p, ok := props[k]; ok {
				c := val
				if w.norm(p, &c, depth+1) {
					x[k] = c
					changed = true
				}
			} else if ap, ok := m["additionalProperties"]; ok {
				c := val
				if w.norm(ap, &c, depth+1) {
					x[k] = c
					changed = true
				}
			}
		}
	}
	return changed
}

// chronoDate is the error NaiveDate::from_str ("%Y-%m-%d") gives, "" for a valid date.
func chronoDate(s string) string {
	const (
		invalid = "input contains invalid characters"
		short   = "premature end of input"
	)
	i := 0
	number := func(minDigits, maxDigits int) (int, string) {
		start := i
		for i < len(s) && i-start < maxDigits && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			if i >= len(s) {
				return 0, short
			}
			return 0, invalid
		}
		n, _ := strconv.Atoi(s[start:i])
		return n, ""
	}
	dash := func() string {
		if i >= len(s) {
			return short
		}
		if s[i] != '-' {
			return invalid
		}
		i++
		return ""
	}
	neg := false
	maxYear := 4
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		neg = s[i] == '-'
		i++
		maxYear = 9
	}
	y, msg := number(1, maxYear)
	if msg != "" {
		return msg
	}
	if msg = dash(); msg != "" {
		return msg
	}
	m, msg := number(1, 2)
	if msg != "" {
		return msg
	}
	if msg = dash(); msg != "" {
		return msg
	}
	d, msg := number(1, 2)
	if msg != "" {
		return msg
	}
	if i < len(s) {
		return "trailing input"
	}
	if neg {
		y = -y
	}
	if m < 1 || m > 12 || d < 1 || d > 31 || y > 262143 || y < -262144 {
		return "input is out of range"
	}
	if d > time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day() {
		return "no possible date and time matching input"
	}
	return ""
}
