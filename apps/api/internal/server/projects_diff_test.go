package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// Twin-fixture differential test for the project item and workflow commands (item.*, triage.*, workflow.*,
// space.drift/realign, evidence.record): the same script runs against fixture A through the Rust server and
// against an identical fixture B through the Go server. Responses (status and body, ids and timestamps
// normalized by order of appearance), the resulting rows, the emitted events and the jobs the Rust worker will
// consume must be equal. Afterwards the read commands are raw-compared (bytes) between both servers.

type pfixture struct {
	owner, member, guest, bob, agent, apikey, scoped, other, noauth, badkey who
	agentID                                                                 string
}

func (e *env) pfixture() pfixture {
	o := e.signup("Owner", true)
	f := pfixture{owner: o, other: e.signup("Other", true), badkey: who{bearer: "cn_x_y"}}
	f.member = e.addMember(o, e.signup("Member", false), "member")
	f.guest = e.addMember(o, e.signup("Guest", false), "guest")
	f.bob = e.addMember(o, e.signup("Bob", false), "member")
	a := e.must(o, "agent.create", map[string]any{"name": "diff-bot"})
	f.agent = who{ws: o.ws, bearer: a["api_key"].(string)}
	f.agentID = a["agent_id"].(string)
	f.apikey = e.apiKey(o)
	f.scoped = who{ws: o.ws, bearer: e.must(o, "apikey.create", map[string]any{"name": "scoped", "scopes": []string{"read", "write_items"}})["api_key"].(string)}
	return f
}

func (f pfixture) who(n string) who {
	return map[string]who{"owner": f.owner, "member": f.member, "guest": f.guest, "bob": f.bob, "agent": f.agent,
		"apikey": f.apikey, "scoped": f.scoped, "other": f.other, "noauth": f.noauth, "badkey": f.badkey}[n]
}

var allCallers = []string{"owner", "member", "guest", "bob", "agent", "apikey", "scoped", "other", "noauth", "badkey"}

type prunner struct {
	e          *env
	base       string
	f          pfixture
	vars       map[string]string
	toks       map[string]string
	transcript []string
}

func (r *prunner) norm(s string) string {
	s = tsTok.ReplaceAllString(s, "TS")
	return uuidTok.ReplaceAllStringFunc(s, func(u string) string {
		u = strings.ReplaceAll(strings.ToLower(u), "-", "")
		if t, ok := r.toks[u]; ok {
			return t
		}
		t := fmt.Sprintf("<id%d>", len(r.toks))
		r.toks[u] = t
		return t
	})
}

func (r *prunner) call(caller, cmd string, in any) map[string]any {
	r.e.t.Helper()
	time.Sleep(25 * time.Millisecond) // stay under the fair-use refill rate (owner, agent and keys share a bucket)
	w := r.f.who(caller)
	res := r.e.cmd(r.base, w, r.f.owner.ws, cmd, in, nil)
	body := string(res.body)
	if (cmd == "item.list" || cmd == "triage.list") && res.status == 200 {
		// rows with equal rank (the first item of every space) come back in physical order, which differs between
		// twin fixtures; the same-fixture raw comparison at the end checks the real order
		var items []json.RawMessage
		if json.Unmarshal(res.body, &items) == nil {
			parts := make([]string, len(items))
			for i, h := range items {
				parts[i] = r.norm(string(h))
			}
			sort.Strings(parts)
			body = strings.Join(parts, "\n")
		}
	}
	r.transcript = append(r.transcript, fmt.Sprintf("%s %s -> %d %s", caller, cmd, res.status, r.norm(body)))
	return res.json()
}

func (r *prunner) raw(caller, cmd, body string) {
	r.e.t.Helper()
	time.Sleep(25 * time.Millisecond)
	w := r.f.who(caller)
	w.ws = r.f.owner.ws
	res := r.e.rawJSON(r.base, w, cmd, body)
	out := string(res.body)
	if (cmd == "item.list" || cmd == "triage.list") && res.status == 200 {
		var items []json.RawMessage
		if json.Unmarshal(res.body, &items) == nil {
			parts := make([]string, len(items))
			for i, h := range items {
				parts[i] = r.norm(string(h))
			}
			sort.Strings(parts)
			out = strings.Join(parts, "\n")
		}
	}
	r.transcript = append(r.transcript, fmt.Sprintf("%s %s RAW %s -> %d %s", caller, cmd, r.norm(body), res.status, r.norm(out)))
}

func (r *prunner) id(name, caller, cmd string, in any) {
	r.e.t.Helper()
	if id, ok := r.call(caller, cmd, in)["id"].(string); ok {
		r.vars[name] = id
	}
}

func (r *prunner) v(n string) string { return r.vars[n] }

const missingID = "00000000-0000-4000-8000-000000000000"

