package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// helper: create a space via Rust and return its ID.
func (e *env) spaceFor(w who) string {
	e.t.Helper()
	return e.space(w)
}

// helper: create a sprint via Rust and return its full response map.
func (e *env) sprint(w who, spaceID, name string) map[string]any {
	e.t.Helper()
	return e.must(w, "sprint.create", map[string]any{"space_id": spaceID, "name": name})
}

// helper: create a version via Rust.
func (e *env) version(w who, spaceID, name string) map[string]any {
	e.t.Helper()
	return e.must(w, "version.create", map[string]any{"space_id": spaceID, "name": name})
}

// helper: create a component via Rust.
func (e *env) component(w who, spaceID, name string) map[string]any {
	e.t.Helper()
	return e.must(w, "component.create", map[string]any{"space_id": spaceID, "name": name})
}

// ============================================================
// Integration tests (Go vs expected behaviour)
// ============================================================

func TestSprintCreateAndList(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	space := e.spaceFor(owner)

	// owner creates a sprint
	r := e.cmd(e.gosv.URL, owner, owner.ws, "sprint.create", map[string]any{
		"space_id": space,
		"name":     "Sprint 1",
		"goal":     "ship it",
	}, nil)
	if r.status != 200 {
		t.Fatalf("sprint.create: %d %s", r.status, r.body)
	}
	var s map[string]any
	_ = json.Unmarshal(r.body, &s)
	if s["name"] != "Sprint 1" || s["state"] != "future" || s["goal"] != "ship it" {
		t.Fatalf("unexpected sprint: %v", s)
	}
	sid := s["id"].(string)

	// list – owner sees it
	lr := e.cmd(e.gosv.URL, owner, owner.ws, "sprint.list", map[string]any{"space_id": space}, nil)
	var sprints []map[string]any
	_ = json.Unmarshal(lr.body, &sprints)
	if len(sprints) != 1 || sprints[0]["id"] != sid {
		t.Fatalf("list: %s", lr.body)
	}

	// member sees it too (readable space)
	lr2 := e.cmd(e.gosv.URL, member, owner.ws, "sprint.list", map[string]any{"space_id": space}, nil)
	var sprints2 []map[string]any
	_ = json.Unmarshal(lr2.body, &sprints2)
	if len(sprints2) != 1 {
		t.Fatalf("member list: %s", lr2.body)
	}

	// guest cannot create (write access denied on open space for guest without space membership)
	bad := e.cmd(e.gosv.URL, guest, owner.ws, "sprint.create", map[string]any{"space_id": space, "name": "X"}, nil)
	if bad.status != 403 {
		t.Fatalf("guest create expected 403, got %d %s", bad.status, bad.body)
	}

	// empty name rejected
	bad2 := e.cmd(e.gosv.URL, owner, owner.ws, "sprint.create", map[string]any{"space_id": space, "name": "   "}, nil)
	if bad2.status != 400 {
		t.Fatalf("empty name: %d %s", bad2.status, bad2.body)
	}

	// date validation
	bad3 := e.cmd(e.gosv.URL, owner, owner.ws, "sprint.create", map[string]any{
		"space_id": space, "name": "X", "start_date": "2026-03-10", "end_date": "2026-03-01",
	}, nil)
	if bad3.status != 400 || !strings.Contains(string(bad3.body), "end_date is before start_date") {
		t.Fatalf("date check: %d %s", bad3.status, bad3.body)
	}

	// event emitted
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var n int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where kind = 'sprint.created'").Scan(&n)
	if n < 1 {
		t.Fatal("sprint.created event not emitted")
	}
}

