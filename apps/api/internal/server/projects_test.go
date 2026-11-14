package server_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Ports of the item, workflow and evidence tests in crates/cnote-projects/tests/projects.rs. Every assertion is
// kept; the commands under test run on the Go server (planning commands used for fixtures are proxied to Rust).
type pj struct {
	*env
	owner who
}

func newPj(t *testing.T) *pj {
	e := newEnv(t)
	return &pj{e, e.signup("Tess", true)}
}

func (p *pj) try(w who, cmd string, in any) resp {
	p.t.Helper()
	return p.cmd(p.gosv.URL, w, p.owner.ws, cmd, in, nil)
}

func (p *pj) as(w who, cmd string, in any) map[string]any {
	p.t.Helper()
	r := p.try(w, cmd, in)
	if r.status != 200 {
		p.t.Fatalf("%s failed: %d %s", cmd, r.status, r.body)
	}
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (p *pj) asList(w who, cmd string, in any) []map[string]any {
	p.t.Helper()
	r := p.try(w, cmd, in)
	if r.status != 200 {
		p.t.Fatalf("%s failed: %d %s", cmd, r.status, r.body)
	}
	var m []map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (p *pj) run(cmd string, in any) map[string]any { return p.as(p.owner, cmd, in) }

func (p *pj) space(key string) string {
	return p.run("space.create", map[string]any{"key": key, "name": key, "template": "software"})["id"].(string)
}

func (p *pj) item(space, title string, extra map[string]any) map[string]any {
	in := map[string]any{"space_id": space, "title": title}
	for k, v := range extra {
		in[k] = v
	}
	return p.run("item.create", in)
}

func (p *pj) member(role string) who {
	return p.addMember(p.owner, p.signup("Mo", false), role)
}

func (p *pj) fail(w who, cmd string, in any, status int, code string) resp {
	p.t.Helper()
	r := p.try(w, cmd, in)
	if r.status != status || !strings.Contains(string(r.body), `"code":"`+code+`"`) {
		p.t.Fatalf("%s %v: want %d %s, got %d %s", cmd, in, status, code, r.status, r.body)
	}
	return r
}

func (p *pj) msg(r resp) string {
	var m struct{ Error struct{ Message string } }
	_ = json.Unmarshal(r.body, &m)
	return m.Error.Message
}

func pjKeys(l []map[string]any) []string {
	out := []string{}
	for _, i := range l {
		out = append(out, i["key"].(string))
	}
	return out
}

func pjEq(t *testing.T, got, want any, what string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func TestProjectsConcurrentCreatesGetSequentialKeys(t *testing.T) {
	p := newPj(t)
	space := p.space("ENG")
	var wg sync.WaitGroup
	keys := make([]string, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys[n] = p.item(space, fmt.Sprintf("task %d", n), nil)["key"].(string)
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, k := range keys {
		seen[k] = true
	}
	for n := 1; n <= 8; n++ {
		if !seen[fmt.Sprintf("ENG-%d", n)] {
			t.Fatalf("missing ENG-%d in %v", n, keys)
		}
	}
	first := p.run("item.get", map[string]any{"key": "eng-1"})
	pjEq(t, first["status"], "Backlog", "status")
	pjEq(t, first["status_category"], "todo", "category")
	pjEq(t, first["item_type"], "task", "type")
	pjEq(t, first["reporter_id"], p.owner.userID, "reporter")
	r := p.fail(p.owner, "item.create", map[string]any{"space_id": space, "title": "x", "item_type": "deal"}, 400, "invalid")
	if !strings.Contains(p.msg(r), "epic, story, task, bug, subtask") {
		t.Fatal(p.msg(r))
	}
}

func TestProjectsTransitionGuards(t *testing.T) {
	p := newPj(t)
	space := p.space("ENG")
	def := p.run("workflow.get", map[string]any{"space_id": space})["definition"].(map[string]any)
	for _, tr := range def["transitions"].([]any) {
		tr := tr.(map[string]any)
		if tr["name"] == "Start" {
			tr["guards"] = []any{map[string]any{"type": "required_fields", "fields": []string{"estimate", "assignee"}}}
		}
		if tr["name"] == "Complete" {
			tr["guards"] = []any{map[string]any{"type": "min_role", "role": "admin"}}
		}
	}
	member := p.member("member")
	p.fail(member, "workflow.update", map[string]any{"space_id": space, "definition": def}, 403, "forbidden") // only admins edit workflows
	up := p.run("workflow.update", map[string]any{"space_id": space, "definition": def, "migrate_items": true})
	pjEq(t, up["version"], 2, "version")

	item := p.item(space, "guarded", nil)
	pjEq(t, item["workflow_version"], 2, "item version")
	p.run("item.transition", map[string]any{"key": "ENG-1", "to": "Todo"})
	r := p.fail(p.owner, "item.transition", map[string]any{"key": "ENG-1", "to": "In Progress"}, 400, "invalid")
	if m := p.msg(r); !strings.Contains(m, "estimate") || !strings.Contains(m, "assignee") {
		t.Fatal(m)
	}
	p.run("item.update", map[string]any{"key": "ENG-1", "estimate": 3.0})
	r = p.fail(p.owner, "item.transition", map[string]any{"key": "ENG-1", "to": "In Progress"}, 400, "invalid")
	if m := p.msg(r); !strings.Contains(m, "assignee") || strings.Contains(m, "estimate") {
		t.Fatal(m)
	}
	p.run("item.assign", map[string]any{"key": "ENG-1", "user_id": member.userID})
	moved := p.run("item.transition", map[string]any{"key": "ENG-1", "to": "In Progress"})
	pjEq(t, moved["status"], "In Progress", "status")
	pjEq(t, moved["status_category"], "in_progress", "category")

	// Role guard: members cannot complete, the owner can.
	r = p.fail(member, "item.transition", map[string]any{"key": "ENG-1", "to": "Done"}, 400, "invalid")
	if m := p.msg(r); !strings.Contains(m, "requires role admin") {
		t.Fatal(m)
	}
	done := p.run("item.transition", map[string]any{"key": "ENG-1", "to": "Done"})
	if _, ok := done["resolved_at"].(string); !ok {
		t.Fatal("resolved_at not set")
	}
	// Undefined transition names the allowed targets.
	r = p.fail(p.owner, "item.transition", map[string]any{"key": "ENG-1", "to": "In Review"}, 400, "invalid")
	if m := p.msg(r); !strings.Contains(m, "allowed targets") {
		t.Fatal(m)
	}
	var hist []map[string]any
	h := p.try(p.owner, "note.history", map[string]any{"id": done["id"]})
	_ = json.Unmarshal(h.body, &hist)
	var tr map[string]any
	for _, ev := range hist {
		if ev["kind"] == "item.transitioned" && tr == nil { // like Rust's find: the first match
			tr = ev["payload"].(map[string]any)
		}
	}
	if tr == nil {
		t.Fatal("no item.transitioned event")
	}
	pjEq(t, tr["to"], "Done", "event to")
	pjEq(t, tr["category"], "done", "event category")
	pjEq(t, tr["from"], "In Progress", "event from")
}

func TestProjectsWorkflowVersionsAndDrift(t *testing.T) {
	p := newPj(t)
	space := p.space("ENG")
	old := p.item(space, "old", nil)
	d := p.run("space.drift", map[string]any{"space_id": space})
	pjEq(t, d["in_sync"], true, "in sync")
	pjEq(t, p.run("space.realign", map[string]any{"space_id": space})["changed"], false, "realign no-op")

	def := p.run("workflow.get", map[string]any{"space_id": space})["definition"].(map[string]any)
	def["statuses"] = append(def["statuses"].([]any), map[string]any{"name": "QA", "category": "in_progress"})
	def["transitions"] = append(def["transitions"].([]any), map[string]any{"name": "To QA", "from": []string{"In Review"}, "to": "QA"})
	up := p.run("workflow.update", map[string]any{"space_id": space, "definition": def})
	pjEq(t, up["version"], 2, "version")
	d = p.run("space.drift", map[string]any{"space_id": space})
	pjEq(t, d["in_sync"], false, "drifted")
	diffs, _ := json.Marshal(d["differences"])
	if !strings.Contains(string(diffs), "'QA' is not in the template") || !strings.Contains(string(diffs), "To QA") {
		t.Fatal(string(diffs))
	}

	// The old item keeps v1 (no QA); a new item follows v2.
	nw := p.item(space, "new", nil)
	pjEq(t, []any{old["workflow_version"], nw["workflow_version"]}, []any{1.0, 2.0}, "versions")
	for _, k := range []string{"ENG-1", "ENG-2"} {
		p.run("item.transition", map[string]any{"key": k, "to": "In Progress"})
		p.run("item.transition", map[string]any{"key": k, "to": "In Review"})
	}
	r := p.fail(p.owner, "item.transition", map[string]any{"key": "ENG-1", "to": "QA"}, 400, "invalid")
	if !strings.Contains(p.msg(r), "no transition") {
		t.Fatal(p.msg(r))
	}
	pjEq(t, p.run("item.transition", map[string]any{"key": "ENG-2", "to": "QA"})["status"], "QA", "QA status")

	// Invalid definitions are rejected.
	bad := map[string]any{"statuses": def["statuses"], "transitions": append([]any{map[string]any{"name": "x", "from": "*", "to": "Nowhere"}}, def["transitions"].([]any)...)}
	p.fail(p.owner, "workflow.update", map[string]any{"space_id": space, "definition": bad}, 400, "invalid")

	// Realign applies the template as v3 and migrates items whose status still exists.
	rr := p.run("space.realign", map[string]any{"space_id": space, "migrate_items": true})
	pjEq(t, rr["version"], 3, "realign version")
	pjEq(t, p.run("space.drift", map[string]any{"space_id": space})["in_sync"], true, "in sync again")
	pjEq(t, p.run("item.get", map[string]any{"key": "ENG-1"})["workflow_version"], 3, "ENG-1 migrated")
	// ENG-2 sits in QA, which the template lacks, so it stays on its version.
	pjEq(t, p.run("item.get", map[string]any{"key": "ENG-2"})["workflow_version"], 2, "ENG-2 stays")
}

func TestProjectsPRMergedMovesInReviewToDone(t *testing.T) {
	p := newPj(t)
	space := p.space("ENG")
	p.item(space, "Login page", nil)
	p.item(space, "Other", nil)
	for _, to := range []string{"In Progress", "In Review"} {
		p.run("item.transition", map[string]any{"key": "ENG-1", "to": to})
	}
	r := p.run("evidence.record", map[string]any{"source": "github", "kind": "pr_merged", "item_keys": []string{"ENG-1", "eng-99"}, "url": "https://example.test/pr/7"})
	pjEq(t, r["recorded"], 1, "recorded")
	pjEq(t, r["unknown"], []any{"eng-99"}, "unknown")
	tr := r["transitions"].([]any)[0].(map[string]any)
	pjEq(t, tr["from"], "In Review", "from")
	pjEq(t, tr["to"], "Done", "to")
	item := p.run("item.get", map[string]any{"key": "ENG-1"})
	pjEq(t, item["status"], "Done", "status")
	ev := item["evidence"].([]any)[0].(map[string]any)
	pjEq(t, ev["kind"], "pr_merged", "kind")
	pjEq(t, ev["url"], "https://example.test/pr/7", "url")

	// pr_opened pulls a Todo item into review; unrelated evidence changes nothing.
	r = p.run("evidence.record", map[string]any{"source": "github", "kind": "pr_opened", "item_keys": []string{"ENG-2"}})
	pjEq(t, r["transitions"].([]any)[0].(map[string]any)["to"], "In Review", "pr_opened")
	r = p.run("evidence.record", map[string]any{"source": "ci", "kind": "deploy_succeeded", "item_keys": []string{"ENG-2"}})
	pjEq(t, len(r["transitions"].([]any)), 0, "no transitions")
	pjEq(t, p.run("item.get", map[string]any{"key": "ENG-2"})["status"], "In Review", "still in review")
	p.fail(p.owner, "evidence.record", map[string]any{"source": "x", "kind": "Bad Kind", "item_keys": []string{}}, 400, "invalid")
}

func TestProjectsCNQLListRespectsPrivateSpacesAndFilters(t *testing.T) {
	p := newPj(t)
	open := p.space("ENG")
	private := p.run("space.create", map[string]any{"key": "SEC", "name": "Secret", "template": "software", "visibility": "private"})["id"].(string)
	member := p.member("member")
	p.item(open, "Public login bug", map[string]any{"item_type": "bug", "priority": "high", "labels": []string{"auth"}, "estimate": 2.0})
	p.item(open, "Public docs", map[string]any{"labels": []string{"docs"}, "assignee_id": member.userID})
	p.item(private, "Secret login plan", nil)

	q := func(s string) map[string]any { return map[string]any{"cnql": s} }
	pjEq(t, pjKeys(p.asList(p.owner, "item.list", q("text ~ login"))), []string{"ENG-1", "SEC-1"}, "owner sees private spaces")
	pjEq(t, pjKeys(p.asList(member, "item.list", q("text ~ login"))), []string{"ENG-1"}, "member does not see the private space")
	pjEq(t, len(p.asList(member, "item.list", q("project = SEC"))), 0, "member project = SEC")
	p.fail(member, "item.get", map[string]any{"key": "SEC-1"}, 404, "not_found")
	p.fail(member, "item.update", map[string]any{"key": "SEC-1", "priority": "low"}, 404, "not_found")
	p.fail(member, "item.create", map[string]any{"space_key": "SEC", "title": "sneaky"}, 404, "not_found")

	// Filters and ordering.
	pjEq(t, pjKeys(p.asList(p.owner, "item.list", q("type = bug"))), []string{"ENG-1"}, "type")
	pjEq(t, pjKeys(p.asList(p.owner, "item.list", q("labels IN (docs, nothing) AND project = ENG"))), []string{"ENG-2"}, "labels")
	pjEq(t, pjKeys(p.asList(member, "item.list", q("assignee = currentUser()"))), []string{"ENG-2"}, "currentUser")
	pjEq(t, pjKeys(p.asList(p.owner, "item.list", q("assignee IS EMPTY AND project = ENG"))), []string{"ENG-1"}, "assignee empty")
	pjEq(t, pjKeys(p.asList(p.owner, "item.list", q("priority >= high"))), []string{"ENG-1"}, "priority")
	pjEq(t, pjKeys(p.asList(p.owner, "item.list", q("project = ENG ORDER BY key DESC"))), []string{"ENG-2", "ENG-1"}, "order by key desc")
	pjEq(t, pjKeys(p.asList(p.owner, "item.list", q("created >= -1d AND NOT (type = bug) AND project = ENG"))), []string{"ENG-2"}, "created/not")
	pjEq(t, len(p.asList(p.owner, "item.list", q(`statusCategory = "To Do" AND status != Done AND project = ENG`))), 2, "status category")
	pjEq(t, len(p.asList(p.owner, "item.list", q("statusCategory = Done"))), 0, "no done items")
	pjEq(t, len(p.asList(p.owner, "item.list", map[string]any{"cnql": "project = ENG", "limit": 1})), 1, "limit")
	// Values are binds: an injection attempt is just a string that matches nothing.
	pjEq(t, len(p.asList(p.owner, "item.list", q(`status = "x'; drop table core.notes; --"`))), 0, "injection")
	// Errors carry the position.
	r := p.fail(p.owner, "item.list", q("project = ENG AND stauts = Done"), 400, "invalid")
	if m := p.msg(r); !strings.Contains(m, "unknown field 'stauts'") || !strings.Contains(m, "column 19") {
		t.Fatal(m)
	}
	// Another tenant sees nothing.
	other := newPj(t)
	pjEq(t, len(other.asList(other.owner, "item.list", map[string]any{})), 0, "other tenant")
}

func TestProjectsVersionsComponentsRankAndTriage(t *testing.T) {
	p := newPj(t)
	space := p.space("ENG")
	v := p.run("version.create", map[string]any{"space_id": space, "name": "1.0"})
	c := p.run("component.create", map[string]any{"space_id": space, "name": "api"})
	for n := 0; n < 3; n++ {
		p.item(space, fmt.Sprintf("t%d", n), map[string]any{"fix_version_id": v["id"], "component_id": c["id"]})
	}
	pjEq(t, len(p.asList(p.owner, "item.list", map[string]any{"cnql": `fixVersion = "1.0" AND component = api`})), 3, "fixVersion and component")
	pjEq(t, p.run("version.release", map[string]any{"id": v["id"]})["released"], true, "released")
	p.fail(p.owner, "version.release", map[string]any{"id": v["id"]}, 409, "conflict")
	order := func() []string { return pjKeys(p.asList(p.owner, "item.list", map[string]any{})) }
	pjEq(t, order(), []string{"ENG-1", "ENG-2", "ENG-3"}, "initial order")
	p.run("item.rank", map[string]any{"key": "ENG-3", "before": "ENG-1"})
	pjEq(t, order(), []string{"ENG-3", "ENG-1", "ENG-2"}, "before")
	p.run("item.rank", map[string]any{"key": "ENG-3", "after": "ENG-1"})
	pjEq(t, order(), []string{"ENG-1", "ENG-3", "ENG-2"}, "after")
	p.fail(p.owner, "item.rank", map[string]any{"key": "ENG-3"}, 400, "invalid")

	// Items made by agents land in triage until accepted.
	created := p.run("agent.create", map[string]any{"name": "Triager"})
	agent := who{ws: p.owner.ws, bearer: created["api_key"].(string)}
	filed := p.as(agent, "item.create", map[string]any{"space_id": space, "title": "from agent"})
	pjEq(t, filed["triage"], true, "triage")
	pjEq(t, len(p.asList(p.owner, "triage.list", map[string]any{})), 1, "triage list")
	acc := p.run("triage.accept", map[string]any{"id": filed["id"], "priority": "urgent"})
	pjEq(t, []any{acc["triage"], acc["priority"]}, []any{false, "urgent"}, "accepted")
	pjEq(t, len(p.asList(p.owner, "triage.list", map[string]any{})), 0, "triage empty")
	p.fail(p.owner, "triage.accept", map[string]any{"id": filed["id"]}, 409, "conflict")
	// update: clear and merge custom fields
	u := p.run("item.update", map[string]any{"key": "ENG-1", "due_date": "2030-01-02", "custom": map[string]any{"team": "core"}, "title": "renamed"})
	pjEq(t, []any{u["due_date"], u["custom"].(map[string]any)["team"], u["title"]}, []any{"2030-01-02", "core", "renamed"}, "update")
	u = p.run("item.update", map[string]any{"key": "ENG-1", "clear": []string{"due_date"}})
	if u["due_date"] != nil {
		t.Fatal("due_date not cleared")
	}
	pjEq(t, u["custom"].(map[string]any)["team"], "core", "custom kept")
}

func TestProjectsAgentAssigneeIsRecordedAsActor(t *testing.T) {
	p := newPj(t)
	space := p.space("ENG")
	created := p.run("agent.create", map[string]any{"name": "Coder"})
	agentID := created["agent_id"].(string)
	agent := who{ws: p.owner.ws, bearer: created["api_key"].(string)}
	item := p.item(space, "Implement feature", nil)
	events := func(kind string) [][2]string {
		rows := p.dbStrings(p.owner.ws, "select actor_type || '|' || coalesce(actor_id::text,'') from core.events where note_id = $1::uuid and kind = $2 order by id", item["id"], kind)
		out := [][2]string{}
		for _, r := range rows {
			a, b, _ := strings.Cut(r, "|")
			out = append(out, [2]string{a, b})
		}
		return out
	}

	assigned := p.run("item.assign", map[string]any{"key": "ENG-1", "agent_id": agentID})
	pjEq(t, assigned["assignee_agent_id"], agentID, "agent assignee")
	if assigned["assignee_id"] != nil {
		t.Fatal("assignee_id should be null")
	}
	pjEq(t, len(events("item.assigned_to_agent")), 1, "assigned_to_agent events")
	pjEq(t, len(p.asList(p.owner, "item.list", map[string]any{"cnql": "assignee = Coder"})), 1, "assignee = Coder")

	// The agent works its own item: events are attributed to the agent, on behalf of its owner.
	p.as(agent, "item.transition", map[string]any{"key": "ENG-1", "to": "In Progress"})
	pjEq(t, events("item.transitioned"), [][2]string{{"agent", agentID}}, "transition actor")
	p.as(agent, "item.assign", map[string]any{"key": "ENG-1", "agent_id": agentID})
	ev := events("item.assigned")
	pjEq(t, ev[1], [2]string{"agent", agentID}, "assign actor")
	// Assigning a person afterwards clears the agent.
	back := p.run("item.assign", map[string]any{"key": "ENG-1", "user_id": p.owner.userID})
	if back["assignee_agent_id"] != nil {
		t.Fatal("agent assignee not cleared")
	}
	p.fail(p.owner, "item.assign", map[string]any{"key": "ENG-1", "agent_id": missingID}, 400, "invalid")
	p.fail(p.owner, "item.assign", map[string]any{"key": "ENG-1", "agent_id": agentID, "user_id": p.owner.userID}, 400, "invalid")
}

// Events and jobs the Rust worker consumes: an agent assignment enqueues event.dispatch jobs for both events.
func TestProjectsEventsAndJobsForWorker(t *testing.T) {
	p := newPj(t)
	space := p.space("ENG")
	agentID := p.run("agent.create", map[string]any{"name": "Coder"})["agent_id"].(string)
	p.item(space, "Implement", nil)
	p.run("item.assign", map[string]any{"key": "ENG-1", "agent_id": agentID})
	p.run("item.update", map[string]any{"key": "ENG-1", "priority": "low"})
	p.run("item.transition", map[string]any{"key": "ENG-1", "to": "Todo"})
	p.run("item.rank", map[string]any{"key": "ENG-1", "after": "ENG-1"}) // single item: after itself is a no-op rank
	p.settle(p.owner.ws)
	kinds := p.dbStrings(p.owner.ws, `select e.kind from core.jobs j join core.events e on e.id = (j.payload->>'event_id')::bigint
	   where j.kind = 'event.dispatch' and e.kind like 'item.%' order by j.id`)
	pjEq(t, kinds, []string{"item.created", "item.assigned", "item.assigned_to_agent", "item.updated", "item.transitioned", "item.ranked"}, "dispatch jobs")
}
