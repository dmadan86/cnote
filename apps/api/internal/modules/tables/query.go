package tables

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/command"
)

// The record.list query compiler (17.2). Field ids are checked against the table's fields and bound as parameters;
// every value is a parameter; the only text in the SQL is fixed fragments chosen from the field's type. Sorting is
// keyset on (sort values, id) with NULLS LAST in both directions; the cursor values travel as text and are cast back.

func init() {
	handlers["record.list"] = recordList
	handlers["record.search"] = recordSearch
}

type qb struct {
	args []any
	keys map[string]string
}

func (q *qb) p(v any) string { q.args = append(q.args, v); return "$" + strconv.Itoa(len(q.args)) }
func (q *qb) key(id string) string {
	if q.keys == nil {
		q.keys = map[string]string{}
	}
	if k, ok := q.keys[id]; ok {
		return k
	}
	k := q.p(id) + "::text"
	q.keys[id] = k
	return k
}

// col is a field as SQL: val sorts and compares (cast says how a cursor value is cast back), fval is the text/number
// form filters compare, kind picks the operator set.
type col struct {
	f          Field
	kind       string // text num date sel multi user uuid bool link attach
	val, fval  string
	cast       string
	sortable   bool
	groupValue string // SQL for the group value, shaped like data[field]
}

func (q *qb) col(c *command.Ctx, tc *openedTable, f Field) col {
	k := ""
	switch f.Type {
	case Autonumber, CreatedTime, ModifiedTime, CreatedBy, ModifiedBy, Link: // not read from data: an unused argument makes pgx refuse the query
	default:
		k = q.key(f.ID)
	}
	d := "r.data -> " + k
	x := col{f: f}
	switch f.Type {
	case Text, LongText, Phone, Email, URL:
		x.kind, x.val, x.cast, x.sortable = "text", "(r.data ->> "+k+")", "text", true
	case SingleSelect:
		x.kind, x.val, x.cast, x.sortable = "sel", "(r.data ->> "+k+")", "text", true
	case Date:
		x.kind, x.val, x.cast, x.sortable = "date", "(r.data ->> "+k+")", "text", true
	case Number, Percent, Currency:
		x.kind, x.val, x.cast, x.sortable = "num", "(case when jsonb_typeof("+d+") = 'number' then (r.data ->> "+k+")::numeric end)", "numeric", true
	case Autonumber:
		x.kind, x.val, x.cast, x.sortable = "num", "(r.seq::numeric)", "numeric", true
	case Checkbox:
		x.kind, x.val, x.cast, x.sortable = "bool", "(case when "+d+" = 'true'::jsonb then 1 else 0 end)", "int", true
		x.groupValue = "(case when " + x.val + " = 1 then true end)"
	case MultiSelect, User:
		x.kind, x.val = "multi", "("+d+")"
	case CreatedTime, ModifiedTime:
		col := "r.created_at"
		if f.Type == ModifiedTime {
			col = "r.updated_at"
		}
		x.kind, x.val, x.cast, x.sortable = "date", col, "timestamptz", true
		x.fval = "to_char(" + col + " at time zone 'Asia/Kolkata', 'YYYY-MM-DD')"
	case CreatedBy, ModifiedBy:
		col := "r.created_by"
		if f.Type == ModifiedBy {
			col = "r.updated_by"
		}
		x.kind, x.val, x.cast, x.sortable = "uuid", col, "uuid", true
		x.fval = col + "::text"
	case Link:
		x.kind = "link"
	case Attachment:
		x.kind, x.val = "attach", "("+d+")"
	}
	if x.fval == "" {
		x.fval = x.val
	}
	if x.groupValue == "" {
		x.groupValue = "to_jsonb(" + x.val + ")"
	}
	return x
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func istDay(q *qb, offsetDays string) string {
	return "to_char(((now() at time zone 'Asia/Kolkata')::date + " + offsetDays + "), 'YYYY-MM-DD')"
}

func strList(v any, me *string) ([]string, error) {
	var in []string
	switch x := v.(type) {
	case string:
		in = []string{x}
	case []any:
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, command.ErrInvalid("values must be text")
			}
			in = append(in, s)
		}
	default:
		return nil, command.ErrInvalid("value must be a text or a list of text")
	}
	for i, s := range in {
		if s == "me" {
			if me == nil {
				return nil, command.ErrInvalid(`"me" needs a signed-in person`)
			}
			in[i] = *me
		}
	}
	return in, nil
}