func TestSprintStartAndComplete(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	space := e.spaceFor(owner)

	s1 := e.sprint(owner, space, "Sprint 1")
	s2 := e.sprint(owner, space, "Sprint 2")
	s1id := s1["id"].(string)
	s2id := s2["id"].(string)

	// cannot complete a future sprint
	bad := e.cmd(e.gosv.URL, owner, owner.ws, "sprint.complete", map[string]any{"id": s1id}, nil)
	if bad.status != 409 {
		t.Fatalf("complete future: expected 409, got %d %s", bad.status, bad.body)
	}

	// start
	sr := e.cmd(e.gosv.URL, owner, owner.ws, "sprint.start", map[string]any{"id": s1id}, nil)
	if sr.status != 200 {
		t.Fatalf("sprint.start: %d %s", sr.status, sr.body)
	}
	var started map[string]any
	_ = json.Unmarshal(sr.body, &started)
	if started["state"] != "active" {
		t.Fatalf("state: %v", started["state"])
	}

	// cannot start a second one
	bad2 := e.cmd(e.gosv.URL, owner, owner.ws, "sprint.start", map[string]any{"id": s2id}, nil)
	if bad2.status != 409 {
		t.Fatalf("second start: expected 409, got %d %s", bad2.status, bad2.body)
	}

	// complete with explicit move-to
	cr := e.cmd(e.gosv.URL, owner, owner.ws, "sprint.complete", map[string]any{
		"id": s1id, "move_to_sprint_id": s2id,
	}, nil)
	if cr.status != 200 {
		t.Fatalf("sprint.complete: %d %s", cr.status, cr.body)
	}
	var done map[string]any
	_ = json.Unmarshal(cr.body, &done)
	if done["moved_to"] != s2id {
		t.Fatalf("moved_to: %v", done["moved_to"])
	}

	// events
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var started_n, completed_n int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where kind = 'sprint.started'").Scan(&started_n)
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where kind = 'sprint.completed'").Scan(&completed_n)
	if started_n < 1 || completed_n < 1 {
		t.Fatalf("events: started=%d completed=%d", started_n, completed_n)
	}
}

func TestVersionCreateListRelease(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	space := e.spaceFor(owner)

	r := e.cmd(e.gosv.URL, owner, owner.ws, "version.create", map[string]any{
		"space_id": space, "name": "1.0", "release_date": "2026-12-31",
	}, nil)
	if r.status != 200 {
		t.Fatalf("version.create: %d %s", r.status, r.body)
	}
	var v map[string]any
	_ = json.Unmarshal(r.body, &v)
	if v["name"] != "1.0" || v["released"] != false {
		t.Fatalf("unexpected version: %v", v)
	}
	vid := v["id"].(string)

	lr := e.cmd(e.gosv.URL, owner, owner.ws, "version.list", map[string]any{"space_id": space}, nil)
	var versions []map[string]any
	_ = json.Unmarshal(lr.body, &versions)
	if len(versions) != 1 || versions[0]["id"] != vid {
		t.Fatalf("list: %s", lr.body)
	}

	// release
	rr := e.cmd(e.gosv.URL, owner, owner.ws, "version.release", map[string]any{"id": vid}, nil)
	if rr.status != 200 {
		t.Fatalf("version.release: %d %s", rr.status, rr.body)
	}
	var rel map[string]any
	_ = json.Unmarshal(rr.body, &rel)
	if rel["released"] != true {
		t.Fatalf("released: %v", rel["released"])
	}

	// cannot release twice
	bad := e.cmd(e.gosv.URL, owner, owner.ws, "version.release", map[string]any{"id": vid}, nil)
	if bad.status != 409 {
		t.Fatalf("double release: expected 409, got %d %s", bad.status, bad.body)
	}

	// event
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var n int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where kind = 'version.released'").Scan(&n)
	if n < 1 {
		t.Fatal("version.released event not emitted")
	}
}

func TestComponentCreateAndList(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	space := e.spaceFor(owner)

	r := e.cmd(e.gosv.URL, owner, owner.ws, "component.create", map[string]any{
		"space_id": space, "name": "api",
	}, nil)
	if r.status != 200 {
		t.Fatalf("component.create: %d %s", r.status, r.body)
	}
	var comp map[string]any
	_ = json.Unmarshal(r.body, &comp)
	if comp["name"] != "api" {
		t.Fatalf("unexpected component: %v", comp)
	}
	cid := comp["id"].(string)

	lr := e.cmd(e.gosv.URL, owner, owner.ws, "component.list", map[string]any{"space_id": space}, nil)
	var comps []map[string]any
	_ = json.Unmarshal(lr.body, &comps)
	if len(comps) != 1 || comps[0]["id"] != cid {
		t.Fatalf("list: %s", lr.body)
	}

	// event
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var n int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where kind = 'component.created'").Scan(&n)
	if n < 1 {
		t.Fatal("component.created event not emitted")
	}
}

