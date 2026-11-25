package tables

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/dataimport"
)

// Coerce turns pasted or imported text into the stored value of a field; Validate checks a JSON value from a
// command. Both return exactly what goes into records.data[field.ID] (string, float64, int64, bool, []string) and
// (nil, nil) for "empty", which means the key is absent. Computed and link fields are always an error.
// shortcut: user membership in the workspace is a database check; callers run assertWorkspaceUsers on the result.

const (
	maxText     = 10000
	maxLongText = 100000
	maxMulti    = 50
	maxUsers    = 100
	maxAbs      = 1e15
)

var (
	uuidRe  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	phoneRe = regexp.MustCompile(`^\+?[0-9]{7,15}$`)
	moneyRe = regexp.MustCompile(`[₹,\s]|(?i:rs\.?|inr)`)
)

func bad(format string, a ...any) error { return command.ErrInvalid(fmt.Sprintf(format, a...)) }

func settable(f Field) error {
	switch {
	case f.Type == Link:
		return bad("%q is a link field: set it through links", f.Name)
	case f.Type.Computed():
		return bad("%q is computed and cannot be set", f.Name)
	case f.Type == Attachment:
		return bad("%q holds files, which are added in the record panel and cannot be pasted or imported", f.Name)
	}
	return nil
}

func Coerce(f Field, raw string) (any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if err := settable(f); err != nil {
		return nil, err
	}
	s := strings.TrimSpace(raw)
	switch f.Type {
	case Text, LongText:
		return Validate(f, raw)
	case Number, Percent:
		n, err := parseNumber(s)
		if err != nil {
			return nil, bad("%q is not a number", s)
		}
		return Validate(f, n)
	case Currency:
		p, err := dataimport.ParseMoney(s)
		if err != nil {
			return nil, bad("%q is not an amount", s)
		}
		return Validate(f, p)
	case Date:
		if _, err := time.Parse("2006-01-02", s); err == nil {
			return Validate(f, s)
		}
		d, err := dataimport.ParseDate(s) // shortcut: this parser accepts years 1990-2100 only; ISO dates have no such limit
		if err != nil {
			return nil, bad("%q is not a date (use DD-MM-YYYY)", s)
		}
		return Validate(f, d)
	case Checkbox:
		switch strings.ToLower(s) {
		case "yes", "y", "true", "1", "✓", "✔", "x", "checked":
			return true, nil
		case "no", "n", "false", "0", "unchecked":
			return nil, nil
		}
		return nil, bad("%q is not yes or no", s)
	case MultiSelect, User:
		parts := []any{}
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		return Validate(f, parts)
	case URL:
		if !strings.Contains(s, "://") {
			s = "https://" + s
		}
		return Validate(f, s)
	}
	return Validate(f, s)
}

func parseNumber(s string) (float64, error) {
	t := moneyRe.ReplaceAllString(strings.TrimSuffix(strings.TrimSpace(s), "%"), "")
	if strings.ContainsAny(t, "xXpP_") { // ParseFloat would take hex floats and underscores
		return 0, fmt.Errorf("not a number")
	}
	return strconv.ParseFloat(t, 64)
}

func Validate(f Field, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if err := settable(f); err != nil {
		return nil, err
	}
	switch f.Type {
	case Text, LongText:
		s, ok := v.(string)
		if !ok {
			return nil, bad("%q takes text", f.Name)
		}
		max := maxText
		if f.Type == LongText {
			max = maxLongText
		}
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		if utf8.RuneCountInString(s) > max {
			return nil, bad("%q is limited to %d characters", f.Name, max)
		}
		return s, nil
	case Number, Percent:
		n, ok := toFloat(v)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > maxAbs {
			return nil, bad("%q takes a number", f.Name)
		}
		return n, nil
	case Currency:
		n, ok := toFloat(v)
		if !ok || n != math.Trunc(n) || math.Abs(n) > maxAbs {
			return nil, bad("%q takes whole paise (an integer)", f.Name)
		}
		return int64(n), nil
	case Date:
		s, ok := v.(string)
		if !ok {
			return nil, bad("%q takes a date as YYYY-MM-DD", f.Name)
		}
		if d, err := time.Parse("2006-01-02", s); err != nil || d.Format("2006-01-02") != s {
			return nil, bad("%q takes a date as YYYY-MM-DD", f.Name)
		}
		return s, nil
	case Checkbox:
		b, ok := v.(bool)
		if !ok {
			return nil, bad("%q takes true or false", f.Name)
		}
		if !b {
			return nil, nil
		}
		return true, nil
	case SingleSelect:
		s, ok := v.(string)
		if !ok {
			return nil, bad("%q takes one option", f.Name)
		}
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		return resolveOption(f, s)
	case MultiSelect:
		items, ok := toStrings(v)
		if !ok {
			return nil, bad("%q takes a list of options", f.Name)
		}
		out := []string{}
		seen := map[string]bool{}
		for _, it := range items {
			id, err := resolveOption(f, it)
			if err != nil {
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		if len(out) > maxMulti {
			return nil, bad("%q takes at most %d options", f.Name, maxMulti)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	case Phone:
		s, ok := v.(string)
		if !ok {
			return nil, bad("%q takes a phone number", f.Name)
		}
		p := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(s))
		if !phoneRe.MatchString(p) {
			return nil, bad("%q is not a phone number (7 to 15 digits, optional +)", s)
		}
		return p, nil
	case Email:
		s, ok := v.(string)
		s = strings.TrimSpace(s)
		if !ok || len(s) > 254 || !emailRe.MatchString(s) {
			return nil, bad("%q is not an email address", s)
		}
		return s, nil
	case URL:
		s, ok := v.(string)
		s = strings.TrimSpace(s)
		u, err := url.Parse(s)
		if !ok || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(s) > 2048 {
			return nil, bad("%q is not a web address (http or https)", s)
		}
		return s, nil
	case User:
		items, ok := toStrings(v)
		if !ok {
			return nil, bad("%q takes person ids", f.Name)
		}
		out := []string{}
		seen := map[string]bool{}
		for _, id := range items {
			id = strings.ToLower(strings.TrimSpace(id))
			if !uuidRe.MatchString(id) {
				return nil, bad("%q takes person ids", f.Name)
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		if len(out) > maxUsers {
			return nil, bad("%q takes at most %d people", f.Name, maxUsers)
		}
		if len(out) > 1 && !cfgOf[UserConfig](f).Multiple {
			return nil, bad("%q takes one person", f.Name)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}
	return nil, bad("%q has an unknown type", f.Name)
}

func cfgOf[T any](f Field) T {
	var v T
	_ = json.Unmarshal(f.Config, &v)
	return v
}

// resolveOption maps an option id, or failing that an option name (case-insensitive), to the option id.
func resolveOption(f Field, s string) (string, error) {
	s = strings.TrimSpace(s)
	opts := cfgOf[SelectConfig](f).Options
	for _, o := range opts {
		if o.ID == s {
			return o.ID, nil
		}
	}
	for _, o := range opts {
		if strings.EqualFold(o.Name, s) {
			return o.ID, nil
		}
	}
	return "", bad("%q is not an option of %q", s, f.Name)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func toStrings(v any) ([]string, bool) {
	switch x := v.(type) {
	case string:
		return []string{x}, true
	case []string:
		return x, true
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}
