package server_test

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

// Docs parity B2, Q1 (docs-parity-b 5.1, 5.5, 5.6, 13.2.7): request access, manual archive, the viewers byline.

func (d dg) recips(page string) []string {
	d.t.Helper()
	s := d.scalar(`select coalesce(string_agg(user_id::text, ',' order by user_id), '') from core.notifications where kind = 'access_request' and note_id = $1::uuid`, page)
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

func sorted(ids ...string) []string { slices.Sort(ids); return ids }

func (d dg) request(w who, page string, msg ...string) resp {
	in := map[string]any{"note_id": page}
	if len(msg) > 0 {
		in["message"] = msg[0]
	}
	return d.run(w, "note.request_access", in)
}

func (d dg) bucket(key string, n int, day bool) {
	unit := "hour"
	if day {
		unit = "day"
	}
	d.sql(`insert into core.rate_buckets (key, bucket, n) values ($1, date_trunc('`+unit+`', now() at time zone 'utc') at time zone 'utc', $2)
		on conflict (key, bucket) do update set n = $2`, key, n)
}

func TestRequestAccessRecipients(t *testing.T) {
	d, owner := newDG(t)
	a, b, c, v := d.member(owner), d.member(owner), d.member(owner), d.member(owner)

	// Private space: its admins decide, never an editor of the space.
	prv := d.space(owner, "PRV", "private")
	d.ok(owner, "space.member_set", map[string]any{"space_id": prv, "user_id": a.userID, "role": "editor"})
	d.ok(owner, "space.member_set", map[string]any{"space_id": prv, "user_id": b.userID, "role": "admin"})
	p := d.page(a, prv, "Plan", nil, "x")
	if r := d.request(c, p, "please"); r.status != 200 || r.json()["ok"] != true {
		t.Fatalf("request: %d %s", r.status, r.body)
	}
	if got := d.recips(p); !slices.Equal(got, sorted(owner.userID, b.userID)) {
		t.Fatalf("private space recipients: %v (owner %s, admin %s)", got, owner.userID, b.userID)
	}
	if d.scalar(`select count(*)::text from core.notifications where user_id = $1::uuid and kind = 'access_request'`, c.userID) != "0" {
		t.Fatal("the requester was notified")
	}
	if d.scalar(`select title from core.notifications where user_id = $1::uuid and kind = 'access_request'`, b.userID) != "Plan - please" {
		t.Fatal("the title is the page and the message")
	}

	// Inherited restriction in an open space: the people who can edit the restricting page, owner first, never a viewer.
	opn := d.space(owner, "OPN", "open")
	q := d.page(owner, opn, "Board", nil, "x")
	kid := d.child(owner, opn, "Minutes", q)
	d.ok(owner, "note.share", map[string]any{"note_id": q, "user_id": a.userID, "level": "edit"})
	d.ok(owner, "note.share", map[string]any{"note_id": q, "user_id": v.userID, "level": "view"})
	d.ok(owner, "note.restrict", map[string]any{"note_id": q, "restriction": "view"})
	d.ok(v, "note.get", map[string]any{"id": kid}) // v reads it through the view share
	d.request(c, kid)
	if got := d.recips(kid); !slices.Equal(got, sorted(owner.userID, a.userID)) {
		t.Fatalf("inherited restriction recipients: %v", got)
	}

	// Private space and an inherited restriction together: the share goes on the restricting page, so the space admin, who cannot
	// read that page, is never asked.
	r := d.child(a, prv, "Secret", p)
	d.ok(a, "note.restrict", map[string]any{"note_id": p, "restriction": "view"})
	d.request(c, r)
	if got := d.recips(r); !slices.Equal(got, sorted(owner.userID, a.userID)) || slices.Contains(got, b.userID) {
		t.Fatalf("combined recipients: %v", got)
	}

	// A guest is routed to a page share only: people who can edit the page, never a space viewer or another guest.
	g1 := d.e.addMember(owner, d.e.signup("G", false), "guest")
	g2 := d.e.addMember(owner, d.e.signup("G2", false), "guest")
	d.ok(owner, "space.member_set", map[string]any{"space_id": prv, "user_id": v.userID, "role": "viewer"})
	pg := d.page(a, prv, "Guest page", nil, "x")
	d.request(g1, pg)
	got := d.recips(pg)
	if !slices.Contains(got, a.userID) || slices.Contains(got, v.userID) || slices.Contains(got, g1.userID) || slices.Contains(got, g2.userID) {
		t.Fatalf("guest recipients: %v", got)
	}
	// The page owner comes first even with five other editors: at most five people.
	if len(got) > 5 {
		t.Fatalf("more than five recipients: %v", got)
	}

	// Nobody can grant (the space is archived): no notification, the same answer.
	ar := d.space(owner, "ARC", "private")
	ap := d.page(owner, ar, "Old", nil, "x")
	d.archive(ar, true)
	if r := d.request(c, ap); r.status != 200 || r.json()["ok"] != true || len(d.recips(ap)) != 0 {
		t.Fatalf("archived space: %d %s %v", r.status, r.body, d.recips(ap))
	}

	// The title is visible only to the recipient.
	if n := len(d.list(c, "notification.list", map[string]any{})); n != 0 {
		t.Fatalf("the requester sees %d notifications", n)
	}
	seen := false
	for _, n := range d.list(b, "notification.list", map[string]any{}) {
		seen = seen || n["kind"] == "access_request"
	}
	if !seen {
		t.Fatal("the space admin has no access_request notification")
	}
}

func TestRequestAccessAnswersAreIdentical(t *testing.T) {
	d, owner := newDG(t)
	c := d.member(owner)
	prv := d.space(owner, "PRV", "private")
	hidden := d.page(owner, prv, "Hidden", nil, "x")
	trashed := d.page(owner, prv, "Trashed", nil, "x")
	d.ok(owner, "note.delete", map[string]any{"id": trashed})
	opn := d.space(owner, "OPN", "open")
	readable := d.page(owner, opn, "Readable", nil, "x")
	missing := "00000000-0000-4000-8000-000000000000"
	var first string
	for _, id := range []string{missing, trashed, readable, hidden, hidden} {
		r := d.request(c, id, "hi")
		if r.status != 200 {
			t.Fatalf("%s: %d %s", id, r.status, r.body)
		}
		if first == "" {
			first = string(r.body)
		} else if string(r.body) != first {
			t.Fatalf("answer differs: %s vs %s", r.body, first)
		}
	}
	if first != `{"ok":true}` {
		t.Fatalf("answer: %s", first)
	}
	if len(d.recips(readable)) != 0 || len(d.recips(trashed)) != 0 {
		t.Fatal("a page the caller can read or that is gone notified someone")
	}
	d.code("invalid", c, "note.request_access", map[string]any{"note_id": hidden, "message": strings.Repeat("x", 201)})
}

func TestRequestAccessLimits(t *testing.T) {
	d, owner := newDG(t)
	b, c := d.member(owner), d.member(owner)
	prv := d.space(owner, "PRV", "private")
	d.ok(owner, "space.member_set", map[string]any{"space_id": prv, "user_id": b.userID, "role": "admin"})
	count := func(u who) string {
		return d.scalar(`select count(*)::text from core.notifications where user_id = $1::uuid and kind = 'access_request'`, u.userID)
	}

	// one per requester, page and recipient per day
	p := d.page(owner, prv, "P", nil, "x")
	d.request(c, p)
	d.request(c, p)
	if count(b) != "1" || d.scalar(`select n::text from core.rate_buckets where key = $1 order by bucket desc limit 1`, "ar:rcp:"+b.userID) != "1" {
		t.Fatalf("the second request the same day created something: %s", count(b))
	}

	// 10 per requester per hour (the first request above counts as one hit and the repeat as another)
	var pages []string
	for i := 0; i < 12; i++ {
		pages = append(pages, d.page(owner, prv, "Q", nil, "x"))
	}
	for _, id := range pages {
		d.request(c, id)
	}
	if count(b) != "9" { // 10 hits in the hour: p, p again, then 8 new pages (9 notifications with p)
		t.Fatalf("hourly cap: recipient has %s", count(b))
	}
	d.bucket("ar:req:"+c.userID, 0, false) // the counter is durable and keyed by the requester

	// 20 per recipient per day: the recipient at the cap gets nothing, the other admin still does
	d.bucket("ar:rcp:"+b.userID, 20, true)
	p2 := d.page(owner, prv, "P2", nil, "x")
	d.request(c, p2)
	if got := d.recips(p2); !slices.Equal(got, []string{owner.userID}) {
		t.Fatalf("recipient cap: %v", got)
	}

	// 200 per workspace per day: nobody
	d.bucket("ar:ws:"+d.ws, 200, true)
	p3 := d.page(owner, prv, "P3", nil, "x")
	d.request(c, p3)
	if len(d.recips(p3)) != 0 {
		t.Fatalf("workspace cap: %v", d.recips(p3))
	}
	// over every cap the answer is still ok
	if r := d.request(c, p3); string(r.body) != `{"ok":true}` {
		t.Fatalf("over the limit: %s", r.body)
	}
}

func (d dg) archivedList(w who) []string {
	return ids(d.list(w, "doc.archived", map[string]any{}))
}

func TestArchiveSubtreeReadOnlyAndRestore(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	root := d.page(m, sp, "Root", nil, "x")
	k1 := d.child(m, sp, "K1", root)
	k2 := d.child(m, sp, "K2", root)
	g := d.child(m, sp, "G", k1)
	other := d.page(m, sp, "Other", nil, "x")

	if got := d.ok(m, "doc.archive", map[string]any{"id": root})["archived"]; got != float64(4) {
		t.Fatalf("archived: %v", got)
	}
	if got := d.archivedList(owner); len(got) != 4 || slices.Contains(got, other) {
		t.Fatalf("archived list: %v", got)
	}
	if n := d.scalar(`select count(*)::text from core.events where kind = 'doc.archived' and payload->>'reason' = 'manual' and payload->>'root_id' = $1`, root); n != "4" {
		t.Fatalf("events: %s", n)
	}
	for _, id := range []string{root, k1, k2, g} { // read-only for everyone through the central gate, readable by link
		d.ok(m, "note.get", map[string]any{"id": id})
		if code, msg := d.errOf(m, "note.update", map[string]any{"id": id, "title": "x"}); code != "forbidden" || msg != "This page is archived" {
			t.Fatalf("update: %s %s", code, msg)
		}
		if code, _ := d.errOf(owner, "note.update", map[string]any{"id": id, "title": "x"}); code != "forbidden" {
			t.Fatal("an admin edited an archived page")
		}
	}
	d.ok(m, "note.update", map[string]any{"id": other, "title": "still editable"})
	if code, _ := d.errOf(m, "doc.archive", map[string]any{"id": root}); code != "conflict" {
		t.Fatalf("archiving twice: %s", code)
	}

	// restore the page alone, then with the rest
	if got := d.ok(m, "doc.restore", map[string]any{"note_id": root}); got["restored"] != float64(1) {
		t.Fatalf("restore alone: %v", got)
	}
	d.ok(m, "note.update", map[string]any{"id": root, "title": "Root again"})
	if len(d.archivedList(owner)) != 3 {
		t.Fatalf("children stay archived: %v", d.archivedList(owner))
	}
	if got := d.ok(owner, "doc.restore", map[string]any{"note_id": k1, "with_children": true}); got["restored"] != float64(2) {
		t.Fatalf("restore with children: %v", got)
	}
	if got := d.archivedList(owner); !slices.Equal(got, []string{k2}) {
		t.Fatalf("after restore: %v", got)
	}
	d.ok(m, "note.update", map[string]any{"id": g, "title": "G again"})
}

func TestArchiveRefusals(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	root := d.page(m, sp, "Root", nil, "x")
	hid := d.child(owner, sp, "Hidden", root)
	d.ok(owner, "note.restrict", map[string]any{"note_id": hid, "restriction": "edit"})

	// a hidden (not editable) descendant refuses the whole call with the generic text and archives nothing
	d.refused(m, "doc.archive", map[string]any{"id": root})
	if len(d.archivedList(owner)) != 0 {
		t.Fatal("a refused call archived pages")
	}

	// a typed page is refused
	typed := d.ok(owner, "note.create", map[string]any{"space_id": sp, "title": "Typed", "types": []string{"deal"}})["id"].(string)
	d.code("invalid", owner, "doc.archive", map[string]any{"id": typed})
	plain := d.page(owner, sp, "Plain", nil, "x")
	d.sql(`update core.notes set parent_id = $1::uuid where id = $2::uuid`, plain, typed)
	d.code("invalid", owner, "doc.archive", map[string]any{"id": plain})

	// a viewer cannot archive, and an archived space refuses with its own reason
	d.code("forbidden", m, "doc.archive", map[string]any{"id": hid})
	d.archive(sp, true)
	d.archivedRefusal(owner, "doc.archive", map[string]any{"id": root})

	// restore with children skips what the caller cannot edit, silently
	d.archive(sp, false)
	d.sql(`update core.notes set restriction = 'none' where id = $1::uuid`, hid)
	d.ok(m, "doc.archive", map[string]any{"id": root})
	d.sql(`update core.notes set restriction = 'edit' where id = $1::uuid`, hid)
	if got := d.ok(m, "doc.restore", map[string]any{"note_id": root, "with_children": true}); got["restored"] != float64(1) {
		t.Fatalf("restore skipped: %v", got)
	}
	if got := d.archivedList(owner); !slices.Equal(got, []string{hid}) {
		t.Fatalf("still archived: %v", got)
	}
}

func TestViewersByline(t *testing.T) {
	d, owner := newDG(t)
	m, reader, former := d.member(owner), d.member(owner), d.member(owner)
	guest := d.e.addMember(owner, d.e.signup("G", false), "guest")
	sp := d.space(owner, "OPN", "open")
	p := d.page(m, sp, "Page", nil, "x")
	if got := d.ok(m, "note.viewers", map[string]any{"note_id": p}); got["count"] != float64(0) || len(got["viewers"].([]any)) != 0 {
		t.Fatalf("no views: %v", got)
	}
	d.ok(owner, "note.share", map[string]any{"note_id": p, "user_id": guest.userID, "level": "edit"})
	for _, w := range []who{m, reader, former, guest} {
		d.ok(w, "note.viewed", map[string]any{"note_id": p})
	}
	d.sql(`delete from core.memberships where user_id = $1::uuid`, former.userID) // leaves the workspace
	got := d.ok(m, "note.viewers", map[string]any{"note_id": p})
	vs := got["viewers"].([]any)
	if got["count"] != float64(3) || len(vs) != 3 {
		t.Fatalf("editor: %v", got)
	}
	for _, x := range vs {
		if x.(map[string]any)["user_id"] == former.userID {
			t.Fatal("a former member is listed")
		}
	}
	// a viewer-only reader (the page is edit-restricted) gets the count and no names; so does a guest, even with edit access
	d.ok(owner, "note.restrict", map[string]any{"note_id": p, "restriction": "edit"})
	for _, w := range []who{reader, guest} {
		got := d.ok(w, "note.viewers", map[string]any{"note_id": p})
		if _, has := got["viewers"]; has || got["count"] != float64(3) {
			t.Fatalf("names leaked: %v", got)
		}
	}
	d.code("not_found", d.member(owner), "note.viewers", map[string]any{"note_id": d.page(owner, d.space(owner, "PRV", "private"), "Hidden", nil, "x")})
}

// A page moved into a subtree while it is being archived is either swept by the archive or refused: it never ends up live
// under an archived parent. Both commands take the tree lock (lock order: space lock, then rows).
func TestArchiveRacesAMoveIntoTheSubtree(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	for round := 0; round < 8; round++ {
		root := d.page(owner, sp, "Root", nil, "x")
		d.child(owner, sp, "kid", root)
		late := d.page(owner, sp, "Late", nil, "x")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); d.run(owner, "doc.archive", map[string]any{"id": root}) }()
		go func() { defer wg.Done(); d.run(owner, "note.move", map[string]any{"id": late, "parent_id": root}) }()
		wg.Wait()
		if n := d.scalar(`select count(*)::text from core.notes c join core.notes p on p.id = c.parent_id
			where p.id = $1::uuid and p.freshness = 'archived' and c.deleted_at is null and c.freshness <> 'archived'`, root); n != "0" {
			t.Fatalf("round %d: %s live pages under an archived parent", round, n)
		}
	}
}

