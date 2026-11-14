package projects

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Port of the unit tests in crates/cnote-projects/src/cnql.rs.

const nilUUID = "00000000-0000-0000-0000-000000000000"

func tcx() *Cx {
	u := nilUUID
	return &Cx{Now: time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC), User: &u}
}

func mustC(t *testing.T, q string) *Compiled {
	t.Helper()
	p, err := ParseCNQL(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	c, err := CompileCNQL(p, tcx(), 3)
	if err != nil {
		t.Fatalf("compile %q: %v", q, err)
	}
	return c
}

func perr(t *testing.T, q string) *CnqlError {
	t.Helper()
	_, err := ParseCNQL(q)
	if err == nil {
		t.Fatalf("parse %q: expected an error", q)
	}
	return err
}

func cerrOf(t *testing.T, q string) *CnqlError {
	t.Helper()
	p, err := ParseCNQL(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	_, cerr := CompileCNQL(p, tcx(), 1)
	if cerr == nil {
		t.Fatalf("compile %q: expected an error", q)
	}
	return cerr
}

func tsB(y int, mo time.Month, d, h, mi int) Bind {
	return Bind{Kind: BTs, T: time.Date(y, mo, d, h, mi, 0, 0, time.UTC)}
}
func textArr(v ...string) Bind { return Bind{Kind: BTextArr, Texts: v} }

func sameBinds(t *testing.T, got, want []Bind) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("binds %v, want %v", got, want)
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Text != w.Text || !reflect.DeepEqual(g.Texts, w.Texts) || g.Int != w.Int || !g.T.Equal(w.T) {
			t.Fatalf("bind %d: %+v, want %+v", i, g, w)
		}
	}
}

func contains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("%q does not contain %q", s, sub)
		}
	}
}

func TestEmptyAndOrderOnly(t *testing.T) {
	if mustC(t, "").Where != "true" {
		t.Fatal("empty query")
	}
	if mustC(t, "   ").Order != DefaultOrder {
		t.Fatal("blank query order")
	}
	o := mustC(t, "ORDER BY updated DESC, key")
	if o.Where != "true" || !strings.HasPrefix(o.Order, "n.updated_at desc nulls last, sp.key asc, i.number asc nulls last") {
		t.Fatalf("%+v", o)
	}
}

func TestSimpleEqualityIsParameterised(t *testing.T) {
	r := mustC(t, `project = ENG AND status = "In Progress"`)
	if r.Where != "((lower(sp.key) = any($3::text[])) and (lower(i.status) = any($4::text[])))" {
		t.Fatal(r.Where)
	}
	sameBinds(t, r.Binds, []Bind{textArr("eng"), textArr("in progress")})
}

func TestInjectionAttemptStaysABind(t *testing.T) {
	r := mustC(t, `status = "x'; drop table core.notes; --"`)
	if strings.Contains(r.Where, "drop") || len(r.Binds) != 1 {
		t.Fatal(r)
	}
	r = mustC(t, `text ~ "100%_\\"`)
	if strings.Contains(r.Where, "100") {
		t.Fatal(r.Where)
	}
	sameBinds(t, r.Binds, []Bind{{Kind: BText, Text: `%100\%\_\\%`}})
}

func TestPrecedenceNotAndParens(t *testing.T) {
	r := mustC(t, "status = a OR status = b AND NOT type = bug")
	if !strings.HasPrefix(r.Where, "((lower(i.status) = any($3::text[])) or (") || !strings.Contains(r.Where, "(not coalesce(") {
		t.Fatal(r.Where)
	}
	r = mustC(t, "(status = a OR status = b) AND type = bug")
	if !strings.HasPrefix(r.Where, "(((lower(i.status) = any($3::text[])) or") {
		t.Fatal(r.Where)
	}
}

func TestNegationsKeepUnassignedRows(t *testing.T) {
	r := mustC(t, "assignee != currentUser()")
	if !strings.HasPrefix(r.Where, "(not coalesce(") {
		t.Fatal(r.Where)
	}
	sameBinds(t, r.Binds, []Bind{{Kind: BUUIDArr, Texts: []string{nilUUID}}})
	r = mustC(t, "status NOT IN (Done, Cancelled)")
	if !strings.HasPrefix(r.Where, "(not coalesce(") {
		t.Fatal(r.Where)
	}
	sameBinds(t, r.Binds, []Bind{textArr("done", "cancelled")})
}

