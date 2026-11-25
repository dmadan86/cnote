package server_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/files"
)

// Docs parity A2, work package L1 (docs-parity 7.9 as revised by 7.10): move, reorder, delete with the coordinated fold,
// trash, undelete and duplicate. Real Postgres, the real Go server and, for the fold, the real collab service.

const restricted = "Some pages here are restricted"

// foldFixture is a Yjs update that appends the paragraph "folded edit" to an empty page (made with apps/collab).
const foldFixture = "AQOy8hkABwEHZGVmYXVsdAMJcGFyYWdyYXBoBwCy8hkABgQAsvIZAQtmb2xkZWQgZWRpdAA="

func (d dg) errOf(w who, cmd string, in any) (code, msg string) {
	d.t.Helper()
	r := d.run(w, cmd, in)
	e, _ := r.json()["error"].(map[string]any)
	if e == nil {
		d.t.Fatalf("%s %v: want an error, got %d %s", cmd, in, r.status, r.body)
	}
	return e["code"].(string), e["message"].(string)
}

func (d dg) refused(w who, cmd string, in any) {
	d.t.Helper()
	if code, msg := d.errOf(w, cmd, in); code != "forbidden" || msg != restricted {
		d.t.Fatalf("%s: want forbidden %q, got %s %q", cmd, restricted, code, msg)
	}
}

func (d dg) pos(id string) string {
	return d.scalar("select coalesce(position,0)::text from core.notes where id = $1::uuid", id)
}

func (d dg) child(w who, space, title string, parent string) string {
	return d.page(w, space, title, parent, "x")
}

func (d dg) asPrincipal(p command.Principal, cmd string, in any) (map[string]any, error) {
	b, _ := json.Marshal(in)
	reg := command.NewRegistry(modules.Commands()...)
	out, err := reg.Execute(context.Background(), d.e.pool, cmd, b, p, command.ExecOptions{})
	var m map[string]any
	if err == nil {
		_ = json.Unmarshal(out, &m)
	}
	return m, err
}

func (d dg) agent(w who, scopes []string) command.Principal {
	a := d.scalar("select gen_random_uuid()::text")
	u := w.userID
	return command.Principal{Kind: command.KindAgent, AgentID: &a, UserID: &u, WorkspaceID: w.ws, Role: command.Member, Scopes: scopes}
}

func isRestricted(err error) bool {
	e, ok := err.(*command.Error)
	return ok && e.Code == command.Forbidden && e.Message == restricted
}

// ---------- structural lock ----------

func TestA2OpposingMovesLeaveNoCycle(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	for round := 0; round < 8; round++ {
		a, b := d.page(owner, sp, "A", nil, "x"), d.page(owner, sp, "B", nil, "x")
		var wg sync.WaitGroup
		res := make([]resp, 2)
		for i, mv := range [][2]string{{a, b}, {b, a}} {
			wg.Add(1)
			go func(i int, id, parent string) {
				defer wg.Done()
				res[i] = d.run(owner, "note.move", map[string]any{"id": id, "parent_id": parent})
			}(i, mv[0], mv[1])
		}
		wg.Wait()
		okN := 0
		for _, r := range res {
			if r.status == 200 {
				okN++
			}
		}
		if okN != 1 {
			t.Fatalf("round %d: exactly one move must win: %d %s / %d %s", round, res[0].status, res[0].body, res[1].status, res[1].body)
		}
		if d.scalar(`select count(*)::text from (with recursive up as (select id, parent_id, 1 dd from core.notes where id in ($1::uuid,$2::uuid)
		   union all select p.id, p.parent_id, up.dd+1 from core.notes p join up on p.id = up.parent_id where up.dd < 5) select 1 from up where dd = 5) x`, a, b) != "0" {
			t.Fatal("cycle")
		}
	}
}

func TestA2InsertRacesDeleteOfItsParent(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	for round := 0; round < 8; round++ {
		parent := d.page(owner, sp, "P", nil, "x")
		d.child(owner, sp, "kid", parent)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			d.run(owner, "note.create", map[string]any{"space_id": sp, "title": "late", "parent_id": parent})
		}()
		go func() { defer wg.Done(); d.run(owner, "note.delete", map[string]any{"id": parent}) }()
		wg.Wait()
		if n := d.scalar("select count(*)::text from core.notes where parent_id = $1::uuid and deleted_at is null", parent); n != "0" {
			t.Fatalf("round %d: %s live pages left under a deleted parent", round, n)
		}
	}
}

func TestA2ConcurrentReordersKeepPositionsUnique(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	var pages []string
	for i := 0; i < 8; i++ {
		pages = append(pages, d.page(owner, sp, "p"+strconv.Itoa(i), nil, "x"))
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := map[string]any{"id": pages[i%8], "before_id": pages[(i*3+1)%8]}
			if i%5 == 0 {
				in["before_id"] = nil
			}
			d.run(owner, "note.reorder", in)
		}(i)
	}
	wg.Wait()
	if d.scalar("select (count(*) = count(distinct position))::text from core.notes where space_id = $1::uuid and deleted_at is null", sp) != "true" {
		t.Fatal("positions are not unique after concurrent reorders")
	}
}

