package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
