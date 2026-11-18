package reports

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
)

const evil = "'; drop table x;--"

func testEnv() Env {
	uid := "11111111-1111-1111-1111-111111111111"
	return Env{Spaces: []string{"22222222-2222-2222-2222-222222222222"}, UserID: &uid, Role: 1, Finance: true,
		ModuleOn: func(string) bool { return true }, FYStartMonth: 4, Today: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
}

func code(err error) command.Code {
	var e *command.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func allSQL(q Query) string { return q.SQL + "\n" + q.TotalsSQL + "\n" + q.CountSQL }

func TestRowModeDefaults(t *testing.T) {
	q, err := Compile(Spec{Dataset: "invoices"}, testEnv(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Columns) != 6 || q.Columns[1].Label != "Customer" || q.Limit != MaxRows {
		t.Fatalf("columns %+v limit %d", q.Columns, q.Limit)
	}
	for _, want := range []string{"core.note_access", "order by i.invoice_date desc nulls last, i.note_id", "limit 2001"} {
		if !strings.Contains(q.SQL, want) {
			t.Errorf("missing %q in %s", want, q.SQL)
		}
	}
	if q.TotalsSQL == "" || !strings.Contains(q.TotalsSQL, "sum(i.total_minor)") || strings.Contains(q.TotalsSQL, "limit") {
		t.Errorf("totals %s", q.TotalsSQL)
	}
	if !strings.HasPrefix(q.CountSQL, "select count(*) from billing.invoices") {
		t.Errorf("count %s", q.CountSQL)
	}
	// scope binds: spaces, user, admin
	if len(q.Args) != 3 {
		t.Errorf("args %v", q.Args)
	}
}

func TestNoUserStringInSQL(t *testing.T) {
	cases := []Spec{
		{Dataset: "invoices", Filters: []Filter{{Field: "number", Op: "eq", Value: evil}}},
		{Dataset: "invoices", Filters: []Filter{{Field: "number", Op: "contains", Value: evil}}},
		{Dataset: "invoices", Filters: []Filter{{Field: "status", Op: "in", Value: []any{evil}}}},
		{Dataset: "invoices", Filters: []Filter{{Field: "contact", Op: "in", Value: []any{evil}}}},
		{Dataset: "invoices", Filters: []Filter{{Field: "invoice_date", Op: "before", Value: evil}}},
		{Dataset: "invoices", Filters: []Filter{{Field: evil, Op: "eq", Value: "x"}}},
		{Dataset: "invoices", Filters: []Filter{{Field: "number", Op: evil, Value: "x"}}},
		{Dataset: "invoices", Columns: []string{evil}},
		{Dataset: "invoices", GroupBy: []Group{{Field: evil}}},
		{Dataset: "invoices", GroupBy: []Group{{Field: "invoice_date", Bucket: evil}}},
		{Dataset: "invoices", GroupBy: []Group{{Field: "status"}}, Measures: []Measure{{Field: evil, Agg: "sum"}}},
		{Dataset: "invoices", GroupBy: []Group{{Field: "status"}}, Measures: []Measure{{Field: "total", Agg: evil}}},
		{Dataset: "invoices", Sort: []Sort{{Key: evil}}},
		{Dataset: "invoices", Sort: []Sort{{Key: "total", Dir: evil}}},
		{Dataset: "invoices", Period: &Period{Field: evil, Preset: "this_fy"}},
		{Dataset: "invoices", Period: &Period{Preset: "custom", From: evil, To: evil}},
		{Dataset: evil},
		{Dataset: "invoices", Viz: evil},
	}
	for i, s := range cases {
		q, err := Compile(s, testEnv(), 0)
		if err == nil {
			// accepted: the string must be bound, not inlined
			if strings.Contains(allSQL(q), "drop table") || strings.Contains(allSQL(q), evil) {
				t.Errorf("case %d: user string reached SQL: %s", i, q.SQL)
			}
			found := false
			for _, a := range q.Args {
				if strings.Contains(fmtAny(a), "drop table") {
					found = true
				}
			}
			if !found {
				t.Errorf("case %d: accepted but value not bound", i)
			}
			continue
		}
		if c := code(err); c != command.Invalid && c != command.Forbidden {
			t.Errorf("case %d: wrong error %v", i, err)
		}
	}
}

func fmtAny(a any) string {
	switch x := a.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, ",")
	}
	return ""
}

func TestContainsEscapesWildcards(t *testing.T) {
	q, err := Compile(Spec{Dataset: "invoices", Filters: []Filter{{Field: "number", Op: "contains", Value: "50%_a"}}}, testEnv(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if q.Args[len(q.Args)-1] != `%50\%\_a%` {
		t.Errorf("args %v", q.Args)
	}
}

func TestForbiddenEverywhere(t *testing.T) {
	env := testEnv()
	env.Finance = false
	env.ModuleOn = func(k string) bool { return k != "crm" }
	for _, s := range []Spec{
		{Dataset: "invoices"}, {Dataset: "payments"}, {Dataset: "purchase_bills"}, // finance datasets
		{Dataset: "deals"}, {Dataset: "tickets"}, // module off
	} {
		if _, err := Compile(s, env, 0); code(err) != command.Forbidden {
			t.Errorf("%s: want forbidden, got %v", s.Dataset, err)
		}
	}
	env.Role = 0
	if _, err := Compile(Spec{Dataset: "tasks"}, env, 0); code(err) != command.Forbidden {
		t.Errorf("guest must be forbidden: %v", err)
	}
	if got := Catalog(env); len(got) != 0 {
		t.Errorf("guest catalog %v", got)
	}
}

// A per-field finance flag must block every position. Registers a throwaway field on the expenses dataset.
func TestFieldFinanceEveryPosition(t *testing.T) {
	d := datasetByKey["expenses"]
	d.fields = append(d.fields, field{key: "secret", label: "Secret", typ: TMoney, sql: "e.amount_minor", group: true, finance: true})
	d.byKey["secret"] = &d.fields[len(d.fields)-1]
	defer func() { d.fields = d.fields[:len(d.fields)-1]; delete(d.byKey, "secret") }()
	env := testEnv()
	env.Finance = false
	positions := []Spec{
		{Dataset: "expenses", Columns: []string{"secret"}},
		{Dataset: "expenses", Filters: []Filter{{Field: "secret", Op: "gt", Value: 1.0}}},
		{Dataset: "expenses", GroupBy: []Group{{Field: "secret"}}},
		{Dataset: "expenses", GroupBy: []Group{{Field: "status"}}, Measures: []Measure{{Field: "secret", Agg: "sum"}}},
		{Dataset: "expenses", Sort: []Sort{{Key: "secret"}}},
		{Dataset: "expenses", Period: &Period{Field: "secret", Preset: "this_fy"}},
	}
	for i, s := range positions {
		if _, err := Compile(s, env, 0); code(err) != command.Forbidden {
			t.Errorf("position %d: want forbidden, got %v", i, err)
		}
	}
	for _, di := range Catalog(env) {
		for _, f := range di.Fields {
			if f.Key == "secret" {
				t.Error("secret field in catalog")
			}
		}
	}
	env.Finance = true
	if _, err := Compile(positions[0], env, 0); err != nil {
		t.Errorf("finance user: %v", err)
	}
}

func TestCaps(t *testing.T) {
	env := testEnv()
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "number"
		}
		return out
	}
	if _, err := Compile(Spec{Dataset: "invoices", Columns: many(MaxColumns + 1)}, env, 0); code(err) != command.Invalid {
		t.Errorf("columns cap: %v", err)
	}
	fl := make([]Filter, MaxFilters+1)
	for i := range fl {
		fl[i] = Filter{Field: "number", Op: "not_empty"}
	}
	if _, err := Compile(Spec{Dataset: "invoices", Filters: fl}, env, 0); code(err) != command.Invalid {
		t.Errorf("filters cap: %v", err)
	}
	if _, err := Compile(Spec{Dataset: "invoices", Filters: fl[:MaxFilters]}, env, 0); err != nil {
		t.Errorf("exactly the cap must pass: %v", err)
	}
	if _, err := Compile(Spec{Dataset: "invoices", GroupBy: []Group{{Field: "status"}, {Field: "kind"}}}, env, 0); code(err) != command.Invalid {
		t.Errorf("group cap: %v", err)
	}
	ms := make([]Measure, MaxMeasures+1)
	if _, err := Compile(Spec{Dataset: "invoices", GroupBy: []Group{{Field: "status"}}, Measures: ms}, env, 0); code(err) != command.Invalid {
		t.Errorf("measure cap: %v", err)
	}
	// limits
	for _, c := range []struct{ spec, param, want int }{
		{0, 0, MaxRows}, {50, 0, 50}, {5000, 0, MaxRows}, {0, MaxCSVRows, MaxCSVRows}, {0, 5000, MaxRows}, {10, 20, 20},
	} {
		q, err := Compile(Spec{Dataset: "invoices", Limit: c.spec}, env, c.param)
		if err != nil || q.Limit != c.want || !strings.HasSuffix(q.SQL, "limit "+itoa(c.want+1)) {
			t.Errorf("limit %+v: got %d %v", c, q.Limit, err)
		}
	}
	if _, err := Compile(Spec{Dataset: "invoices"}, env, MaxCSVRows+1); code(err) != command.Invalid {
		t.Error("over csv cap must fail")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestOpTypeWhitelist(t *testing.T) {
	env := testEnv()
	for _, f := range []Filter{
		{Field: "total", Op: "contains", Value: "x"},           // money
		{Field: "status", Op: "eq", Value: "paid"},             // enum
		{Field: "number", Op: "gt", Value: 1.0},                // text
		{Field: "invoice_date", Op: "eq", Value: "2026-01-01"}, // date
		{Field: "overdue", Op: "in", Value: true},              // bool
		{Field: "contact", Op: "me"},                           // contact
		{Field: "status", Op: "in", Value: []any{"nope"}},      // not an option
		{Field: "total", Op: "eq", Value: 1.5},                 // paise are whole
		{Field: "total", Op: "between", Value: []any{1.0}},
		{Field: "contact", Op: "in", Value: []any{"not-a-uuid"}},
	} {
		if _, err := Compile(Spec{Dataset: "invoices", Filters: []Filter{f}}, env, 0); code(err) != command.Invalid {
			t.Errorf("%+v: want invalid, got %v", f, err)
		}
	}
	good := []Filter{
		{Field: "status", Op: "in", Value: []any{"issued", "paid"}},
		{Field: "total", Op: "between", Value: []any{100.0, 5000.0}},
		{Field: "invoice_date", Op: "before", Value: "today"},
		{Field: "invoice_date", Op: "between", Value: []any{"2026-04-01", "2026-04-30"}},
		{Field: "overdue", Op: "is", Value: true},
		{Field: "contact", Op: "not_in", Value: []any{"33333333-3333-3333-3333-333333333333"}},
		{Field: "number", Op: "empty"},
	}
	for _, f := range good {
		if _, err := Compile(Spec{Dataset: "invoices", Filters: []Filter{f}}, env, 0); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	q, _ := Compile(Spec{Dataset: "tasks", Filters: []Filter{{Field: "assignee", Op: "me"}}}, env, 0)
	if len(q.Args) != 4 || q.Args[3] != *env.UserID {
		t.Errorf("me must bind the viewer: %v", q.Args)
	}
	env.UserID = nil
	if _, err := Compile(Spec{Dataset: "tasks", Filters: []Filter{{Field: "assignee", Op: "me"}}}, env, 0); code(err) != command.Invalid {
		t.Error("me without a person must fail")
	}
}

func TestGroupMode(t *testing.T) {
	env := testEnv()
	q, err := Compile(Spec{Dataset: "invoices", GroupBy: []Group{{Field: "contact"}},
		Measures: []Measure{{Field: "total", Agg: "sum"}, {Agg: "count"}, {Field: "total", Agg: "avg"}}}, env, 0)
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for _, c := range q.Columns {
		labels = append(labels, c.Key+":"+c.Label)
	}
	if strings.Join(labels, "|") != "g0:Customer|m0:Total total amount|m1:Number of invoices|m2:Average total amount" {
		t.Errorf("labels %v", labels)
	}
	for _, want := range []string{"group by i.contact_id, cn.title", "order by m0 desc nulls last, g0 asc nulls last", "coalesce(sum(i.total_minor), 0)::bigint as m0", "count(*) as m1", "round(avg(i.total_minor))::bigint as m2"} {
		if !strings.Contains(q.SQL, want) {
			t.Errorf("missing %q in %s", want, q.SQL)
		}
	}
	if strings.Contains(q.TotalsSQL, "group by") || !strings.HasPrefix(q.TotalsSQL, "select null, coalesce(sum") {
		t.Errorf("totals %s", q.TotalsSQL)
	}
	if !strings.HasPrefix(q.CountSQL, "select count(*) from (select 1 from billing.invoices") || !strings.Contains(q.CountSQL, "group by i.contact_id, cn.title) grouped") {
		t.Errorf("count %s", q.CountSQL)
	}
	// default measure is a count; default sort m0 desc
	q, err = Compile(Spec{Dataset: "tickets", GroupBy: []Group{{Field: "priority"}}}, env, 0)
	if err != nil || q.Columns[1].Label != "Number of tickets" {
		t.Fatalf("%v %+v", err, q.Columns)
	}
	// sort keys in group mode
	for _, k := range []string{"total", "m3", "m", "M0"} {
		if _, err := Compile(Spec{Dataset: "invoices", GroupBy: []Group{{Field: "status"}}, Sort: []Sort{{Key: k}}}, env, 0); code(err) != command.Invalid {
			t.Errorf("sort %q: %v", k, err)
		}
	}
	if _, err := Compile(Spec{Dataset: "invoices", GroupBy: []Group{{Field: "status"}}, Sort: []Sort{{Key: "g0", Dir: "asc"}, {Key: "m0", Dir: "desc"}}}, env, 0); err != nil {
		t.Error(err)
	}
	// not groupable / not summable
	for _, s := range []Spec{
		{Dataset: "invoices", GroupBy: []Group{{Field: "total"}}},
		{Dataset: "invoices", GroupBy: []Group{{Field: "status", Bucket: "month"}}},
		{Dataset: "invoices", GroupBy: []Group{{Field: "status"}}, Measures: []Measure{{Field: "number", Agg: "sum"}}},
		{Dataset: "invoices", GroupBy: []Group{{Field: "status"}}, Measures: []Measure{{Agg: "sum"}}},
		{Dataset: "invoices", GroupBy: []Group{{Field: "status"}}, Measures: []Measure{{Field: "invoice_date", Agg: "sum"}}},
	} {
		if _, err := Compile(s, env, 0); code(err) != command.Invalid {
			t.Errorf("%+v: %v", s, err)
		}
	}
	// aggregate (number view): measures without a group
	q, err = Compile(Spec{Dataset: "invoices", Measures: []Measure{{Field: "balance", Agg: "sum"}}}, env, 0)
	if err != nil || strings.Contains(q.SQL, "group by") || q.TotalsSQL != "" || q.Limit != 1 {
		t.Errorf("aggregate %v %+v", err, q)
	}
}

func TestBuckets(t *testing.T) {
	for _, c := range []struct {
		bucket, want string
		fy           int
	}{
		{"", "i.invoice_date", 4},
		{"day", "i.invoice_date", 4},
		{"week", "date_trunc('week', i.invoice_date::timestamp)::date", 4},
		{"month", "date_trunc('month', i.invoice_date::timestamp)::date", 4},
		{"quarter", "(date_trunc('month', i.invoice_date::timestamp) - make_interval(months => ((extract(month from i.invoice_date)::int - 4 + 12) % 12) % 3))::date", 4},
		{"fy", "(date_trunc('month', i.invoice_date::timestamp) - make_interval(months => ((extract(month from i.invoice_date)::int - 1 + 12) % 12)))::date", 1},
	} {
		got, _, err := bucketSQL("i.invoice_date", c.bucket, c.fy)
		if err != nil || got != c.want {
			t.Errorf("%q: %v\n got %s\nwant %s", c.bucket, err, got, c.want)
		}
	}
	if _, _, err := bucketSQL("x", "decade; drop", 4); err == nil {
		t.Error("unknown bucket must fail")
	}
	q, err := Compile(Spec{Dataset: "payments", GroupBy: []Group{{Field: "received_on", Bucket: "month"}}, Measures: []Measure{{Field: "amount", Agg: "sum"}}}, testEnv(), 0)
	if err != nil || q.Columns[0].Label != "Date received by month" || q.Columns[0].Type != TDate ||
		!strings.Contains(q.SQL, "date_trunc('month', (p.received_at at time zone 'Asia/Kolkata')::date::timestamp)::date::text as g0") {
		t.Errorf("%v %+v\n%s", err, q.Columns, q.SQL)
	}
}

func TestPeriodApplied(t *testing.T) {
	q, err := Compile(Spec{Dataset: "payments", Period: &Period{Preset: "this_fy"}}, testEnv(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if q.From != "2026-04-01" || q.To != "2027-03-31" || q.PeriodLbl != "This FY (1 Apr 2026 – 31 Mar 2027)" {
		t.Errorf("%+v", q)
	}
	if !strings.Contains(q.SQL, "(p.received_at at time zone 'Asia/Kolkata')::date >= $4::date and (p.received_at at time zone 'Asia/Kolkata')::date <= $5::date") {
		t.Errorf("sql %s", q.SQL)
	}
	if q.Args[3] != "2026-04-01" || q.Args[4] != "2027-03-31" {
		t.Errorf("args %v", q.Args)
	}
	// period on a non-date field
	if _, err := Compile(Spec{Dataset: "payments", Period: &Period{Field: "amount", Preset: "this_fy"}}, testEnv(), 0); code(err) != command.Invalid {
		t.Error("non-date period field must fail")
	}
}

func TestScopeAlwaysPresent(t *testing.T) {
	env := testEnv()
	for _, d := range registry {
		q, err := Compile(Spec{Dataset: d.key}, env, 0)
		if err != nil {
			t.Fatalf("%s: %v", d.key, err)
		}
		if !strings.Contains(q.SQL, "deleted_at is null") {
			t.Errorf("%s: no scope in %s", d.key, q.SQL)
		}
		// default columns and date exist and every sql is reachable in group mode too
		for _, c := range d.defCols {
			if d.byKey[c] == nil {
				t.Errorf("%s: default column %s unknown", d.key, c)
			}
		}
		if f := d.byKey[d.defDate]; f == nil || f.typ != TDate {
			t.Errorf("%s: bad default date", d.key)
		}
		for _, f := range d.fields {
			if f.group {
				if _, err := Compile(Spec{Dataset: d.key, GroupBy: []Group{{Field: f.key}}}, env, 0); err != nil {
					t.Errorf("%s.%s group: %v", d.key, f.key, err)
				}
			}
			if _, err := Compile(Spec{Dataset: d.key, Columns: []string{f.key}}, env, 0); err != nil {
				t.Errorf("%s.%s column: %v", d.key, f.key, err)
			}
			if (f.typ == TPerson || f.typ == TContact) && f.labelSQL == "" {
				t.Errorf("%s.%s: no label sql", d.key, f.key)
			}
		}
		// every placeholder 1..n is referenced by all three statements, or the server rejects the arg count
		for _, s := range []string{q.SQL, q.TotalsSQL, q.CountSQL} {
			if s == "" {
				continue
			}
			for i := 1; i <= len(q.Args); i++ {
				if !strings.Contains(s, "$"+itoa(i)+"::") {
					t.Errorf("%s: $%d missing in %s", d.key, i, s)
				}
			}
		}
	}
	// expenses: own/admin/finance rule binds admin, user, finance
	q, _ := Compile(Spec{Dataset: "expenses"}, env, 0)
	if !strings.Contains(q.SQL, "e.paid_by_user_id = $2::uuid") || len(q.Args) != 3 {
		t.Errorf("expense scope %s %v", q.SQL, q.Args)
	}
}

func TestCatalogShape(t *testing.T) {
	env := testEnv()
	got := Catalog(env)
	if len(got) != 8 {
		t.Fatalf("want 8 datasets, got %d", len(got))
	}
	env.Finance = false
	got = Catalog(env)
	keys := []string{}
	for _, d := range got {
		keys = append(keys, d.Key)
	}
	if strings.Join(keys, ",") != "expenses,deals,tickets,tasks,time_entries" {
		t.Errorf("non-finance catalog %v", keys)
	}
}