func TestAutomationCRUD(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	space := e.spaceFor(owner)

	trigger := map[string]any{"kind": "event", "event": "item.created"}
	actions := []map[string]any{{"type": "comment", "body": "hi"}}

	r := e.cmd(e.gosv.URL, owner, owner.ws, "automation.create", map[string]any{
		"space_id": space, "name": "my rule", "trigger": trigger, "actions": actions,
	}, nil)
	if r.status != 200 {
		t.Fatalf("automation.create: %d %s", r.status, r.body)
	}
	var a map[string]any
	_ = json.Unmarshal(r.body, &a)
	if a["name"] != "my rule" || a["enabled"] != true || a["draft"] != false {
		t.Fatalf("unexpected automation: %v", a)
	}
	aid := a["id"].(string)

	// list
	lr := e.cmd(e.gosv.URL, owner, owner.ws, "automation.list", map[string]any{"space_id": space}, nil)
	var autos []map[string]any
	_ = json.Unmarshal(lr.body, &autos)
	if len(autos) != 1 || autos[0]["id"] != aid {
		t.Fatalf("list: %s", lr.body)
	}

	// update: disable
	ur := e.cmd(e.gosv.URL, owner, owner.ws, "automation.update", map[string]any{
		"id": aid, "enabled": false,
	}, nil)
	if ur.status != 200 {
		t.Fatalf("automation.update: %d %s", ur.status, ur.body)
	}
	var updated map[string]any
	_ = json.Unmarshal(ur.body, &updated)
	if updated["enabled"] != false {
		t.Fatalf("enabled: %v", updated["enabled"])
	}

	// runs (empty)
	rr := e.cmd(e.gosv.URL, owner, owner.ws, "automation.runs", map[string]any{"automation_id": aid}, nil)
	if rr.status != 200 {
		t.Fatalf("automation.runs: %d %s", rr.status, rr.body)
	}
	var runs []any
	_ = json.Unmarshal(rr.body, &runs)
	if len(runs) != 0 {
		t.Fatalf("expected empty runs, got %d", len(runs))
	}

	// API key (machine) cannot create automations
	key := e.apiKey(owner)
	bad := e.cmd(e.gosv.URL, key, owner.ws, "automation.create", map[string]any{
		"space_id": space, "name": "bot rule", "trigger": trigger, "actions": actions,
	}, nil)
	if bad.status != 403 {
		t.Fatalf("api key create: expected 403, got %d %s", bad.status, bad.body)
	}

	// workspace-wide by member (should be 403)
	bad2 := e.cmd(e.gosv.URL, member, owner.ws, "automation.create", map[string]any{
		"name": "wide", "trigger": trigger, "actions": actions,
	}, nil)
	if bad2.status != 403 {
		t.Fatalf("member wide: expected 403, got %d %s", bad2.status, bad2.body)
	}

	// validation: no actions
	bad3 := e.cmd(e.gosv.URL, owner, owner.ws, "automation.create", map[string]any{
		"space_id": space, "name": "X", "trigger": trigger, "actions": []any{},
	}, nil)
	if bad3.status != 400 {
		t.Fatalf("no actions: expected 400, got %d %s", bad3.status, bad3.body)
	}

	// event emitted
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var n int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where kind = 'automation.created'").Scan(&n)
	if n < 1 {
		t.Fatal("automation.created event not emitted")
	}
}

