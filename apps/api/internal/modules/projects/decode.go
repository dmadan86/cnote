package projects

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
)

// Request bodies are decoded like serde would: exact (case-sensitive) keys, null only for Option fields, and
// anything serde would reject (wrong type, odd UUID spelling, lone surrogate, non-canonical number...) returns
// ErrFallback so Rust produces the error text. Unknown fields are ignored; for duplicate keys the last wins.

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func parseObj(input json.RawMessage) (jobj, error) {
	o, ok := asObj(input)
	if !ok {
		return nil, command.ErrFallback
	}
	return o, nil
}

func isNull(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }

// get returns the value unless the key is missing or null (Option fields).
func (o jobj) get(k string) (json.RawMessage, bool) {
	v, ok := o[k]
	if !ok || isNull(v) {
		return nil, false
	}
	return v, true
}

func (o jobj) optStr(k string) (*string, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	s, ok := asStr(v, true)
	if !ok {
		return nil, command.ErrFallback
	}
	return &s, nil
}

func (o jobj) reqStr(k string) (string, error) {
	v, ok := o[k]
	if !ok {
		return "", command.ErrFallback
	}
	s, ok := asStr(v, true)
	if !ok {
		return "", command.ErrFallback
	}
	return s, nil
}

func (o jobj) optUUID(k string) (*string, error) {
	s, err := o.optStr(k)
	if err != nil || s == nil {
		return nil, err
	}
	if !uuidRe.MatchString(*s) {
		return nil, command.ErrFallback
	}
	l := strings.ToLower(*s)
	return &l, nil
}

func (o jobj) reqUUID(k string) (string, error) {
	if _, ok := o.get(k); !ok {
		return "", command.ErrFallback
	}
	u, err := o.optUUID(k)
	if err != nil {
		return "", err
	}
	return *u, nil
}

func (o jobj) optI64(k string) (*int64, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	n, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64)
	if err != nil {
		return nil, command.ErrFallback
	}
	return &n, nil
}

func (o jobj) optF64(k string) (*float64, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	t := string(bytes.TrimSpace(v))
	if t == "" || !(t[0] == '-' || isDigit(t[0])) {
		return nil, command.ErrFallback
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return nil, command.ErrFallback
	}
	return &f, nil
}

func (o jobj) boolDef(k string, def bool) (bool, error) {
	v, ok := o[k]
	if !ok {
		return def, nil
	}
	var b bool
	if isNull(v) || json.Unmarshal(v, &b) != nil {
		return false, command.ErrFallback
	}
	return b, nil
}

func (o jobj) optBool(k string) (*bool, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	var b bool
	if json.Unmarshal(v, &b) != nil {
		return nil, command.ErrFallback
	}
	return &b, nil
}

// strVecDef is `Vec<String>` with #[serde(default)]: missing is empty, null is an error.
func (o jobj) strVecDef(k string) ([]string, error) {
	v, ok := o[k]
	if !ok {
		return []string{}, nil
	}
	out, ok := asStrs(v, true)
	if !ok {
		return nil, command.ErrFallback
	}
	return out, nil
}

// optStrVec is `Option<Vec<String>>`.
func (o jobj) optStrVec(k string) ([]string, bool, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, false, nil
	}
	out, ok := asStrs(v, true)
	if !ok {
		return nil, false, command.ErrFallback
	}
	return out, true, nil
}

func (o jobj) optDate(k string) (*string, error) {
	s, err := o.optStr(k)
	if err != nil || s == nil {
		return nil, err
	}
	if !dateRe.MatchString(*s) {
		return nil, command.ErrFallback
	}
	if _, err := time.Parse("2006-01-02", *s); err != nil {
		return nil, command.ErrFallback
	}
	return s, nil
}

// optValue is Option<serde_json::Value>: numbers must survive a serde round trip unchanged.
func (o jobj) optValue(k string) (json.RawMessage, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	if !plainJSON(v) {
		return nil, command.ErrFallback
	}
	return v, nil
}