// ---------- note.update parent change is a move ----------

func TestA2NoteUpdateParentChangeUsesTheMoveGate(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	guest := d.e.addMember(owner, d.e.signup("Gst", false), "guest")
	sp := d.space(owner, "OPN", "open")
	target := d.page(owner, sp, "Target", nil, "x")
	page := d.page(owner, sp, "Page", nil, "x")
	hidden := d.child(owner, sp, "Hidden", page)
	d.ok(owner, "note.restrict", map[string]any{"note_id": hidden, "restriction": "view"})
	// a guest with edit access to the page: title edits work, a parent change never does
	d.ok(owner, "note.share", map[string]any{"note_id": page, "email": guest.email, "level": "edit"})
	d.ok(guest, "note.update", map[string]any{"id": page, "title": "Renamed"})
	d.code("forbidden", guest, "note.update", map[string]any{"id": page, "parent_id": target})
	// an API key and an AI employee act with the person's access: the hidden sub-page blocks the move, and nothing is named
	key := d.e.apiKey(member)
	d.refused(key, "note.update", map[string]any{"id": page, "parent_id": target})
	d.refused(member, "note.update", map[string]any{"id": page, "parent_id": target})
	if _, err := d.asPrincipal(d.agent(member, nil), "note.update", map[string]any{"id": page, "parent_id": target}); !isRestricted(err) {
		t.Fatalf("AI employee: %v", err)
	}
	// the read-only AI scope cannot change the tree at all, but may list the trash
	ro := d.agent(owner, []string{"read"})
	for cmd, in := range map[string]any{
		"note.update": map[string]any{"id": page, "parent_id": target}, "note.move": map[string]any{"id": page, "parent_id": target},
		"note.reorder": map[string]any{"id": page, "before_id": nil}, "note.delete": map[string]any{"id": page},
		"note.undelete": map[string]any{"deletion_id": target}, "note.duplicate": map[string]any{"id": page},
	} {
		if _, err := d.asPrincipal(ro, cmd, in); err == nil {
			t.Fatalf("%s must not run in the read-only scope", cmd)
		}
	}
	if _, err := d.asPrincipal(ro, "note.trash", map[string]any{}); err != nil {
		t.Fatalf("trash in read scope: %v", err)
	}
	// the owner can: one version bump, parent changed, a note.moved event
	v := d.ok(owner, "note.get", map[string]any{"id": page})["version"].(float64)
	got := d.ok(owner, "note.update", map[string]any{"id": page, "parent_id": target, "title": "Moved"})
	if got["parent_id"] != target || got["version"].(float64) != v+1 {
		t.Fatalf("update: %v", got)
	}
	if d.scalar("select count(*)::text from core.events where note_id = $1::uuid and kind = 'note.moved'", page) != "1" {
		t.Fatal("no note.moved event")
	}
}

// ---------- move, reorder ----------

