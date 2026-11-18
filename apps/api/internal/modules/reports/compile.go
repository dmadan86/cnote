package reports

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cnote/api/internal/command"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}$`)

var likeEsc = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func bad(f string, a ...any) error { return command.ErrInvalid(fmt.Sprintf(f, a...)) }

// Compile turns a Spec into parameterised SQL. Identifiers come only from the registry; every value is a bind.
// limit: 0 means spec.Limit (default MaxRows); capped at MaxRows unless the caller passes MaxCSVRows. Row and group
// queries fetch limit+1 rows (the extra row means truncated). Totals cells align with Columns (null where n/a).
// A spec with measures but no group_by is an aggregate (one row of measures) used by the "number" view.
func Compile(spec Spec, env Env, limit int) (Query, error) {
	d := datasetByKey[spec.Dataset]
	if d == nil {
		return Query{}, bad("unknown dataset")
	}
	if !d.allowed(env) {
		return Query{}, command.ErrForbidden("you do not have access to this report subject")
	}
	if len(spec.Columns) > MaxColumns || len(spec.Filters) > MaxFilters || len(spec.GroupBy) > MaxGroups || len(spec.Measures) > MaxMeasures {
		return Query{}, bad("too many columns, filters, groups or measures")
	}
	if len(spec.Sort) > 3 {
		return Query{}, bad("at most 3 sort keys")
	}
	switch spec.Viz {
	case "", "table", "bar", "number":
	default:
		return Query{}, bad("unknown view")
	}
	if limit < 0 || limit > MaxCSVRows || spec.Limit < 0 {
		return Query{}, bad("invalid row limit")
	}
	if limit == 0 {
		limit = spec.Limit
	}
	if limit == 0 || (limit > MaxRows && limit != MaxCSVRows) {
		limit = MaxRows
	}

	look := func(key string) (*field, error) {
		f := d.byKey[key]
		if f == nil {
			return nil, bad("unknown field %q", key)
		}
		if !f.visible(env) {
			return nil, command.ErrForbidden(fmt.Sprintf("you do not have access to %q", f.label))
		}
		return f, nil
	}

	g := &gen{}
	where := []string{"(" + d.scope(g, env) + ")"}
	q := Query{Limit: limit}

	for _, fl := range spec.Filters {
		f, err := look(fl.Field)
		if err != nil {
			return q, err
		}
		w, err := filterSQL(g, f, fl, env)
		if err != nil {
			return q, err
		}
		where = append(where, w)
	}

	if p := spec.Period; p != nil {
		key := p.Field
		if key == "" {
			key = d.defDate
		}
		f, err := look(key)
		if err != nil {
			return q, err
		}
		if f.typ != TDate {
			return q, bad("the period must use a date field")
		}
		from, to, label, err := ResolvePeriod(*p, env.FYStartMonth, env.Today)
		if err != nil {
			return q, err
		}
		q.From, q.To, q.PeriodLbl = from.Format(dateLayout), to.Format(dateLayout), label
		where = append(where, fmt.Sprintf("%s >= %s and %s <= %s", f.sql, g.push(q.From, "date"), f.sql, g.push(q.To, "date")))
	}

	fromW := " from " + d.from + " where " + strings.Join(where, " and ")
	lim := " limit " + strconv.Itoa(limit+1)

	if len(spec.GroupBy) == 0 && len(spec.Measures) == 0 {
		return rowMode(q, d, spec, look, fromW, lim, g)
	}
	return groupMode(q, d, spec, env.FYStartMonth, look, fromW, lim, g)
}

func dir(s string) (string, error) {
	switch strings.ToLower(s) {
	case "", "asc":
		return "asc", nil
	case "desc":
		return "desc", nil
	}
	return "", bad("sort direction must be asc or desc")
}

// out is the select expression of a field: dates as YYYY-MM-DD text, money as bigint, number as float8,
// person/contact as {id,label} (null when empty).
func out(f *field) string {
	switch f.typ {
	case TDate:
		return f.sql + "::text"
	case TMoney:
		return f.sql + "::bigint"
	case TNumber:
		return f.sql + "::float8"
	case TPerson, TContact:
		return fmt.Sprintf("(case when %s is null then null else jsonb_build_object('id', %s::text, 'label', coalesce(%s, '')) end)", f.sql, f.sql, f.labelSQL)
	}
	return f.sql
}

