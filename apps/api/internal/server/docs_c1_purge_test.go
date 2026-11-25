package server_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

// Docs C1, R4 (design 5.6, 13.3.2, 13.3.3, 13.5): purge and retention. Real Postgres and the real Go server.

func (d dg) trash(w who, page string) string {
	d.t.Helper()
	return d.ok(w, "note.delete", map[string]any{"id": page})["deletion_id"].(string)
}

// age makes a deletion look old; clock moves the retention clock into the past.
func (d dg) age(did string, days int) {
	d.sql("update core.notes set deleted_at = now() - make_interval(days => $2) where deletion_id = $1::uuid", did, days)
}
func (d dg) clock(days int) {
	d.sql("update core.workspaces set trash_purge_from = now() - make_interval(days => $1) where id = $2::uuid", days, d.ws)
}
func (d dg) due() int {
	d.t.Helper()
	n, err := notes.PurgeDue(context.Background(), d.e.pool, d.ws)
	if err != nil {
		d.t.Fatal(err)
	}
	return n
}
func (d dg) exists(page string) bool {
	return d.scalar("select count(*)::text from core.notes where id = $1::uuid", page) == "1"
}

func TestC1PurgeRemovesEverythingTheDeletionOwned(t *testing.T) {
	m := useMemS3(t)
	d, owner := newDG(t)
	sp := d.space(owner, "PRG", "open")
	parent := d.page(owner, sp, "Doomed root", nil, "SECRET BODY TEXT")
	kid := d.child(owner, sp, "Kid", parent)
	live := d.page(owner, sp, "Survivor", nil, "y")
	file := d.pageFile(owner, kid)
	d.ok(owner, "comment.add", map[string]any{"note_id": parent, "body": "hello"})
	d.sql(`insert into core.public_links (workspace_id, note_id, token_hash, purpose, created_by) values ($1::uuid,$2::uuid,'r4hash','view',$3::uuid)`, d.ws, parent, owner.userID)
	d.ok(owner, "note.update", map[string]any{"id": parent, "title": "Doomed root"})
	did := d.trash(owner, parent)
	// a live child of a purged page (a delete older than A2) goes to the space root
	d.sql("update core.notes set parent_id = $1::uuid where id = $2::uuid", parent, live)

	out := d.ok(owner, "note.purge", map[string]any{"deletion_id": did})
	if out["pages"] != float64(2) || out["files"] != float64(1) {
		t.Fatalf("result %v", out)
	}
	// no row of any table that references a page still names these pages
	rows := d.scalar(`select coalesce(string_agg(format('select count(*) from %s where %I = any($1::uuid[])', c.conrelid::regclass, a.attname), ' union all '), 'select 0')
		from pg_constraint c join pg_attribute a on a.attrelid = c.conrelid and a.attnum = c.conkey[array_upper(c.conkey, 1)]
		where c.contype = 'f' and c.confrelid = 'core.notes'::regclass and c.confdeltype = 'c'`)
	left := 0
	for _, q := range strings.Split(rows, " union all ") {
		n := d.scalar("select coalesce(sum(n),0)::text from ("+q+") t(n)", []string{parent, kid})
		if n != "0" {
			t.Errorf("rows left: %s -> %s", q, n)
		}
		left++
	}
	if left == 0 || d.exists(parent) || d.exists(kid) {
		t.Fatal("pages remain")
	}
	if d.scalar("select coalesce(parent_id::text,'root') from core.notes where id = $1::uuid", live) != "root" {
		t.Fatal("a live child must move to the space root")
	}
	// the audit event carries ids and the root title, never the body; history of the pages keeps its payload with note_id null
	ev := d.scalar("select payload::text from core.events where kind = 'note.purged' and workspace_id = $1::uuid", d.ws)
	if !strings.Contains(ev, parent) || !strings.Contains(ev, kid) || !strings.Contains(ev, "Doomed root") || !strings.Contains(ev, `"manual"`) || strings.Contains(ev, "SECRET BODY") {
		t.Fatalf("payload %s", ev)
	}
	if d.scalar("select count(*)::text from core.events where kind = 'note.created' and note_id is null and payload::text like '%Doomed%' or kind='note.deleted' and note_id is null") == "0" {
		t.Fatal("earlier events must stay")
	}
	// objects go through the gc trigger and the sweep, after commit
	if !m.has(d.ws, file) {
		t.Fatal("the purge must not delete objects inline")
	}
	d.sweep()
	if m.has(d.ws, file) {
		t.Fatal("sweep did not remove the object")
	}
	// and a restore of the purged deletion has nothing to restore
	if code, _ := d.errOf(owner, "note.undelete", map[string]any{"deletion_id": did}); code != "not_found" {
		t.Fatal(code)
	}
}

