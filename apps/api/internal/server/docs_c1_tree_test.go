package server_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// C1 R3 (design 5.4, 13.3.4, 13.5): the lazy page tree. The access predicate runs before LIMIT, the cursor and has_children, so a
// hidden page never shows as a row, a gap or an arrow; the Shared with you level comes from the reader's own permission rows.

type tlevel struct {
	parent  any
	rows    []map[string]any
	next    any
	prev    any
	shared  any
	rawBody string
}

func (d dg) tree(w who, in map[string]any) tlevel {
	d.t.Helper()
	r := d.run(w, "note.tree", in)
	if r.status != 200 {
		d.t.Fatalf("note.tree %v: %d %s", in, r.status, r.body)
	}
	return levelOf(r.json(), string(r.body))
}

func levelOf(m map[string]any, raw string) tlevel {
	l := tlevel{parent: m["parent_id"], next: m["next"], prev: m["prev"], shared: m["shared"], rawBody: raw}
	for _, x := range m["rows"].([]any) {
		l.rows = append(l.rows, x.(map[string]any))
	}
	return l
}

func (l tlevel) ids() []string {
	var out []string
	for _, r := range l.rows {
		out = append(out, r["id"].(string))
	}
	return out
}

func (l tlevel) titles() []string {
	var out []string
	for _, r := range l.rows {
		out = append(out, r["title"].(string))
	}
	return out
}

