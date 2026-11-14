package command

import (
	"bytes"
	"encoding/json"
	"time"
)

// Marshal matches serde_json::to_vec: compact, no trailing newline, no HTML escaping.
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return rawLineSeparators(bytes.TrimRight(b.Bytes(), "\n")), nil
}

// encoding/json always escapes U+2028/U+2029; serde_json emits them raw. Walk escape pairs so an
// escaped backslash followed by the text "u2028" is left alone.
func rawLineSeparators(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u202`)) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' || i+1 >= len(b) {
			out = append(out, b[i])
			continue
		}
		if i+6 <= len(b) && b[i+1] == 'u' && (string(b[i+2:i+6]) == "2028" || string(b[i+2:i+6]) == "2029") {
			out = append(out, map[string]string{"2028": "\u2028", "2029": "\u2029"}[string(b[i+2:i+6])]...)
			i += 5
			continue
		}
		out = append(out, b[i], b[i+1])
		i++
	}
	return out
}

// Norm rewrites a JSON document the way serde_json::Value does: objects with sorted keys (BTreeMap),
// compact, numbers kept as written. Use it for every jsonb column (config, payload, results).
func Norm(raw []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return Marshal(v)
}

// Ts serializes like chrono::DateTime<Utc>: RFC 3339 with 0, 3, 6 or 9 fractional digits ("AutoSi") and Z.
type Ts time.Time

func (t Ts) MarshalJSON() ([]byte, error) {
	u := time.Time(t).UTC()
	frac := ""
	switch ns := u.Nanosecond(); {
	case ns == 0:
	case ns%1e6 == 0:
		frac = ".000"
	case ns%1e3 == 0:
		frac = ".000000"
	default:
		frac = ".000000000"
	}
	return []byte(`"` + u.Format("2006-01-02T15:04:05"+frac) + `Z"`), nil
}

func TsPtr(t *time.Time) *Ts {
	if t == nil {
		return nil
	}
	v := Ts(*t)
	return &v
}