func TestReportBurndownBasic(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	space := e.spaceFor(owner)
	s := e.sprint(owner, space, "Sprint R")
	sid := s["id"].(string)

	// burndown without start_date should fail
	r := e.cmd(e.gosv.URL, owner, owner.ws, "report.burndown", map[string]any{"sprint_id": sid}, nil)
	if r.status != 400 || !strings.Contains(string(r.body), "has not started") {
		t.Fatalf("no start_date: %d %s", r.status, r.body)
	}

	// start the sprint
	e.must(owner, "sprint.start", map[string]any{"id": sid})

	// now burndown should work
	r2 := e.cmd(e.gosv.URL, owner, owner.ws, "report.burndown", map[string]any{"sprint_id": sid}, nil)
	if r2.status != 200 {
		t.Fatalf("burndown: %d %s", r2.status, r2.body)
	}
	var bd map[string]any
	_ = json.Unmarshal(r2.body, &bd)
	if bd["sprint_id"] != sid {
		t.Fatalf("sprint_id: %v", bd["sprint_id"])
	}
	pts, _ := bd["points"].([]any)
	if len(pts) == 0 {
		t.Fatalf("no points: %v", bd)
	}
}

func TestReportVelocityBasic(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	space := e.spaceFor(owner)

	r := e.cmd(e.gosv.URL, owner, owner.ws, "report.velocity", map[string]any{"space_id": space}, nil)
	if r.status != 200 {
		t.Fatalf("velocity: %d %s", r.status, r.body)
	}
	var v map[string]any
	_ = json.Unmarshal(r.body, &v)
	if v["space_id"] != space {
		t.Fatalf("space_id: %v", v["space_id"])
	}
	sprints, _ := v["sprints"].([]any)
	if sprints == nil {
		t.Fatalf("sprints nil: %v", v)
	}
}

func TestSprintNotFound(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	missing := "00000000-0000-4000-8000-000000000000"
	for _, cmd := range []string{"sprint.start", "sprint.complete"} {
		r := e.cmd(e.gosv.URL, owner, owner.ws, cmd, map[string]any{"id": missing}, nil)
		if r.status != 404 {
			t.Errorf("%s missing: expected 404, got %d %s", cmd, r.status, r.body)
		}
	}
	r := e.cmd(e.gosv.URL, owner, owner.ws, "report.burndown", map[string]any{"sprint_id": missing}, nil)
	if r.status != 404 {
		t.Fatalf("burndown missing: %d %s", r.status, r.body)
	}
}

// ============================================================
// Differential test: planning commands
// ============================================================

