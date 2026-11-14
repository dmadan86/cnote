package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Ports of the Rust tests that cover the note commands (crates/cnote-core/tests/command_layer.rs and
// page_permissions.rs). Every command runs through the Go server (commands Go does not own are proxied).

type gw struct {
	t *testing.T
	e *env
}

func (g gw) run(w who, cmd string, in any) resp {
	g.t.Helper()
	return g.e.cmd(g.e.gosv.URL, w, w.ws, cmd, in, nil)
}

func (g gw) ok(w who, cmd string, in any) map[string]any {
	g.t.Helper()
	r := g.run(w, cmd, in)
	if r.status != 200 {
		g.t.Fatalf("%s: %d %s", cmd, r.status, r.body)
	}
	return r.json()
}

func (g gw) list(w who, cmd string, in any) []map[string]any {
	g.t.Helper()
	r := g.run(w, cmd, in)
	var out []map[string]any
	if r.status != 200 || json.Unmarshal(r.body, &out) != nil {
		g.t.Fatalf("%s: %d %s", cmd, r.status, r.body)
	}
	return out
}

// code asserts an error response with the given HTTP status and error code.
func (g gw) code(want string, w who, cmd string, in any) {
	g.t.Helper()
	r := g.run(w, cmd, in)
	got, _ := r.json()["error"].(map[string]any)
	if got == nil || got["code"] != want {
		g.t.Fatalf("%s %v: want %s, got %d %s", cmd, in, want, r.status, r.body)
	}
}

func ids(l []map[string]any) []string {
	out := []string{}
	for _, n := range l {
		out = append(out, n["id"].(string))
	}
	return out
}

func (g gw) space(w who, key, vis string) string {
	g.t.Helper()
	return g.ok(w, "space.create", map[string]any{"key": key, "name": key, "visibility": vis})["id"].(string)
}

func (g gw) page(w who, space, title string, parent any, body string) string {
	g.t.Helper()
	return g.ok(w, "note.create", map[string]any{"space_id": space, "title": title, "parent_id": parent, "content_md": body})["id"].(string)
}

func TestNotesAreIsolatedBetweenTenants(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	a, b := e.signup("Tess", true), e.signup("Tess", true)
	space := g.ok(a, "space.create", map[string]any{"key": "ENG", "name": "Engineering"})["id"]
	note := g.ok(a, "note.create", map[string]any{"space_id": space, "title": "Runbook", "content_md": "restart the pods"})
	if note["owner_id"] != a.userID {
		t.Fatalf("owner defaults to creator: %v", note["owner_id"])
	}
	if _, ok := note["verify_by"].(string); !ok {
		t.Fatal("pages get a verify-by date")
	}
	g.code("not_found", b, "note.get", map[string]any{"id": note["id"]})
	if n := len(g.list(b, "search.notes", map[string]any{"q": "runbook"})); n != 0 {
		t.Fatalf("tenant B found %d hits", n)
	}
	if n := len(g.list(a, "search.notes", map[string]any{"q": "runbook"})); n != 1 {
		t.Fatalf("tenant A found %d hits", n)
	}
	// raw SQL without a tenant sees nothing
	var n int
	if err := e.pool.QueryRow(context.Background(), "select count(*) from core.notes").Scan(&n); err != nil || n != 0 {
		t.Fatalf("untenanted select: %d %v", n, err)
	}
}

func TestOptimisticConcurrencyAndHistory(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	a := e.signup("Tess", true)
	space := g.ok(a, "space.create", map[string]any{"key": "DOC", "name": "Docs"})["id"]
	n := g.ok(a, "note.create", map[string]any{"space_id": space, "title": "A"})
	g.ok(a, "note.update", map[string]any{"id": n["id"], "title": "B", "expected_version": 1})
	r := g.run(a, "note.update", map[string]any{"id": n["id"], "title": "C", "expected_version": 1})
	if r.status != 409 || string(r.body) != `{"error":{"code":"conflict","message":"note changed (version 2 != 1)"}}` {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var kinds []string
	for _, ev := range g.list(a, "note.history", map[string]any{"id": n["id"]}) {
		if ev["actor_type"] == "user" {
			kinds = append(kinds, ev["kind"].(string))
		}
	}
	if !slices.Equal(kinds, []string{"note.updated", "note.created"}) {
		t.Fatalf("history: %v", kinds)
	}
}

func TestAgentGroupsIdempotencyAndEvents(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	a := e.signup("Tess", true)
	agent := who{ws: a.ws, bearer: e.must(a, "agent.create", map[string]any{"name": "Coder"})["api_key"].(string)}
	space := g.ok(agent, "space.create", map[string]any{"key": "AG", "name": "Agents"})["id"]
	n := g.ok(agent, "note.create", map[string]any{"space_id": space, "title": "x"})
	g.code("forbidden", agent, "note.delete", map[string]any{"id": n["id"]}) // destructive group is off for agents

	h := map[string]string{"Idempotency-Key": "k-" + rnd()}
	in := map[string]any{"space_id": space, "title": "once"}
	r1, r2 := e.cmd(e.gosv.URL, agent, a.ws, "note.create", in, h), e.cmd(e.gosv.URL, agent, a.ws, "note.create", in, h)
	if r1.status != 200 || r1.json()["id"] != r2.json()["id"] || string(r1.body) != string(r2.body) {
		t.Fatalf("idempotency: %s vs %s", r1.body, r2.body)
	}
	hist := g.list(a, "note.history", map[string]any{"id": r1.json()["id"]})
	if hist[len(hist)-1]["actor_type"] != "agent" || hist[len(hist)-1]["on_behalf_of"] != a.userID {
		t.Fatalf("event attribution: %v", hist)
	}
	// the event and its dispatch job are written in the same transaction as the note
	tx, _ := dbTenant(e, a.ws)
	defer tx.Rollback(context.Background())
	var events, jobs int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where note_id = $1::uuid and kind = 'note.created'", r1.json()["id"]).Scan(&events)
	_ = tx.QueryRow(context.Background(), "select count(*) from core.jobs j join core.events e on (j.payload->>'event_id')::bigint = e.id where e.note_id = $1::uuid", r1.json()["id"]).Scan(&jobs)
	if events != 1 || jobs < 1 {
		t.Fatalf("events %d jobs %d", events, jobs)
	}
	// a personal API key without scopes still respects workspace tool groups
	k := e.apiKey(a)
	g.code("forbidden", k, "member.add", map[string]any{"email": "x@y.z", "role": "member"})
}

func TestSecurityRegressions(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Owner", true)
	space := g.ok(owner, "space.create", map[string]any{"key": "SEC", "name": "Sec"})["id"]
	admin := e.addMember(owner, e.signup("Admin", false), "admin")
	// idempotency keys never replay another principal's result
	h := map[string]string{"Idempotency-Key": "same-" + rnd()}
	a := e.cmd(e.gosv.URL, owner, owner.ws, "note.create", map[string]any{"space_id": space, "title": "mine"}, h)
	b := e.cmd(e.gosv.URL, admin, owner.ws, "note.create", map[string]any{"space_id": space, "title": "theirs"}, h)
	if a.status != 200 || b.status != 200 || a.json()["id"] == b.json()["id"] {
		t.Fatalf("%s %s", a.body, b.body)
	}
	// relations to private notes don't leak titles
	private := g.space(owner, "PRV", "private")
	secret := g.page(owner, private, "Secret plan", nil, "")
	open := g.page(owner, space.(string), "Open", nil, "")
	g.ok(owner, "relation.add", map[string]any{"from_id": open, "to_id": secret, "kind": "relates"})
	member := e.addMember(owner, e.signup("Member", false), "member")
	seen := g.ok(member, "note.get", map[string]any{"id": open})
	if len(seen["relations"].([]any)) != 0 {
		t.Fatalf("relation leaked: %v", seen["relations"])
	}
	if own := g.ok(owner, "note.get", map[string]any{"id": open}); len(own["relations"].([]any)) != 1 {
		t.Fatalf("owner relations: %v", own["relations"])
	}
}

func TestViewRestrictionHidesEverywhere(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	sp := g.space(owner, "VR", "open")
	secret := g.page(owner, sp, "Layoff plan", nil, "zebrafish budget")
	child := g.page(owner, sp, "Layoff child", secret, "zebrafish details")
	open := g.page(owner, sp, "Lunch menu", nil, "zebrafish tacos")
	g.ok(owner, "comment.add", map[string]any{"note_id": secret, "body": "hush"})
	g.ok(owner, "relation.add", map[string]any{"from_id": open, "to_id": secret, "kind": "relates"})
	if n := len(g.list(bob, "note.list", map[string]any{"space_id": sp})); n != 3 {
		t.Fatalf("before restricting: %d", n)
	}
	g.ok(owner, "note.restrict", map[string]any{"note_id": secret, "restriction": "view"})
	for _, id := range []string{secret, child} {
		g.code("not_found", bob, "note.get", map[string]any{"id": id})
		g.code("not_found", bob, "comment.list", map[string]any{"note_id": id})
		g.code("not_found", bob, "note.history", map[string]any{"id": id})
		g.code("not_found", bob, "note.update", map[string]any{"id": id, "title": "x"})
		g.code("not_found", bob, "comment.add", map[string]any{"note_id": id, "body": "x"})
	}
	if l := ids(g.list(bob, "note.list", map[string]any{"space_id": sp})); !slices.Equal(l, []string{open}) {
		t.Fatalf("list: %v", l)
	}
	if l := g.list(bob, "note.list", map[string]any{"parent_id": secret}); len(l) != 0 {
		t.Fatalf("children of hidden page: %v", l)
	}
	if l := ids(g.list(bob, "search.notes", map[string]any{"q": "zebrafish"})); !slices.Equal(l, []string{open}) {
		t.Fatalf("search: %v", l)
	}
	if d := g.ok(bob, "note.get", map[string]any{"id": open}); len(d["relations"].([]any)) != 0 {
		t.Fatalf("relation title leaked: %v", d)
	}
	g.code("not_found", bob, "note.create", map[string]any{"space_id": sp, "title": "t", "parent_id": secret})
	g.code("not_found", bob, "note.update", map[string]any{"id": open, "parent_id": secret})
	g.code("not_found", bob, "note.permissions", map[string]any{"note_id": secret})
	g.code("not_found", bob, "note.share", map[string]any{"note_id": secret, "user_id": bob.userID, "level": "edit"})
	if n := len(g.list(owner, "note.list", map[string]any{"space_id": sp})); n != 3 {
		t.Fatalf("owner sees %d", n)
	}
}

func TestEditRestrictionAllowsReadingBlocksWriting(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	sp := g.space(owner, "ER", "open")
	page := g.page(owner, sp, "Policy", nil, "rules")
	child := g.page(owner, sp, "Policy FAQ", page, "faq")
	g.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "edit"})
	g.ok(bob, "note.get", map[string]any{"id": page})
	g.code("forbidden", bob, "note.update", map[string]any{"id": page, "title": "x"})
	g.code("forbidden", bob, "note.update", map[string]any{"id": child, "title": "x"}) // inherited
	g.code("forbidden", bob, "note.delete", map[string]any{"id": child})
	g.ok(bob, "comment.add", map[string]any{"note_id": page, "body": "question"}) // commenting is reading
	g.code("forbidden", bob, "note.restrict", map[string]any{"note_id": page, "restriction": "none"})
	perms := g.ok(bob, "note.permissions", map[string]any{"note_id": child})
	if perms["can_manage"] != false || perms["inherited_from"].(map[string]any)["id"] != page {
		t.Fatalf("%v", perms)
	}
	g.ok(owner, "note.share", map[string]any{"note_id": page, "email": strings.ToUpper(bob.email), "level": "edit"})
	g.ok(bob, "note.update", map[string]any{"id": child, "title": "FAQ v2"})
}