// A page cannot be moved or created under an archived parent: that would leave a live page inside a read-only subtree.
func TestNothingEntersAnArchivedSubtree(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	root := d.page(owner, sp, "Root", nil, "x")
	late := d.page(owner, sp, "Late", nil, "x")
	d.ok(owner, "doc.archive", map[string]any{"id": root})
	d.code("forbidden", owner, "note.move", map[string]any{"id": late, "parent_id": root})
	d.code("forbidden", owner, "note.update", map[string]any{"id": late, "parent_id": root})
	d.code("forbidden", owner, "note.create", map[string]any{"space_id": sp, "title": "New", "parent_id": root})
}

// An archived page behind a public link answers the single 404 (the resolver), and works again after restore.
func TestArchivedPageBehindAPublicLinkIs404(t *testing.T) {
	x := newPG(t)
	sp := x.space(x.owner, "PUB", "open")
	page := x.page(x.owner, sp, "P", nil, "x")
	token := x.link(x.owner, page)
	if s, _, b := x.get(token, ""); s != 200 {
		t.Fatalf("before: %d %s", s, b)
	}
	x.ok(x.owner, "doc.archive", map[string]any{"id": page})
	x.expectGone(token)
	x.ok(x.owner, "doc.restore", map[string]any{"note_id": page})
	if s, _, b := x.get(token, ""); s != 200 {
		t.Fatalf("after restore: %d %s", s, b)
	}
}