func TestDifferentialPlanning(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	other := e.signup("Other", true)
	key := e.apiKey(owner)
	agentResp := e.must(owner, "agent.create", map[string]any{"name": "plan-bot"})
	agent := who{ws: owner.ws, bearer: agentResp["api_key"].(string)}
	missing := "00000000-0000-4000-8000-000000000000"

	space := e.spaceFor(owner)

	// Create fixtures through Rust
	s1 := e.sprint(owner, space, "Sprint One")
	s2 := e.sprint(owner, space, "Sprint Two")
	s1id := s1["id"].(string)
	s2id := s2["id"].(string)
	v := e.version(owner, space, "1.0")
	vid := v["id"].(string)
	comp := e.component(owner, space, "api")
	compid := comp["id"].(string)

	// Create automation
	auto := e.must(owner, "automation.create", map[string]any{
		"space_id": space, "name": "diff auto",
		"trigger": map[string]any{"kind": "event", "event": "item.created"},
		"actions": []map[string]any{{"type": "comment", "body": "hello"}},
	})
	autoid := auto["id"].(string)

	callers := map[string]who{
		"owner":                owner,
		"member":               member,
		"guest":                guest,
		"other-workspace-user": other,
		"api-key":              key,
		"agent-key":            agent,
		"no-auth":              {},
		"bad-cookie":           {cookie: "nope"},
		"bad-key":              {bearer: "cn_x_y"},
	}

	type call struct {
		cmd string
		in  any
	}
	readCalls := []call{
		{"sprint.list", map[string]any{}},
		{"sprint.list", map[string]any{"space_id": space}},
		{"sprint.list", map[string]any{"space_id": space, "state": "future"}},
		{"sprint.list", map[string]any{"state": "active"}},
		{"version.list", map[string]any{"space_id": space}},
		{"component.list", map[string]any{"space_id": space}},
		{"automation.list", map[string]any{}},
		{"automation.list", map[string]any{"space_id": space}},
		{"automation.runs", map[string]any{"automation_id": autoid}},
		{"report.velocity", map[string]any{"space_id": space}},
		// missing/bad ids
		{"sprint.list", map[string]any{"space_id": missing}},
		{"version.list", map[string]any{"space_id": missing}},
		{"component.list", map[string]any{"space_id": missing}},
		{"automation.runs", map[string]any{"automation_id": missing}},
		{"report.burndown", map[string]any{"sprint_id": missing}},
		{"report.velocity", map[string]any{"space_id": missing}},
		// fallback inputs
		{"sprint.list", nil},
		{"sprint.list", map[string]any{"space_id": "not-a-uuid"}},
		{"version.list", map[string]any{"space_id": "not-a-uuid"}},
		{"component.list", map[string]any{"space_id": "not-a-uuid"}},
		{"automation.runs", map[string]any{"automation_id": "not-a-uuid"}},
		{"report.burndown", map[string]any{"sprint_id": "not-a-uuid"}},
	}
	// Suppress unused variable warnings for ids we reference in write tests below.
	_ = s1id
	_ = s2id
	_ = vid
	_ = compid

	ran := 0
	for cname, c := range callers {
		ws := owner.ws
		if cname == "other-workspace-user" {
			ws = other.ws
		}
		for _, k := range readCalls {
			g := e.cmd(e.gosv.URL, c, ws, k.cmd, k.in, nil)
			r := e.cmd(e.rust, c, ws, k.cmd, k.in, nil)
			ran++
			if g.status != r.status || string(g.body) != string(r.body) {
				t.Errorf("%s %s %v\n go   %d %s\n rust %d %s",
					cname, k.cmd, k.in, g.status, g.body, r.status, r.body)
			}
		}
	}
	t.Logf("planning read differential: %d pairs compared", ran)
}