func TestShareIntoPrivateSpace(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	carol := e.addMember(owner, e.signup("Carol", false), "member")
	sp := g.space(owner, "PR", "private")
	shared := g.page(owner, sp, "Roadmap", nil, "quokka plan")
	kid := g.page(owner, sp, "Roadmap Q3", shared, "quokka q3")
	other := g.page(owner, sp, "Payroll", nil, "quokka salaries")
	for _, p := range []who{bob, guest} {
		g.code("not_found", p, "note.get", map[string]any{"id": shared})
	}
	g.ok(owner, "note.share", map[string]any{"note_id": shared, "user_id": bob.userID, "level": "view"})
	g.ok(owner, "note.share", map[string]any{"note_id": shared, "email": guest.email, "level": "view"})
	g.ok(owner, "note.share", map[string]any{"note_id": shared, "user_id": carol.userID, "level": "edit"})
	for _, p := range []who{bob, guest, carol} {
		g.ok(p, "note.get", map[string]any{"id": shared})
		g.ok(p, "note.get", map[string]any{"id": kid})
		g.code("not_found", p, "note.get", map[string]any{"id": other})
		if l := g.list(p, "note.list", map[string]any{"space_id": sp}); len(l) != 0 {
			t.Fatalf("space listing: %v", l)
		}
		if l := ids(g.list(p, "note.list", map[string]any{"parent_id": shared})); !slices.Equal(l, []string{kid}) {
			t.Fatalf("children: %v", l)
		}
		if l := ids(g.list(p, "search.notes", map[string]any{"q": "quokka"})); !slices.Equal(l, []string{shared}) {
			t.Fatalf("search: %v", l)
		}
		for _, s := range g.list(p, "space.list", map[string]any{}) {
			if s["id"] == sp {
				t.Fatal("private space listed")
			}
		}
		if r := g.run(p, "note.create", map[string]any{"space_id": sp, "title": "x"}); r.status < 400 {
			t.Fatalf("created in private space: %s", r.body)
		}
	}
	g.code("forbidden", bob, "note.update", map[string]any{"id": kid, "title": "x"})
	g.ok(carol, "note.update", map[string]any{"id": kid, "title": "edited by carol"})
	g.code("not_found", carol, "note.update", map[string]any{"id": other, "title": "x"})
	g.ok(owner, "note.unshare", map[string]any{"note_id": shared, "user_id": bob.userID})
	g.code("not_found", bob, "note.get", map[string]any{"id": kid})
}