func (l tlevel) row(id string) map[string]any {
	for _, r := range l.rows {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

func (d dg) reveal(w who, id string) []tlevel {
	d.t.Helper()
	r := d.run(w, "note.tree", map[string]any{"reveal": id})
	if r.status != 200 {
		d.t.Fatalf("reveal: %d %s", r.status, r.body)
	}
	var out []tlevel
	for _, x := range r.json()["levels"].([]any) {
		m := x.(map[string]any)
		out = append(out, levelOf(m, string(r.body)))
	}
	return out
}

func noLeak(t *testing.T, body string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(body, s) {
			t.Fatalf("hidden title %q leaked: %s", s, body)
		}
	}
}

func TestC1TreeRestrictedAncestorsAndArrows(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	sp := d.space(owner, "RST", "open")
	pub := d.page(owner, sp, "Public root", nil, "x")
	sec := d.page(owner, sp, "Secret root", nil, "x")
	d.page(owner, sp, "Secret kid", sec, "x")
	par := d.page(owner, sp, "Parent of hidden", nil, "x")
	hid := d.page(owner, sp, "Hidden kid", par, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": sec, "restriction": "view"})
	d.ok(owner, "note.restrict", map[string]any{"note_id": hid, "restriction": "view"})

	root := d.tree(m, map[string]any{"space_id": sp})
	if !slices.Equal(root.titles(), []string{"Public root", "Parent of hidden"}) {
		t.Fatalf("member root: %v", root.titles())
	}
	noLeak(t, root.rawBody, "Secret", "Hidden kid")
	if root.row(par)["has_children"] != false || root.row(pub)["has_children"] != false {
		t.Fatalf("an arrow over hidden children: %v", root.rows)
	}
	if root.shared != false {
		t.Fatalf("shared flag: %v", root.shared)
	}
	kids := d.tree(m, map[string]any{"space_id": sp, "parent_id": par})
	if len(kids.rows) != 0 {
		t.Fatalf("hidden child listed: %v", kids.titles())
	}
	// the reveal of a page the reader cannot read is not found, with no title in the error
	d.code("not_found", m, "note.tree", map[string]any{"reveal": hid})

	ow := d.tree(owner, map[string]any{"space_id": sp})
	if len(ow.rows) != 3 || ow.row(par)["has_children"] != true || ow.row(sec)["restriction"] != "view" {
		t.Fatalf("owner root: %v", ow.rows)
	}
}

func TestC1TreeSharedUnderSyntheticRootNeverLeaks(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	g := d.e.addMember(owner, d.e.signup("G", false), "guest")
	sp := d.space(owner, "PRV", "private")
	a := d.page(owner, sp, "Alpha secret", nil, "x")
	b := d.page(owner, sp, "Beta secret", a, "x")
	c := d.page(owner, sp, "Gamma shared", b, "x")
	dd := d.page(owner, sp, "Delta below", c, "x")
	d.page(owner, sp, "Echo secret", nil, "x")
	for _, w := range []who{m, g} {
		d.ok(owner, "note.share", map[string]any{"note_id": c, "user_id": w.userID, "level": "view"})
		root := d.tree(w, map[string]any{"space_id": sp})
		if len(root.rows) != 0 || root.shared != true {
			t.Fatalf("space root for a reader outside the space: %v shared=%v", root.titles(), root.shared)
		}
		noLeak(t, root.rawBody, "secret")
		sh := d.tree(w, map[string]any{"space_id": sp, "parent_id": "shared"})
		if !slices.Equal(sh.ids(), []string{c}) || sh.parent != "shared" || sh.row(c)["has_children"] != true {
			t.Fatalf("shared level: %v", sh.rows)
		}
		noLeak(t, sh.rawBody, "secret", "Alpha", "Beta", "Echo")
		kids := d.tree(w, map[string]any{"space_id": sp, "parent_id": c})
		if !slices.Equal(kids.ids(), []string{dd}) {
			t.Fatalf("shared children: %v", kids.rows)
		}
		lv := d.reveal(w, dd)
		if len(lv) != 2 || lv[0].parent != "shared" || !slices.Equal(lv[0].ids(), []string{c}) || !slices.Equal(lv[1].ids(), []string{dd}) {
			t.Fatalf("reveal: %+v", lv)
		}
		noLeak(t, lv[0].rawBody, "secret", "Alpha", "Beta", "Echo")
		// a parent that is archived or trashed does not move the page out of the shared level of a space the reader cannot open
		d.sql("update core.notes set freshness = 'archived' where id = $1::uuid", b)
		if sh := d.tree(w, map[string]any{"space_id": sp, "parent_id": "shared"}); !slices.Equal(sh.ids(), []string{c}) {
			t.Fatalf("shared level under an archived parent: %v", sh.rows)
		}
		d.sql("update core.notes set freshness = 'fresh', deleted_at = now() where id = $1::uuid", b)
		if sh := d.tree(w, map[string]any{"space_id": sp, "parent_id": "shared"}); !slices.Equal(sh.ids(), []string{c}) {
			t.Fatalf("shared level under a trashed parent: %v", sh.rows)
		}
		d.sql("update core.notes set deleted_at = null where id = $1::uuid", b)
		d.sql("delete from core.note_permissions where note_id = $1::uuid and user_id = $2::uuid", c, w.userID)
		if sh := d.tree(w, map[string]any{"space_id": sp, "parent_id": "shared"}); len(sh.rows) != 0 {
			t.Fatalf("shared level after the share was removed: %v", sh.rows)
		}
	}
	// reading a page that the reader holds no share for stays not found
	d.code("not_found", m, "note.tree", map[string]any{"reveal": a})
}

// A page is readable while its parent is not only through a direct share on that page (13.5 item 3). Checked by brute force over a
// space with restricted, archived and trashed pages, for readers inside and outside the space.
func TestC1TreeInvariantReadableChildOfHiddenParentIsShared(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	op := d.space(owner, "OPN", "open")
	pv := d.space(owner, "PRV", "private")
	for _, sp := range []string{op, pv} {
		r1 := d.page(owner, sp, "r1", nil, "x")
		r2 := d.page(owner, sp, "r2", nil, "x")
		c1 := d.page(owner, sp, "c1", r1, "x")
		c2 := d.page(owner, sp, "c2", r1, "x")
		g1 := d.page(owner, sp, "g1", c1, "x")
		g2 := d.page(owner, sp, "g2", c2, "x")
		d.page(owner, sp, "h1", g1, "x")
		d.ok(owner, "note.restrict", map[string]any{"note_id": c1, "restriction": "view"})
		d.ok(owner, "note.restrict", map[string]any{"note_id": r2, "restriction": "edit"})
		d.sql("update core.notes set freshness = 'archived' where id = $1::uuid", c2)
		d.ok(owner, "note.share", map[string]any{"note_id": g1, "user_id": m.userID, "level": "view"}) // under a view-restricted parent
		d.ok(owner, "note.share", map[string]any{"note_id": g2, "user_id": m.userID, "level": "edit"}) // under an archived parent
	}
	got := d.scalar(`with r as (select array(select s.id from core.spaces s left join core.space_members sm on sm.space_id = s.id and sm.user_id = $1::uuid
	                      where sm.user_id is not null or s.visibility = 'open') as ids)
	  select count(*)::text from core.notes n, core.notes p, r
	   where n.parent_id = p.id and n.deleted_at is null and p.deleted_at is null and n.freshness <> 'archived' and p.freshness <> 'archived'
	     and core.note_access(n.id, $1::uuid, false, r.ids) <> 'none' and core.note_access(p.id, $1::uuid, false, r.ids) = 'none'
	     and not exists (select 1 from core.note_permissions pm where pm.note_id = n.id and pm.user_id = $1::uuid)`, m.userID)
	if got != "0" {
		t.Fatalf("%s pages readable under a hidden parent without a direct share", got)
	}
	// and every such page is listed under the shared root, never in the space's own levels
	for _, sp := range []string{op, pv} {
		sh := d.tree(m, map[string]any{"space_id": sp, "parent_id": "shared"})
		root := d.tree(m, map[string]any{"space_id": sp})
		for _, x := range sh.rows {
			if root.row(x["id"].(string)) != nil {
				t.Fatalf("%v in both the root and the shared level", x["title"])
			}
		}
		// g1 sits under a view-restricted parent: the share on g1 alone does not open it, so it is in no level at all
		if sp == pv && !slices.Equal(sh.titles(), []string{"g2"}) {
			t.Fatalf("private space shared level: %v", sh.titles())
		}
		if sp == op && len(sh.rows) != 0 {
			// g2 sits under an archived parent in a readable space: it is a root level row, not a shared one
			t.Fatalf("open space shared level: %v", sh.titles())
		}
	}
}

func TestC1TreeLiveArchivedTrashedAndGuests(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	g := d.e.addMember(owner, d.e.signup("G", false), "guest")
	sp := d.space(owner, "LIV", "open")
	keep := d.page(owner, sp, "Keep", nil, "x")
	arch := d.page(owner, sp, "Archived", nil, "x")
	archKid := d.page(owner, sp, "Under archived", arch, "x")
	gone := d.page(owner, sp, "Trashed", nil, "x")
	goneKid := d.page(owner, sp, "Under trashed", gone, "x")
	d.sql("update core.notes set freshness = 'archived' where id = $1::uuid", arch)
	d.sql("update core.notes set deleted_at = now() where id = $1::uuid", gone) // a purge or partial restore can leave a live child
	root := d.tree(m, map[string]any{"space_id": sp})
	// the two live children of hidden parents are root rows, in sibling order
	if !slices.Equal(root.ids(), []string{keep, archKid, goneKid}) {
		t.Fatalf("root: %v", root.titles())
	}
	noLeak(t, root.rawBody, `"Archived"`, `"Trashed"`)
	if kids := d.tree(m, map[string]any{"space_id": sp, "parent_id": arch}); len(kids.rows) != 1 {
		t.Fatalf("children of an archived page are still served: %v", kids.rows)
	}
	// a guest outside the space reads nothing from it
	if gr := d.tree(g, map[string]any{"space_id": sp}); len(gr.rows) != 0 || gr.shared != false {
		t.Fatalf("guest root: %v", gr.rows)
	}
	d.code("not_found", g, "note.tree", map[string]any{"reveal": keep})
	d.code("not_found", m, "note.tree", map[string]any{"reveal": gone})
	// reveal of an archived page returns only its ancestors' levels, never the archived row
	for _, lv := range d.reveal(m, arch) {
		if lv.row(arch) != nil {
			t.Fatal("an archived page is a row of a level")
		}
	}
	// a page under a trashed parent starts a root level
	if lv := d.reveal(m, goneKid); len(lv) != 1 || lv[0].parent != nil || lv[0].row(goneKid) == nil {
		t.Fatalf("reveal under a trashed parent: %+v", lv)
	}
}

// siblings inserts n pages under parent (nil = space root) with positions 1..n.
func (d dg) siblings(space string, parent any, prefix string, n int) {
	d.sql(`insert into core.notes (workspace_id, space_id, parent_id, title, position)
	       select $1::uuid, $2::uuid, $3::uuid, $4 || i, i from generate_series(1, $5::int) i`, d.ws, space, parent, prefix, n)
}

func (d dg) pageAll(w who, in map[string]any, key string) (all []string, pages int) {
	d.t.Helper()
	for {
		l := d.tree(w, in)
		all = append(all, l.ids()...)
		pages++
		if l.next == nil {
			return
		}
		in[key] = l.next
		delete(in, map[string]string{"after": "before", "before": "after"}[key])
	}
}

func TestC1TreePagesForwardAndBackWithAReorder(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	sp := d.space(owner, "BIG", "open")
	root := d.page(owner, sp, "Big", nil, "x")
	const n = 1200
	d.siblings(sp, root, "S", n)
	analyze(t)
	hidden := d.scalar(`update core.notes set restriction = 'view' where id = (select id from core.notes where parent_id = $1::uuid and position = 250) returning id::text`, root)

	start := time.Now()
	first := d.tree(m, map[string]any{"space_id": sp, "parent_id": root, "limit": 200})
	t.Logf("level of %d siblings, first page of 200: %v", n, time.Since(start))
	if len(first.rows) != 200 || first.next == nil || first.prev != nil {
		t.Fatalf("first page: %d next=%v prev=%v", len(first.rows), first.next, first.prev)
	}
	// forward, with one already-seen row moved to the end and one unseen row moved to the front between the calls
	seen := first.ids()
	seenSet := map[string]bool{}
	for _, id := range seen {
		seenSet[id] = true
	}
	movedSeen := seen[3]
	unseen := d.scalar(`select id::text from core.notes where parent_id = $1::uuid and position = 700`, root)
	d.sql("update core.notes set position = 5000 where id = $1::uuid", movedSeen)
	d.sql("update core.notes set position = 0 where id = $1::uuid", unseen)
	rest, pages := d.pageAll(m, map[string]any{"space_id": sp, "parent_id": root, "limit": 200, "after": first.next}, "after")
	all := append(seen, rest...)
	count := map[string]int{}
	for _, id := range all {
		count[id]++
	}
	if count[hidden] != 0 {
		t.Fatal("the restricted page was listed")
	}
	// readable rows seen: all but the restricted one and the unseen one that moved in front of the cursor; the moved seen one twice
	if len(count) != n-2 || count[movedSeen] != 2 {
		t.Fatalf("pages seen: %d, moved page %d times", len(count), count[movedSeen])
	}
	every := strings.Split(d.scalar(`select string_agg(id::text, ',') from core.notes where parent_id = $1::uuid`, root), ",")
	for _, id := range every {
		if id != hidden && id != unseen && count[id] == 0 {
			t.Fatalf("%s was skipped over %d pages", id, pages)
		}
	}
	if pages < 5 {
		t.Fatalf("pages: %d", pages)
	}

	// restore the order, then page backward from the end with prev and meet the beginning exactly
	d.sql("update core.notes set position = (regexp_replace(title, '^S', ''))::int where parent_id = $1::uuid", root)
	last := d.tree(m, map[string]any{"space_id": sp, "parent_id": root, "limit": 200, "before": d.tree(m, map[string]any{"space_id": sp, "parent_id": root, "limit": 200, "after": first.next}).next})
	if last.prev == nil || last.next == nil {
		t.Fatalf("a middle page needs both cursors: %v %v", last.prev, last.next)
	}
	var back [][]string
	cur := last
	for {
		back = append([][]string{cur.ids()}, back...)
		if cur.prev == nil {
			break
		}
		cur = d.tree(m, map[string]any{"space_id": sp, "parent_id": root, "limit": 200, "before": cur.prev})
	}
	var flat []string
	for _, p := range back {
		flat = append(flat, p...)
	}
	fwd, _ := d.pageAll(m, map[string]any{"space_id": sp, "parent_id": root, "limit": 200}, "after")
	if !slices.Equal(flat, fwd[:len(flat)]) {
		t.Fatal("paging back does not retrace the forward order")
	}

	// reveal of a page in the middle of a long level: a 200 row window around it, with a cursor each way
	mid := d.scalar(`select id::text from core.notes where parent_id = $1::uuid and position = 600`, root)
	lv := d.reveal(m, mid)
	if len(lv) != 2 || lv[0].parent != nil {
		t.Fatalf("reveal levels: %d", len(lv))
	}
	w := lv[1]
	if w.row(mid) == nil || len(w.rows) != 200 || w.next == nil || w.prev == nil {
		t.Fatalf("window: %d rows next=%v prev=%v", len(w.rows), w.next, w.prev)
	}
	// both directions continue from the window without a gap
	up := d.tree(m, map[string]any{"space_id": sp, "parent_id": root, "limit": 200, "before": w.prev})
	down := d.tree(m, map[string]any{"space_id": sp, "parent_id": root, "limit": 200, "after": w.next})
	all2 := append(append(up.ids(), w.ids()...), down.ids()...)
	at := slices.Index(fwd, up.ids()[0])
	if at < 0 || !slices.Equal(all2, fwd[at:at+len(all2)]) {
		t.Fatal("window and its neighbours are not contiguous")
	}
}

func TestC1TreeRevealDeepChain(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	sp := d.space(owner, "DEP", "open")
	var parent any
	var chain []string
	for i := 0; i < 30; i++ {
		id := d.page(owner, sp, fmt.Sprintf("L%d", i), parent, "x")
		d.page(owner, sp, fmt.Sprintf("sib%d", i), parent, "x")
		chain = append(chain, id)
		parent = id
	}
	lv := d.reveal(m, chain[29])
	if len(lv) != 30 {
		t.Fatalf("levels: %d", len(lv))
	}
	for i, l := range lv {
		if l.row(chain[i]) == nil || len(l.rows) != 2 {
			t.Fatalf("level %d: %v", i, l.titles())
		}
		if (i == 0) != (l.parent == nil) || (i > 0 && l.parent != chain[i-1]) {
			t.Fatalf("level %d parent %v", i, l.parent)
		}
	}
	// the same chain with a restricted middle page: the reader cannot reveal anything below it
	d.ok(owner, "note.restrict", map[string]any{"note_id": chain[10], "restriction": "view"})
	d.code("not_found", m, "note.tree", map[string]any{"reveal": chain[29]})
}

func analyze(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, os.Getenv("DATABASE_ADMIN_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, "analyze core.notes"); err != nil {
		t.Fatal(err)
	}
}

// The timing check of 13.5 and the brief: a level query over 15,000 pages stays under 50 ms locally.
func TestC1TreeLevelTimingOn15kPages(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	sp := d.space(owner, "T15", "open")
	// 15,000 pages in a fan-out of eight under 300 roots, one page with 1,000 children, every 50th restricted
	d.sql(`with g as (select i, gen_random_uuid() as id from generate_series(1, 15000) i)
	       insert into core.notes (id, workspace_id, space_id, parent_id, title, position)
	       select g.id, $1::uuid, $2::uuid, p.id, 'P' || g.i, g.i from g left join g p on g.i > 300 and p.i = (g.i - 301) / 8 + 1`, d.ws, sp)
	wide := d.scalar(`select id::text from core.notes where title = 'P1'`)
	d.siblings(sp, wide, "W", 1000)
	d.sql(`update core.notes set restriction = 'view' where title ~ '^P[0-9]*[05]0$'`)
	d.sql(`update core.notes set deleted_at = now() where title ~ '^P[0-9]+$' and substr(title, 2)::int > 300 and substr(title, 2)::int % 50 = 7`)
	analyze(t) // the app role cannot ANALYZE; a bulk load needs statistics before the planner picks notes_tree_level
	measure := func(name string, w who, in map[string]any) {
		t.Helper()
		var ds []time.Duration
		for i := 0; i < 9; i++ {
			s := time.Now()
			l := d.tree(w, in)
			ds = append(ds, time.Since(s))
			if len(l.rows) == 0 {
				t.Fatalf("%s: empty level", name)
			}
		}
		slices.Sort(ds)
		t.Logf("TIMING %s: best %v, median %v, max %v", name, ds[0], ds[4], ds[8])
		if ds[0] > 50*time.Millisecond { // the best of nine: a loaded machine slows the median, not what the query needs
			t.Errorf("%s: best of nine %v is over 50ms", name, ds[0])
		}
	}
	for _, who := range []struct {
		n string
		w who
	}{{"member", m}, {"owner", owner}} {
		measure(who.n+" root level", who.w, map[string]any{"space_id": sp})
		measure(who.n+" level of 1,000 children", who.w, map[string]any{"space_id": sp, "parent_id": wide})
		next := d.tree(who.w, map[string]any{"space_id": sp, "parent_id": wide}).next
		measure(who.n+" level of 1,000 children, page 2", who.w, map[string]any{"space_id": sp, "parent_id": wide, "after": next})
	}
	deep := d.scalar(`select id::text from core.notes where title = 'P14003'`)
	s := time.Now()
	lv := d.reveal(m, deep)
	t.Logf("TIMING reveal of a page %d levels deep: %v", len(lv), time.Since(s))
}