func TestA2MoveRules(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	sp, other := d.space(owner, "OPN", "open"), d.space(owner, "TWO", "open")
	root := d.page(owner, sp, "Root", nil, "x")
	kid := d.child(owner, sp, "Kid", root)
	grand := d.child(owner, sp, "Grand", kid)
	// cycle, other space, missing before
	d.code("invalid", owner, "note.move", map[string]any{"id": root, "parent_id": grand})
	d.code("invalid", owner, "note.move", map[string]any{"id": kid, "parent_id": d.page(owner, other, "Foreign", nil, "x")})
	d.code("invalid", owner, "note.move", map[string]any{"id": kid, "parent_id": nil, "before_id": grand})
	d.code("invalid", owner, "note.move", map[string]any{"id": kid}) // parent_id is required
	// restricted new parent: moving in needs only the edit checks
	res := d.page(owner, sp, "Restricted", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": res, "restriction": "edit"})
	d.ok(owner, "note.share", map[string]any{"note_id": res, "email": strings.Replace("x", "x", d.scalar("select email from core.users where id = $1::uuid", member.userID), 1), "level": "edit"})
	mine := d.page(member, sp, "Mine", nil, "x")
	d.ok(member, "note.move", map[string]any{"id": mine, "parent_id": res})
	// a restriction released by leaving needs the owner of the restricted page or an admin; hidden and read-only look the same
	d.refused(member, "note.move", map[string]any{"id": mine, "parent_id": nil})
	got := d.ok(owner, "note.move", map[string]any{"id": mine, "parent_id": nil})
	if got["parent_id"] != nil || d.scalar("select (payload->>'restrictions_released') from core.events where note_id = $1::uuid and kind = 'note.moved' order by id desc limit 1", mine) != "true" {
		t.Fatalf("release: %v", got)
	}
	ro := d.page(owner, sp, "ReadOnly", nil, "x")
	hid := d.page(owner, sp, "Hidden", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": ro, "restriction": "edit"})
	d.ok(owner, "note.restrict", map[string]any{"note_id": hid, "restriction": "view"})
	_, m1 := d.errOf(member, "note.move", map[string]any{"id": ro, "parent_id": root})
	_, m2 := d.errOf(member, "note.move", map[string]any{"id": hid, "parent_id": root})
	if m1 != restricted {
		t.Fatalf("read-only: %q", m1)
	}
	if m2 != "note not found" { // the page the caller named is checked first, as before
		t.Fatalf("hidden: %q", m2)
	}
	// a hidden page below the moved one gives the same generic text
	d.child(owner, sp, "Sub", mine)
	h2 := d.child(owner, sp, "SubHidden", mine)
	d.ok(owner, "note.restrict", map[string]any{"note_id": h2, "restriction": "view"})
	d.refused(member, "note.move", map[string]any{"id": mine, "parent_id": root})
}

func TestA2ReorderGapsLegacyAndHiddenSiblings(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	var p []string
	for i := 0; i < 4; i++ {
		p = append(p, d.page(owner, sp, "p"+strconv.Itoa(i), nil, "x"))
	}
	if d.pos(p[0]) != "1024" || d.pos(p[3]) != "4096" {
		t.Fatalf("trigger positions: %s %s", d.pos(p[0]), d.pos(p[3]))
	}
	// legacy: every position is 0, order falls back to creation time; a reorder renumbers
	d.sql("update core.notes set position = 0 where space_id = $1::uuid", sp)
	d.ok(owner, "note.reorder", map[string]any{"id": p[3], "before_id": p[0]})
	if got := d.scalar("select string_agg(title, ',' order by position, created_at, id) from core.notes where space_id = $1::uuid", sp); got != "p3,p0,p1,p2" {
		t.Fatalf("order: %s", got)
	}
	// exhaust the gap between two neighbours: the siblings are renumbered (hidden ones too)
	d.sql("update core.notes set position = 10 where id = $1::uuid", p[0])
	d.sql("update core.notes set position = 11 where id = $1::uuid", p[1])
	d.ok(owner, "note.restrict", map[string]any{"note_id": p[1], "restriction": "view"})
	d.ok(owner, "note.reorder", map[string]any{"id": p[2], "before_id": p[1]})
	if got := d.scalar("select string_agg(title, ',' order by position, created_at, id) from core.notes where space_id = $1::uuid", sp); got != "p0,p2,p1,p3" {
		t.Fatalf("order after renumber: %s", got)
	}
	if d.scalar("select (count(*) = count(distinct position))::text from core.notes where space_id = $1::uuid", sp) != "true" {
		t.Fatal("positions not unique")
	}
	// before itself is a no-op; last when null; a member cannot reorder a page they cannot edit
	before := d.pos(p[0])
	d.ok(owner, "note.reorder", map[string]any{"id": p[0], "before_id": p[0]})
	if d.pos(p[0]) != before {
		t.Fatal("before == id must not move")
	}
	d.ok(owner, "note.reorder", map[string]any{"id": p[0], "before_id": nil})
	if last := d.scalar("select title from core.notes where space_id = $1::uuid order by position desc limit 1", sp); last != "p0" {
		t.Fatalf("last: %s", last)
	}
	d.ok(owner, "note.restrict", map[string]any{"note_id": p[2], "restriction": "edit"})
	d.refused(member, "note.reorder", map[string]any{"id": p[2], "before_id": nil})
	// reorder does not touch the epoch or the version
	if d.scalar("select (collab_epoch = 1 and version = 1)::text from core.notes where id = $1::uuid", p[3]) != "true" {
		t.Fatal("epoch or version changed")
	}
}

func TestA2DepthLimitAndAccessFailsClosedOnDeepAndCyclicChains(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	// existing data: no tree in this database is deeper than 50 and none loops (the check the deploy notes ask for)
	if n := d.scalar(`select count(*)::text from (with recursive up as (select id, parent_id, 1 dd from core.notes
	   union all select p.id, p.parent_id, up.dd+1 from core.notes p join up on p.id = up.parent_id where up.dd < 52) select 1 from up where dd > 50) x`); n != "0" {
		t.Fatalf("%s pages deeper than 50", n)
	}
	chain := []string{d.page(owner, sp, "L1", nil, "x")}
	for i := 2; i <= 50; i++ {
		chain = append(chain, d.child(owner, sp, "L"+strconv.Itoa(i), chain[i-2]))
	}
	d.code("invalid", owner, "note.create", map[string]any{"space_id": sp, "title": "L51", "parent_id": chain[49]})
	loose := d.page(owner, sp, "Loose", nil, "x")
	d.code("invalid", owner, "note.move", map[string]any{"id": loose, "parent_id": chain[49]})
	d.code("invalid", owner, "note.update", map[string]any{"id": loose, "parent_id": chain[49]})
	d.code("invalid", owner, "note.move", map[string]any{"id": chain[0], "parent_id": loose}) // 51 deep after the move
	d.ok(member, "note.get", map[string]any{"id": chain[49]})
	// data made behind the commands' back: a chain of 51 and a cycle fail closed for members, admins keep access
	d.sql("insert into core.notes (workspace_id, space_id, parent_id, title, created_by) select workspace_id, space_id, $1::uuid, 'L51', created_by from core.notes where id = $1::uuid", chain[49])
	deep := d.scalar("select id::text from core.notes where title = 'L51' and space_id = $1::uuid", sp)
	d.code("not_found", member, "note.get", map[string]any{"id": deep})
	d.ok(owner, "note.get", map[string]any{"id": deep})
	a, b := d.page(owner, sp, "CycA", nil, "x"), d.page(owner, sp, "CycB", nil, "x")
	d.sql("update core.notes set parent_id = $2::uuid where id = $1::uuid", a, b)
	d.sql("update core.notes set parent_id = $2::uuid where id = $1::uuid", b, a)
	d.code("not_found", member, "note.get", map[string]any{"id": a})
	d.ok(owner, "note.get", map[string]any{"id": b})
	if d.scalar("select core.note_access_any($1::uuid, $2::uuid, false, array[$3::uuid])", a, member.userID, sp) != "none" {
		t.Fatal("note_access_any must fail closed on a cycle")
	}
}

// ---------- delete, trash, undelete ----------

func TestA2DeleteSubtreeKeepsOwnDeletionsAndRefusesHiddenOrTyped(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	top := d.page(owner, sp, "Top", nil, "x")
	mid := d.child(owner, sp, "Mid", top)
	leaf := d.child(owner, sp, "Leaf", mid)
	old := d.child(owner, sp, "OldTrash", top)
	d.ok(owner, "note.delete", map[string]any{"id": old})
	oldDel := d.scalar("select deletion_id::text from core.notes where id = $1::uuid", old)
	got := d.ok(owner, "note.delete", map[string]any{"id": top})
	if got["deleted"] != true || got["count"].(float64) != 3 {
		t.Fatalf("delete: %v", got)
	}
	did := got["deletion_id"].(string)
	if d.scalar("select count(*)::text from core.notes where deletion_id = $1::uuid and deleted_by = $2::uuid", did, owner.userID) != "3" ||
		d.scalar("select (deleted_parent_id = $2::uuid)::text from core.notes where id = $1::uuid", mid, top) != "true" ||
		d.scalar("select deletion_id::text from core.notes where id = $1::uuid", old) != oldDel {
		t.Fatal("deletion columns")
	}
	if d.scalar("select count(*)::text from core.notes where deletion_id = $1::uuid", did) != "3" {
		t.Fatal("the subtree is one deletion")
	}
	if d.scalar("select count(*)::text from core.events where kind = 'note.deleted' and payload->>'deletion_id' = $1", did) != "3" {
		t.Fatal("one note.deleted per page")
	}
	d.code("not_found", owner, "note.get", map[string]any{"id": leaf})
	// a hidden descendant refuses the whole delete with the generic text
	p := d.page(owner, sp, "P", nil, "x")
	d.child(owner, sp, "Open", p)
	hid := d.child(owner, sp, "Hid", p)
	d.ok(owner, "note.restrict", map[string]any{"note_id": hid, "restriction": "view"})
	d.refused(member, "note.delete", map[string]any{"id": p})
	// a typed page below the root refuses; a typed root behaves as before
	q := d.page(owner, sp, "Q", nil, "x")
	typed := d.ok(owner, "note.create", map[string]any{"space_id": sp, "title": "Typed", "parent_id": q, "types": []string{"deal"}})["id"].(string)
	if code, msg := d.errOf(owner, "note.delete", map[string]any{"id": q}); code != "invalid" || msg != "This page has items that cannot be deleted with it" {
		t.Fatalf("typed: %s %s", code, msg)
	}
	d.ok(owner, "note.delete", map[string]any{"id": typed})
	// two concurrent deletes of overlapping subtrees: no page gets two deletions
	a := d.page(owner, sp, "A", nil, "x")
	b := d.child(owner, sp, "B", a)
	d.child(owner, sp, "C", b)
	var wg sync.WaitGroup
	for _, id := range []string{a, b} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); d.run(owner, "note.delete", map[string]any{"id": id}) }(id)
	}
	wg.Wait()
	if n := d.scalar("select count(distinct deletion_id)::text from core.notes where id in (select id from core.notes where title in ('A','B','C') and space_id = $1::uuid)", sp); n != "2" && n != "1" {
		t.Fatalf("deletions: %s", n)
	}
	if d.scalar("select count(*)::text from core.notes where title in ('A','B','C') and space_id = $1::uuid and deleted_at is null", sp) != "0" {
		t.Fatal("all deleted")
	}
	// the checkpoint holds the content at deletion
	if d.scalar("select count(*)::text from core.note_revisions where note_id = $1::uuid and content_md = 'x'", leaf) != "1" {
		t.Fatal("no checkpoint revision")
	}
}