func TestShareRulesLockout(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	alice := e.addMember(owner, e.signup("Alice", false), "member")
	admin := e.addMember(owner, e.signup("Admin", false), "admin")
	sp := g.space(owner, "LK", "open")
	page := g.page(owner, sp, "Plan", nil, "alpaca")
	g.ok(alice, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	perms := g.ok(alice, "note.permissions", map[string]any{"note_id": page})
	if perms["restriction"] != "view" || perms["people"].([]any)[0].(map[string]any)["level"] != "edit" || perms["can_manage"] != true {
		t.Fatalf("%v", perms)
	}
	g.ok(owner, "note.get", map[string]any{"id": page})
	g.ok(admin, "note.update", map[string]any{"id": page, "title": "admin edit"})
	g.code("conflict", alice, "note.unshare", map[string]any{"note_id": page, "user_id": alice.userID})
	g.code("conflict", alice, "note.share", map[string]any{"note_id": page, "user_id": alice.userID, "level": "view"})
	g.ok(alice, "note.update", map[string]any{"id": page, "title": "still mine"}) // rolled back: still editor
	g.code("invalid", alice, "note.share", map[string]any{"note_id": page, "email": "nobody@t.local", "level": "view"})
	stranger := e.signup("S", false)
	g.code("invalid", alice, "note.share", map[string]any{"note_id": page, "user_id": stranger.userID, "level": "view"})
	g.code("invalid", alice, "note.restrict", map[string]any{"note_id": page, "restriction": "bogus"})
	g.ok(alice, "note.restrict", map[string]any{"note_id": page, "restriction": "none"})
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	g.ok(bob, "note.update", map[string]any{"id": page, "title": "open again"})
}

func TestSavedViewsAndRawAccessFunction(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	sp := g.space(owner, "AG", "open")
	page := g.page(owner, sp, "Salary bands", nil, "wombat")
	g.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	if v := g.list(bob, "view.list", map[string]any{}); len(v) != 0 {
		t.Fatalf("views: %v", v)
	}
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var lvl string
	if err := tx.QueryRow(context.Background(), "select core.note_access($1::uuid, $2::uuid, false, array[$3]::uuid[])", page, bob.userID, sp).Scan(&lvl); err != nil || lvl != "none" {
		t.Fatalf("%q %v", lvl, err)
	}
	if err := tx.QueryRow(context.Background(), "select core.note_access($1::uuid, null, true, '{}')", page).Scan(&lvl); err != nil || lvl != "edit" {
		t.Fatalf("%q %v", lvl, err)
	}
	// a view round-trips, and a private space's view is hidden from non-members
	v := g.ok(owner, "view.create", map[string]any{"space_id": sp, "name": "Open", "layout": "board", "query": "x", "options": map[string]any{"b": 1, "a": []any{2}}})
	if v["name"] != "Open" || fmt.Sprint(v["options"]) != "map[a:[2] b:1]" {
		t.Fatalf("%v", v)
	}
	g.code("invalid", owner, "view.create", map[string]any{"name": "x", "layout": "nonsense", "query": "x"})
}

func TestMentionsNotifyOnceAndOnlyReaders(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	carol := e.addMember(owner, e.signup("Carol", false), "member")
	sp := g.space(owner, "MN", "open")
	page := g.page(owner, sp, "Plan", nil, "")
	link := func(p who) string { return fmt.Sprintf("[@x](/w/%s/people/%s)", owner.ws, p.userID) }
	count := func(p who) float64 { return g.ok(p, "notification.unread_count", map[string]any{})["count"].(float64) }
	save := func(body string) { g.ok(owner, "note.update", map[string]any{"id": page, "content_md": body}) }

	body := fmt.Sprintf("hi %s and %s", link(bob), link(owner))
	save(body)
	save(body + " more")
	if count(bob) != 1 || count(owner) != 0 {
		t.Fatalf("bob %v owner %v", count(bob), count(owner))
	}
	if l := g.list(bob, "notification.list", map[string]any{}); l[0]["note_title"] != "Plan" {
		t.Fatalf("%v", l)
	}
	g.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	save(body + " " + link(carol))
	if count(carol) != 0 {
		t.Fatal("carol cannot read the page")
	}
	g.ok(bob, "notification.read", map[string]any{})
	if count(bob) != 0 {
		t.Fatal("badge not cleared")
	}
	// mention.created events are emitted for the one notification
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var n int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.events where kind = 'mention.created' and note_id = $1::uuid", page).Scan(&n)
	if n != 1 {
		t.Fatalf("mention.created events: %d", n)
	}
}

func TestInlineCommentsReplyResolveAndAccess(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	sp := g.space(owner, "IC", "open")
	page := g.page(owner, sp, "Plan", nil, "We ship on Friday.")
	c := g.ok(bob, "comment.add", map[string]any{"note_id": page, "body": "Which Friday?", "anchor_text": "Friday", "anchor_prefix": "We ship on "})
	if c["anchor_text"] != "Friday" || c["anchor_prefix"] != "We ship on " {
		t.Fatalf("%v", c)
	}
	mention := fmt.Sprintf("The 20th, [@Bob](/w/%s/people/%s)", owner.ws, bob.userID)
	reply := g.ok(owner, "comment.add", map[string]any{"note_id": page, "body": mention, "parent_id": c["id"]})
	if reply["parent_id"] != c["id"] {
		t.Fatalf("%v", reply)
	}
	if n := g.ok(bob, "notification.unread_count", map[string]any{})["count"]; n != float64(1) {
		t.Fatalf("a mention in a comment notifies like one in the page: %v", n)
	}
	g.code("invalid", bob, "comment.add", map[string]any{"note_id": page, "body": "x", "parent_id": reply["id"]})
	g.code("invalid", bob, "comment.add", map[string]any{"note_id": page, "body": "x", "parent_id": c["id"], "anchor_text": "ship"})
	g.code("invalid", bob, "comment.add", map[string]any{"note_id": page, "body": "x", "anchor_text": strings.Repeat("y", 501)})
	r := g.ok(bob, "comment.resolve", map[string]any{"id": c["id"]})
	if _, ok := r["resolved_at"].(string); !ok {
		t.Fatalf("%v", r)
	}
	if _, ok := r["resolved_by_name"].(string); !ok {
		t.Fatalf("%v", r)
	}
	if r := g.ok(owner, "comment.resolve", map[string]any{"id": c["id"], "resolved": false}); r["resolved_at"] != nil {
		t.Fatalf("%v", r)
	}
	g.code("not_found", bob, "comment.resolve", map[string]any{"id": reply["id"]})
	g.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	g.code("not_found", bob, "comment.resolve", map[string]any{"id": c["id"]})
	if all := g.list(owner, "comment.list", map[string]any{"note_id": page}); len(all) != 2 {
		t.Fatalf("%v", all)
	}
	// events: inline/reply flags and resolve/reopen kinds
	hist := g.list(owner, "note.history", map[string]any{"id": page})
	var kinds []string
	for _, ev := range hist {
		if ev["actor_type"] == "user" {
			kinds = append(kinds, ev["kind"].(string))
		}
	}
	for _, k := range []string{"comment.added", "comment.resolved", "comment.reopened", "mention.created"} {
		if !slices.Contains(kinds, k) {
			t.Fatalf("missing %s in %v", k, kinds)
		}
	}
}

func TestGuestSharedAsEditorCanSave(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	for i, vis := range []string{"open", "private"} {
		sp := g.space(owner, []string{"GO", "GP"}[i], vis)
		page := g.page(owner, sp, "Brief", nil, "draft")
		g.ok(owner, "note.share", map[string]any{"note_id": page, "email": guest.email, "level": "edit"})
		if got := g.ok(guest, "note.get", map[string]any{"id": page}); got["level"] != "edit" {
			t.Fatalf("%s: %v", vis, got["level"])
		}
		g.ok(guest, "note.update", map[string]any{"id": page, "title": "Brief v2", "content_md": "guest wrote this"})
		g.ok(guest, "comment.add", map[string]any{"note_id": page, "body": "looks good"})
		other := g.page(owner, sp, "Other", nil, "x")
		g.code("not_found", guest, "note.update", map[string]any{"id": other, "title": "x"})
		g.ok(owner, "note.share", map[string]any{"note_id": other, "email": guest.email, "level": "view"})
		g.code("forbidden", guest, "note.update", map[string]any{"id": other, "title": "x"})
	}
}

func TestMovesCannotCycleOrCrossSpaces(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	a, b := g.space(owner, "MA", "open"), g.space(owner, "MB", "open")
	top := g.page(owner, a, "Top", nil, "x")
	mid := g.page(owner, a, "Mid", top, "x")
	leaf := g.page(owner, a, "Leaf", mid, "x")
	other := g.page(owner, b, "Elsewhere", nil, "x")
	for _, c := range [][2]string{{top, top}, {top, leaf}, {mid, leaf}, {top, other}} {
		g.code("invalid", owner, "note.update", map[string]any{"id": c[0], "parent_id": c[1]})
	}
	if r := g.run(owner, "note.create", map[string]any{"space_id": b, "title": "x", "parent_id": top}); r.status < 400 {
		t.Fatalf("%s", r.body)
	}
	g.ok(owner, "note.update", map[string]any{"id": leaf, "parent_id": top}) // a legal move still works
}

func TestNoteUpdateEpochCheckpointAndDelete(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	sp := g.space(owner, "EP", "open")
	page := g.page(owner, sp, "Page", nil, "")
	g.code("conflict", owner, "note.update", map[string]any{"id": page, "content_md": "x", "expected_epoch": 7})
	g.ok(owner, "note.update", map[string]any{"id": page, "title": "t", "expected_epoch": 7}) // epoch only guards content
	g.ok(owner, "note.update", map[string]any{"id": page, "content_md": "v1", "expected_epoch": 1})
	g.ok(owner, "note.update", map[string]any{"id": page, "content_md": "v2"}) // within the checkpoint gap: no second revision
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var revs int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.note_revisions where note_id = $1::uuid", page).Scan(&revs)
	if revs != 1 {
		t.Fatalf("revisions: %d", revs)
	}
	g.ok(owner, "note.delete", map[string]any{"id": page})
	g.code("not_found", owner, "note.get", map[string]any{"id": page})
	g.code("not_found", owner, "note.delete", map[string]any{"id": page})
	if l := g.list(owner, "note.list", map[string]any{"space_id": sp}); len(l) != 0 {
		t.Fatalf("%v", l)
	}
}