// orderExpr sorts people and contacts by name.
func orderExpr(f *field) string {
	if f.typ == TPerson || f.typ == TContact {
		return f.labelSQL
	}
	return f.sql
}

func rowMode(q Query, d *dataset, spec Spec, look func(string) (*field, error), fromW, lim string, g *gen) (Query, error) {
	cols := spec.Columns
	if len(cols) == 0 {
		cols = d.defCols
	}
	var sel, tot []string
	var summed bool
	for _, k := range cols {
		f, err := look(k)
		if err != nil {
			return q, err
		}
		sel = append(sel, out(f))
		q.Columns = append(q.Columns, Column{Key: f.key, Label: f.label, Type: f.typ})
		switch f.typ {
		case TMoney:
			tot, summed = append(tot, "coalesce(sum("+f.sql+"), 0)::bigint"), true
		case TNumber:
			tot, summed = append(tot, "coalesce(sum("+f.sql+"), 0)::float8"), true
		default:
			tot = append(tot, "null")
		}
	}
	var order []string
	for _, s := range spec.Sort {
		f, err := look(s.Key)
		if err != nil {
			return q, err
		}
		dr, err := dir(s.Dir)
		if err != nil {
			return q, err
		}
		order = append(order, orderExpr(f)+" "+dr+" nulls last")
	}
	if len(order) == 0 {
		df, err := look(d.defDate)
		if err != nil {
			return q, err
		}
		order = append(order, df.sql+" desc nulls last")
	}
	order = append(order, d.pk)
	q.SQL = "select " + strings.Join(sel, ", ") + fromW + " order by " + strings.Join(order, ", ") + lim
	if summed {
		q.TotalsSQL = "select " + strings.Join(tot, ", ") + fromW
	}
	q.CountSQL = "select count(*)" + fromW
	q.Args = g.args
	return q, nil
}

func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) > 1 && unicode.IsUpper(r[0]) && unicode.IsLower(r[1]) {
		r[0] = unicode.ToLower(r[0])
	}
	return string(r)
}

func groupMode(q Query, d *dataset, spec Spec, fy int, look func(string) (*field, error), fromW, lim string, g *gen) (Query, error) {
	var sel, tot, groupBy []string
	gSel := ""
	var gField *field
	if len(spec.GroupBy) == 1 {
		gr := spec.GroupBy[0]
		f, err := look(gr.Field)
		if err != nil {
			return q, err
		}
		if !f.group {
			return q, bad("you cannot group by %q", f.label)
		}
		gField = f
		lbl, typ := f.label, f.typ
		switch {
		case f.typ == TDate:
			b, word, err := bucketSQL(f.sql, gr.Bucket, fy)
			if err != nil {
				return q, err
			}
			groupBy, gSel = []string{b}, b+"::text"
			if word != "" {
				lbl += " by " + word
			}
		case gr.Bucket != "":
			return q, bad("only dates can be grouped by day, week, month, quarter or year")
		case f.typ == TPerson || f.typ == TContact:
			groupBy, gSel = []string{f.sql, f.labelSQL}, out(f)
		default:
			groupBy, gSel = []string{f.sql}, f.sql
		}
		sel = append(sel, gSel+" as g0")
		tot = append(tot, "null")
		q.Columns = append(q.Columns, Column{Key: "g0", Label: lbl, Type: typ})
	}
	ms := spec.Measures
	if len(ms) == 0 {
		ms = []Measure{{Agg: "count"}}
	}
	for i, m := range ms {
		e, c, err := measure(d, m, look)
		if err != nil {
			return q, err
		}
		c.Key = "m" + strconv.Itoa(i)
		sel = append(sel, e+" as "+c.Key)
		tot = append(tot, e)
		q.Columns = append(q.Columns, c)
	}
	q.CountSQL = "select count(*)" + fromW
	q.Args = g.args
	if gField == nil { // aggregate: one row of measures
		q.SQL = "select " + strings.Join(sel, ", ") + fromW
		q.Limit = 1
		return q, nil
	}
	var order []string
	for _, s := range spec.Sort {
		dr, err := dir(s.Dir)
		if err != nil {
			return q, err
		}
		switch {
		case s.Key == "g0":
			if gField.typ == TPerson || gField.typ == TContact {
				order = append(order, gField.labelSQL+" "+dr+" nulls last")
			} else {
				order = append(order, "g0 "+dr+" nulls last")
			}
		case len(s.Key) == 2 && s.Key[0] == 'm' && s.Key[1] >= '0' && int(s.Key[1]-'0') < len(ms):
			order = append(order, s.Key+" "+dr+" nulls last")
		default:
			return q, bad("in a summary, sort by the group or by one of the totals")
		}
	}
	if len(order) == 0 {
		order = append(order, "m0 desc nulls last")
	}
	order = append(order, "g0 asc nulls last")
	gb := " group by " + strings.Join(groupBy, ", ")
	q.SQL = "select " + strings.Join(sel, ", ") + fromW + gb + " order by " + strings.Join(order, ", ") + lim
	q.TotalsSQL = "select " + strings.Join(tot, ", ") + fromW
	q.CountSQL = "select count(*) from (select 1" + fromW + gb + ") grouped"
	return q, nil
}