func TestC1PurgeRollbackKeepsTheObject(t *testing.T) {
	m := useMemS3(t)
	d, owner := newDG(t)
	sp := d.space(owner, "RLB", "open")
	page := d.page(owner, sp, "P", nil, "x")
	file := d.pageFile(owner, page)
	d.trash(owner, page)
	tx, err := dbTenant(d.e, d.ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "delete from core.notes where id = $1::uuid", page); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(context.Background())
	d.sweep()
	if !m.has(d.ws, file) || !d.exists(page) || d.scalar("select count(*)::text from core.file_gc") != "0" {
		t.Fatal("a rolled-back purge must keep page, row and object")
	}
}

func TestC1RetentionClock(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "CLK", "open")
	mk := func() (string, string) {
		p := d.page(owner, sp, "P", nil, "x")
		return p, d.trash(owner, p)
	}
	p, did := mk()
	d.age(did, 100)
	// a new workspace, and an existing one on release day (clock = now): nothing old is purged yet
	if d.due() != 0 || !d.exists(p) {
		t.Fatal("purged before a full period after the clock started")
	}
	d.clock(29)
	if d.due() != 0 {
		t.Fatal("purged 29 days after the clock started")
	}
	d.clock(31)
	if d.due() != 1 || d.exists(p) {
		t.Fatal("a deletion older than the period, after a full period, must be purged")
	}
	// a recent deletion is kept even with an old clock
	p2, did2 := mk()
	d.age(did2, 10)
	if d.due() != 0 || !d.exists(p2) {
		t.Fatal("a 10 day old deletion was purged at 30 days")
	}
	// shortening never purges early: the setting restarts the clock
	d.ok(owner, "trash.retention_set", map[string]any{"days": 365})
	d.clock(400)
	d.ok(owner, "trash.retention_set", map[string]any{"days": 30})
	d.age(did2, 100)
	if d.due() != 0 || !d.exists(p2) {
		t.Fatal("shortening the period purged at once")
	}
	if got := d.ok(owner, "trash.retention_get", map[string]any{}); got["days"] != float64(30) || got["purge_from"] == nil {
		t.Fatal(got)
	}
	d.clock(31)
	// never, and a NULL clock, disable the sweep
	d.ok(owner, "trash.retention_set", map[string]any{"days": 0})
	d.clock(1000)
	if d.due() != 0 || !d.exists(p2) {
		t.Fatal("retention never purged")
	}
	d.ok(owner, "trash.retention_set", map[string]any{"days": 30})
	d.sql("update core.workspaces set trash_purge_from = null where id = $1::uuid", d.ws)
	if d.due() != 0 || !d.exists(p2) {
		t.Fatal("a null clock must disable the purge")
	}
	// the same value again does not restart the clock
	d.clock(31)
	before := d.scalar("select trash_purge_from::text from core.workspaces where id = $1::uuid", d.ws)
	d.ok(owner, "trash.retention_set", map[string]any{"days": 30})
	if d.scalar("select trash_purge_from::text from core.workspaces where id = $1::uuid", d.ws) != before {
		t.Fatal("an unchanged setting must keep the clock")
	}
	if d.due() != 1 || d.exists(p2) {
		t.Fatal("expected the purge")
	}
	if d.scalar("select (payload->>'reason') || (payload->>'retention_days') from core.events where kind = 'note.purged' order by id desc limit 1") != "retention30" {
		t.Fatal("event reason")
	}
}