func TestA2TrashListsOnlyWhatTheCallerCouldRestoreAndPagesByTimestampAndID(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	var ids []string
	for i := 0; i < 5; i++ {
		id := d.page(owner, sp, "t"+strconv.Itoa(i), nil, "x")
		if i == 2 { // restricted before it was deleted: the member could not restore it
			d.ok(owner, "note.restrict", map[string]any{"note_id": id, "restriction": "edit"})
			d.ok(owner, "note.delete", map[string]any{"id": id})
		} else {
			d.ok(member, "note.delete", map[string]any{"id": id})
		}
		ids = append(ids, id)
	}
	// equal timestamps for the first four; the fifth is restricted before it was deleted, so the member cannot restore it
	d.sql("update core.notes set deleted_at = '2026-01-01T10:00:00Z' where id = any($1::text[]::uuid[])", ids)
	seen := map[string]bool{}
	var cursor map[string]any
	pages := 0
	for {
		in := map[string]any{"limit": 2}
		if cursor != nil {
			in["before"], in["before_id"] = cursor["deleted_at"], cursor["deletion_id"]
		}
		rows := d.list(member, "note.trash", in)
		pages++
		for _, r := range rows {
			if seen[r["deletion_id"].(string)] {
				t.Fatalf("duplicate entry %v", r)
			}
			seen[r["deletion_id"].(string)] = true
			if r["restore_to"] != "original" && r["restore_to"] != "space_root" {
				t.Fatalf("restore_to: %v", r)
			}
		}
		if len(rows) < 2 {
			break
		}
		cursor = rows[len(rows)-1]
		if pages > 10 {
			t.Fatal("paging does not end")
		}
	}
	if len(seen) != 4 { // the restricted deletion is never listed, counted or hinted at
		t.Fatalf("member sees %d deletions", len(seen))
	}
	if all := d.list(owner, "note.trash", map[string]any{}); len(all) != 5 {
		t.Fatalf("owner sees %d", len(all))
	}
	// a full page is filled even when entries in between are filtered out
	if got := d.list(member, "note.trash", map[string]any{"limit": 3}); len(got) != 3 {
		t.Fatalf("scan until full: %d", len(got))
	}
	// a viewer of the space lists nothing (restore needs space write access)
	viewer := d.e.addMember(owner, d.e.signup("Vw", false), "member")
	priv := d.space(owner, "PRV", "private")
	d.sql("insert into core.space_members (workspace_id, space_id, user_id, role) values ($1::uuid,$2::uuid,$3::uuid,'viewer')", owner.ws, priv, viewer.userID)
	pp := d.page(owner, priv, "p", nil, "x")
	d.ok(owner, "note.delete", map[string]any{"id": pp})
	if got := d.list(viewer, "note.trash", map[string]any{"space_id": priv}); len(got) != 0 {
		t.Fatalf("viewer: %v", got)
	}
}

