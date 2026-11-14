package notes

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
)

// obj is a request body decoded like serde would: every getter returns ErrFallback for input serde would
// reject (wrong type, null for a non-Option field, odd UUID spelling, lone surrogate...), so Rust produces the
// error text. Unknown fields are ignored; duplicate keys: last wins (Rust parses a Value first).
type obj map[string]json.RawMessage

func parseObj(input json.RawMessage) (obj, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil || m == nil {
		return nil, command.ErrFallback
	}
	return m, nil
}

func (o obj) get(k string) (json.RawMessage, bool) {
	v, ok := o[k]
	if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return nil, false
	}
	return v, true
}

func (o obj) str(k string, required bool) (*string, error) {
	v, ok := o.get(k)
	if !ok {
		if _, present := o[k]; present || required {
			return nil, command.ErrFallback // null for String, or missing
		}
		return nil, nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil || strings.ContainsRune(s, '�') {
		return nil, command.ErrFallback
	}
	return &s, nil
}

// optStr is Option<String>: missing or null is None.
func (o obj) optStr(k string) (*string, error) {
	if _, ok := o.get(k); !ok {
		return nil, nil
	}
	return o.str(k, false)
}

func (o obj) reqStr(k string) (string, error) {
	s, err := o.str(k, true)
	if err != nil {
		return "", err
	}
	return *s, nil
}

func (o obj) optUUID(k string) (*string, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil || !uuidRe.MatchString(s) {
		return nil, command.ErrFallback
	}
	s = strings.ToLower(s)
	return &s, nil
}

func (o obj) reqUUID(k string) (string, error) {
	if _, ok := o.get(k); !ok {
		return "", command.ErrFallback
	}
	u, err := o.optUUID(k)
	if err != nil {
		return "", err
	}
	return *u, nil
}

// optInt is Option<i32|i64>: only plain integers inside the range.
func (o obj) optInt(k string, bits int) (*int64, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	n, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, bits)
	if err != nil {
		return nil, command.ErrFallback
	}
	return &n, nil
}

func (o obj) boolDef(k string, def bool) (bool, error) {
	v, ok := o[k]
	if !ok {
		return def, nil
	}
	var b bool
	if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, &b) != nil {
		return false, command.ErrFallback
	}
	return b, nil
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (o obj) optDate(k string) (*string, error) {
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

// types is `Vec<String>` with #[serde(default)]: missing is empty, null is an error.
func (o obj) types(k string) ([]string, error) {
	v, ok := o[k]
	if !ok {
		return []string{}, nil
	}
	var out []string
	if json.Unmarshal(v, &out) != nil || out == nil {
		return nil, command.ErrFallback
	}
	for _, s := range out {
		if strings.ContainsRune(s, '�') {
			return nil, command.ErrFallback
		}
	}
	return out, nil
}

// optValue is Option<serde_json::Value>. Rust re-serializes floats and big integers (1e2 -> 100.0), which
// jsonb would not reproduce from the raw text, so anything but plain integers falls back.
func (o obj) optValue(k string) (json.RawMessage, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(v))
	dec.UseNumber()
	var any any
	if dec.Decode(&any) != nil || !plainNumbers(any) {
		return nil, command.ErrFallback
	}
	return v, nil
}

func plainNumbers(v any) bool {
	switch x := v.(type) {
	case json.Number:
		n, err := strconv.ParseInt(x.String(), 10, 64)
		return err == nil && x.String() != "-0" && (n != 0 || x.String() == "0")
	case string:
		return !strings.ContainsRune(x, '�')
	case []any:
		for _, e := range x {
			if !plainNumbers(e) {
				return false
			}
		}
	case map[string]any:
		for k, e := range x {
			if strings.ContainsRune(k, '�') || !plainNumbers(e) {
				return false
			}
		}
	}
	return true
}