func TestC1RetentionCommands(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	if got := d.ok(member, "trash.retention_get", map[string]any{}); got["days"] != float64(30) {
		t.Fatal(got)
	}
	if code, _ := d.errOf(member, "trash.retention_set", map[string]any{"days": 90}); code != "forbidden" {
		t.Fatal(code)
	}
	for _, bad := range []any{7, -1, "30", nil} {
		if r := d.run(owner, "trash.retention_set", map[string]any{"days": bad}); r.status == 200 {
			t.Fatalf("days %v accepted", bad)
		}
	}
	if got := d.ok(owner, "trash.retention_set", map[string]any{"days": 90}); got["days"] != float64(90) {
		t.Fatal(got)
	}
	if d.scalar("select count(*)::text from core.events where kind = 'workspace.settings_updated' and payload::text like '%trash_retention_days%'") != "1" {
		t.Fatal("the change must be audited")
	}
}

func TestC1PurgeNeverTouchesWhatItMustKeep(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "KEP", "open")
	d.clock(1000)
	check := func(name string, page, did string) {
		t.Helper()
		d.age(did, 400)
		if d.due() != 0 || !d.exists(page) {
			t.Errorf("%s: purged by the sweep", name)
		}
		if r := d.run(owner, "note.purge", map[string]any{"deletion_id": did}); r.status == 200 || !d.exists(page) {
			t.Errorf("%s: purged by hand", name)
		}
	}
	// a typed page, alone and as a child
	typed := d.page(owner, sp, "Typed", nil, "x")
	tdid := d.trash(owner, typed)
	d.sql("update core.notes set types = '{invoice}' where id = $1::uuid", typed)
	check("typed", typed, tdid)
	root := d.page(owner, sp, "Root", nil, "x")
	tk := d.child(owner, sp, "TypedKid", root)
	rdid := d.trash(owner, root)
	d.sql("update core.notes set types = '{item}' where id = $1::uuid", tk)
	check("typed child", root, rdid)
	// pending edits
	pend := d.page(owner, sp, "Pending", nil, "x")
	did := d.trash(owner, pend)
	d.logEdit(pend)
	check("pending updates", pend, did)
	// a template source, with a binding from a page outside the deletion
	src := d.page(owner, sp, "Source", nil, "x")
	user := d.page(owner, sp, "Made from it", nil, "x")
	d.sql("insert into docs.template_bindings (note_id, workspace_id, source_kind, source_id) values ($1::uuid, $3::uuid, 'page', $2::uuid)", user, src, d.ws)
	check("template source", src, d.trash(owner, src))
	if d.scalar("select count(*)::text from docs.template_bindings") != "1" {
		t.Fatal("the binding of the other page was removed")
	}
	// a binding inside the same deletion does not block it
	a := d.page(owner, sp, "A", nil, "x")
	b := d.child(owner, sp, "B", a)
	d.sql("insert into docs.template_bindings (note_id, workspace_id, source_kind, source_id) values ($1::uuid, $3::uuid, 'page', $2::uuid)", b, a, d.ws)
	d.ok(owner, "note.purge", map[string]any{"deletion_id": d.trash(owner, a)})
	if d.exists(a) || d.exists(b) {
		t.Fatal("a self-contained deletion should purge")
	}
	// a pre-A2 deletion without an id is not listed and cannot be purged
	old := d.page(owner, sp, "Old", nil, "x")
	d.trash(owner, old)
	d.sql("update core.notes set deletion_id = null where id = $1::uuid", old)
	if d.due() != 0 || !d.exists(old) {
		t.Fatal("a page without a deletion id was purged")
	}
}