func pscript(r *prunner) {
	f := r.f
	// ---- fixtures: spaces, sprints, versions, components (planning commands are proxied to Rust in both runs)
	r.id("eng", "owner", "space.create", map[string]any{"key": "ENG", "name": "Engineering", "template": "software"})
	r.id("kan", "owner", "space.create", map[string]any{"key": "KAN", "name": "Kanban", "template": "kanban"})
	r.id("prv", "owner", "space.create", map[string]any{"key": "PRV", "name": "Private", "template": "software", "visibility": "private"})
	r.id("doc", "owner", "space.create", map[string]any{"key": "DOC", "name": "Docs", "template": "docs"})
	eng, kan, prv, doc := r.v("eng"), r.v("kan"), r.v("prv"), r.v("doc")
	r.id("s1", "owner", "sprint.create", map[string]any{"space_id": eng, "name": "Sprint 1"})
	r.call("owner", "sprint.start", map[string]any{"id": r.v("s1")})
	r.id("s2", "owner", "sprint.create", map[string]any{"space_id": eng, "name": "Sprint 2"})
	r.id("ks", "owner", "sprint.create", map[string]any{"space_id": kan, "name": "Kanban sprint"})
	r.id("v1", "owner", "version.create", map[string]any{"space_id": eng, "name": "1.0"})
	r.call("owner", "version.release", map[string]any{"id": r.v("v1")})
	r.id("v2", "owner", "version.create", map[string]any{"space_id": eng, "name": "2.0"})
	r.id("c1", "owner", "component.create", map[string]any{"space_id": eng, "name": "api"})
	r.id("kc", "owner", "component.create", map[string]any{"space_id": kan, "name": "kc"})

	// ---- item.create
	r.id("i1", "owner", "item.create", map[string]any{"space_id": eng, "title": "  First task  ", "description": "Login <b>&\"flow\" ünï 🚀\n\n<script>",
		"priority": "high", "labels": []string{"auth", "Backend"}, "estimate": 2.5, "due_date": "2030-01-02",
		"custom":    map[string]any{"team": "core", "n": 1, "nested": map[string]any{"a": []any{1, "x", nil, true}}},
		"sprint_id": r.v("s1"), "fix_version_id": r.v("v2"), "component_id": r.v("c1")})
	r.id("i2", "owner", "item.create", map[string]any{"space_id": eng, "title": "Crash on save", "item_type": " BUG ", "assignee_id": f.member.userID, "priority": "urgent", "estimate": 3, "labels": []string{"auth"}})
	r.id("i3", "owner", "item.create", map[string]any{"space_id": eng, "title": "Epic: onboarding", "item_type": "epic", "triage": false})
	r.id("i4", "member", "item.create", map[string]any{"space_key": " eng ", "title": "Story under epic", "item_type": "story", "parent_id": r.v("i3"), "priority": "low", "due_date": "2029-12-31"})
	r.id("i5", "owner", "item.create", map[string]any{"space_key": "ENG", "title": "Subtask", "item_type": "subtask", "parent_id": r.v("i4"), "priority": "none", "estimate": 0, "assignee_agent_id": f.agentID})
	r.id("i6", "agent", "item.create", map[string]any{"space_id": eng, "title": "Filed by the agent", "description": "triage me"})
	r.id("i7", "apikey", "item.create", map[string]any{"space_id": eng, "title": "Filed by an API key", "triage": false})
	r.id("i8", "scoped", "item.create", map[string]any{"space_id": eng, "title": "Filed by a scoped key", "triage": true, "estimate": -1, "labels": []string{}})
	r.id("i9", "owner", "item.create", map[string]any{"space_id": eng, "title": "Unicode ünï 🚀 日本語 \"q\" <&>", "description": "", "custom": map[string]any{"k": ""}})
	r.id("k1", "owner", "item.create", map[string]any{"space_key": "kan", "title": "Kanban one", "item_type": "bug"})
	r.id("k2", "member", "item.create", map[string]any{"space_key": " KAN ", "title": "Kanban two", "assignee_id": f.bob.userID, "sprint_id": r.v("ks"), "component_id": r.v("kc")})
	r.id("p1", "owner", "item.create", map[string]any{"space_id": prv, "title": "Secret plan"})
	r.id("d1", "owner", "item.create", map[string]any{"space_id": doc, "title": "In a docs space"})
	for _, in := range []map[string]any{
		{"space_id": eng, "title": "x", "item_type": "deal"}, {"space_key": "kan", "title": "x", "item_type": "epic"}, {"space_id": eng, "title": "x", "priority": "Spicy"},
		{"space_id": eng, "title": "x", "assignee_id": f.member.userID, "assignee_agent_id": f.agentID}, {"space_id": eng, "title": "x", "assignee_id": f.other.userID},
		{"space_id": eng, "title": "x", "assignee_agent_id": missingID}, {"space_id": eng, "title": "x", "sprint_id": r.v("ks")}, {"space_id": eng, "title": "x", "fix_version_id": r.v("v1x")},
		{"space_id": eng, "title": "x", "component_id": r.v("kc")}, {"space_id": eng, "title": "x", "component_id": missingID}, {"space_key": "nope", "title": "x"}, {"title": "x"},
		{"space_id": eng, "title": ""}, {"space_id": eng, "title": "   \t "}, {"space_id": kan, "title": "x", "parent_id": r.v("i3")}, {"space_id": eng, "title": "x", "parent_id": missingID},
		{"space_id": missingID, "title": "x"}, {"space_id": eng, "title": "x", "custom": map[string]any{}}, {"space_id": eng, "title": "nul\u0000"},
		{"space_id": eng, "title": "x", "due_date": "2024-02-30"}, {"space_id": eng, "title": "x", "estimate": 1e30}, {"space_id": eng, "title": "x", "estimate": 0.1},
		{"space_id": eng, "title": "x", "labels": []string{"a", "a", "ünï"}, "item_type": "Épic"}, {"space_id": prv, "title": "x"}, {"space_id": eng, "title": "tabs\tand\nnewlines", "item_type": "TASK"},
		{"space_id": eng, "title": "x", "space_key": "kan"}, {"space_id": eng, "title": "x", "space_key": nil, "item_type": nil, "triage": nil},
	} {
		r.call("owner", "item.create", in)
	}
	for _, c := range []string{"member", "guest", "bob", "other", "noauth", "badkey"} {
		r.call(c, "item.create", map[string]any{"space_id": eng, "title": "by " + c})
	}
	r.call("member", "item.create", map[string]any{"space_id": prv, "title": "member in private"})
	r.call("member", "item.create", map[string]any{"space_key": "prv", "title": "member in private by key"})
	r.call("owner", "item.create", map[string]any{"space_id": eng, "title": "x", "parent_id": r.v("p1")})
	for _, raw := range []string{
		`{"space_id":"` + eng + `"}`, `{"title":"x"}`, `{"space_id":"` + eng + `","title":5}`, `{"space_id":"nope","title":"x"}`, `{"space_id":"` + eng + `","title":null}`,
		`{"space_id":"` + eng + `","title":"x","labels":null}`, `{"space_id":"` + eng + `","title":"x","labels":[1]}`, `{"space_id":"` + eng + `","title":"x","estimate":"3"}`,
		`{"space_id":"` + eng + `","title":"x","estimate":1e999}`, `{"space_id":"` + eng + `","title":"x","estimate":true}`, `{"space_id":"` + eng + `","title":"x","due_date":"2024-1-5"}`,
		`{"space_id":"` + eng + `","title":"x","due_date":"nope"}`, `{"space_id":"` + eng + `","title":"x","triage":"yes"}`, `{"space_id":"` + eng + `","title":"x","custom":{"f":1.5}}`,
		`{"space_id":"` + eng + `","title":"x","custom":[1,2]}`, `{"space_id":"` + eng + `","title":"x","custom":"text"}`, `{"space_id":"` + eng + `","title":"\ud800"}`,
		`{"space_id":"` + strings.ReplaceAll(eng, "-", "") + `","title":"x"}`, `{"space_id":"` + strings.ToUpper(eng) + `","title":"uppercase uuid"}`, `{"Space_ID":"` + eng + `","title":"x"}`,
		`{"space_id":"` + eng + `","title":"x","assignee_id":"nope"}`, `{"space_id":"` + eng + `","title":"x","priority":5}`, `null`, `[]`, `5`, `{bad`,
	} {
		r.raw("owner", "item.create", raw)
	}

	// ---- item.get / item.list / triage.list before changes
	for _, c := range allCallers {
		for _, in := range []any{map[string]any{"key": "ENG-1"}, map[string]any{"key": "eng-2"}, map[string]any{"id": r.v("i1")}, map[string]any{"id": r.v("p1")}, map[string]any{"key": "PRV-1"},
			map[string]any{"key": "NOPE-1"}, map[string]any{"id": missingID}, map[string]any{}, map[string]any{"id": nil, "key": nil}, map[string]any{"id": r.v("i1"), "key": "eng-1"}, map[string]any{"key": "KAN-1"}} {
			r.call(c, "item.get", in)
		}
	}
	for _, raw := range []string{`{"id":"nope"}`, `{"key":5}`, `{"id":5}`, `[]`, `null`, `{"key":"ENG-1","id":"` + strings.ToUpper(r.v("i1")) + `"}`, `{"id":"` + strings.ReplaceAll(r.v("i1"), "-", "") + `"}`} {
		r.raw("owner", "item.get", raw)
	}

	// ---- item.update
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "priority": "low", "labels": []string{"x"}, "estimate": 5, "due_date": "2031-05-06", "custom": map[string]any{"team": "edge", "extra": 2}})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "title": "  Renamed first  ", "description": "new body ünï"})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "title": "", "description": ""})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "title": "Renamed first"})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1"})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "title": nil, "description": nil, "priority": nil, "labels": nil, "estimate": nil, "custom": nil, "clear": []string{}})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "clear": []string{"estimate", "due_date", "sprint", "fix_version", "component"}})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "clear": []string{"estimate"}, "estimate": 8})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "sprint_id": r.v("s2"), "fix_version_id": r.v("v1"), "component_id": r.v("c1")})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "labels": []string{}})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "item_type": " STORY "})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "item_type": "deal"})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "priority": "bogus"})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "custom": 5})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "custom": []int{1}})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "clear": []string{"labels"}})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "sprint_id": r.v("ks")})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "fix_version_id": missingID})
	r.call("owner", "item.update", map[string]any{"key": "ENG-1", "component_id": r.v("kc")})
	r.call("owner", "item.update", map[string]any{"id": r.v("i1"), "custom": map[string]any{"team": nil, "nested": map[string]any{"z": 1}}})
	r.call("owner", "item.update", map[string]any{"key": "ENG-2", "estimate": 0, "due_date": "2000-02-29", "description": "x"})
	r.call("owner", "item.update", map[string]any{"key": "ENG-99", "priority": "low"})
	r.call("owner", "item.update", map[string]any{"priority": "low"})
	r.call("member", "item.update", map[string]any{"key": "ENG-1", "priority": "medium", "title": "by member"})
	r.call("member", "item.update", map[string]any{"key": "PRV-1", "priority": "low"})
	r.call("guest", "item.update", map[string]any{"key": "ENG-1", "priority": "low"})
	r.call("agent", "item.update", map[string]any{"key": "ENG-6", "priority": "high", "title": "agent retitle", "description": "agent text"})
	r.call("apikey", "item.update", map[string]any{"key": "ENG-7", "labels": []string{"k"}})
	r.call("scoped", "item.update", map[string]any{"key": "ENG-8", "labels": []string{"scoped"}})
	r.call("scoped", "item.update", map[string]any{"key": "ENG-8", "title": "scoped retitle"}) // nested note.update needs write_notes
	r.call("other", "item.update", map[string]any{"key": "ENG-1", "priority": "low"})
	r.call("noauth", "item.update", map[string]any{"key": "ENG-1", "priority": "low"})
	r.call("badkey", "item.update", map[string]any{"key": "ENG-1", "priority": "low"})
	for _, raw := range []string{`{"key":"ENG-1","priority":5}`, `{"key":"ENG-1","labels":"a"}`, `{"key":"ENG-1","labels":[1]}`, `{"key":"ENG-1","clear":null}`, `{"key":"ENG-1","clear":[1]}`,
		`{"key":"ENG-1","estimate":"3"}`, `{"key":"ENG-1","due_date":"2024-13-45"}`, `{"key":"ENG-1","sprint_id":"nope"}`, `{"key":"ENG-1","custom":{"f":1.25}}`, `{"key":"ENG-1","title":"\ud800"}`,
		`{"key":"ENG-1","custom":{"big":99999999999999999999}}`, `{}`, `[]`} {
		r.raw("owner", "item.update", raw)
	}

	// ---- item.assign
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1", "user_id": f.bob.userID})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1", "agent_id": f.agentID})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1", "user_id": f.member.userID})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1"})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1", "user_id": nil, "agent_id": nil})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1", "user_id": f.member.userID, "agent_id": f.agentID})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1", "user_id": f.other.userID})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1", "user_id": missingID})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-1", "agent_id": missingID})
	r.call("owner", "item.assign", map[string]any{"id": r.v("i4"), "agent_id": f.agentID})
	r.call("owner", "item.assign", map[string]any{"id": r.v("i4"), "agent_id": f.agentID})
	r.call("member", "item.assign", map[string]any{"key": "ENG-3", "user_id": f.member.userID})
	r.call("agent", "item.assign", map[string]any{"key": "ENG-6", "agent_id": f.agentID})
	r.call("apikey", "item.assign", map[string]any{"key": "ENG-7", "user_id": f.owner.userID})
	r.call("scoped", "item.assign", map[string]any{"key": "ENG-8", "user_id": f.bob.userID})
	r.call("guest", "item.assign", map[string]any{"key": "ENG-1", "user_id": f.bob.userID})
	r.call("member", "item.assign", map[string]any{"key": "PRV-1", "user_id": f.bob.userID})
	r.call("other", "item.assign", map[string]any{"key": "ENG-1", "user_id": f.bob.userID})
	r.call("noauth", "item.assign", map[string]any{"key": "ENG-1", "user_id": f.bob.userID})
	r.call("owner", "item.assign", map[string]any{"key": "ENG-77", "user_id": f.bob.userID})
	r.call("owner", "item.assign", map[string]any{"key": "KAN-2", "user_id": f.bob.userID})
	for _, raw := range []string{`{"key":"ENG-1","user_id":"nope"}`, `{"key":"ENG-1","agent_id":5}`, `{"user_id":"` + f.bob.userID + `"}`, `{"key":"ENG-1","user_id":"` + strings.ToUpper(f.bob.userID) + `"}`} {
		r.raw("owner", "item.assign", raw)
	}

	// ---- item.rank
	for _, in := range []map[string]any{
		{"key": "ENG-3", "before": "ENG-1"}, {"key": "ENG-3", "after": "ENG-1"}, {"key": "ENG-3", "after": "eng-2", "before": "ENG-4"}, {"key": "ENG-3", "after": "ENG-5", "before": "ENG-1"},
		{"key": "ENG-3"}, {"key": "ENG-3", "after": nil, "before": nil}, {"key": "ENG-3", "after": "ENG-3"}, {"key": "ENG-3", "before": "ENG-3"}, {"key": "ENG-3", "after": "ENG-3", "before": "ENG-3"},
		{"key": "ENG-3", "after": "NOPE-1"}, {"key": "ENG-3", "before": "KAN-1"}, {"key": "ENG-3", "after": r.v("i2")}, {"key": "ENG-3", "before": strings.ToUpper(r.v("i2"))}, {"key": "ENG-3", "after": ""},
		{"key": "ENG-3", "after": "ENG-9"}, {"key": "ENG-9", "before": "ENG-1"}, {"key": "ENG-9", "before": "ENG-1"}, {"key": "ENG-9", "after": "ENG-1", "before": "ENG-1"},
		{"id": r.v("i5"), "after": "ENG-9"}, {"key": "ENG-77", "after": "ENG-1"}, {"after": "ENG-1"}, {"key": "PRV-1", "after": "ENG-1"},
	} {
		r.call("owner", "item.rank", in)
	}
	for _, c := range []string{"member", "guest", "agent", "apikey", "scoped", "other", "noauth", "badkey"} {
		r.call(c, "item.rank", map[string]any{"key": "ENG-6", "before": "ENG-1"})
	}
	r.call("member", "item.rank", map[string]any{"key": "PRV-1", "before": "ENG-1"})
	r.raw("owner", "item.rank", `{"key":"ENG-3","after":5}`)
	r.raw("owner", "item.rank", `{"key":"ENG-3","after":"ENG-1","before":"ENG-2","extra":1}`)
	for i := 0; i < 8; i++ { // repeated inserts at the same spot exercise the fractional ranks
		r.call("owner", "item.rank", map[string]any{"key": fmt.Sprintf("ENG-%d", 4+i%4), "after": "ENG-1", "before": "ENG-2"})
	}

	// ---- triage
	for _, c := range allCallers {
		for _, in := range []any{map[string]any{}, map[string]any{"limit": 1}, map[string]any{"limit": 0}, map[string]any{"limit": -3}, map[string]any{"limit": 1000000}, map[string]any{"limit": nil}} {
			r.call(c, "triage.list", in)
		}
	}
	r.raw("owner", "triage.list", `{"limit":1.5}`)
	r.raw("owner", "triage.list", `{"limit":"5"}`)
	r.raw("owner", "triage.list", `[]`)
	r.call("owner", "triage.list", nil)
	r.call("member", "triage.accept", map[string]any{"key": "ENG-6", "priority": "urgent"})
	r.call("owner", "triage.accept", map[string]any{"key": "ENG-6", "priority": "bogus"})
	r.call("owner", "triage.accept", map[string]any{"key": "ENG-6", "assignee_id": f.bob.userID, "priority": "high"})
	r.call("owner", "triage.accept", map[string]any{"key": "ENG-6"})
	r.call("owner", "triage.accept", map[string]any{"key": "ENG-3"})
	r.call("owner", "triage.accept", map[string]any{"id": r.v("i7")})
	r.call("owner", "triage.accept", map[string]any{"key": "ENG-8", "assignee_agent_id": f.agentID})
	r.call("owner", "triage.accept", map[string]any{"key": "ENG-9", "assignee_id": f.member.userID, "assignee_agent_id": f.agentID})
	r.call("owner", "triage.accept", map[string]any{"key": "ENG-9", "assignee_id": f.other.userID})
	r.call("owner", "triage.accept", map[string]any{"key": "ENG-9", "priority": "none"})
	r.call("guest", "triage.accept", map[string]any{"key": "ENG-9"})
	r.call("other", "triage.accept", map[string]any{"key": "ENG-9"})
	r.call("noauth", "triage.accept", map[string]any{"key": "ENG-9"})
	r.call("owner", "triage.accept", map[string]any{"key": "NOPE-1"})
	r.call("owner", "triage.accept", map[string]any{})
	r.raw("owner", "triage.accept", `{"key":"ENG-9","priority":5}`)

	// ---- workflow.get / space.drift / space.realign (read and admin gates)
	for _, c := range allCallers {
		for _, sp := range []string{eng, kan, prv, doc, missingID} {
			r.call(c, "workflow.get", map[string]any{"space_id": sp})
			r.call(c, "space.drift", map[string]any{"space_id": sp})
		}
	}
	for _, raw := range []string{`{}`, `{"space_id":"nope"}`, `{"space_id":null}`, `[]`} {
		r.raw("owner", "workflow.get", raw)
		r.raw("owner", "space.drift", raw)
		r.raw("owner", "space.realign", raw)
		r.raw("owner", "workflow.update", raw)
	}
	for _, c := range []string{"member", "guest", "agent", "apikey", "scoped", "other", "noauth", "badkey"} {
		r.call(c, "space.realign", map[string]any{"space_id": kan})
		r.call(c, "workflow.update", map[string]any{"space_id": kan, "definition": map[string]any{"statuses": []any{}, "transitions": []any{}}})
	}
	r.call("owner", "space.realign", map[string]any{"space_id": kan}) // in sync: nothing changes
	r.call("owner", "space.realign", map[string]any{"space_id": prv, "migrate_items": nil})

	// ---- transitions on the software template
	for _, in := range []map[string]any{
		{"key": "ENG-2", "to": "Todo"}, {"key": "ENG-2", "to": "in progress"}, {"key": "ENG-2", "to": "Request review"}, {"key": "ENG-2", "to": "Done"}, {"key": "ENG-2", "to": "Todo"},
		{"key": "ENG-2", "to": "Backlog"}, {"key": "ENG-2", "to": "Cancelled"}, {"key": "ENG-2", "to": "Reopen"}, {"key": "ENG-2", "to": "Nowhere"}, {"key": "ENG-2", "to": ""},
		{"key": "ENG-2", "to": "Cancel"}, {"key": "ENG-2", "to": "In Review"}, {"key": "ENG-3", "to": "PR merged"}, {"key": "ENG-3", "to": "pr opened"}, {"key": "ENG-3", "to": "Done"},
		{"id": r.v("i4"), "to": "Todo"}, {"key": "ENG-4", "to": "Todo"}, {"key": "ENG-77", "to": "Todo"}, {"key": "PRV-1", "to": "Todo"}, {"to": "Todo"},
	} {
		r.call("owner", "item.transition", in)
	}
	for _, c := range []string{"member", "guest", "bob", "agent", "apikey", "scoped", "other", "noauth", "badkey"} {
		r.call(c, "item.transition", map[string]any{"key": "ENG-5", "to": "Todo"})
	}
	r.call("member", "item.transition", map[string]any{"key": "PRV-1", "to": "Todo"})
	r.raw("owner", "item.transition", `{"key":"ENG-5"}`)
	r.raw("owner", "item.transition", `{"key":"ENG-5","to":5}`)
	r.raw("owner", "item.transition", `{"key":"ENG-5","to":null}`)

	// ---- evidence.record
	for _, in := range []map[string]any{
		{"source": "github", "kind": "pr_opened", "item_keys": []string{"ENG-5"}, "url": "https://example.test/pr/1"},
		{"source": "github", "kind": "pr_merged", "item_keys": []string{"ENG-5", "eng-4", "NOPE-9", "PRV-1", "ENG-4"}, "url": "https://example.test/pr/2"},
		{"source": " github ", "kind": " pr_merged ", "item_keys": []string{"ENG-1"}},
		{"source": "ci", "kind": "deploy_succeeded", "item_keys": []string{"ENG-1", "ENG-2"}, "url": nil},
		{"source": "ci", "kind": "pr_opened", "item_keys": []string{"ENG-9", "KAN-1"}},
		{"source": "ci", "kind": "pr_merged", "item_keys": []string{}}, {"source": "x", "kind": "Bad Kind", "item_keys": []string{}}, {"source": "x", "kind": "", "item_keys": []string{"ENG-1"}},
		{"source": "x", "kind": "UPPER", "item_keys": []string{}}, {"source": "x", "kind": "ünï", "item_keys": []string{}}, {"source": "  ", "kind": "pr_merged", "item_keys": []string{"ENG-1"}},
		{"source": "", "kind": "pr_merged", "item_keys": []string{}},
		{"source": "ci", "kind": "pr_merged", "item_keys": []string{"KAN-1", "KAN-2"}, "url": "https://example.test/pr/3"},
	} {
		r.call("owner", "evidence.record", in)
	}
	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf("ENG-%d", i+1)
	}
	r.call("owner", "evidence.record", map[string]any{"source": "ci", "kind": "pr_opened", "item_keys": many})
	r.call("owner", "evidence.record", map[string]any{"source": "ci", "kind": "pr_opened", "item_keys": many[:50]})
	for _, c := range []string{"member", "guest", "bob", "agent", "apikey", "scoped", "other", "noauth", "badkey"} {
		r.call(c, "evidence.record", map[string]any{"source": "ci", "kind": "build_ok", "item_keys": []string{"ENG-7"}})
	}
	r.call("member", "evidence.record", map[string]any{"source": "ci", "kind": "build_ok", "item_keys": []string{"PRV-1", "ENG-7"}})
	for _, raw := range []string{`{"source":"x","kind":"y"}`, `{"source":"x","kind":"y","item_keys":null}`, `{"source":"x","kind":"y","item_keys":[1]}`, `{"source":5,"kind":"y","item_keys":[]}`,
		`{"source":"x","kind":"y","item_keys":[],"url":5}`, `[]`} {
		r.raw("owner", "evidence.record", raw)
	}
	r.call("owner", "item.get", map[string]any{"key": "ENG-5"})

	// ---- workflow.update on the kanban space, then guards, post actions and migration
	def := map[string]any{
		"types": []string{"task", "bug"},
		"statuses": []any{map[string]any{"name": "To Do", "category": "todo"}, map[string]any{"name": "Doing", "category": "in_progress"},
			map[string]any{"name": "Review", "category": "in_progress"}, map[string]any{"name": "Done", "category": "done"}},
		"transitions": []any{
			map[string]any{"name": "Start", "from": []string{"To Do"}, "to": "Doing", "guards": []any{map[string]any{"type": "required_fields", "fields": []string{"estimate", "assignee"}}}},
			map[string]any{"name": "Submit", "from": []string{"Doing"}, "to": "Review", "post_actions": []any{
				map[string]any{"type": "set_field", "field": "priority", "value": "high"}, map[string]any{"type": "set_field", "field": "estimate", "value": 5},
				map[string]any{"type": "set_field", "field": "due_date", "value": "2031-02-03"}, map[string]any{"type": "set_field", "field": "labels", "value": []string{"x", "y"}},
				map[string]any{"type": "set_field", "field": "team", "value": map[string]any{"a": 1}}, map[string]any{"type": "assign_reporter"},
				map[string]any{"type": "emit", "event": "custom.submitted"}}},
			map[string]any{"name": "Ship", "from": "*", "to": "Done", "guards": []any{map[string]any{"type": "min_role", "role": "admin"}}},
			map[string]any{"name": "Merge", "from": []string{"Review"}, "to": "Done", "guards": []any{map[string]any{"type": "evidence", "event": "pr_merged"}}},
			map[string]any{"name": "Reopen", "from": []string{"Done"}, "to": "To Do"},
			map[string]any{"name": "Bad priority", "from": []string{"Review"}, "to": "Doing", "post_actions": []any{map[string]any{"type": "set_field", "field": "priority", "value": 5}}},
			map[string]any{"name": "Bad priority 2", "from": []string{"Review"}, "to": "Doing", "post_actions": []any{map[string]any{"type": "set_field", "field": "priority", "value": "bogus"}}},
			map[string]any{"name": "Bad estimate", "from": []string{"Review"}, "to": "Doing", "post_actions": []any{map[string]any{"type": "set_field", "field": "estimate", "value": "x"}}},
			map[string]any{"name": "Null estimate", "from": []string{"Review"}, "to": "Doing", "post_actions": []any{map[string]any{"type": "set_field", "field": "estimate", "value": nil}, map[string]any{"type": "set_field", "field": "due_date", "value": nil}}},
			map[string]any{"name": "Bad date", "from": []string{"Review"}, "to": "Doing", "post_actions": []any{map[string]any{"type": "set_field", "field": "due_date", "value": "nope"}}},
			map[string]any{"name": "Bad date 2", "from": []string{"Review"}, "to": "Doing", "post_actions": []any{map[string]any{"type": "set_field", "field": "due_date", "value": 5}}},
			map[string]any{"name": "Loose date", "from": []string{"Review"}, "to": "Doing", "post_actions": []any{map[string]any{"type": "set_field", "field": "due_date", "value": " 2032-1-2 "}}},
			map[string]any{"name": "Bad labels", "from": []string{"Review"}, "to": "Doing", "post_actions": []any{map[string]any{"type": "set_field", "field": "labels", "value": 5}}},
		},
	}
	r.call("owner", "workflow.update", map[string]any{"space_id": kan, "definition": def, "migrate_items": true})
	r.call("owner", "workflow.get", map[string]any{"space_id": kan})
	r.call("owner", "space.drift", map[string]any{"space_id": kan})
	r.id("k3", "member", "item.create", map[string]any{"space_key": "KAN", "title": "Kanban three", "estimate": 2, "assignee_id": f.bob.userID})
	for _, in := range []map[string]any{
		{"key": "KAN-1", "to": "Doing"}, {"key": "KAN-3", "to": "Doing"}, {"key": "KAN-3", "to": "Review"}, {"key": "KAN-3", "to": "Done"}, {"key": "KAN-3", "to": "Merge"}, {"key": "KAN-3", "to": "Bad priority"},
		{"key": "KAN-3", "to": "Bad priority 2"}, {"key": "KAN-3", "to": "Bad estimate"}, {"key": "KAN-3", "to": "Null estimate"}, {"key": "KAN-3", "to": "Review"}, {"key": "KAN-3", "to": "Bad date"},
		{"key": "KAN-3", "to": "Bad date 2"}, {"key": "KAN-3", "to": "Loose date"}, {"key": "KAN-3", "to": "Bad labels"}, {"key": "KAN-3", "to": "Review"}, {"key": "KAN-3", "to": "Ship"}, {"key": "KAN-3", "to": "Reopen"},
	} {
		r.call("owner", "item.transition", in)
	}
	r.call("member", "item.transition", map[string]any{"key": "KAN-2", "to": "Ship"})
	r.call("member", "item.transition", map[string]any{"key": "KAN-2", "to": "Start"})
	r.call("owner", "item.update", map[string]any{"key": "KAN-2", "estimate": 1})
	r.call("member", "item.transition", map[string]any{"key": "KAN-2", "to": "Start"})
	r.call("member", "item.transition", map[string]any{"key": "KAN-2", "to": "Submit"})
	r.call("owner", "evidence.record", map[string]any{"source": "github", "kind": "pr_merged", "item_keys": []string{"KAN-2", "KAN-3", "KAN-1"}})
	r.call("owner", "item.get", map[string]any{"key": "KAN-2"})
	r.call("owner", "item.get", map[string]any{"key": "KAN-3"})
	r.call("owner", "space.realign", map[string]any{"space_id": kan, "migrate_items": true})
	r.call("owner", "space.realign", map[string]any{"space_id": kan})
	r.call("owner", "item.get", map[string]any{"key": "KAN-2"})
	r.call("owner", "item.get", map[string]any{"key": "KAN-1"})
	// invalid definitions: every validation message
	base := func(mut func(map[string]any)) map[string]any {
		d := map[string]any{"statuses": []any{map[string]any{"name": "A", "category": "todo"}, map[string]any{"name": "B", "category": "done"}},
			"transitions": []any{map[string]any{"name": "Go", "from": []string{"A"}, "to": "B"}}}
		mut(d)
		return d
	}
	tr := func(d map[string]any, i int) map[string]any { return d["transitions"].([]any)[i].(map[string]any) }
	for _, mut := range []func(map[string]any){
		func(d map[string]any) {},
		func(d map[string]any) { d["statuses"] = []any{} },
		func(d map[string]any) {
			d["statuses"] = append(d["statuses"].([]any), map[string]any{"name": " ", "category": "todo"})
		},
		func(d map[string]any) {
			d["statuses"] = append(d["statuses"].([]any), map[string]any{"name": "b", "category": "todo"})
		},
		func(d map[string]any) { tr(d, 0)["to"] = "Nowhere" },
		func(d map[string]any) { tr(d, 0)["from"] = "all" },
		func(d map[string]any) { tr(d, 0)["from"] = []string{"Z"} },
		func(d map[string]any) {
			d["transitions"] = append(d["transitions"].([]any), map[string]any{"name": "GO", "from": "*", "to": "A"})
		},
		func(d map[string]any) {
			tr(d, 0)["guards"] = []any{map[string]any{"type": "required_fields", "fields": []string{}}}
		},
		func(d map[string]any) { tr(d, 0)["guards"] = []any{map[string]any{"type": "min_role", "role": "boss"}} },
		func(d map[string]any) { tr(d, 0)["guards"] = []any{map[string]any{"type": "evidence", "event": " "}} },
		func(d map[string]any) { tr(d, 0)["post_actions"] = []any{map[string]any{"type": "emit", "event": ""}} },
		func(d map[string]any) { d["types"] = []string{"a", "b"} },
		func(d map[string]any) {
			d["statuses"] = []any{map[string]any{"name": "ünï ΣΑΣ", "category": "in_progress"}, map[string]any{"name": "ΑΣ", "category": "done"}}
			tr(d, 0)["from"] = "*"
			tr(d, 0)["to"] = "ΑΣ"
		},
		func(d map[string]any) { d["transitions"] = []any{} },
	} {
		r.call("owner", "workflow.update", map[string]any{"space_id": doc, "definition": base(mut)})
	}
	r.call("owner", "workflow.update", map[string]any{"space_id": missingID, "definition": base(func(map[string]any) {})})
	r.call("owner", "workflow.update", map[string]any{"space_id": prv, "definition": base(func(map[string]any) {}), "migrate_items": true})
	r.call("owner", "item.get", map[string]any{"key": "PRV-1"})
	r.call("owner", "item.transition", map[string]any{"key": "PRV-1", "to": "B"})
	r.call("owner", "space.drift", map[string]any{"space_id": doc})
	r.call("owner", "space.realign", map[string]any{"space_id": doc, "migrate_items": true})
	r.call("owner", "item.get", map[string]any{"key": "DOC-1"})
	for _, raw := range []string{
		`{"space_id":"` + doc + `","definition":null}`, `{"space_id":"` + doc + `"}`, `{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"nope"}],"transitions":[]}}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"todo"}],"transitions":[{"name":"x","from":5,"to":"A"}]}}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"todo"}],"transitions":[],"types":null}}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"todo"}],"transitions":[{"name":"x","from":"*","to":"A","post_actions":[{"type":"set_field","field":"f","value":1.5}]}]}}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"todo"}],"transitions":[{"name":"x","from":"*","to":"A","post_actions":[{"type":"set_field","field":"f"}]}]}}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"todo"}],"transitions":[{"name":"x","from":"*","to":"A","guards":[{"type":"nope"}]}]}}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"todo"}],"transitions":[]},"migrate_items":null}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"todo"}],"transitions":[]},"migrate_items":"yes"}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"A","category":"todo","extra":1}],"transitions":[],"unknown":true}}`,
		`{"space_id":"` + doc + `","definition":{"statuses":[{"name":"\ud800","category":"todo"}],"transitions":[]}}`,
	} {
		r.raw("owner", "workflow.update", raw)
	}
	r.call("owner", "workflow.get", map[string]any{"space_id": doc})
	r.call("owner", "workflow.get", map[string]any{"space_id": kan})

	// ---- item.list with a handful of queries for every caller (the big corpus runs in TestDifferentialCNQLCorpus)
	for _, c := range allCallers {
		for _, q := range []any{map[string]any{}, map[string]any{"cnql": ""}, map[string]any{"cnql": "project = ENG"}, map[string]any{"cnql": "assignee = currentUser()"},
			map[string]any{"cnql": "assignee = Bob ORDER BY key DESC"}, map[string]any{"cnql": "statusCategory != Done ORDER BY key", "limit": 3}, map[string]any{"cnql": "text ~ secret"},
			map[string]any{"cnql": "project = ENG AND stauts = Done"}, map[string]any{"cnql": "created >= -1d ORDER BY priority DESC, updated"}, map[string]any{"cnql": "ORDER BY key", "limit": 0}, map[string]any{"limit": 100000},
			map[string]any{"cnql": nil, "limit": nil}} {
			r.call(c, "item.list", q)
		}
	}
	r.raw("owner", "item.list", `{"cnql":5}`)
	r.raw("owner", "item.list", `{"limit":1.5}`)
	r.raw("owner", "item.list", `{"cnql":"project = ENG","limit":"3"}`)
	r.raw("owner", "item.list", `[]`)
	r.raw("owner", "item.list", `{"cnql":"status = \ud800"}`)
	r.call("owner", "item.list", nil)
}

