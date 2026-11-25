package server_test

import "testing"

// Docs parity B1, P5 (docs-parity-b 4.5): comment edit, delete and reactions. Real Postgres and the real Go server.

func reactionsOf(m map[string]any) map[string][2]any {
	out := map[string][2]any{}
	for _, r := range m["reactions"].([]any) {
		x := r.(map[string]any)
		out[x["emoji"].(string)] = [2]any{x["count"], x["mine"]}
	}
	return out
}

func (d dg) commentsOf(w who, page string) map[string]map[string]any {
	d.t.Helper()
	out := map[string]map[string]any{}
	for _, c := range d.list(w, "comment.list", map[string]any{"note_id": page}) {
		out[c["id"].(string)] = c
	}
	return out
}

func TestCommentEditIsAuthorOnlyAndMarked(t *testing.T) {
	d, owner := newDG(t)
	a, b := d.member(owner), d.member(owner)
	sp := d.space(owner, "OPN", "open")
	page := d.page(owner, sp, "Plan", nil, "x")
	id := d.ok(a, "comment.add", map[string]any{"note_id": page, "body": "first"})["id"].(string)

	d.code("forbidden", b, "comment.edit", map[string]any{"id": id, "body": "hijack"})
	d.code("forbidden", owner, "comment.edit", map[string]any{"id": id, "body": "admin edit"}) // not even a workspace admin
	if got := d.commentsOf(a, page)[id]; got["body"] != "first" || got["edited_at"] != nil {
		t.Fatalf("untouched: %v", got)
	}
	d.code("invalid", a, "comment.edit", map[string]any{"id": id, "body": "  "})
	got := d.ok(a, "comment.edit", map[string]any{"id": id, "body": "second"})
	if got["body"] != "second" || got["edited_at"] == nil {
		t.Fatalf("edit: %v", got)
	}

	// Editing tells only people newly mentioned, and only once.
	inb := func() int { return len(inbox(d.gw, b, "mention")) }
	d.ok(a, "comment.edit", map[string]any{"id": id, "body": "hi " + mention(owner.ws, b.userID, "B")})
	if inb() != 1 {
		t.Fatalf("new mention: %d", inb())
	}
	d.ok(a, "comment.edit", map[string]any{"id": id, "body": "again " + mention(owner.ws, b.userID, "B")})
	if inb() != 1 || len(inbox(d.gw, owner, "comment")) != 0 {
		t.Fatalf("an edit re-notified: %d", inb())
	}
	if n := d.scalar("select count(*)::text from core.events where kind = 'comment.edited' and payload::text like '%again%'"); n != "0" {
		t.Fatal("the event carries text")
	}
}

func TestCommentDeleteKeepsTheThread(t *testing.T) {
	d, owner := newDG(t)
	a, b := d.member(owner), d.member(owner)
	sp := d.space(owner, "OPN", "open")
	page := d.page(a, sp, "Plan", nil, "x") // a owns the page
	top := d.ok(b, "comment.add", map[string]any{"note_id": page, "body": "secret top", "anchor_text": "x"})["id"].(string)
	reply := d.ok(a, "comment.add", map[string]any{"note_id": page, "body": "a reply", "parent_id": top})["id"].(string)
	lone := d.ok(b, "comment.add", map[string]any{"note_id": page, "body": "alone"})["id"].(string)

	other := d.member(owner)
	d.code("forbidden", other, "comment.delete", map[string]any{"id": lone})
	d.ok(a, "comment.delete", map[string]any{"id": top}) // the page owner moderates
	got := d.commentsOf(b, page)
	if t0 := got[top]; t0 == nil || t0["deleted"] != true || t0["body"] != "" || t0["author_name"] != nil || t0["anchor_text"] != nil {
		t.Fatalf("tombstone: %v", t0)
	}
	if got[reply]["body"] != "a reply" {
		t.Fatalf("reply lost: %v", got[reply])
	}
	if d.scalar("select body from core.comments where id = $1::uuid", top) != "" {
		t.Fatal("body not erased in the row")
	}
	d.code("not_found", b, "comment.edit", map[string]any{"id": top, "body": "back"})
	d.code("not_found", b, "comment.react", map[string]any{"id": top, "emoji": "👍", "on": true})
	d.code("invalid", b, "comment.add", map[string]any{"note_id": page, "body": "to a ghost", "parent_id": top})

	d.ok(b, "comment.delete", map[string]any{"id": lone}) // the author
	d.ok(b, "comment.delete", map[string]any{"id": lone}) // idempotent
	if _, in := d.commentsOf(b, page)[lone]; in {
		t.Fatal("a deleted comment with no replies should vanish")
	}
	d.ok(a, "comment.delete", map[string]any{"id": reply})
	if len(d.commentsOf(b, page)) != 0 {
		t.Fatalf("the last live reply went, the tombstone goes too: %v", d.commentsOf(b, page))
	}

	// A space admin and a workspace admin may delete too; a plain member may not.
	c1 := d.ok(b, "comment.add", map[string]any{"note_id": page, "body": "one"})["id"].(string)
	d.sql("insert into core.space_members (workspace_id, space_id, user_id, role) values ($1::uuid,$2::uuid,$3::uuid,'admin')", owner.ws, sp, other.userID)
	d.ok(other, "comment.delete", map[string]any{"id": c1})
	c2 := d.ok(b, "comment.add", map[string]any{"note_id": page, "body": "two"})["id"].(string)
	d.ok(owner, "comment.delete", map[string]any{"id": c2})
}