func TestC1PurgeAndRestoreRaceOnTheStructuralLock(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "RAC", "open")
	// restore wins before purge takes the lock: purge waits on the lock, the restore commits, purge finds nothing
	p := d.page(owner, sp, "P", nil, "x")
	did := d.trash(owner, p)
	d.sql("select 1") // warm
	hold, err := dbTenant(d.e, d.ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(context.Background(), "select pg_advisory_xact_lock(hashtextextended('notes.tree:' || $1::text, 0))", sp); err != nil {
		t.Fatal(err)
	}
	done := make(chan resp, 1)
	go func() { done <- d.run(owner, "note.purge", map[string]any{"deletion_id": did}) }()
	for d.scalar("select count(*)::text from pg_locks where locktype = 'advisory' and not granted") == "0" {
	}
	if _, err := hold.Exec(context.Background(), "update core.notes set deleted_at = null, deletion_id = null, deleted_by = null, deleted_parent_id = null, deleted_restricted_by = null where id = $1::uuid", p); err != nil {
		_ = hold.Rollback(context.Background())
		t.Fatal(err)
	}
	if err := hold.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.status != 404 || !d.exists(p) {
		t.Fatalf("restore first: %d %s", r.status, r.body)
	}
	// purge wins first: the restore finds nothing
	p2 := d.page(owner, sp, "P2", nil, "x")
	did2 := d.trash(owner, p2)
	d.ok(owner, "note.purge", map[string]any{"deletion_id": did2})
	if code, _ := d.errOf(owner, "note.undelete", map[string]any{"deletion_id": did2}); code != "not_found" {
		t.Fatal(code)
	}
	// and racing for real: exactly one of them wins
	for i := 0; i < 5; i++ {
		q := d.page(owner, sp, "Q", nil, "x")
		qd := d.trash(owner, q)
		var wg sync.WaitGroup
		var a, b resp
		wg.Add(2)
		go func() { defer wg.Done(); a = d.run(owner, "note.purge", map[string]any{"deletion_id": qd}) }()
		go func() { defer wg.Done(); b = d.run(owner, "note.undelete", map[string]any{"deletion_id": qd}) }()
		wg.Wait()
		if (a.status == 200) == (b.status == 200) || (a.status == 200) == d.exists(q) {
			t.Fatalf("round %d: purge %d, restore %d, exists %v", i, a.status, b.status, d.exists(q))
		}
	}
}