func TestEmptyOperators(t *testing.T) {
	for q, want := range map[string]string{
		"assignee IS EMPTY":     "(i.assignee_id is null and i.assignee_agent_id is null)",
		"assignee is not empty": "(not ((i.assignee_id is null and i.assignee_agent_id is null)))",
		"labels IS EMPTY":       "(cardinality(i.labels) = 0)",
		"due IS NULL":           "(i.due_date is null)",
	} {
		if got := mustC(t, q).Where; got != want {
			t.Errorf("%s: %s", q, got)
		}
	}
}

func TestAssigneeByNameOrID(t *testing.T) {
	id := "6f1c1a52-0b1c-4a54-9d0e-2a3f7c8d9e10"
	r := mustC(t, fmt.Sprintf(`assignee IN ("Tess", %s)`, id))
	if len(r.Binds) != 2 {
		t.Fatal(r.Binds)
	}
	sameBinds(t, r.Binds, []Bind{{Kind: BUUIDArr, Texts: []string{id}}, textArr("tess")})
	contains(t, r.Where, "assignee_agent_id")
	if strings.Contains(mustC(t, "reporter = currentUser()").Where, "assignee") {
		t.Fatal("reporter query mentions assignee")
	}
	// every uuid spelling the uuid crate accepts is an id, anything else is a name
	r = mustC(t, `assignee = 6F1C1A520B1C4A549D0E2A3F7C8D9E10`)
	sameBinds(t, r.Binds, []Bind{{Kind: BUUIDArr, Texts: []string{id}}})
	r = mustC(t, `assignee = "{6f1c1a52-0b1c-4a54-9d0e-2a3f7c8d9e10}"`)
	sameBinds(t, r.Binds, []Bind{{Kind: BUUIDArr, Texts: []string{id}}})
	r = mustC(t, `assignee = "URN:UUID:6f1c1a52-0b1c-4a54-9d0e-2a3f7c8d9e10"`)
	sameBinds(t, r.Binds, []Bind{{Kind: BUUIDArr, Texts: []string{id}}})
	r = mustC(t, `assignee = 6f1c1a52-0b1c-4a54-9d0e-2a3f7c8d9e1`)
	sameBinds(t, r.Binds, []Bind{textArr("6f1c1a52-0b1c-4a54-9d0e-2a3f7c8d9e1")})
}

func TestStatusCategoryAndPriority(t *testing.T) {
	sameBinds(t, mustC(t, `statusCategory = "To Do"`).Binds, []Bind{textArr("todo")})
	sameBinds(t, mustC(t, `statusCategory IN ("In Progress", Done)`).Binds, []Bind{textArr("in_progress", "done")})
	contains(t, cerrOf(t, "statusCategory = Weird").Msg, "To Do, In Progress or Done")
	r := mustC(t, "priority >= High")
	sameBinds(t, r.Binds, []Bind{{Kind: BInt, Int: 3}})
	contains(t, r.Where, ">= $3::int")
	sameBinds(t, mustC(t, "priority = Highest").Binds, []Bind{textArr("urgent")})
	contains(t, cerrOf(t, "priority = Spicy").Msg, "Urgent, High")
}

func TestSprintFunctionsAndNames(t *testing.T) {
	r := mustC(t, "sprint in openSprints()")
	contains(t, r.Where, "from projects.sprints where state = 'active'")
	if len(r.Binds) != 0 {
		t.Fatal(r.Binds)
	}
	r = mustC(t, `sprint IN (closedSprints(), "Sprint 4")`)
	contains(t, r.Where, "state = 'closed'")
	sameBinds(t, r.Binds, []Bind{textArr("sprint 4")})
	contains(t, mustC(t, "sprint IS EMPTY").Where, "i.sprint_id is null")
	contains(t, cerrOf(t, "component = openSprints()").Msg, "cannot be used")
}

