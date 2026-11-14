package server_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
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
	e.must(owner, "note.share", map[string]any{"note_id": hidden, "email": strings.ToUpper(member.email), "level": "view"})
	e.must(owner, "note.restrict", map[string]any{"note_id": child, "restriction": "edit"})
	e.must(owner, "comment.add", map[string]any{"note_id": parent, "body": "hello"})
	byAgent := e.must(agent, "note.create", map[string]any{"space_id": space, "title": "made by the agent"})["id"].(string)
	missing := "00000000-0000-4000-8000-000000000000"

	e.settle(owner.ws) // let the Rust worker finish its async events (doc.bindings_synced) before comparing history
	callers := map[string]who{
		"owner": owner, "member": member, "guest": guest, "other-workspace-user": other,
		"api-key": key, "agent-key": agent, "no-auth": {}, "bad-cookie": {cookie: "nope"}, "bad-key": {bearer: "cn_x_y"},
	}
	type call struct {
		cmd string
		in  any
	}
	calls := []call{{"space.list", map[string]any{}}, {"space.list", nil}, {"space.list", map[string]any{"ignored": 1}}}
	// sharing: read commands (writes are compared on twin fixtures in TestDifferentialSharingWrites)
	for _, id := range []string{parent, child, hidden, shared, missing, "not-a-uuid"} {
		calls = append(calls, call{"note.permissions", map[string]any{"note_id": id}})
	}
	calls = append(calls, call{"note.permissions", map[string]any{}}, call{"note.permissions", map[string]any{"note_id": nil}},
		call{"note.permissions", map[string]any{"Note_ID": parent}}, call{"note.permissions", []int{1}},
		call{"note.shared_with_me", map[string]any{}}, call{"note.shared_with_me", nil}, call{"note.shared_with_me", map[string]any{"x": 1}},
		call{"note.shared_with_me", []int{1}})
	for name, id := range map[string]string{"parent": parent, "child": child, "hidden": hidden, "shared": shared, "agent": byAgent, "missing": missing} {
		_ = name
		calls = append(calls, call{"note.get", map[string]any{"id": id}}, call{"note.history", map[string]any{"id": id}})
	}
	for _, name := range []string{"member.list", "agent.list", "apikey.list", "workspace.get", "confirmation.list"} {
		calls = append(calls, call{name, map[string]any{}})
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

// Writes run on two identical fixtures (one per server). Responses must match byte for byte after replacing
// the ids the fixtures do not share, and the resulting permission rows and events must match too.
var tsRe = regexp.MustCompile(`\d{4}-\d\d-\d\dT[\d:.]+Z`)

// settle waits until the workspace has no queued or running jobs.
func (e *env) settle(ws string) {
	e.t.Helper()
	for i := 0; i < 100; i++ {
		if e.dbStrings(ws, "select count(*)::text from core.jobs where status in ('queued','running')")[0] == "0" {
			time.Sleep(200 * time.Millisecond)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatal("worker did not drain")
}

func TestDifferentialSharingWrites(t *testing.T) {
	e := newEnv(t)
	type fx struct {
		base                        string
		owner, member, guest, other who
		key, agent                  who
		page, kid, hidden           string
	}
	mk := func(base string) *fx {
		f := &fx{base: base}
		f.owner = e.signup("Owner", true)
		f.member = e.addMember(f.owner, e.signup("Member", false), "member")
		f.guest = e.addMember(f.owner, e.signup("Guest", false), "guest")
		f.other = e.signup("Other", true)
		f.key = e.apiKey(f.owner)
		f.agent = who{ws: f.owner.ws, bearer: e.must(f.owner, "agent.create", map[string]any{"name": "a"})["api_key"].(string)}
		f.page = e.note(f.owner, "Page ünï 🚀")
		f.kid = e.must(f.owner, "note.create", map[string]any{"space_id": e.space(f.owner), "title": "Kid", "parent_id": f.page})["id"].(string)
		f.hidden = e.note(f.owner, "Hidden")
		e.must(f.owner, "note.restrict", map[string]any{"note_id": f.hidden, "restriction": "view"})
		return f
	}
	g, r := mk(e.gosv.URL), mk(e.rust)
	// the same logical step on both fixtures; ids and emails are mapped to placeholders before comparing
	norm := func(f *fx, b []byte) string {
		s := string(b)
		s = tsRe.ReplaceAllString(s, "TS")
		for k, v := range map[string]string{e.space(f.owner): "SPACE", f.page: "PAGE", f.kid: "KID", f.hidden: "HIDDEN", f.owner.userID: "OWNER", f.member.userID: "MEMBER",
			f.guest.userID: "GUEST", f.other.userID: "OTHER", f.owner.ws: "WS", f.member.email: "MEMBER_EMAIL", f.guest.email: "GUEST_EMAIL",
			f.owner.email: "OWNER_EMAIL", f.other.email: "OTHER_EMAIL"} {
			s = strings.ReplaceAll(s, k, v)
		}
		return s
	}
	type step struct {
		who  string
		cmd  string
		in   func(f *fx) any
		note string
	}
	pick := func(f *fx, n string) who {
		return map[string]who{"owner": f.owner, "member": f.member, "guest": f.guest, "other": f.other, "key": f.key, "agent": f.agent, "none": {}}[n]
	}
	N := func(m func(f *fx) map[string]any) func(f *fx) any { return func(f *fx) any { return m(f) } }
	steps := []step{
		{"member", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "restriction": "edit"} }), "member restricts, becomes editor"},
		{"member", "note.permissions", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.kid} }), "inherited"},
		{"guest", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "restriction": "none"} }), "guest below min role"},
		{"owner", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "restriction": "bogus"} }), "bad value"},
		{"owner", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "restriction": ""} }), "empty value"},
		{"owner", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"restriction": "view"} }), "missing id"},
		{"owner", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "restriction": 5} }), "wrong type"},
		{"other", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "restriction": "view"} }), "other workspace"},
		{"none", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "restriction": "view"} }), "no auth"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.page, "email": strings.ToUpper(f.guest.email), "level": "edit"}
		}), "email upper"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.page, "email": "  " + f.member.email + " ", "level": "view"}
		}), "email padded, downgrade last-not editor"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.page, "user_id": f.member.userID, "level": "edit"}
		}), "by id"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.page, "user_id": f.member.userID, "level": "owner"}
		}), "bad level"},
		{"owner", "note.share", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "level": "view"} }), "no target"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.page, "user_id": nil, "email": nil, "level": "view"}
		}), "null target"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.page, "email": "nobody@example.test", "level": "view"}
		}), "unknown email"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.page, "user_id": f.other.userID, "level": "view"}
		}), "not a member"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.page, "user_id": "zzz", "level": "view"}
		}), "bad uuid"},
		{"owner", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": "00000000-0000-4000-8000-000000000000", "email": f.guest.email, "level": "view"}
		}), "missing note"},
		{"member", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.hidden, "email": f.guest.email, "level": "view"}
		}), "hidden note"},
		{"agent", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.kid, "email": f.guest.email, "level": "view"}
		}), "agent"},
		{"key", "note.share", N(func(f *fx) map[string]any {
			return map[string]any{"note_id": f.kid, "user_id": f.member.userID, "level": "edit"}
		}), "api key"},
		{"guest", "note.shared_with_me", N(func(f *fx) map[string]any { return map[string]any{} }), "guest list"},
		{"owner", "note.unshare", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "user_id": f.guest.userID} }), "unshare"},
		{"owner", "note.unshare", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "user_id": f.guest.userID} }), "unshare again"},
		{"owner", "note.unshare", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "user_id": f.owner.userID} }), "owner not listed"},
		{"member", "note.unshare", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "user_id": f.member.userID} }), "last editor? member still listed owner?"},
		{"owner", "note.unshare", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page} }), "missing user"},
		{"owner", "note.permissions", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page} }), "final"},
		{"owner", "note.restrict", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.page, "restriction": "none"} }), "lift"},
		{"owner", "note.permissions", N(func(f *fx) map[string]any { return map[string]any{"note_id": f.kid} }), "after lift"},
	}
	for _, st := range steps {
		gr := e.cmd(g.base, pick(g, st.who), g.owner.ws, st.cmd, st.in(g), nil)
		rr := e.cmd(r.base, pick(r, st.who), r.owner.ws, st.cmd, st.in(r), nil)
		if gr.status != rr.status || norm(g, gr.body) != norm(r, rr.body) {
			t.Errorf("%s (%s)\n go   %d %s\n rust %d %s", st.cmd, st.note, gr.status, norm(g, gr.body), rr.status, norm(r, rr.body))
		}
	}
	e.settle(g.owner.ws)
	e.settle(r.owner.ws)
	// the Rust steps above ran on the Rust fixture through its own server, the Go steps through Go; compare DB state
	rowsOf := func(f *fx) []string {
		q := func(sql string, args ...any) []string { return e.dbStrings(f.owner.ws, sql, args...) }
		out := q("select n.restriction || ' ' || coalesce(u.name,'') || ' ' || p.level || ' ' || (p.granted_by is not null)::text from core.note_permissions p join core.users u on u.id=p.user_id join core.notes n on n.id=p.note_id order by n.title, u.name")
		out = append(out, q("select n.title || ' ' || n.restriction from core.notes n order by n.title")...)
		for _, ev := range q(`select e.kind || ' ' || e.actor_type || ' ' || (e.note_id is not null)::text || ' ' || e.payload::text
			from core.events e where e.kind in ('note.restricted','note.shared','note.unshared') order by e.id`) {
			out = append(out, norm(f, []byte(ev)))
		}
		out = append(out, q(`select count(*)::text from core.jobs where kind='event.dispatch' and (payload->>'event_id')::bigint in
			(select id from core.events where kind in ('note.restricted','note.shared','note.unshared'))`)...)
		return out
	}
	if gr, rr := rowsOf(g), rowsOf(r); !reflect.DeepEqual(gr, rr) {
		t.Errorf("db state differs\n go   %q\n rust %q", gr, rr)
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
