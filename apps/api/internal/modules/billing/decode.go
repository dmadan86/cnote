package billing

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
)

// Private copy of the serde-mimicking decoder in modules/notes (its methods are unexported): every getter
// returns ErrFallback for input serde would reject, so Rust produces the error text.
type obj map[string]json.RawMessage

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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

// optStr is Option<String>: missing or null is None.
func (o obj) optStr(k string) (*string, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return nil, command.ErrFallback
	}
	return &s, nil
}

func (o obj) reqStr(k string) (string, error) {
	s, err := o.optStr(k)
	if err != nil || s == nil {
		return "", orFallback(err)
	}
	return *s, nil
}

func orFallback(err error) error {
	if err != nil {
		return err
	}
	return command.ErrFallback
}

func (o obj) optUUID(k string) (*string, error) {
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

func (o obj) reqUUID(k string) (string, error) {
	s, err := o.optUUID(k)
	if err != nil || s == nil {
		return "", orFallback(err)
	}
	return *s, nil
}

// intOf parses a JSON integer exactly as serde reads i64/u32: plain integers in range ("-0" is a float there).
func intOf(v json.RawMessage, bits int, unsigned bool) (int64, error) {
	t := string(bytes.TrimSpace(v))
	if t == "-0" {
		return 0, command.ErrFallback
	}
	if unsigned {
		n, err := strconv.ParseUint(t, 10, bits)
		if err != nil || n > math.MaxInt64 {
			return 0, command.ErrFallback
		}
		return int64(n), nil
	}
	n, err := strconv.ParseInt(t, 10, bits)
	if err != nil {
		return 0, command.ErrFallback
	}
	return n, nil
}

func (o obj) optInt(k string, bits int, unsigned bool) (*int64, error) {
	v, ok := o.get(k)
	if !ok {
		return nil, nil
	}
	n, err := intOf(v, bits, unsigned)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (o obj) reqInt(k string) (int64, error) {
	v, ok := o.get(k)
	if !ok {
		return 0, command.ErrFallback
	}
	return intOf(v, 64, false)
}

func (o obj) optBool(k string) (*bool, error) {
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

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// validDate accepts YYYY-MM-DD for years 0000-9999 (chrono also takes wider and negative years; those are refused).
func validDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func (o obj) optDate(k string) (*string, error) {
	s, err := o.optStr(k)
	if err != nil || s == nil {
		return nil, err
	}
	if !validDate(*s) {
		return nil, command.ErrFallback
	}
	return s, nil
}

func (o obj) reqDate(k string) (string, error) {
	s, err := o.optDate(k)
	if err != nil || s == nil {
		return "", orFallback(err)
	}
	return *s, nil
}

var numRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// quantityText is Option<serde_json::Number> whose to_string() feeds rust_decimal: integers print as written,
// every other number goes through f64 and ryu, and rust_decimal reads the result back (2.50 -> "2.5", 1.0 ->
// "1.0", 1e-7 -> "0.0000001", 1e16 -> "10000000000000000").
func quantityText(raw json.RawMessage) (string, error) {
	t := string(bytes.TrimSpace(raw))
	if !numRe.MatchString(t) {
		return "", command.ErrFallback
	}
	if !strings.ContainsAny(t, ".eE") && t != "-0" {
		if _, err := strconv.ParseInt(t, 10, 64); err == nil {
			return t, nil
		}
		if _, err := strconv.ParseUint(t, 10, 64); err == nil {
			return t, nil
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsInf(f, 0) {
		return "", command.ErrFallback
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0", nil
		}
		return "0.0", nil
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if a := math.Abs(f); a < 1e16 && !strings.Contains(s, ".") {
		s += ".0"
	}
	return s, nil
}