// TestDifferentialPlanningWrites runs write commands against twin fixtures and compares results.
func TestDifferentialPlanningWrites(t *testing.T) {
	e := newEnv(t)

	type fx struct {
		base          string
		owner, member who
		space         string
		sprint1ID     string
		sprint2ID     string
		versionID     string
		compID        string
		autoID        string
	}
	mk := func(base string) *fx {
		f := &fx{base: base}
		f.owner = e.signup("Owner", true)
		f.member = e.addMember(f.owner, e.signup("Member", false), "member")
		f.space = e.spaceFor(f.owner)
		s1 := e.sprint(f.owner, f.space, "Sprint 1")
		f.sprint1ID = s1["id"].(string)
		s2 := e.sprint(f.owner, f.space, "Sprint 2")
		f.sprint2ID = s2["id"].(string)
		v := e.version(f.owner, f.space, "1.0")
		f.versionID = v["id"].(string)
		c := e.component(f.owner, f.space, "api")
		f.compID = c["id"].(string)
		auto := e.must(f.owner, "automation.create", map[string]any{
			"space_id": f.space, "name": "rule1",
			"trigger": map[string]any{"kind": "schedule", "every_minutes": 30},
			"actions": []map[string]any{{"type": "comment", "body": "nag"}},
		})
		f.autoID = auto["id"].(string)
		return f
	}
	g, r := mk(e.gosv.URL), mk(e.rust)

	// anyUUIDRe matches any UUID in the JSON output – used to redact new IDs from create responses.
	anyUUIDRe := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	// floatRe normalizes -0.0 → 0.0 (Rust empty iterator sum quirk).
	floatRe := regexp.MustCompile(`-0\.0`)
	norm := func(f *fx, b []byte) string {
		s := string(b)
		s = tsRe.ReplaceAllString(s, "TS")
		for k, v := range map[string]string{
			f.space: "SPACE", f.sprint1ID: "SPRINT1", f.sprint2ID: "SPRINT2",
			f.versionID: "VERSION", f.compID: "COMP", f.autoID: "AUTO",
			f.owner.ws: "WS", f.owner.userID: "OWNER",
		} {
			s = strings.ReplaceAll(s, k, v)
		}
		// Normalize remaining UUIDs (auto-generated from create commands) to UUID placeholder.
		s = anyUUIDRe.ReplaceAllString(s, "UUID")
		// Normalize -0.0 to 0.0 (Rust empty iterator f64::sum returns -0.0).
		s = floatRe.ReplaceAllString(s, "0.0")
		return s
	}
	pick := func(f *fx, n string) who {
		return map[string]who{"owner": f.owner, "member": f.member, "none": {}}[n]
	}

	type step struct {
		who  string
		cmd  string
		in   func(f *fx) any
		note string
	}
	N := func(m func(f *fx) map[string]any) func(f *fx) any {
		return func(f *fx) any { return m(f) }
	}
	steps := []step{
		// sprint.create various shapes
		{"owner", "sprint.create", N(func(f *fx) map[string]any {
			return map[string]any{"space_id": f.space, "name": "New Sprint"}
		}), "simple create"},
		{"owner", "sprint.create", N(func(f *fx) map[string]any {
			return map[string]any{"space_id": f.space, "name": " trimmed ", "goal": "do stuff",
				"start_date": "2026-06-01", "end_date": "2026-06-14"}
		}), "with goal and dates"},
		// empty name
		{"owner", "sprint.create", N(func(f *fx) map[string]any {
			return map[string]any{"space_id": f.space, "name": ""}
		}), "empty name"},
		// missing space_id fallback
		{"owner", "sprint.create", N(func(f *fx) map[string]any {
			return map[string]any{"name": "X"}
		}), "missing space_id"},
		// date range error
		{"owner", "sprint.create", N(func(f *fx) map[string]any {
			return map[string]any{"space_id": f.space, "name": "X",
				"start_date": "2026-03-10", "end_date": "2026-03-01"}
		}), "end before start"},
		// member can create
		{"member", "sprint.create", N(func(f *fx) map[string]any {
			return map[string]any{"space_id": f.space, "name": "Member Sprint"}
		}), "member create"},
		// sprint.start
		{"owner", "sprint.start", N(func(f *fx) map[string]any {
			return map[string]any{"id": f.sprint1ID}
		}), "start sprint1"},
		// sprint.start already active → conflict
		{"owner", "sprint.start", N(func(f *fx) map[string]any {
			return map[string]any{"id": f.sprint2ID}
		}), "second start conflict"},
		// sprint.list filter
		{"owner", "sprint.list", N(func(f *fx) map[string]any {
			return map[string]any{"space_id": f.space, "state": "active"}
		}), "list active"},
		// sprint.complete
		{"owner", "sprint.complete", N(func(f *fx) map[string]any {
			return map[string]any{"id": f.sprint1ID, "move_to_sprint_id": f.sprint2ID}
		}), "complete sprint1"},
		// sprint.complete already closed → conflict
		{"owner", "sprint.complete", N(func(f *fx) map[string]any {
			return map[string]any{"id": f.sprint1ID}
		}), "complete closed"},
		// version.create
		{"owner", "version.create", N(func(f *fx) map[string]any {
			return map[string]any{"space_id": f.space, "name": "2.0"}
		}), "version create"},
		// version.release
		{"owner", "version.release", N(func(f *fx) map[string]any {
			return map[string]any{"id": f.versionID}
		}), "version release"},
		// double release
		{"owner", "version.release", N(func(f *fx) map[string]any {
			return map[string]any{"id": f.versionID}
		}), "double release"},
		// component.create
		{"owner", "component.create", N(func(f *fx) map[string]any {
			return map[string]any{"space_id": f.space, "name": "frontend"}
		}), "component create"},
		// automation.update enable/disable
		{"owner", "automation.update", N(func(f *fx) map[string]any {
			return map[string]any{"id": f.autoID, "enabled": false}
		}), "disable automation"},
		{"owner", "automation.update", N(func(f *fx) map[string]any {
			return map[string]any{"id": f.autoID, "enabled": true, "draft": true}
		}), "re-enable as draft"},
	}

	for _, st := range steps {
		gr := e.cmd(g.base, pick(g, st.who), g.owner.ws, st.cmd, st.in(g), nil)
		rr := e.cmd(r.base, pick(r, st.who), r.owner.ws, st.cmd, st.in(r), nil)
		if gr.status != rr.status || norm(g, gr.body) != norm(r, rr.body) {
			t.Errorf("%s (%s)\n go   %d %s\n rust %d %s",
				st.cmd, st.note, gr.status, norm(g, gr.body), rr.status, norm(r, rr.body))
		}
	}

	// Compare DB state: events emitted for planning commands
	rowsOf := func(f *fx) []string {
		q := func(sql string, args ...any) []string { return e.dbStrings(f.owner.ws, sql, args...) }
		var out []string
		for _, kind := range []string{
			"sprint.created", "sprint.started", "sprint.completed",
			"version.created", "version.released",
			"component.created", "automation.created", "automation.updated",
		} {
			rows := q(fmt.Sprintf(
				"select count(*)::text from core.events where kind = '%s'", kind))
			out = append(out, kind+"="+rows[0])
		}
		// dispatch jobs
		out = append(out, q(`select count(*)::text from core.jobs where kind = 'event.dispatch'`)...)
		return out
	}

	gRows, rRows := rowsOf(g), rowsOf(r)
	if !reflect.DeepEqual(gRows, rRows) {
		t.Errorf("event counts differ\n go   %q\n rust %q", gRows, rRows)
	}

	t.Logf("planning write differential: %d steps completed", len(steps))
}