func numArg(v any) (string, error) {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case string:
		if _, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return strings.TrimSpace(x), nil
		}
	}
	return "", command.ErrInvalid("value must be a number")
}

func (q *qb) rule(c *command.Ctx, tc *openedTable, r Rule) (string, error) {
	f, ok := tc.byID[r.Field]
	if !ok {
		return "", command.ErrInvalid("unknown field in filter")
	}
	x := q.col(c, tc, f)
	op, arg, _ := strings.Cut(r.Op, ":")
	if arg == "" && (op == "past_days" || op == "next_days") {
		if n, ok := r.Value.(float64); ok {
			arg = strconv.Itoa(int(n))
		}
	}
	bad := func() (string, error) {
		return "", command.ErrInvalid(fmt.Sprintf("%q cannot be filtered with %q", f.Name, r.Op))
	}
	// empty and not_empty exist for every type
	if op == "empty" || op == "not_empty" {
		var e string
		switch x.kind {
		case "link":
			s, err := resolveLink(c, tc, f.ID)
			if err != nil {
				return "", err
			}
			e = "not exists (select 1 from tables.links l join tables.records o on o.workspace_id=l.workspace_id and o.id=l." + s.other + " and o.deleted_at is null where l.workspace_id=r.workspace_id and l.field_id=" + q.p(s.fwd.ID) + "::uuid and l." + s.selfCol + "=r.id)"
		case "multi", "attach":
			e = "coalesce(jsonb_array_length(" + x.val + "), 0) = 0"
		case "bool":
			e = x.val + " = 0"
		default:
			e = x.val + " is null"
		}
		if op == "not_empty" {
			return "not (" + e + ")", nil
		}
		return "(" + e + ")", nil
	}
	switch x.kind {
	case "text":
		s, ok := r.Value.(string)
		if !ok {
			return "", command.ErrInvalid("value must be text")
		}
		switch op {
		case "contains":
			return "(" + x.val + " ilike " + q.p("%"+likeEscape(s)+"%") + `::text escape '\')`, nil
		case "is":
			return "(lower(" + x.val + ") = lower(" + q.p(s) + "::text))", nil
		case "is_not":
			return "(" + x.val + " is null or lower(" + x.val + ") <> lower(" + q.p(s) + "::text))", nil
		}
	case "num", "date":
		cmp := map[string]string{"=": "=", "!=": "<>", "<": "<", "<=": "<=", ">": ">", ">=": ">="}[op]
		if cmp != "" {
			var rhs string
			if x.kind == "num" {
				s, err := numArg(r.Value)
				if err != nil {
					return "", err
				}
				rhs = q.p(s) + "::text::numeric"
			} else {
				s, ok := r.Value.(string)
				if !ok || !isoDate.MatchString(s) {
					return "", command.ErrInvalid("value must be a date as YYYY-MM-DD")
				}
				rhs = q.p(s) + "::text"
			}
			if cmp == "<>" {
				return "(" + x.fval + " is null or " + x.fval + " <> " + rhs + ")", nil
			}
			return "(" + x.fval + " " + cmp + " " + rhs + ")", nil
		}
		if x.kind == "date" {
			switch op {
			case "today":
				return "(" + x.fval + " = " + istDay(q, "0") + ")", nil
			case "this_week":
				w := "date_trunc('week', now() at time zone 'Asia/Kolkata')"
				return "(" + x.fval + " >= to_char(" + w + ", 'YYYY-MM-DD') and " + x.fval + " < to_char(" + w + " + interval '7 days', 'YYYY-MM-DD'))", nil
			case "this_month":
				m := "date_trunc('month', now() at time zone 'Asia/Kolkata')"
				return "(" + x.fval + " >= to_char(" + m + ", 'YYYY-MM-DD') and " + x.fval + " < to_char(" + m + " + interval '1 month', 'YYYY-MM-DD'))", nil
			case "past_days", "next_days":
				n, err := strconv.Atoi(arg)
				if err != nil || n < 0 || n > 3650 {
					return "", command.ErrInvalid("past_days and next_days take a number of days from 0 to 3650")
				}
				np := q.p(n) + "::int"
				if op == "past_days" {
					return "(" + x.fval + " >= " + istDay(q, "(-"+np+")") + " and " + x.fval + " <= " + istDay(q, "0") + ")", nil
				}
				return "(" + x.fval + " >= " + istDay(q, "0") + " and " + x.fval + " <= " + istDay(q, np) + ")", nil
			}
		}
	case "sel", "uuid":
		if op == "any_of" || op == "none_of" {
			var me *string
			if x.kind == "uuid" {
				me = c.Principal.UserID
			}
			l, err := strList(r.Value, me)
			if err != nil {
				return "", err
			}
			e := x.fval + " = any(" + q.p(l) + "::text[])"
			if op == "none_of" {
				return "(" + x.fval + " is null or not (" + e + "))", nil
			}
			return "(" + e + ")", nil
		}
	case "multi":
		if op == "any_of" || op == "none_of" {
			var me *string
			if f.Type == User {
				me = c.Principal.UserID
			}
			l, err := strList(r.Value, me)
			if err != nil {
				return "", err
			}
			e := "coalesce(" + x.val + " ?| " + q.p(l) + "::text[], false)"
			if op == "none_of" {
				return "(not " + e + ")", nil
			}
			return "(" + e + ")", nil
		}
	case "bool":
		if op == "is" {
			b, ok := r.Value.(bool)
			if !ok {
				return "", command.ErrInvalid("value must be true or false")
			}
			if b {
				return "(" + x.val + " = 1)", nil
			}
			return "(" + x.val + " = 0)", nil
		}
	}
	return bad()
}

