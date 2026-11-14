package server_test

import (
	"strings"
	"testing"
)

// TestDifferential calls every ported command on the real Rust server and on the Go server with identical
// inputs and callers and requires identical status codes and identical response BYTES (stronger than
// parsed-JSON equality). When a module is ported, add its commands and inputs here before flipping routing.
func TestDifferential(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	other := e.signup("Other", true)
	key := e.apiKey(owner)
	agent := who{ws: owner.ws, bearer: e.must(owner, "agent.create", map[string]any{"name": "diff-bot"})["api_key"].(string)}

	space := e.space(owner)
	parent := e.note(owner, "Parent <b>&  \"quoted\" ünïcode 🚀")
	child := e.must(owner, "note.create", map[string]any{
		"space_id": space, "title": "Child", "parent_id": parent, "types": []string{"task", "meeting"},
		"content_md": "# Heading\n\n<script>alert(1)</script> & more   \\ \t tab",
	})["id"].(string)
	e.must(owner, "relation.add", map[string]any{"from_id": parent, "to_id": child, "kind": "blocks"})
	e.must(owner, "note.update", map[string]any{"id": child, "title": "Child (edited)"})
	hidden := e.note(owner, "hidden")
	e.must(owner, "note.restrict", map[string]any{"note_id": hidden, "restriction": "view"})
	shared := e.note(owner, "shared with guest")
	e.must(owner, "note.share", map[string]any{"note_id": shared, "email": guest.email, "level": "edit"})
	e.must(owner, "comment.add", map[string]any{"note_id": parent, "body": "hello"})
	byAgent := e.must(agent, "note.create", map[string]any{"space_id": space, "title": "made by the agent"})["id"].(string)
	missing := "00000000-0000-4000-8000-000000000000"

	callers := map[string]who{
		"owner": owner, "member": member, "guest": guest, "other-workspace-user": other,
		"api-key": key, "agent-key": agent, "no-auth": {}, "bad-cookie": {cookie: "nope"}, "bad-key": {bearer: "cn_x_y"},
	}
	type call struct {
		cmd string
		in  any
	}
	calls := []call{{"space.list", map[string]any{}}, {"space.list", nil}, {"space.list", map[string]any{"ignored": 1}}}
	for name, id := range map[string]string{"parent": parent, "child": child, "hidden": hidden, "shared": shared, "agent": byAgent, "missing": missing} {
		_ = name
		calls = append(calls, call{"note.get", map[string]any{"id": id}}, call{"note.history", map[string]any{"id": id}})
	}
	ran := 0
	for cname, c := range callers {
		for _, k := range calls {
			ws := owner.ws
			if cname == "other-workspace-user" && strings.HasPrefix(k.cmd, "space") {
				ws = other.ws // same caller in its own workspace too
			}
			g := e.cmd(e.gosv.URL, c, ws, k.cmd, k.in, nil)
			r := e.cmd(e.rust, c, ws, k.cmd, k.in, nil)
			ran++
			if g.status != r.status || string(g.body) != string(r.body) {
				t.Errorf("%s %s %v\n go   %d %s\n rust %d %s", cname, k.cmd, k.in, g.status, g.body, r.status, r.body)
			}
		}
	}
	// sanity: the comparison is not vacuous
	if g := e.cmd(e.gosv.URL, owner, owner.ws, "note.get", map[string]any{"id": child}, nil); g.status != 200 ||
		!strings.Contains(string(g.body), `"other_title":"Parent`) || !strings.Contains(string(g.body), `"types":["task","meeting"]`) {
		t.Fatalf("fixture not rich enough: %s", g.body)
	}
	if h := e.cmd(e.gosv.URL, owner, owner.ws, "note.history", map[string]any{"id": byAgent}, nil); !strings.Contains(string(h.body), `"actor_name":"diff-bot"`) {
		t.Fatalf("agent history: %s", h.body)
	}
	t.Logf("compared %d request pairs", ran)
}

// Error responses for the cases the task names explicitly, spelled out.
func TestDifferentialErrors(t *testing.T) {
	e := newEnv(t)
	a := e.signup("Alice", true)
	for name, c := range map[string]struct {
		w      who
		cmd    string
		in     any
		status int
	}{
		"note not found":  {a, "note.get", map[string]any{"id": "00000000-0000-4000-8000-000000000000"}, 404},
		"history missing": {a, "note.history", map[string]any{"id": "00000000-0000-4000-8000-000000000000"}, 404},
		"unauthenticated": {who{}, "note.get", map[string]any{"id": "00000000-0000-4000-8000-000000000000"}, 401},
	} {
		g := e.cmd(e.gosv.URL, c.w, a.ws, c.cmd, c.in, nil)
		r := e.cmd(e.rust, c.w, a.ws, c.cmd, c.in, nil)
		if g.status != c.status || r.status != c.status || string(g.body) != string(r.body) {
			t.Errorf("%s: go %d %s rust %d %s", name, g.status, g.body, r.status, r.body)
		}
	}
}