// measure builds one aggregate expression and its output column.
func measure(d *dataset, m Measure, look func(string) (*field, error)) (string, Column, error) {
	if m.Agg == "count" {
		return "count(*)", Column{Label: "Number of " + d.noun, Type: TNumber}, nil
	}
	if !slices.Contains([]string{"sum", "avg", "min", "max"}, m.Agg) {
		return "", Column{}, bad("unknown way to add up: %q", m.Agg)
	}
	f, err := look(m.Field)
	if err != nil {
		return "", Column{}, err
	}
	if !slices.Contains(f.aggs(), m.Agg) {
		return "", Column{}, bad("you cannot %s %q", m.Agg, f.label)
	}
	name := lowerFirst(f.label)
	var e, label string
	typ := f.typ
	switch m.Agg {
	case "sum":
		label = "Total " + name
		e = "coalesce(sum(" + f.sql + "), 0)"
	case "avg":
		label = "Average " + name
		e = "avg(" + f.sql + ")"
		if f.typ == TMoney {
			e = "round(" + e + ")"
		}
	case "min":
		label = "Lowest " + name
		if f.typ == TDate {
			label = "Earliest " + name
		}
		e = "min(" + f.sql + ")"
	case "max":
		label = "Highest " + name
		if f.typ == TDate {
			label = "Latest " + name
		}
		e = "max(" + f.sql + ")"
	}
	switch f.typ {
	case TMoney:
		e += "::bigint"
	case TNumber:
		e += "::float8"
	case TDate:
		e += "::text"
	}
	return e, Column{Label: label, Type: typ}, nil
}

// bucketSQL buckets a date expression in IST. quarter and fy align to the FY start month (a validated int, inlined).
func bucketSQL(col, bucket string, fy int) (sql, word string, err error) {
	if fy < 1 || fy > 12 {
		fy = 4
	}
	// months since the start of the current FY / FY quarter, from the month number
	since := fmt.Sprintf("((extract(month from %s)::int - %d + 12) %% 12)", col, fy)
	trunc := "date_trunc('month', " + col + "::timestamp)"
	switch bucket {
	case "", "day":
		return col, "", nil
	case "week":
		return "date_trunc('week', " + col + "::timestamp)::date", "week", nil
	case "month":
		return trunc + "::date", "month", nil
	case "quarter":
		return fmt.Sprintf("(%s - make_interval(months => %s %% 3))::date", trunc, since), "quarter", nil
	case "fy":
		return fmt.Sprintf("(%s - make_interval(months => %s))::date", trunc, since), "financial year", nil
	}
	return "", "", bad("a date can be grouped by day, week, month, quarter or financial year")
}

// ---------- filters ----------

func strList(v any) ([]string, bool) {
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

func numOf(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func numList(v any, n int) ([]float64, bool) {
	var raw []any
	switch x := v.(type) {
	case []any:
		raw = x
	case []float64:
		for _, f := range x {
			raw = append(raw, f)
		}
	default:
		return nil, false
	}
	if len(raw) != n {
		return nil, false
	}
	out := make([]float64, n)
	for i, e := range raw {
		f, ok := numOf(e)
		if !ok {
			return nil, false
		}
		out[i] = f
	}
	return out, true
}

func dateOf(v any, today time.Time) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", bad("dates must look like 2026-04-01")
	}
	if s == "today" {
		return today.Format(dateLayout), nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return "", bad("dates must look like 2026-04-01")
	}
	return t.Format(dateLayout), nil
}