func (q *qb) filter(c *command.Ctx, tc *openedTable, fl *Filter) (string, error) {
	if fl == nil || len(fl.Rules) == 0 {
		return "", nil
	}
	if len(fl.Rules) > 10 {
		return "", command.ErrInvalid("a filter has at most 10 rules")
	}
	join := " and "
	switch fl.Match {
	case "or":
		join = " or "
	case "", "and":
	default:
		return "", command.ErrInvalid(`match must be "and" or "or"`)
	}
	parts := make([]string, len(fl.Rules))
	for i, r := range fl.Rules {
		s, err := q.rule(c, tc, r)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return "(" + strings.Join(parts, join) + ")", nil
}

type listIn struct {
	TableID string  `json:"table_id"`
	ViewID  string  `json:"view_id"`
	Filters *Filter `json:"filters"`
	Sorts   []Sort  `json:"sorts"`
	Group   *struct {
		Field string `json:"field"`
	} `json:"group"`
	Search string `json:"search"`
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

func recordList(c *command.Ctx, in json.RawMessage) (any, error) {
	var a listIn
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	tc, err := openTable(c, a.TableID, RoleRead)
	if err != nil {
		return nil, err
	}
	if a.ViewID != "" { // a view supplies what the request leaves out
		var raw json.RawMessage
		if err := c.Tx.QueryRow(c.Context, `select config from tables.views where workspace_id=$1::uuid and table_id=$2::uuid and id=$3::uuid`, c.WorkspaceID(), a.TableID, a.ViewID).Scan(&raw); err != nil {
			return nil, command.DB(err)
		}
		var vc listIn
		_ = json.Unmarshal(raw, &vc)
		// a view may still name a field that was deleted: those parts are ignored, not errors
		if a.Filters == nil && vc.Filters != nil {
			f := *vc.Filters
			f.Rules = nil
			for _, r := range vc.Filters.Rules {
				if _, ok := tc.byID[r.Field]; ok {
					f.Rules = append(f.Rules, r)
				}
			}
			a.Filters = &f
		}
		if a.Sorts == nil {
			for _, s := range vc.Sorts {
				if _, ok := tc.byID[s.Field]; ok {
					a.Sorts = append(a.Sorts, s)
				}
			}
		}
		if a.Group == nil || a.Group.Field == "" {
			if vc.Group != nil {
				if _, ok := tc.byID[vc.Group.Field]; ok {
					a.Group = vc.Group
				}
			}
		}
	}
	if a.Limit <= 0 {
		a.Limit = 100
	}
	if a.Limit > 200 {
		a.Limit = 200
	}
	if len(a.Sorts) > 3 {
		return nil, command.ErrInvalid("at most 3 sorts")
	}
	sorts := a.Sorts
	if a.Group != nil && a.Group.Field != "" {
		g := Sort{Field: a.Group.Field}
		if len(sorts) > 0 && sorts[0].Field == g.Field {
			g.Desc = sorts[0].Desc
			sorts = sorts[1:]
		}
		sorts = append([]Sort{g}, sorts...)
	}
	q := &qb{}
	q.p(c.WorkspaceID()) // $1
	q.p(tc.Table.ID)     // $2
	where := "r.workspace_id = $1::uuid and r.table_id = $2::uuid and r.deleted_at is null"
	fw, err := q.filter(c, tc, a.Filters)
	if err != nil {
		return nil, err
	}
	if fw != "" {
		where += " and " + fw
	}
	if s := strings.TrimSpace(a.Search); s != "" {
		var ors []string
		pat := q.p("%" + likeEscape(s) + "%")
		for _, f := range tc.Fields {
			switch f.Type {
			case Text, LongText, Phone, Email, URL:
				ors = append(ors, "r.data ->> "+q.key(f.ID)+" ilike "+pat+`::text escape '\'`)
			}
		}
		if len(ors) == 0 {
			ors = []string{"false"}
		}
		where += " and (" + strings.Join(ors, " or ") + ")"
	}
	nWhere := len(q.args) // the count runs before the sort keys are added: pgx refuses unused arguments
	nGroup := nWhere
	cols := make([]col, len(sorts))
	for i, s := range sorts {
		f, ok := tc.byID[s.Field]
		if !ok {
			return nil, command.ErrInvalid("unknown field in sort or group")
		}
		cols[i] = q.col(c, tc, f)
		if !cols[i].sortable {
			return nil, command.ErrInvalid(fmt.Sprintf("records cannot be sorted or grouped by %q", f.Name))
		}
		if i == 0 {
			nGroup = len(q.args)
		}
	}
	// total: the filter without the cursor
	var total int
	if err := c.Tx.QueryRow(c.Context, "select count(*) from tables.records r where "+where, q.args[:nWhere]...).Scan(&total); err != nil {
		return nil, command.DB(err)
	}
	var groups []map[string]any
	if a.Group != nil && a.Group.Field != "" {
		g := cols[0]
		dir := "asc"
		if sorts[0].Desc {
			dir = "desc"
		}
		rows, err := c.Tx.Query(c.Context, "select "+g.groupValue+", count(*) from tables.records r where "+where+" group by "+g.val+" order by "+g.val+" "+dir+" nulls last limit 500", q.args[:nGroup]...)
		if err != nil {
			return nil, command.DB(err)
		}
		groups = []map[string]any{}
		for rows.Next() {
			var v json.RawMessage
			var n int
			if err := rows.Scan(&v, &n); err != nil {
				rows.Close()
				return nil, command.DB(err)
			}
			if v == nil {
				v = json.RawMessage("null")
			}
			groups = append(groups, map[string]any{"value": v, "count": n})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, command.DB(err)
		}
	}
	if a.Cursor != "" {
		cur, err := DecodeCursor(a.Cursor)
		if err != nil || len(cur.Sort) != len(cols) {
			return nil, command.ErrInvalid("invalid cursor")
		}
		var levels []string
		for i := 0; i <= len(cols); i++ {
			if i < len(cols) && cur.Sort[i] == nil {
				continue // nothing sorts after NULL in this column
			}
			var parts []string
			for j := 0; j < i; j++ {
				if cur.Sort[j] == nil {
					parts = append(parts, cols[j].val+" is null")
				} else {
					parts = append(parts, cols[j].val+" is not distinct from "+q.p(fmt.Sprint(cur.Sort[j]))+"::text::"+cols[j].cast)
				}
			}
			if i == len(cols) {
				parts = append(parts, "r.id > "+q.p(cur.ID)+"::text::uuid")
			} else {
				op := ">"
				if sorts[i].Desc {
					op = "<"
				}
				parts = append(parts, "("+cols[i].val+" "+op+" "+q.p(fmt.Sprint(cur.Sort[i]))+"::text::"+cols[i].cast+" or "+cols[i].val+" is null)")
			}
			levels = append(levels, "("+strings.Join(parts, " and ")+")")
		}
		where += " and (" + strings.Join(levels, " or ") + ")"
	}
	sel, order := "", ""
	for i, x := range cols {
		sel += ", (" + x.val + ")::text"
		dir := ""
		if sorts[i].Desc {
			dir = " desc"
		}
		order += x.val + dir + " nulls last, "
	}
	rows, err := c.Tx.Query(c.Context, "select "+recCols+sel+" from tables.records r where "+where+" order by "+order+"r.id limit "+strconv.Itoa(a.Limit+1), q.args...)
	if err != nil {
		return nil, command.DB(err)
	}
	type sortVals = []*string
	recs := []recRow{}
	var last sortVals
	more := false
	for rows.Next() {
		sv := make(sortVals, len(cols))
		extra := make([]any, len(cols))
		for i := range sv {
			extra[i] = &sv[i]
		}
		r, err := scanRec(rows, extra...)
		if err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		if len(recs) == a.Limit { // the extra row only proves there is a next page
			more = true
			break
		}
		recs = append(recs, r)
		last = sv
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	var next *string
	if more {
		cur := Cursor{ID: recs[len(recs)-1].ID, Sort: make([]any, len(cols))}
		for i, v := range last {
			if v != nil {
				cur.Sort[i] = *v
			}
		}
		s := cur.Encode()
		next = &s
	}
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	chips, err := linkChips(c, tc, ids)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"records": recs, "links": chips, "next_cursor": next, "total": total}
	if groups != nil {
		out["groups"] = groups
	}
	return out, nil
}

func recordSearch(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		TableID string `json:"table_id"`
		Q       string `json:"q"`
		Limit   int    `json:"limit"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	tc, err := openTable(c, a.TableID, RoleRead)
	if err != nil {
		return nil, err
	}
	if a.Limit <= 0 || a.Limit > 20 {
		a.Limit = 20
	}
	var pf *Field
	var prev []string // up to 4 other plain fields for the preview
	var pid string
	_ = c.Tx.QueryRow(c.Context, `select coalesce(primary_field_id::text,'') from tables.tables where workspace_id=$1::uuid and id=$2::uuid`, c.WorkspaceID(), tc.Table.ID).Scan(&pid)
	for i := range tc.Fields {
		f := tc.Fields[i]
		switch {
		case f.ID == pid:
			pf = &tc.Fields[i]
		case len(prev) < 4 && !f.Type.Computed() && f.Type != Attachment && f.Type != Checkbox && f.Type != MultiSelect && f.Type != User:
			prev = append(prev, f.ID)
		}
	}
	q := &qb{}
	q.p(c.WorkspaceID())
	q.p(tc.Table.ID)
	title := "''"
	cond := "true"
	if pf != nil {
		if pf.Type == Autonumber {
			title = "(" + q.p(linkAutoPrefix(*pf)) + "::text || r.seq::text)"
		} else {
			title = "coalesce(r.data ->> " + q.key(pf.ID) + ", '')"
		}
		if s := strings.TrimSpace(a.Q); s != "" {
			cond = title + " ilike " + q.p("%"+likeEscape(s)+"%") + `::text escape '\'`
		}
	}
	pv := "array[]::text[]"
	if len(prev) > 0 {
		var xs []string
		for _, id := range prev {
			xs = append(xs, "r.data ->> "+q.key(id))
		}
		pv = "array_remove(array[" + strings.Join(xs, ",") + "], null)"
	}
	rows, err := c.Tx.Query(c.Context, "select r.id::text, "+title+", "+pv+" from tables.records r where r.workspace_id=$1::uuid and r.table_id=$2::uuid and r.deleted_at is null and "+cond+" order by r.seq limit "+strconv.Itoa(a.Limit), q.args...)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	type hit struct {
		ID      string   `json:"id"`
		Title   string   `json:"title"`
		Preview []string `json:"preview"`
	}
	out := []hit{}
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.ID, &h.Title, &h.Preview); err != nil {
			return nil, command.DB(err)
		}
		if len(h.Preview) > 2 {
			h.Preview = h.Preview[:2]
		}
		out = append(out, h)
	}
	return out, command.DB(rows.Err())
}

func linkAutoPrefix(f Field) string {
	var cfg AutonumberConfig
	_ = json.Unmarshal(f.Config, &cfg)
	return cfg.Prefix
}