func TestLabelsFixVersionComponent(t *testing.T) {
	r := mustC(t, `labels = urgent AND fixVersion = "1.0" AND component = Api`)
	if len(r.Binds) != 3 {
		t.Fatal(r.Binds)
	}
	contains(t, r.Where, "unnest(i.labels)", "projects.versions", "projects.components")
}

func TestDatesRelativeAndAbsolute(t *testing.T) {
	sameBinds(t, mustC(t, "created >= -7d").Binds, []Bind{tsB(2026, 3, 8, 10, 30)})
	sameBinds(t, mustC(t, "updated < -2w").Binds, []Bind{tsB(2026, 3, 1, 10, 30)})
	sameBinds(t, mustC(t, "created > startOfDay()").Binds, []Bind{tsB(2026, 3, 15, 0, 0)})
	sameBinds(t, mustC(t, `created > startOfDay("-1d")`).Binds, []Bind{tsB(2026, 3, 14, 0, 0)})
	sameBinds(t, mustC(t, "resolved <= now()").Binds, []Bind{{Kind: BTs, T: tcx().Now}})
	r := mustC(t, "created > startOfWeek() AND created > startOfMonth()")
	sameBinds(t, r.Binds, []Bind{tsB(2026, 3, 9, 0, 0), tsB(2026, 3, 1, 0, 0)})
	sameBinds(t, mustC(t, "created > -1M").Binds, []Bind{tsB(2026, 2, 15, 10, 30)})
}

func TestDueIsADateAndDayEqualityIsARange(t *testing.T) {
	r := mustC(t, "due < 2026-04-01")
	sameBinds(t, r.Binds, []Bind{{Kind: BDate, T: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}})
	contains(t, r.Where, "::date")
	r = mustC(t, "created = 2026-04-01")
	if len(r.Binds) != 2 {
		t.Fatal(r.Binds)
	}
	contains(t, r.Where, ">= $3::timestamptz and n.created_at < $4::timestamptz")
	if len(mustC(t, `created = "2026-04-01 09:00"`).Binds) != 1 {
		t.Fatal("datetime equality is a point")
	}
}

func TestTextSearch(t *testing.T) {
	r := mustC(t, "text ~ login")
	contains(t, r.Where, "n.title ilike $3::text")
	sameBinds(t, r.Binds, []Bind{{Kind: BText, Text: "%login%"}})
	if !strings.HasPrefix(mustC(t, "text !~ login").Where, "(not coalesce(") {
		t.Fatal("negated text search")
	}
	contains(t, cerrOf(t, "text = login").Msg, "use ~")
}

func TestPlaceholderOffset(t *testing.T) {
	p, _ := ParseCNQL("status = a AND type = b")
	r, _ := CompileCNQL(p, tcx(), 7)
	contains(t, r.Where, "$7::text[]", "$8::text[]")
}

func TestKeywordsAreCaseInsensitiveAndFieldsHaveAliases(t *testing.T) {
	r := mustC(t, "Issuetype = bug and Project in (eng, ops) order by Priority desc")
	if len(r.Binds) != 2 {
		t.Fatal(r.Binds)
	}
	contains(t, r.Order, "desc nulls last")
}

func TestOrderByRankAndRejects(t *testing.T) {
	contains(t, mustC(t, "project = ENG ORDER BY rank").Order, `i.rank collate "C" asc`)
	contains(t, cerrOf(t, "ORDER BY labels").Msg, "cannot ORDER BY 'labels'")
	contains(t, cerrOf(t, "rank = a").Msg, "only be used in ORDER BY")
}