var cmpOps = map[string]string{"eq": "=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}

func filterSQL(g *gen, f *field, fl Filter, env Env) (string, error) {
	if !slices.Contains(OpsByType[f.typ], fl.Op) {
		return "", bad("%q cannot be filtered that way", f.label)
	}
	col := f.sql
	v := fl.Value
	switch fl.Op {
	case "empty", "not_empty":
		neg := ""
		if fl.Op == "not_empty" {
			neg = "not "
		}
		if f.typ == TText {
			return fmt.Sprintf("%s(%s is null or %s = '')", neg, col, col), nil
		}
		return fmt.Sprintf("%s(%s is null)", neg, col), nil
	}
	switch f.typ {
	case TText:
		s, ok := v.(string)
		if !ok || len(s) > 200 {
			return "", bad("%q needs a text value of at most 200 characters", f.label)
		}
		switch fl.Op {
		case "eq":
			return col + " = " + g.push(s, "text"), nil
		case "neq":
			return "(" + col + " is null or " + col + " <> " + g.push(s, "text") + ")", nil
		default: // contains
			return col + " ilike " + g.push("%"+likeEsc.Replace(s)+"%", "text"), nil
		}
	case TEnum, TPerson, TContact:
		if fl.Op == "me" {
			if env.UserID == nil {
				return "", bad("there is no signed-in person to match")
			}
			return col + " = " + g.push(*env.UserID, "uuid"), nil
		}
		vals, ok := strList(v)
		if !ok || len(vals) == 0 || len(vals) > 200 {
			return "", bad("%q needs between 1 and 200 values", f.label)
		}
		for _, s := range vals {
			switch {
			case f.typ != TEnum && !uuidRe.MatchString(s):
				return "", bad("%q values must be ids", f.label)
			case f.typ == TEnum && len(s) > 200:
				return "", bad("%q has a value that is too long", f.label)
			case f.typ == TEnum && len(f.values) > 0 && !slices.ContainsFunc(f.values, func(o Option) bool { return o.Value == s }):
				return "", bad("%q has no option %q", f.label, s)
			}
		}
		var ref string
		if f.typ == TEnum {
			ref = g.push(vals, "text[]")
		} else {
			ref = g.push(vals, "text[]") + "::uuid[]"
		}
		if fl.Op == "in" {
			return col + " = any(" + ref + ")", nil
		}
		return "(" + col + " is null or not (" + col + " = any(" + ref + ")))", nil
	case TDate:
		switch fl.Op {
		case "between":
			r, ok := strList(v)
			if !ok || len(r) != 2 {
				return "", bad("between needs a start and an end date")
			}
			a, err := dateOf(r[0], env.Today)
			if err != nil {
				return "", err
			}
			b, err := dateOf(r[1], env.Today)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s between %s and %s", col, g.push(a, "date"), g.push(b, "date")), nil
		default: // before, after
			s, err := dateOf(v, env.Today)
			if err != nil {
				return "", err
			}
			op := "<"
			if fl.Op == "after" {
				op = ">"
			}
			return fmt.Sprintf("%s %s %s", col, op, g.push(s, "date")), nil
		}
	case TMoney, TNumber:
		bind := func(x float64) (string, error) {
			if f.typ == TNumber {
				return g.push(x, "numeric"), nil
			}
			if x != math.Trunc(x) || math.Abs(x) > 9e15 {
				return "", bad("%q values are whole amounts in paise", f.label)
			}
			return g.push(int64(x), "bigint"), nil
		}
		if fl.Op == "between" {
			r, ok := numList(v, 2)
			if !ok {
				return "", bad("between needs two numbers")
			}
			a, err := bind(r[0])
			if err != nil {
				return "", err
			}
			b, err := bind(r[1])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s between %s and %s", col, a, b), nil
		}
		x, ok := numOf(v)
		if !ok {
			return "", bad("%q needs a number", f.label)
		}
		a, err := bind(x)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s %s", col, cmpOps[fl.Op], a), nil
	case TBool:
		b, ok := v.(bool)
		if !ok {
			return "", bad("%q needs yes or no", f.label)
		}
		return col + " = " + g.push(b, "boolean"), nil
	}
	return "", bad("unsupported filter")
}