func TestCommentReactions(t *testing.T) {
	d, owner := newDG(t)
	a, b := d.member(owner), d.member(owner)
	sp := d.space(owner, "OPN", "open")
	page := d.page(owner, sp, "Plan", nil, "x")
	id := d.ok(a, "comment.add", map[string]any{"note_id": page, "body": "nice"})["id"].(string)
	react := func(w who, e string, on bool) map[string]any {
		return d.ok(w, "comment.react", map[string]any{"id": id, "emoji": e, "on": on})
	}

	react(a, "👍", true)
	react(a, "👍", true) // a second tap is idempotent
	r := reactionsOf(react(b, "👍", true))
	if r["👍"] != [2]any{float64(2), true} {
		t.Fatalf("two people: %v", r)
	}
	react(b, "🎉", true)
	if got := reactionsOf(d.commentsOf(a, page)[id]); got["👍"] != [2]any{float64(2), true} || got["🎉"] != [2]any{float64(1), false} {
		t.Fatalf("per reader: %v", got)
	}
	if n := d.scalar("select count(*)::text from core.comment_reactions where comment_id = $1::uuid", id); n != "3" {
		t.Fatalf("rows: %s", n)
	}
	d.code("invalid", a, "comment.react", map[string]any{"id": id, "emoji": "💩", "on": true})
	react(b, "🎉", false)
	react(b, "🎉", false) // removing twice is fine
	if got := reactionsOf(d.commentsOf(a, page)[id]); len(got) != 1 {
		t.Fatalf("removed: %v", got)
	}
}

func TestReactionsOnARestrictedPageAreInvisible(t *testing.T) {
	d, owner := newDG(t)
	a, outsider := d.member(owner), d.member(owner)
	sp := d.space(owner, "OPN", "open")
	page := d.page(owner, sp, "Plan", nil, "x")
	id := d.ok(owner, "comment.add", map[string]any{"note_id": page, "body": "nice"})["id"].(string)
	d.ok(a, "comment.react", map[string]any{"id": id, "emoji": "👍", "on": true})
	d.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})

	d.code("not_found", outsider, "comment.list", map[string]any{"note_id": page})
	d.code("not_found", outsider, "comment.react", map[string]any{"id": id, "emoji": "❤️", "on": true})
	d.code("not_found", outsider, "comment.edit", map[string]any{"id": id, "body": "x"})
	d.code("not_found", outsider, "comment.delete", map[string]any{"id": id})
	d.code("not_found", a, "comment.react", map[string]any{"id": id, "emoji": "👍", "on": false})
	if n := d.scalar("select count(*)::text from core.comment_reactions where comment_id = $1::uuid", id); n != "1" {
		t.Fatalf("an outsider changed reactions: %s", n)
	}
	// The same refusal for a comment that does not exist.
	d.code("not_found", outsider, "comment.react", map[string]any{"id": "00000000-0000-4000-8000-000000000000", "emoji": "👍", "on": true})
}

func TestArchivedSpaceRefusesCommentEditDeleteReact(t *testing.T) {
	d, owner := newDG(t)
	a := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	page := d.page(owner, sp, "Plan", nil, "x")
	id := d.ok(a, "comment.add", map[string]any{"note_id": page, "body": "mine"})["id"].(string)
	d.archive(sp, true)
	for _, w := range []who{a, owner} {
		d.archivedRefusal(w, "comment.edit", map[string]any{"id": id, "body": "x"})
		d.archivedRefusal(w, "comment.delete", map[string]any{"id": id})
		d.archivedRefusal(w, "comment.react", map[string]any{"id": id, "emoji": "👍", "on": true})
	}
	if d.commentsOf(a, page)[id]["body"] != "mine" {
		t.Fatal("changed")
	}
	d.archive(sp, false)
	d.ok(a, "comment.react", map[string]any{"id": id, "emoji": "👍", "on": true})
}