// TestDifferentialPlanningReportBurndown compares report.burndown byte-for-byte against Rust.
// We skip comparisons involving wall-clock "today" since they are inherently time-sensitive;
// instead we compare completed sprints where start=end (single day, fully determined).
func TestDifferentialPlanningReportBurndown(t *testing.T) {
	e := newEnv(t)
	// Create identical fixtures on both servers, then compare read commands.
	type fx struct {
		base   string
		owner  who
		space  string
		sprint string
	}
	mkFx := func(base string) *fx {
		f := &fx{base: base}
		f.owner = e.signup("BDOwner", true)
		f.space = e.spaceFor(f.owner)
		today := time.Now().UTC().Format("2006-01-02")
		s := e.must(f.owner, "sprint.create", map[string]any{
			"space_id": f.space, "name": "BD Sprint",
			"start_date": today, "end_date": today,
		})
		f.sprint = s["id"].(string)
		e.must(f.owner, "sprint.start", map[string]any{"id": f.sprint})
		return f
	}
	gf, rf := mkFx(e.gosv.URL), mkFx(e.rust)

	negZeroRe := regexp.MustCompile(`-0\.0`)
	normBD := func(f *fx, b []byte) string {
		s := string(b)
		s = tsRe.ReplaceAllString(s, "TS")
		s = strings.ReplaceAll(s, f.sprint, "SPRINT")
		s = strings.ReplaceAll(s, f.space, "SPACE")
		s = negZeroRe.ReplaceAllString(s, "0.0") // Rust may emit -0.0 from empty f64 sums
		return s
	}

	gBD := e.cmd(e.gosv.URL, gf.owner, gf.owner.ws, "report.burndown", map[string]any{"sprint_id": gf.sprint}, nil)
	rBD := e.cmd(e.rust, rf.owner, rf.owner.ws, "report.burndown", map[string]any{"sprint_id": rf.sprint}, nil)
	if gBD.status != rBD.status || normBD(gf, gBD.body) != normBD(rf, rBD.body) {
		t.Errorf("burndown differs\n go   %d %s\n rust %d %s",
			gBD.status, normBD(gf, gBD.body), rBD.status, normBD(rf, rBD.body))
	}
}