func TestC1PurgePermissions(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	spaceAdmin := d.member(owner)
	guest := d.e.addMember(owner, d.e.signup("G", false), "guest")
	sp := d.space(owner, "PRM", "open")
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": spaceAdmin.userID, "role": "admin"})
	mk := func(by who) (string, string) {
		p := d.page(owner, sp, "P", nil, "x")
		return p, d.trash(by, p)
	}
	// a member who could list it still cannot purge
	p, did := mk(member)
	if code, _ := d.errOf(member, "note.purge", map[string]any{"deletion_id": did}); code != "forbidden" || !d.exists(p) {
		t.Fatal(code)
	}
	if r := d.run(guest, "note.purge", map[string]any{"deletion_id": did}); r.status != 403 || !d.exists(p) {
		t.Fatalf("guest %d", r.status)
	}
	// an agent or API key is held for a person, and nothing is deleted
	d.sql(`update core.workspaces set settings = settings || '{"agent_tool_groups":["read","write_notes","destructive"]}'::jsonb where id = $1::uuid`, d.ws)
	key := d.e.apiKey(owner)
	r := d.run(key, "note.purge", map[string]any{"deletion_id": did})
	if r.json()["purged"] == true || r.json()["confirmation_id"] == nil || !d.exists(p) {
		t.Fatalf("api key: %d %s", r.status, r.body)
	}
	ag := d.agent(owner, []string{"destructive"})
	ag.Role = command.Admin
	_, _ = d.asPrincipal(ag, "note.purge", map[string]any{"deletion_id": did})
	if d.scalar("select count(*)::text from core.confirmations") == "0" || !d.exists(p) {
		t.Fatal("an agent must wait for approval")
	}
	// a space admin may, and the trash row says so
	rows := d.list(spaceAdmin, "note.trash", map[string]any{})
	if len(rows) != 1 || rows[0]["can_purge"] != true || rows[0]["purge_at"] == nil {
		t.Fatalf("trash row %v", rows)
	}
	if mrows := d.list(member, "note.trash", map[string]any{}); mrows[0]["can_purge"] != false {
		t.Fatalf("member row %v", mrows)
	}
	// a restricted deletion the space admin could not edit is not_found for them
	rp := d.page(owner, sp, "Secret", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": rp, "restriction": "view"})
	rdid := d.trash(owner, rp)
	if code, _ := d.errOf(spaceAdmin, "note.purge", map[string]any{"deletion_id": rdid}); code != "not_found" || !d.exists(rp) {
		t.Fatal(code)
	}
	// an archived space cannot be purged from
	d.archive(sp, true)
	if r := d.run(owner, "note.purge", map[string]any{"deletion_id": did}); r.status == 200 || !d.exists(p) {
		t.Fatalf("archived: %d", r.status)
	}
	d.archive(sp, false)
	d.ok(spaceAdmin, "note.purge", map[string]any{"deletion_id": did})
	if d.exists(p) {
		t.Fatal("space admin purge")
	}
	d.ok(owner, "note.purge", map[string]any{"deletion_id": rdid}) // a workspace admin may
}

func TestC1PurgeSkipsArchivedAndRetries(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "ARS", "open")
	p := d.page(owner, sp, "Archived space", nil, "x")
	did := d.trash(owner, p)
	d.age(did, 100)
	d.clock(100)
	d.archive(sp, true)
	if d.due() != 0 || !d.exists(p) {
		t.Fatal("archived space was purged")
	}
	d.archive(sp, false)
	d.sql("update core.notes set purge_skipped_at = now() - interval '2 days' where deletion_id = $1::uuid", did)
	if d.due() != 1 || d.exists(p) {
		t.Fatal("eligible deletion was not retried")
	}
}

// waitBlocked waits until a statement matching like is waiting on a lock.
func (d dg) waitBlocked(like string) {
	d.t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if d.scalar("select count(*)::text from pg_stat_activity where wait_event_type = 'Lock' and query ilike $1", like) != "0" {
			return
		}
	}
	d.t.Fatalf("nothing blocked on %s", like)
}

func (d dg) retentionDays(w who) float64 {
	d.t.Helper()
	return d.ok(w, "trash.retention_get", map[string]any{})["days"].(float64)
}

// A retention change that commits first is seen by the purge, which then leaves the page alone (order 1).
func TestC1PurgeRetentionChangeWinsLock(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "RTL", "open")
	p := d.page(owner, sp, "Old", nil, "x")
	did := d.trash(owner, p)
	d.age(did, 100)
	d.clock(100)
	tx, err := dbTenant(d.e, d.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), "select id from core.workspaces where id = $1::uuid for update", d.ws); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { n, _ := notes.PurgeDue(context.Background(), d.e.pool, d.ws); done <- n }()
	d.waitBlocked("%core.workspaces%for share%")
	if _, err := tx.Exec(context.Background(), "update core.workspaces set settings = settings || '{\"trash_retention_days\":90}'::jsonb, trash_purge_from = now() where id = $1::uuid", d.ws); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := <-done; n != 0 || !d.exists(p) {
		t.Fatal("purge ignored changed retention")
	}
}