// pdump reads the rows the commands touch, normalized, in a deterministic order, plus the emitted events and the
// event.dispatch jobs the Rust worker consumes (as the kind of the event each job points at).
func (r *prunner) pdump() []string {
	r.e.t.Helper()
	tx, err := dbTenant(r.e, r.f.owner.ws)
	if err != nil {
		r.e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var out []string
	for _, q := range []struct{ name, sql string }{
		{"spaces", "select (to_jsonb(s) - 'created_at')::text from core.spaces s order by s.key"},
		{"items", "select to_jsonb(i)::text from projects.items i order by i.key"},
		{"item_notes", "select (to_jsonb(n) - 'search')::text from core.notes n where n.id in (select note_id from projects.items) order by (select key from projects.items where note_id = n.id)"},
		{"workflows", "select (to_jsonb(w) - 'id' - 'created_at')::text from projects.workflows w order by (select key from core.spaces where id = w.space_id), w.version"},
		{"evidence", "select (to_jsonb(x) - 'id' - 'created_at')::text from projects.evidence x order by (select key from projects.items where note_id = x.note_id), x.kind, x.source, coalesce(x.url,'')"},
		{"events", "select (to_jsonb(x) - 'id')::text from core.events x where x.actor_type in ('user','agent') and x.kind not like 'doc.%' and x.kind not like 'sprint.%' and x.kind not like 'version.%' and x.kind not like 'component.%' order by x.id"},
		{"jobs", `select e.kind || ' ' || e.actor_type from core.jobs j join core.events e on e.id = (j.payload->>'event_id')::bigint
		           where j.kind = 'event.dispatch' and e.actor_type in ('user','agent') and e.kind not like 'doc.%' order by j.id`},
		{"usage", "select (to_jsonb(u) - 'id' - 'created_at' - 'occurred_at')::text from core.usage u where u.kind = 'mcp.call' order by u.meta::text, u.kind"},
	} {
		rows, err := tx.Query(context.Background(), q.sql)
		if err != nil {
			r.e.t.Fatalf("%s: %v", q.name, err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				r.e.t.Fatal(err)
			}
			out = append(out, q.name+" "+r.norm(s))
		}
		rows.Close()
	}
	return out
}

func TestDifferentialProjectItems(t *testing.T) {
	e := newEnv(t)
	run := func(base string) *prunner {
		r := &prunner{e: e, base: base, f: e.pfixture(), vars: map[string]string{}, toks: map[string]string{}}
		pscript(r)
		e.settle(r.f.owner.ws)
		r.transcript = append(r.transcript, r.pdump()...)
		return r
	}
	rust, gov := run(e.rust), run(e.gosv.URL)
	if len(rust.transcript) != len(gov.transcript) {
		seen := map[string]int{}
		for _, l := range rust.transcript {
			seen[l]++
		}
		for _, l := range gov.transcript {
			seen[l]--
		}
		for l, n := range seen {
			if n != 0 {
				t.Logf("%+d %.600s", n, l)
			}
		}
		t.Fatalf("transcript lengths differ: rust %d go %d", len(rust.transcript), len(gov.transcript))
	}
	bad, ok2xx, errs := 0, 0, 0
	for i := range rust.transcript {
		if rust.transcript[i] != gov.transcript[i] {
			bad++
			if bad <= 20 {
				t.Errorf("step %d differs\n%s", i, diffWindow(rust.transcript[i], gov.transcript[i]))
			}
		}
		if strings.Contains(rust.transcript[i], "-> 200 ") {
			ok2xx++
		} else if strings.Contains(rust.transcript[i], " -> 4") {
			errs++
		}
	}
	if ok2xx < 250 || errs < 200 {
		t.Fatalf("script not rich enough: %d ok, %d errors", ok2xx, errs)
	}
	t.Logf("compared %d steps and rows (%d ok responses, %d 4xx); %d differ", len(rust.transcript), ok2xx, errs, bad)

	// reads: raw bytes, Go vs Rust, on both fixtures (same database state, so ids need no normalization)
	for _, r := range []*prunner{rust, gov} {
		reads := 0
		for _, cn := range allCallers {
			w := r.f.who(cn)
			var calls []struct {
				cmd string
				in  any
			}
			add := func(cmd string, in any) {
				calls = append(calls, struct {
					cmd string
					in  any
				}{cmd, in})
			}
			add("item.list", map[string]any{"cnql": "ORDER BY key"})
			add("item.list", map[string]any{"cnql": "project = ENG"})
			add("item.list", map[string]any{"cnql": "assignee = Bob ORDER BY priority DESC, key"})
			add("item.list", map[string]any{"cnql": "statusCategory in (Done, \"In Progress\") ORDER BY resolved DESC"})
			add("triage.list", map[string]any{})
			for _, k := range []string{"ENG-1", "ENG-2", "ENG-5", "ENG-6", "KAN-2", "KAN-3", "PRV-1", "DOC-1"} {
				add("item.get", map[string]any{"key": k})
			}
			for _, sp := range []string{"eng", "kan", "prv", "doc"} {
				add("workflow.get", map[string]any{"space_id": r.v(sp)})
				add("space.drift", map[string]any{"space_id": r.v(sp)})
			}
			for _, c := range calls {
				time.Sleep(55 * time.Millisecond)
				g := e.cmd(e.gosv.URL, w, r.f.owner.ws, c.cmd, c.in, nil)
				ru := e.cmd(e.rust, w, r.f.owner.ws, c.cmd, c.in, nil)
				reads++
				if g.status != ru.status || string(g.body) != string(ru.body) {
					t.Errorf("%s %s %v\n go   %d %.700s\n rust %d %.700s", cn, c.cmd, c.in, g.status, g.body, ru.status, ru.body)
				}
			}
		}
		t.Logf("raw-compared %d read pairs", reads)
	}
}

// diffWindow shows the first difference between two transcript lines with some context.
func diffWindow(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := max(0, i-150)
	cut := func(s string) string { return s[lo:min(len(s), i+250)] }
	return fmt.Sprintf(" head %.120s\n rust ...%s\n go   ...%s", a, cut(a), cut(b))
}
