package server_test

import (
	"context"
	"testing"
	"time"
)

// Docs parity A1, W3: watching, the one notification consumer (worker/pagenotify.go), access at send and at read,
// and version history access. The background worker of env_test.go consumes the events.

// settle waits until every event.dispatch job of the workspace is done, so "nothing arrived" can be asserted.
func settle(t *testing.T, e *env, ws string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		tx, _ := dbTenant(e, ws)
		var n int
		_ = tx.QueryRow(context.Background(), "select count(*) from core.jobs where kind = 'event.dispatch' and status in ('queued','running')").Scan(&n)
		_ = tx.Rollback(context.Background())
		if n == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("event jobs did not finish")
}

func execTenant(t *testing.T, e *env, ws, sql string, args ...any) {
	t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// inbox is notification.list of p, restricted to one kind.
func inbox(g gw, p who, kind string) []map[string]any {
	out := []map[string]any{}
	for _, n := range g.list(p, "notification.list", map[string]any{}) {
		if n["kind"] == kind {
			out = append(out, n)
		}
	}
	return out
}

func watching(t *testing.T, e *env, p who, note string) bool {
	t.Helper()
	tx, _ := dbTenant(e, p.ws)
	defer tx.Rollback(context.Background())
	var n int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.note_watches where user_id = $1::uuid and note_id = $2::uuid", p.userID, note).Scan(&n)
	return n == 1
}

func TestWatchFanOutAndDedupe(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	carol := e.addMember(owner, e.signup("Carol", false), "member")
	sp := g.space(owner, "WF", "open")
	page := g.page(owner, sp, "Plan", nil, "x")
	settle(t, e, owner.ws)
	if !watching(t, e, owner, page) {
		t.Fatal("the creator watches the page")
	}
	g.ok(bob, "note.watch", map[string]any{"note_id": page, "watching": true})
	g.ok(bob, "note.watch", map[string]any{"note_id": page, "watching": true}) // idempotent

	// comment: watchers and the thread are told, the actor and non-watchers are not; the commenter starts watching
	c := g.ok(carol, "comment.add", map[string]any{"note_id": page, "body": "Looks odd"})
	settle(t, e, owner.ws)
	if !watching(t, e, carol, page) {
		t.Fatal("a commenter watches the page")
	}
	for _, p := range []who{owner, bob} {
		l := inbox(g, p, "comment")
		if len(l) != 1 || l[0]["thread_id"] != c["id"] || l[0]["note_id"] != page || l[0]["note_title"] != "Plan" {
			t.Fatalf("comment notification: %v", l)
		}
	}
	if l := inbox(g, carol, "comment"); len(l) != 0 {
		t.Fatalf("the actor is not notified: %v", l)
	}

	// a reply in the same thread within 10 minutes by the same actor does not notify again; carol (thread) is told once
	g.ok(owner, "comment.add", map[string]any{"note_id": page, "body": "Fixed", "parent_id": c["id"]})
	g.ok(owner, "comment.add", map[string]any{"note_id": page, "body": "Fixed twice", "parent_id": c["id"]})
	g.ok(owner, "comment.resolve", map[string]any{"id": c["id"]})
	settle(t, e, owner.ws)
	if l := inbox(g, carol, "comment"); len(l) != 1 {
		t.Fatalf("carol: %v", l)
	}
	if l := inbox(g, bob, "comment"); len(l) != 2 { // carol's comment, then owner's burst
		t.Fatalf("bob: %v", l)
	}

	// page updates: one per watcher and actor in 10 minutes; the actor and non-watchers get none
	save := func(body string) {
		g.ok(owner, "note.update", map[string]any{"id": page, "schema": 2, "expected_epoch": 1, "content_md": body})
	}
	save("one")
	save("two")
	settle(t, e, owner.ws)
	if l := inbox(g, bob, "page_updated"); len(l) != 1 || l[0]["thread_id"] != nil {
		t.Fatalf("bob: %v", l)
	}
	if len(inbox(g, owner, "page_updated")) != 0 {
		t.Fatal("the actor was notified")
	}
	g.ok(bob, "note.watch", map[string]any{"note_id": page, "watching": false})
	if watching(t, e, bob, page) {
		t.Fatal("unwatch")
	}
	// a stranger to the page cannot watch it
	g.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	g.code("not_found", bob, "note.watch", map[string]any{"note_id": page, "watching": true})
	g.code("invalid", owner, "note.watch", map[string]any{"note_id": page})
}

func TestWatcherLosesAccessBeforeAndAfterSend(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	carol := e.addMember(owner, e.signup("Carol", false), "member")
	sp := g.space(owner, "WL", "open")
	page := g.page(owner, sp, "Secret plan", nil, "x")
	g.ok(bob, "note.watch", map[string]any{"note_id": page, "watching": true})
	g.ok(carol, "note.watch", map[string]any{"note_id": page, "watching": true})

	// after send: carol got one, then loses access; her list drops it and the watch goes
	g.ok(owner, "comment.add", map[string]any{"note_id": page, "body": "first"})
	settle(t, e, owner.ws)
	if len(inbox(g, carol, "comment")) != 1 || len(inbox(g, bob, "comment")) != 1 {
		t.Fatal("both watchers are told")
	}
	g.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	if l := g.list(carol, "notification.list", map[string]any{}); len(l) != 0 {
		t.Fatalf("carol still sees: %v", l)
	}
	if n := g.ok(carol, "notification.unread_count", map[string]any{})["count"]; n != float64(0) {
		t.Fatalf("badge counts a hidden page: %v", n)
	}
	if watching(t, e, carol, page) {
		t.Fatal("watch of a person who lost access is removed on list")
	}

	// before send: Bob loses access before the event is handled; nothing is stored for him (not just hidden) and
	// the watch is removed
	page2 := g.page(owner, sp, "Second plan", nil, "x")
	g.ok(bob, "note.watch", map[string]any{"note_id": page2, "watching": true})
	g.ok(owner, "note.restrict", map[string]any{"note_id": page2, "restriction": "view"})
	g.ok(owner, "comment.add", map[string]any{"note_id": page2, "body": "second"})
	settle(t, e, owner.ws)
	tx, _ := dbTenant(e, owner.ws)
	defer tx.Rollback(context.Background())
	var n int
	_ = tx.QueryRow(context.Background(), "select count(*) from core.notifications where user_id = $1::uuid and note_id = $2::uuid", bob.userID, page2).Scan(&n)
	if n != 0 {
		t.Fatalf("sent to a person who cannot read the page: %d", n)
	}
	if watching(t, e, bob, page2) {
		t.Fatal("send-time check removes the watch")
	}
}

// A snapshot from the collab gateway is a note.updated event row with the editor as actor; it notifies once.
func TestGatewayUpdateNotifiesOnce(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	sp := g.space(owner, "GW", "open")
	page := g.page(owner, sp, "Live page", nil, "x")
	g.ok(bob, "note.watch", map[string]any{"note_id": page, "watching": true})
	settle(t, e, owner.ws)
	snapshot := func() {
		execTenant(t, e, owner.ws, `with ev as (insert into core.events (workspace_id, note_id, kind, actor_type, actor_id, payload)
			values ($1::uuid, $2::uuid, 'note.updated', 'user', $3, '{"changed":["content"],"version":9}') returning id)
			insert into core.jobs (workspace_id, kind, payload, run_at) select $1::uuid, 'event.dispatch', jsonb_build_object('event_id', id), now() from ev`,
			owner.ws, page, owner.userID)
	}
	snapshot()
	settle(t, e, owner.ws)
	snapshot() // the next debounced snapshot of the same editing session
	settle(t, e, owner.ws)
	if l := inbox(g, bob, "page_updated"); len(l) != 1 || l[0]["actor_name"] != "Olive" {
		t.Fatalf("bob: %v", l)
	}
	if len(inbox(g, owner, "page_updated")) != 0 {
		t.Fatal("the editor was notified")
	}
}

func TestVersionCompareAccess(t *testing.T) {
	e := newEnv(t)
	g := gw{t, e}
	owner := e.signup("Olive", true)
	bob := e.addMember(owner, e.signup("Bob", false), "member")
	sp := g.space(owner, "VC", "open")
	page := g.page(owner, sp, "Doc", nil, "old")
	rev := make([]string, 2)
	for i, body := range []string{"old text", "new text"} {
		execTenant(t, e, owner.ws, `insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason)
			values ($1::uuid, $2::uuid, $3, 'Doc', $4, 'edit')`, owner.ws, page, 900+i, body)
	}
	for i, r := range g.list(bob, "note.revisions", map[string]any{"note_id": page}) {
		if i < 2 {
			rev[1-i] = r["id"].(string)
		}
	}
	// read access is enough to list and fetch both sides
	for i, want := range []string{"old text", "new text"} {
		if got := g.ok(bob, "note.revision", map[string]any{"id": rev[i]})["content_md"]; got != want {
			t.Fatalf("revision %d: %v", i, got)
		}
	}
	// a page that is view-only to Bob: compare works, restore is forbidden
	g.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "edit"})
	g.ok(bob, "note.revision", map[string]any{"id": rev[0]})
	g.code("forbidden", bob, "note.restore", map[string]any{"note_id": page, "revision_id": rev[0], "expected_epoch": 1})
	// no read access: history follows the current access, same answer as a missing version
	g.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	g.code("not_found", bob, "note.revision", map[string]any{"id": rev[0]})
	g.code("not_found", bob, "note.revisions", map[string]any{"note_id": page})
}