func TestArchivedPageRejectsCommentWrites(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "CMT", "open")
	page := d.page(owner, sp, "Page", nil, "x")
	id := d.ok(owner, "comment.add", map[string]any{"note_id": page, "body": "hello"})["id"].(string)
	d.ok(owner, "doc.archive", map[string]any{"id": page})
	for _, tc := range []struct {
		cmd   string
		input map[string]any
	}{
		{"comment.add", map[string]any{"note_id": page, "body": "new"}},
		{"comment.resolve", map[string]any{"id": id, "resolved": true}},
		{"comment.resolve", map[string]any{"id": id, "resolved": false}},
		{"comment.edit", map[string]any{"id": id, "body": "edited"}},
		{"comment.delete", map[string]any{"id": id}},
		{"comment.react", map[string]any{"id": id, "emoji": "👍"}},
	} {
		if code, msg := d.errOf(owner, tc.cmd, tc.input); code != "forbidden" || msg != "This page is archived" {
			t.Errorf("%s: %s %s", tc.cmd, code, msg)
		}
	}
}

func TestViewersSpaceViewerGetsCountOnly(t *testing.T) {
	d, owner := newDG(t)
	viewer := d.member(owner)
	sp := d.space(owner, "VWR", "open")
	page := d.page(owner, sp, "Page", nil, "x")
	d.ok(owner, "note.viewed", map[string]any{"note_id": page})
	d.sql("insert into core.space_members (workspace_id, space_id, user_id, role) values ($1::uuid,$2::uuid,$3::uuid,'viewer')", owner.ws, sp, viewer.userID)
	got := d.ok(viewer, "note.viewers", map[string]any{"note_id": page})
	if _, has := got["viewers"]; has || got["count"] != float64(1) {
		t.Fatalf("space viewer saw names: %v", got)
	}
}