// A purge that holds the workspace row first finishes under the old rule, and the retention change waits for it (order 2).
func TestC1PurgeWinsLockBeforeRetentionChange(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "RTP", "open")
	p := d.page(owner, sp, "Old", nil, "x")
	did := d.trash(owner, p)
	d.age(did, 100)
	d.clock(100)
	// Hold the page row: the purge takes the workspace lock first and then waits here, still holding it.
	tx, err := dbTenant(d.e, d.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), "select id from core.notes where id = $1::uuid for update", p); err != nil {
		t.Fatal(err)
	}
	purged := make(chan int, 1)
	go func() { n, _ := notes.PurgeDue(context.Background(), d.e.pool, d.ws); purged <- n }()
	d.waitBlocked("%from core.notes where deletion_id%for update%")
	set := make(chan resp, 1)
	go func() { set <- d.run(owner, "trash.retention_set", map[string]any{"days": 90}) }()
	d.waitBlocked("%core.workspaces%for no key update%")
	select {
	case <-set:
		t.Fatal("retention changed while a purge held the workspace row")
	default:
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := <-purged; n != 1 || d.exists(p) {
		t.Fatal("the purge that got the lock first was lost")
	}
	<-set
	if d.retentionDays(owner) != 90 {
		t.Fatal("retention change lost")
	}
}

// Manual purge and the sweep both refuse an archived space and an archived page.
func TestC1PurgeRefusesArchived(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "ARC", "open")
	d.clock(100)
	p := d.page(owner, sp, "In archived space", nil, "x")
	did := d.trash(owner, p)
	d.age(did, 100)
	d.archive(sp, true)
	d.archivedRefusal(owner, "note.purge", map[string]any{"deletion_id": did})
	if d.due() != 0 || !d.exists(p) {
		t.Fatal("sweep purged an archived space")
	}
	d.archive(sp, false)

	a := d.page(owner, sp, "Archived page", nil, "x")
	adid := d.trash(owner, a)
	d.age(adid, 100)
	d.sql("update core.notes set freshness = 'archived' where id = $1::uuid", a)
	if code, _ := d.errOf(owner, "note.purge", map[string]any{"deletion_id": adid}); code == "" {
		t.Fatal("manual purge of an archived page was allowed")
	}
	d.sql("update core.notes set purge_skipped_at = null") // the archived-space sweep above recorded a skip for p
	if d.due() != 1 || d.exists(p) || !d.exists(a) {       // only the page of the now-live space goes
		t.Fatal("sweep purged an archived page")
	}
}

// Old deletions that can never be purged must not hide newer eligible ones: each is recorded and left alone for a day.
func TestC1PurgeSweepIsNotStarvedByIneligibleDeletions(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "STV", "open")
	d.clock(400)
	const stuck = 201 // one more than a sweep run takes
	ids := make([]string, 0, stuck)
	for i := 0; i < stuck; i++ {
		ids = append(ids, d.page(owner, sp, fmt.Sprintf("Invoice %d", i), nil, "x"))
	}
	good := d.page(owner, sp, "Plain and newer", nil, "x")
	// typed pages are never purged; trashed in one statement, each its own deletion (the command is rate limited)
	d.sql(`update core.notes set types = '{invoice}', deletion_id = gen_random_uuid(), deleted_at = now() - interval '300 days', deleted_by = $2::uuid
	       where id = any($1::text[]::uuid[])`, ids, owner.userID)
	d.age(d.trash(owner, good), 100)
	if d.due() != 0 || !d.exists(good) {
		t.Fatal("first run: the 200 oldest are all ineligible, nothing can be purged yet")
	}
	if n := d.scalar("select count(distinct deletion_id)::text from core.notes where purge_skipped_at is not null"); n != "200" {
		t.Fatalf("skips recorded: %s", n)
	}
	if d.due() != 1 || d.exists(good) {
		t.Fatal("second run did not reach the newer eligible deletion")
	}
	// a skip is retried after a day
	d.sql("update core.notes set purge_skipped_at = now() - interval '2 days' where purge_skipped_at is not null")
	if d.due() != 0 || d.scalar("select count(distinct deletion_id)::text from core.notes where purge_skipped_at > now() - interval '1 hour'") != "200" {
		t.Fatal("old skips were not retried")
	}
}
