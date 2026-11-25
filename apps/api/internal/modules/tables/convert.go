package tables

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
)

// Field type changes (17.4): only the lossless ones, run inside the command's transaction.
//
//	anything scalar -> text, long_text   the displayed text (long_text -> text only if every value fits)
//	number <-> percent <-> currency      currency is paise: x100 / x100 (a number with more than 2 decimals rounds to a paise)
//	single_select -> multi_select        wrapped in a list
//	text -> single_select, multi_select  new options from the distinct values (multi splits on commas), 200 at most
//	text -> phone, email, url            every value must already be valid
//
// A value that would not survive is counted in `cleared` and the conversion is refused, never applied.
// shortcut: no record_history rows are written for converted values (nothing is cleared); add them with conversions that clear.

const refusal = "This type change is not available yet. Create a new field and copy the values"

type convSample struct {
	Seq  int64 `json:"seq"`
	From any   `json:"from"`
	To   any   `json:"to"`
}
type convPreview struct {
	Kept    int          `json:"kept"`
	Changed int          `json:"changed"`
	Cleared int          `json:"cleared"`
	Samples []convSample `json:"samples"`
}

func numeric(t FieldType) bool { return t == Number || t == Percent || t == Currency }
func plain(t FieldType) bool {
	switch t {
	case Text, LongText, Number, Percent, Currency, Date, Checkbox, SingleSelect, MultiSelect, Phone, Email, URL:
		return true
	}
	return false
}

func convAllowed(from, to FieldType) bool {
	switch {
	case to == Text || to == LongText:
		return plain(from)
	case numeric(from) && numeric(to):
		return true
	case from == SingleSelect && to == MultiSelect:
		return true
	case from == Text && (to == SingleSelect || to == MultiSelect || to == Phone || to == Email || to == URL):
		return true
	}
	return false
}

var palette = []string{"gray", "blue", "green", "amber", "red", "purple"}