func TestErrorsHavePositions(t *testing.T) {
	e := perr(t, "status = ")
	if e.Pos != 10 || e.Msg != "expected a value, found end of query" {
		t.Fatal(e)
	}
	e = perr(t, "stauts = Done")
	if e.Pos != 1 {
		t.Fatal(e)
	}
	contains(t, e.Msg, "unknown field 'stauts'", "statusCategory")
	if e = perr(t, "status = Done AND"); e.Pos != 18 {
		t.Fatal(e)
	}
	if e = perr(t, "status Done"); e.Pos != 8 {
		t.Fatal(e)
	}
	contains(t, e.Msg, "expected an operator")
	e = perr(t, "(status = Done")
	contains(t, e.Msg, "expected ')'", "column 1")
	contains(t, perr(t, "status IN Done").Msg, "expected '(' after IN")
	contains(t, perr(t, "status IN (a, b").Msg, "expected ',' or ')'")
	e = perr(t, `status = "abc`)
	if e.Pos != 10 || e.Msg != "unterminated string" {
		t.Fatal(e)
	}
	e = perr(t, "status = Done foo")
	contains(t, e.Msg, "unexpected 'foo'")
	if e.Pos != 15 {
		t.Fatal(e)
	}
	contains(t, perr(t, "status = a # b").Msg, "unexpected character '#'")
	contains(t, perr(t, "status ! a").Msg, "'!' must be followed")
	contains(t, perr(t, "assignee IS FULL").Msg, "expected EMPTY")
	contains(t, perr(t, "ORDER status").Msg, "expected BY")
	contains(t, perr(t, "status = f(").Msg, "function argument")
	contains(t, perr(t, "= a").Error(), "at column 1")
}

func TestCompileErrors(t *testing.T) {
	contains(t, cerrOf(t, "priority ~ high").Msg, "not supported")
	contains(t, cerrOf(t, "status > a").Msg, "not supported")
	contains(t, cerrOf(t, "created = banana").Msg, "is not a date")
	contains(t, cerrOf(t, "created > nope()").Msg, "unknown function nope()")
	contains(t, cerrOf(t, "created > -7x").Msg, "is not a date")
	contains(t, cerrOf(t, "assignee = now()").Msg, "cannot be used")
	p, _ := ParseCNQL("assignee = currentUser()")
	_, err := CompileCNQL(p, &Cx{Now: tcx().Now}, 1)
	if err == nil {
		t.Fatal("expected an error without a user")
	}
	contains(t, err.Msg, "signed-in user")
}

// Extra edge cases for the Rust/Go compatibility helpers.
func TestCompatHelpers(t *testing.T) {
	if got := rustLower("İΣ ΑΣ ΑΣΑ Σ"); got != "i̇ς ας ασα σ" {
		t.Errorf("rustLower: %q", got)
	}
	for in, want := range map[string]bool{"2026-04-01": true, "2026-4-1": true, "2026-02-30": false, "2026-04-01x": false, "26-04-01": true,
		"-0001-04-01": true, "+12345-01-01": true, "2026/04/01": true, "2026-04": false, " 2026 - 04 - 01 ": false} {
		_, ok := chronoDate(in)
		if ok != want {
			t.Errorf("chronoDate(%q) = %v, want %v", in, ok, want)
		}
	}
	for in, want := range map[string]bool{"2026-04-01 09:00": true, "2026-04-0109:00": true, "2026-04-01T09:00:00": true, "2026-04-01 09:00:60": true,
		"2026-04-01 24:00": false, "2026-04-01 9:5": true, "2026-04-01T09:00": false} {
		_, ok, _ := chronoDateTime(in)
		if ok != want {
			t.Errorf("chronoDateTime(%q) = %v, want %v", in, ok, want)
		}
	}
	for in, want := range map[string]bool{"2026-04-01T09:00:00Z": true, "2026-04-01 09:00:00+02:00": true, "2026-04-01t09:00:00z": true,
		"2026-04-01T09:00:00+0200": false, "2026-04-01T09:00:00": false, "2026-04-01T09:00:00.123456789012Z": true, "2026-04-01T09:00:00.Z": false,
		"2026-04-01T09:00:60Z": true, "2026-04-01T09:00:00+24:00": false, "2026-04-01T09:00:00−05:00": true} {
		_, ok, _ := chronoRFC3339(in)
		if ok != want {
			t.Errorf("chronoRFC3339(%q) = %v, want %v", in, ok, want)
		}
	}
	// clamped month arithmetic, like chrono's checked_sub_months
	base := time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC)
	if got, _ := relative(base, "-1M"); !got.Equal(time.Date(2026, 2, 28, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("-1M: %v", got)
	}
	if got, _ := relative(base, "1y"); !got.Equal(time.Date(2027, 3, 31, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("1y: %v", got)
	}
}