func TestA2UndeleteInPlaceBumpsEpochAndRestoresPositionAndRestrictions(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	top := d.page(owner, sp, "Top", nil, "x")
	kid := d.child(owner, sp, "Kid", top)
	d.ok(owner, "note.restrict", map[string]any{"note_id": kid, "restriction": "edit"})
	del := d.ok(owner, "note.delete", map[string]any{"id": top})["deletion_id"].(string)
	got := d.ok(owner, "note.undelete", map[string]any{"deletion_id": del})
	if got["placed"] != "original" || got["count"].(float64) != 2 || got["root_id"] != top {
		t.Fatalf("undelete: %v", got)
	}
	if d.scalar("select (collab_epoch = 2 and deleted_at is null and deletion_id is null and deleted_by is null and parent_id = $2::uuid and restriction = 'edit')::text from core.notes where id = $1::uuid", kid, top) != "true" {
		t.Fatal("restored state")
	}
	if d.scalar("select count(*)::text from core.events where kind = 'note.undeleted' and payload->>'deletion_id' = $1", del) != "2" {
		t.Fatal("events")
	}
	d.code("not_found", owner, "note.undelete", map[string]any{"deletion_id": del}) // gone: nothing to restore
	// a page deleted at a position that is taken meanwhile goes last
	a := d.page(owner, sp, "A", nil, "x")
	posA := d.pos(a)
	d.ok(owner, "note.delete", map[string]any{"id": a})
	n := d.page(owner, sp, "N", nil, "x")
	d.sql("update core.notes set position = $2 where id = $1::uuid", n, posA)
	d.ok(owner, "note.undelete", map[string]any{"deletion_id": d.scalar("select deletion_id::text from core.notes where id = $1::uuid", a)})
	if d.pos(a) == posA {
		t.Fatal("restored into a taken position")
	}
}

