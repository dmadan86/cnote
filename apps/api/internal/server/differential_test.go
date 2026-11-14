package server_test

import (
	"encoding/json"
	"fmt"
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
	parent := e.note(owner, "Parent <b>&  \"quoted\" ünïcode 🚀")
	child := e.must(owner, "note.create", map[string]any{
		"space_id": space, "title": "Child", "parent_id": parent, "types": []string{"task", "meeting"},
		"content_md": "# Heading\n\n<script>alert(1)</script> & more   \\ \t tab",
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

// TestDifferentialNotifications compares Go vs Rust byte-for-byte for notification.list,
// notification.read and notification.unread_count across every caller type and representative inputs.
//
// Read-only commands (list, unread_count) and fallback / zero-effect inputs (bad uuid, missing uuid)
// run in the cross-caller loop against the same fixture.
//
// Mutating commands (notification.read mark-all and mark-by-id) run on twin fixtures: one for Go,
// one for Rust, each in its own workspace with its own fresh notification. Responses are compared
// for byte equality (both should return the same JSON for the same logical operation on fresh data).
func TestDifferentialNotifications(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	other := e.signup("Other", true)
	key := e.apiKey(owner)
	agent := who{ws: owner.ws, bearer: e.must(owner, "agent.create", map[string]any{"name": "notif-bot"})["api_key"].(string)}

	// Create a mention notification for 'member' through Rust (notify_mentions runs in Rust).
	sp := e.space(owner)
	page := e.must(owner, "note.create", map[string]any{"space_id": sp, "title": "MentionPage"})["id"].(string)
	memberLink := fmt.Sprintf("[@member](/w/%s/people/%s)", owner.ws, member.userID)
	e.must(owner, "note.update", map[string]any{"id": page, "content_md": "hi " + memberLink})

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

	// Read-only and idempotent/zero-effect calls: safe to run against both servers with the same fixture.
	readCalls := []call{
		{"notification.list", map[string]any{}},
		{"notification.list", map[string]any{"ignored": 1}},
		{"notification.unread_count", map[string]any{}},
		{"notification.unread_count", map[string]any{"x": nil}},
		// Invalid UUID – handled by fallback, Rust produces identical serde error text.
		{"notification.read", map[string]any{"id": "not-a-uuid"}},
		// Valid UUID that matches no notification in this workspace (or any workspace for unauthenticated):
		// always returns updated=0 for the fixture owner; safe to run repeatedly.
		{"notification.read", map[string]any{"id": "00000000-0000-4000-8000-000000000000"}},
	}

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

	// --- Mutating differential: twin fixtures ---
	// Each mutating call runs on a fresh workspace so Go and Rust each start from updated=1.
	//
	// helper: create a fresh workspace owner, give them a notification, run cmd on server, return response.
	createNotifFixture := func() (who, string) {
		t.Helper()
		own := e.signup("TwinOwner", true)
		rcv := e.addMember(own, e.signup("TwinRcv", false), "member")
		pg := e.must(own, "note.create", map[string]any{"space_id": e.space(own), "title": "T"})["id"].(string)
		lnk := fmt.Sprintf("[@rcv](/w/%s/people/%s)", own.ws, rcv.userID)
		e.must(own, "note.update", map[string]any{"id": pg, "content_md": "hi " + lnk})
		// get the notification id via Rust
		lr := e.cmd(e.rust, rcv, own.ws, "notification.list", map[string]any{}, nil)
		var nl []map[string]any
		nid := ""
		if lr.status == 200 {
			if err := json.Unmarshal(lr.body, &nl); err == nil && len(nl) > 0 {
				nid, _ = nl[0]["id"].(string)
			}
		}
		return rcv, nid
	}

	// mark-all (id omitted) on twin fixtures: both should return {"updated":1}.
	gRcv, _ := createNotifFixture()
	rRcv, _ := createNotifFixture()
	gAll := e.cmd(e.gosv.URL, gRcv, gRcv.ws, "notification.read", map[string]any{}, nil)
	rAll := e.cmd(e.rust, rRcv, rRcv.ws, "notification.read", map[string]any{}, nil)
	ran++
	if gAll.status != rAll.status || string(gAll.body) != string(rAll.body) {
		t.Errorf("mark-all twin\n go   %d %s\n rust %d %s", gAll.status, gAll.body, rAll.status, rAll.body)
	}

	// mark-by-id on twin fixtures: both should return {"updated":1}.
	gRcv2, gNID := createNotifFixture()
	rRcv2, rNID := createNotifFixture()
	if gNID != "" && rNID != "" {
		gByID := e.cmd(e.gosv.URL, gRcv2, gRcv2.ws, "notification.read", map[string]any{"id": gNID}, nil)
		rByID := e.cmd(e.rust, rRcv2, rRcv2.ws, "notification.read", map[string]any{"id": rNID}, nil)
		ran++
		if gByID.status != rByID.status || string(gByID.body) != string(rByID.body) {
			t.Errorf("mark-by-id twin\n go   %d %s\n rust %d %s", gByID.status, gByID.body, rByID.status, rByID.body)
		}
	}

	// Sanity: member's list is non-empty on Go.
	if g := e.cmd(e.gosv.URL, member, owner.ws, "notification.list", map[string]any{}, nil); g.status != 200 ||
		!strings.Contains(string(g.body), `"kind":"mention"`) {
		t.Fatalf("member list sanity: %d %s", g.status, g.body)
	}

	t.Logf("notifications differential: compared %d request pairs", ran)
}