// convertField previews (confirm false) or applies a type change. It returns the preview when nothing was applied.
func convertField(c *command.Ctx, f Field, tbl Table, to FieldType, cfgRaw json.RawMessage, confirm bool) (*convPreview, error) {
	if !convAllowed(f.Type, to) {
		return nil, command.ErrInvalid(refusal)
	}
	var isPrimary bool
	if err := c.Tx.QueryRow(c.Context, `select primary_field_id=$2::uuid from tables.tables where id=$1::uuid`, tbl.ID, f.ID).Scan(&isPrimary); err != nil {
		return nil, command.DB(err)
	}
	if isPrimary && !primaryOK(to) {
		return nil, command.ErrInvalid("The primary field must stay text, number, phone, email or autonumber")
	}
	lock := ""
	if confirm {
		lock = " for update"
	}
	rows, err := c.Tx.Query(c.Context, `select id::text, seq, data->$3 from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and data ? $3 order by seq`+lock,
		c.WorkspaceID(), tbl.ID, f.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	type row struct {
		id  string
		seq int64
		old any
	}
	var data []row
	for rows.Next() {
		var r row
		var b []byte
		if err := rows.Scan(&r.id, &r.seq, &b); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		_ = json.Unmarshal(b, &r.old)
		data = append(data, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}

	cfg, err := newConfig(c, tbl, f, to, cfgRaw)
	if err != nil {
		return nil, err
	}
	nf := Field{ID: f.ID, Name: f.Name, Type: to, Config: cfg}
	if f.Type == Text && (to == SingleSelect || to == MultiSelect) {
		opts := cfgOf[SelectConfig](nf).Options
		have := map[string]bool{}
		for _, o := range opts {
			have[strings.ToLower(o.Name)] = true
		}
		for _, r := range data {
			s, _ := r.old.(string)
			parts := []string{s}
			if to == MultiSelect {
				parts = strings.Split(s, ",")
			}
			for _, p := range parts {
				if p = strings.TrimSpace(p); p != "" && !have[strings.ToLower(p)] {
					have[strings.ToLower(p)] = true
					opts = append(opts, Option{Name: p, Color: palette[len(opts)%len(palette)]})
				}
			}
		}
		if len(opts) > optionLimit {
			return nil, command.ErrInvalid("This field has more than " + itoa(optionLimit) + " different values, too many to become options")
		}
		if opts, err = normalizeOptions(opts); err != nil {
			return nil, err
		}
		cfg, _ = json.Marshal(SelectConfig{Options: opts})
		nf.Config = cfg
	}

	prev := &convPreview{Samples: []convSample{}}
	ids, vals := []string{}, []string{}
	for _, r := range data {
		nv, err := convertValue(f, nf, r.old)
		switch {
		case err != nil || nv == nil:
			prev.Cleared++
		case reflect.DeepEqual(jsonRound(nv), r.old):
			prev.Kept++
		default:
			prev.Changed++
			if len(prev.Samples) < 5 {
				prev.Samples = append(prev.Samples, convSample{Seq: r.seq, From: r.old, To: nv})
			}
			b, _ := json.Marshal(nv)
			ids, vals = append(ids, r.id), append(vals, string(b))
		}
	}
	if !confirm {
		return prev, nil
	}
	if prev.Cleared > 0 {
		return nil, command.ErrConflict(fmt.Sprintf("%d values cannot become %s. Fix them first, or create a new field and copy the values", prev.Cleared, strings.ReplaceAll(string(to), "_", " ")))
	}
	for i := 0; i < len(ids); i += 1000 {
		j := min(i+1000, len(ids))
		if _, err := c.Tx.Exec(c.Context, `update tables.records r set data = jsonb_set(r.data, array[$3::text], v.val)
			from (select unnest($1::text[])::uuid id, unnest($2::text[])::jsonb val) v
			where r.workspace_id=$4::uuid and r.table_id=$5::uuid and r.id=v.id`, ids[i:j], vals[i:j], f.ID, c.WorkspaceID(), tbl.ID); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Tx.Exec(c.Context, `update tables.fields set type=$2, config=$3 where id=$1::uuid`, f.ID, to, cfg); err != nil {
		return nil, command.DB(err)
	}
	return nil, nil
}

func jsonRound(v any) any {
	b, _ := json.Marshal(v)
	var o any
	_ = json.Unmarshal(b, &o)
	return o
}

// newConfig is the config of the new type: what the caller sent, else the old one where it still fits, else the default.
func newConfig(c *command.Ctx, tbl Table, f Field, to FieldType, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		switch {
		case f.Type == SingleSelect && to == MultiSelect:
			raw = f.Config
		case numeric(f.Type) && numeric(to):
			d := cfgOf[NumberConfig](f).Decimals
			if to == Currency {
				d = 2
			} else if f.Type == Currency {
				d = 0
			}
			raw = json.RawMessage(`{"decimals":` + strconv.Itoa(d) + `}`)
		}
	}
	return normalizeConfig(c, tbl, to, raw, nil)
}

func convertValue(from, to Field, v any) (any, error) {
	switch {
	case to.Type == Text || to.Type == LongText:
		s := display(from, v)
		max := maxText
		if to.Type == LongText {
			max = maxLongText
		}
		if utf8.RuneCountInString(s) > max {
			return nil, fmt.Errorf("too long")
		}
		return s, nil
	case numeric(from.Type) && numeric(to.Type):
		n, _ := v.(float64)
		switch {
		case from.Type == Currency && to.Type != Currency:
			return n / 100, nil
		case from.Type != Currency && to.Type == Currency:
			return int64(math.Round(n * 100)), nil
		case to.Type == Currency:
			return int64(n), nil
		}
		return n, nil
	case from.Type == SingleSelect && to.Type == MultiSelect:
		return []string{fmt.Sprint(v)}, nil
	case from.Type == Text && (to.Type == SingleSelect || to.Type == MultiSelect):
		s, _ := v.(string)
		if to.Type == SingleSelect {
			return Validate(to, strings.TrimSpace(s))
		}
		parts := []any{}
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		return Validate(to, parts)
	}
	return Validate(to, v) // text -> phone, email, url
}

// display is the text a person sees in a cell. It is what "anything -> text" stores (not round-trippable for every type).
func display(f Field, v any) string {
	switch f.Type {
	case Number, Percent:
		n, _ := v.(float64)
		s := strconv.FormatFloat(n, 'f', -1, 64)
		if f.Type == Percent {
			s += "%"
		}
		return s
	case Currency:
		n, _ := v.(float64)
		return rupees(int64(n), cfgOf[NumberConfig](f).Decimals)
	case Date:
		s, _ := v.(string)
		if len(s) == 10 {
			return s[8:] + "-" + s[5:7] + "-" + s[:4]
		}
		return s
	case Checkbox:
		return "Yes"
	case SingleSelect, MultiSelect:
		names := map[string]string{}
		for _, o := range cfgOf[SelectConfig](f).Options {
			names[o.ID] = o.Name
		}
		var ids []string
		if l, ok := v.([]any); ok {
			for _, e := range l {
				ids = append(ids, fmt.Sprint(e))
			}
		} else {
			ids = []string{fmt.Sprint(v)}
		}
		for i, id := range ids {
			if n, ok := names[id]; ok {
				ids[i] = n
			}
		}
		return strings.Join(ids, ", ")
	}
	return fmt.Sprint(v)
}

// rupees formats paise the Indian way: 11800050 -> "₹1,18,000.50". With decimals 0 a fractional amount keeps its paise.
func rupees(paise int64, decimals int) string {
	sign := ""
	if paise < 0 {
		sign, paise = "-", -paise
	}
	whole, frac := paise/100, paise%100
	s := strconv.FormatInt(whole, 10)
	if len(s) > 3 {
		head, tail := s[:len(s)-3], s[len(s)-3:]
		var parts []string
		for len(head) > 2 {
			parts = append([]string{head[len(head)-2:]}, parts...)
			head = head[:len(head)-2]
		}
		s = strings.Join(append([]string{head}, append(parts, tail)...), ",")
	}
	if decimals != 0 || frac != 0 {
		s += fmt.Sprintf(".%02d", frac)
	}
	return sign + "₹" + s
}