func TestA2UndeleteRestrictionEvidenceAndAuthority(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	mail := d.scalar("select email from core.users where id = $1::uuid", member.userID)
	// R is restricted (edit) and the member may edit under it; P sits under R and C under P
	r := d.page(owner, sp, "R", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": r, "restriction": "edit"})
	d.ok(owner, "note.share", map[string]any{"note_id": r, "email": mail, "level": "edit"})
	pg := d.child(owner, sp, "P", r)
	c := d.child(member, sp, "C", pg)
	del := d.ok(member, "note.delete", map[string]any{"id": c})["deletion_id"].(string)
	if d.scalar("select deleted_restricted_by::text from core.notes where id = $1::uuid", c) != "{"+r+"}" {
		t.Fatalf("evidence: %s", d.scalar("select deleted_restricted_by::text from core.notes where id = $1::uuid", c))
	}
	// the parent moves out of the restricted area (by the owner): restoring in place would release R for the member's page
	d.ok(owner, "note.move", map[string]any{"id": pg, "parent_id": nil})
	entries := d.list(member, "note.trash", map[string]any{})
	if len(entries) != 1 || entries[0]["restore_to"] != "blocked" || entries[0]["blocked_reason"] == nil {
		t.Fatalf("trash must show the block: %v", entries)
	}
	if code, msg := d.errOf(member, "note.undelete", map[string]any{"deletion_id": del}); code != "conflict" || msg != entries[0]["blocked_reason"] {
		t.Fatalf("member restore: %s %s", code, msg)
	}
	d.ok(owner, "note.undelete", map[string]any{"deletion_id": del}) // the owner of R may
	// the parent in the trash and the page inside a restricted area: refused for a member, an admin goes to the space root
	r2 := d.page(owner, sp, "R2", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": r2, "restriction": "edit"})
	d.ok(owner, "note.share", map[string]any{"note_id": r2, "email": mail, "level": "edit"})
	p2 := d.child(owner, sp, "P2", r2)
	c2 := d.child(member, sp, "C2", p2)
	delC := d.ok(member, "note.delete", map[string]any{"id": c2})["deletion_id"].(string)
	d.ok(owner, "note.delete", map[string]any{"id": p2})
	if code, msg := d.errOf(member, "note.undelete", map[string]any{"deletion_id": delC}); code != "conflict" || !strings.Contains(msg, "parent page is in the trash") {
		t.Fatalf("trashed parent: %s %s", code, msg)
	}
	if got := d.ok(owner, "note.undelete", map[string]any{"deletion_id": delC}); got["placed"] != "space_root" {
		t.Fatalf("admin: %v", got)
	}
	// a parent in the trash without a restriction in the way: the page goes to the space root for anyone
	p3 := d.page(owner, sp, "P3", nil, "x")
	c3 := d.child(member, sp, "C3", p3)
	delC3 := d.ok(member, "note.delete", map[string]any{"id": c3})["deletion_id"].(string)
	d.ok(owner, "note.delete", map[string]any{"id": p3})
	if got := d.ok(member, "note.undelete", map[string]any{"deletion_id": delC3}); got["placed"] != "space_root" {
		t.Fatalf("plain: %v", got)
	}
	// evidence missing (deleted before A2): only an admin may restore
	old := d.page(member, sp, "Old", nil, "x")
	d.ok(member, "note.delete", map[string]any{"id": old})
	d.sql("update core.notes set deleted_restricted_by = null, deleted_parent_id = null where id = $1::uuid", old)
	delOld := d.scalar("select deletion_id::text from core.notes where id = $1::uuid", old)
	if code, msg := d.errOf(member, "note.undelete", map[string]any{"deletion_id": delOld}); code != "conflict" || !strings.Contains(msg, "before restore rules") {
		t.Fatalf("no evidence: %s %s", code, msg)
	}
	d.ok(owner, "note.undelete", map[string]any{"deletion_id": delOld})
	// a deletion the caller could not edit is invisible: not found, never forbidden
	hid := d.page(owner, sp, "Hid", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": hid, "restriction": "view"})
	delHid := d.ok(owner, "note.delete", map[string]any{"id": hid})["deletion_id"].(string)
	d.code("not_found", member, "note.undelete", map[string]any{"deletion_id": delHid})
}

// ---------- the coordinated fold (needs the real collab service) ----------

func newCollabEnv(t *testing.T) (dg, who) {
	t.Helper()
	d, owner := newDG(t)
	startCollab(t)
	return d, owner
}

func (d dg) logEdit(page string) {
	d.sql(`insert into core.note_updates (workspace_id, note_id, epoch, update) select workspace_id, id, collab_epoch, $2 from core.notes where id = $1::uuid`,
		page, mustB64(foldFixture))
}

func mustB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestA2DeleteFoldsEditsOfACrashedOwnerWithNoOpenRoom(t *testing.T) {
	d, owner := newCollabEnv(t)
	sp := d.space(owner, "OPN", "open")
	top := d.page(owner, sp, "Top", nil, "")
	kid := d.child(owner, sp, "Kid", top)
	d.logEdit(top) // the gateway that held the room died after logging this edit: nothing absorbed it
	del := d.ok(owner, "note.delete", map[string]any{"id": top})["deletion_id"].(string)
	if d.scalar("select content_md from core.notes where id = $1::uuid", top) != "folded edit" || d.scalar("select count(*)::text from core.note_updates where note_id = $1::uuid", top) != "0" {
		t.Fatal("the logged edit was not folded into the page")
	}
	if d.scalar("select count(*)::text from core.note_revisions where note_id = $1::uuid and content_md = 'folded edit'", top) != "1" {
		t.Fatal("no checkpoint from the folded content")
	}
	_ = kid
	// restore: nothing pending, the epoch moves, the folded text is the page
	d.ok(owner, "note.undelete", map[string]any{"deletion_id": del})
	if d.scalar("select content_md || ':' || collab_epoch from core.notes where id = $1::uuid", top) != "folded edit:2" {
		t.Fatal("restored content")
	}
	// undelete refuses while edits are still unfolded
	d.ok(owner, "note.delete", map[string]any{"id": top})
	d.logEdit(top)
	if code, msg := d.errOf(owner, "note.undelete", map[string]any{"deletion_id": d.scalar("select deletion_id::text from core.notes where id = $1::uuid", top)}); code != "conflict" || !strings.Contains(msg, "still being saved") {
		t.Fatalf("pending: %s %s", code, msg)
	}
}

func TestA2FailedFoldRefusesTheDeleteAndNothingIsStranded(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	page := d.page(owner, sp, "Page", nil, "")
	d.logEdit(page)
	// no gateway configured, then a gateway that is down: the delete is refused with the exact text and the page stays live
	t.Setenv("COLLAB_URL", "")
	t.Setenv("COLLAB_INTERNAL_SECRET", "")
	for round := 0; round < 2; round++ {
		if code, msg := d.errOf(owner, "note.delete", map[string]any{"id": page}); code != "conflict" || msg != "Couldn't save the latest edits. Try again." {
			t.Fatalf("round %d: %s %q", round, code, msg)
		}
		t.Setenv("COLLAB_URL", "http://127.0.0.1:1")
		t.Setenv("COLLAB_INTERNAL_SECRET", "x")
	}
	if d.scalar("select (deleted_at is null and deletion_id is null)::text from core.notes where id = $1::uuid", page) != "true" || d.scalar("select count(*)::text from core.note_updates where note_id = $1::uuid", page) != "1" {
		t.Fatal("a refused delete changed the page")
	}
	// a page without pending edits does not need the gateway at all
	plain := d.page(owner, sp, "Plain", nil, "x")
	d.ok(owner, "note.delete", map[string]any{"id": plain})
	// the gateway comes back (a restart): the same delete now works
	startCollab(t)
	d.ok(owner, "note.delete", map[string]any{"id": page})
	if d.scalar("select content_md from core.notes where id = $1::uuid", page) != "folded edit" {
		t.Fatal("folded after the restart")
	}
}

// ---------- duplicate ----------

func TestA2DuplicateCopiesPermissionsLabelsAndAttachmentsButNotChildren(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	guest := d.e.addMember(owner, d.e.signup("Gst", false), "guest")
	mail := d.scalar("select email from core.users where id = $1::uuid", member.userID)
	sp := d.space(owner, "OPN", "open")
	src := d.page(owner, sp, "Src", nil, "body")
	d.child(owner, sp, "Child", src)
	d.ok(owner, "label.set", map[string]any{"note_id": src, "labels": []string{"alpha"}})
	up := d.ok(owner, "file.upload", map[string]any{"note_id": src, "filename": "a.png", "data_base64": base64.StdEncoding.EncodeToString(png)})
	link := "/api/w/" + owner.ws + "/files/" + up["id"].(string)
	d.ok(owner, "note.update", map[string]any{"id": src, "schema": 2, "expected_epoch": 1, "content_md": "see ![a](" + link + ") and " + link + "."})
	got := d.ok(owner, "note.duplicate", map[string]any{"id": src})
	cp := got["id"].(string)
	if got["title"] != "Src (copy)" || got["attachments_copied"].(float64) != 1 || got["parent_id"] != nil || got["position"].(float64) <= 0 {
		t.Fatalf("duplicate: %v", got)
	}
	newFile := d.scalar("select id::text from core.attachments where note_id = $1::uuid", cp)
	if newFile == up["id"] || d.scalar("select (data = (select data from core.attachments where id = $1::uuid))::text from core.attachments where id = $2::uuid", up["id"], newFile) != "true" {
		t.Fatal("attachment is not a new row with the same bytes")
	}
	body := d.scalar("select content_md from core.notes where id = $1::uuid", cp)
	if !strings.Contains(body, "/files/"+newFile+")") || strings.Contains(body, up["id"].(string)+")") || strings.Count(body, "/files/"+newFile) != 2 {
		t.Fatalf("links: %s", body)
	}
	if d.scalar("select count(*)::text from core.notes where parent_id = $1::uuid", cp) != "0" ||
		d.scalar("select count(*)::text from core.note_labels where note_id = $1::uuid", cp) != "1" ||
		d.scalar("select (owner_id = created_by and created_by = $2::uuid and version = 1 and page_status is null)::text from core.notes where id = $1::uuid", cp, owner.userID) != "true" {
		t.Fatal("copy state")
	}
	if d.scalar("select count(*)::text from core.events where note_id = $1::uuid and kind = 'note.created' and payload->>'duplicate_of' = $2", cp, src) != "1" {
		t.Fatal("event")
	}
	// typed pages and guests are refused
	typed := d.ok(owner, "note.create", map[string]any{"space_id": sp, "title": "T", "types": []string{"deal"}})["id"].(string)
	if code, msg := d.errOf(owner, "note.duplicate", map[string]any{"id": typed}); code != "invalid" || msg != "Only plain pages can be duplicated" {
		t.Fatalf("typed: %s %s", code, msg)
	}
	d.ok(owner, "note.share", map[string]any{"note_id": src, "email": guest.email, "level": "edit"})
	d.code("forbidden", guest, "note.duplicate", map[string]any{"id": src})
	// a restricted source: permission rows are copied (the copier keeps edit); a viewer who cannot manage it is refused
	res := d.page(owner, sp, "Res", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": res, "restriction": "edit"})
	d.ok(owner, "note.share", map[string]any{"note_id": res, "email": mail, "level": "view"})
	d.refused(member, "note.duplicate", map[string]any{"id": res})
	rc := d.ok(owner, "note.duplicate", map[string]any{"id": res})["id"].(string)
	if d.scalar("select string_agg(user_id::text || level, ',' order by user_id) from core.note_permissions where note_id = $1::uuid", rc) !=
		d.scalar("select string_agg(user_id::text || level, ',' order by user_id) from core.note_permissions where note_id = $1::uuid", res) || d.scalar("select restriction from core.notes where id = $1::uuid", rc) != "edit" {
		t.Fatal("permissions not copied")
	}
	// the same refusal for a page that inherits its restriction
	inh := d.child(owner, sp, "Inherits", res)
	d.refused(member, "note.duplicate", map[string]any{"id": inh})
}

func TestA2AttachmentCopyIsRolledBackAndItsIntentSwept(t *testing.T) {
	fake := &fakeS3{objs: map[string][]byte{}}
	var mu sync.Mutex
	puts, failAt := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method == http.MethodPut {
			puts++
			if failAt > 0 && puts >= failAt {
				mu.Unlock()
				http.Error(w, "boom", 500)
				return
			}
		}
		mu.Unlock()
		fake.ServeHTTP(w, r)
	}))
	defer srv.Close()
	files.UseS3(&files.S3{Endpoint: srv.URL, Bucket: "bkt", Region: "eu-test-1", KeyID: "test-key-id", Secret: "test-secret"})
	defer files.UseS3(nil)
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	src := d.page(owner, sp, "Src", nil, "x")
	b64 := base64.StdEncoding.EncodeToString(png)
	d.ok(owner, "file.upload", map[string]any{"note_id": src, "filename": "a.png", "data_base64": b64})
	d.ok(owner, "file.upload", map[string]any{"note_id": src, "filename": "b.png", "data_base64": b64})
	// the second object copy fails: the whole duplicate rolls back, the first copy's object stays behind with its intent
	mu.Lock()
	failAt = puts + 2
	mu.Unlock()
	r := d.run(owner, "note.duplicate", map[string]any{"id": src})
	if r.status < 400 {
		t.Fatalf("duplicate must fail: %d %s", r.status, r.body)
	}
	if d.scalar("select count(*)::text from core.notes where title = 'Src (copy)' and space_id = $1::uuid", sp) != "0" || d.scalar("select count(*)::text from core.attachments where note_id = $1::uuid", src) != "2" {
		t.Fatal("rolled back state")
	}
	if d.scalar("select count(*)::text from core.file_objects where state = 'intent'") != "2" || len(fake.objs) != 3 {
		t.Fatalf("intents %s objects %d", d.scalar("select count(*)::text from core.file_objects where state = 'intent'"), len(fake.objs))
	}
	// the sweep collects intents older than a day, and with them the object that was written
	d.sql("update core.file_objects set created_at = now() - interval '25 hours' where state = 'intent'")
	if err := files.RecordSweep(context.Background(), d.e.pool); err != nil {
		t.Fatal(err)
	}
	if d.scalar("select count(*)::text from core.file_objects") != "0" || len(fake.objs) != 2 {
		t.Fatalf("after the sweep: intents %s objects %d", d.scalar("select count(*)::text from core.file_objects"), len(fake.objs))
	}
	// and a duplicate that works marks its objects live
	mu.Lock()
	failAt = 0
	mu.Unlock()
	d.ok(owner, "note.duplicate", map[string]any{"id": src})
	if d.scalar("select count(*)::text from core.file_objects where state = 'live'") != "2" || len(fake.objs) != 4 {
		t.Fatal("live objects")
	}
}
