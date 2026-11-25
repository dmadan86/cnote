package server_test

import (
	"strings"
	"sync"
	"testing"
)

// Docs parity B1, P1 (docs-parity-b 4.1, 13.2.1, 13.2.2): space members and settings. Real Postgres and the real Go server.

func (d dg) setRole(by who, space string, u who, role any) resp {
	return d.run(by, "space.member_set", map[string]any{"space_id": space, "user_id": u.userID, "role": role})
}

func (d dg) spaceRow(w who, id string, archived bool) map[string]any {
	for _, s := range d.list(w, "space.list", map[string]any{"include_archived": archived}) {
		if s["id"] == id {
			return s
		}
	}
	return nil
}

func (d dg) hidden(w who, page string) bool {
	r := d.run(w, "note.get", map[string]any{"id": page})
	if r.status == 200 {
		return false
	}
	return d.run(w, "note.update", map[string]any{"id": page, "title": "x"}).status != 200
}

func (d dg) searchHits(w who, q string) int {
	return len(d.list(w, "search.notes", map[string]any{"q": q}))
}

// A space admin manages the space and nothing else: a restricted page stays closed to them, and opening the space, adding
// people or promoting them never changes that (13.2.1).
func TestSpaceAdminNeverGainsPageAccess(t *testing.T) {
	d, owner := newDG(t)
	admin, other := d.member(owner), d.member(owner)
	sp := d.space(owner, "PRV", "private")
	root := d.page(owner, sp, "Handbook", nil, "visible")
	secret := d.child(owner, sp, "Salaries zebra", root)
	d.ok(owner, "note.restrict", map[string]any{"note_id": secret, "restriction": "view"})

	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": admin.userID, "role": "admin"})
	d.ok(admin, "note.get", map[string]any{"id": root})
	if !d.hidden(admin, secret) || d.searchHits(admin, "zebra") != 0 {
		t.Fatal("a space admin reached a restricted descendant")
	}
	// adding a member and promoting one, by the space admin
	d.ok(admin, "space.member_set", map[string]any{"space_id": sp, "user_id": other.userID, "role": "viewer"})
	d.ok(admin, "space.member_set", map[string]any{"space_id": sp, "user_id": other.userID, "role": "admin"})
	for _, w := range []who{admin, other} {
		if !d.hidden(w, secret) || d.searchHits(w, "zebra") != 0 {
			t.Fatal("a promotion exposed a restricted page")
		}
	}
	// opening the space, by the space admin
	d.ok(admin, "space.update", map[string]any{"id": sp, "visibility": "open"})
	outsider := d.member(owner)
	d.ok(outsider, "note.get", map[string]any{"id": root})
	for _, w := range []who{admin, other, outsider} {
		if !d.hidden(w, secret) || d.searchHits(w, "zebra") != 0 {
			t.Fatal("opening the space exposed a restricted page")
		}
	}
	d.ok(owner, "note.get", map[string]any{"id": secret})
}

func TestSpaceMemberRolesFollowTheMatrix(t *testing.T) {
	d, owner := newDG(t)
	admin, editor, viewer, plain := d.member(owner), d.member(owner), d.member(owner), d.member(owner)
	guest := d.e.addMember(owner, d.e.signup("Gst", false), "guest")
	sp := d.space(owner, "PRV", "private")
	page := d.page(owner, sp, "Plan", nil, "x")

	if d.setRole(owner, sp, admin, "admin").status != 200 || d.setRole(owner, sp, editor, "editor").status != 200 ||
		d.setRole(owner, sp, viewer, "viewer").status != 200 {
		t.Fatal("the owner could not add members")
	}
	// who may manage
	d.code("forbidden", editor, "space.member_set", map[string]any{"space_id": sp, "user_id": plain.userID, "role": "viewer"})
	d.code("forbidden", viewer, "space.update", map[string]any{"id": sp, "name": "Mine"})
	d.code("forbidden", editor, "space.archive", map[string]any{"id": sp, "archived": true})
	d.code("not_found", plain, "space.members", map[string]any{"space_id": sp})
	if m := d.ok(editor, "space.members", map[string]any{"space_id": sp}); m["can_manage"] != false {
		t.Fatalf("an editor can manage: %v", m)
	}
	m := d.ok(admin, "space.members", map[string]any{"space_id": sp})
	if m["can_manage"] != true || m["visibility"] != "private" || len(m["members"].([]any)) != 4 {
		t.Fatalf("members: %v", m)
	}
	// roles do what they say
	d.ok(editor, "note.update", map[string]any{"id": page, "title": "Plan 2"})
	d.code("forbidden", viewer, "note.update", map[string]any{"id": page, "title": "Plan 3"})
	d.ok(admin, "space.member_set", map[string]any{"space_id": sp, "user_id": viewer.userID, "role": "editor"})
	d.ok(viewer, "note.update", map[string]any{"id": page, "title": "Plan 4"})
	// a space admin may not touch a workspace admin or owner row
	d.code("forbidden", admin, "space.member_set", map[string]any{"space_id": sp, "user_id": owner.userID, "role": "viewer"})
	// no guests on a whole space, and nobody outside the workspace
	r := d.setRole(owner, sp, guest, "viewer")
	if r.status == 200 || !strings.Contains(string(r.body), "Guests cannot be added") {
		t.Fatalf("guest added: %d %s", r.status, r.body)
	}
	if d.setRole(owner, sp, d.e.signup("Stranger", true), "viewer").status == 200 {
		t.Fatal("a stranger was added")
	}
	// a person added gets a notification titled with the space
	if d.scalar("select count(*)::text from core.notifications where user_id = $1::uuid and kind = 'shared' and title = 'PRV' and note_id is null", editor.userID) != "1" {
		t.Fatal("no shared notification for the new member")
	}
	// removal takes effect at once: pages and search
	d.ok(owner, "note.get", map[string]any{"id": page})
	if d.searchHits(editor, "plan") == 0 {
		t.Fatal("setup: the editor finds the page")
	}
	if got := d.ok(admin, "space.member_set", map[string]any{"space_id": sp, "user_id": editor.userID, "role": nil}); got["removed"] != true {
		t.Fatalf("remove: %v", got)
	}
	d.code("not_found", editor, "note.get", map[string]any{"id": page})
	if d.searchHits(editor, "plan") != 0 {
		t.Fatal("a removed member still finds the page")
	}
}

func TestPrivateSpaceKeepsOneAdmin(t *testing.T) {
	d, owner := newDG(t)
	a, b := d.member(owner), d.member(owner)
	sp := d.space(owner, "PRV", "private") // the creator is the only admin row
	d.code("conflict", owner, "space.member_set", map[string]any{"space_id": sp, "user_id": owner.userID, "role": nil})
	d.code("conflict", owner, "space.member_set", map[string]any{"space_id": sp, "user_id": owner.userID, "role": "viewer"})
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": a.userID, "role": "admin"})
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": b.userID, "role": "admin"})
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": owner.userID, "role": nil})
	// two admins left, both demote the other at once: one of them must lose
	var wg sync.WaitGroup
	res := make([]int, 2)
	for i, c := range []struct{ by, target who }{{a, b}, {b, a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i] = d.setRole(c.by, sp, c.target, nil).status
		}()
	}
	wg.Wait()
	if n := d.scalar("select count(*)::text from core.space_members where space_id = $1::uuid and role = 'admin'", sp); n != "1" {
		t.Fatalf("admins left %s (statuses %v)", n, res)
	}
}

func TestOpenToPrivateKeepsListedMembers(t *testing.T) {
	d, owner := newDG(t)
	admin, listed, unlisted := d.member(owner), d.member(owner), d.member(owner)
	sp := d.space(owner, "OPN", "open")
	page := d.page(owner, sp, "Doc", nil, "x")
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": admin.userID, "role": "admin"})
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": listed.userID, "role": "viewer"})
	d.ok(unlisted, "note.get", map[string]any{"id": page})
	d.code("forbidden", listed, "note.update", map[string]any{"id": page, "title": "no"}) // viewer row in an open space
	if got := d.ok(admin, "space.update", map[string]any{"id": sp, "visibility": "private", "name": "Closed", "description": "Only us"}); got["visibility"] != "private" || got["name"] != "Closed" || got["description"] != "Only us" || got["my_role"] != "admin" {
		t.Fatalf("update: %v", got)
	}
	d.ok(listed, "note.get", map[string]any{"id": page})
	d.code("not_found", unlisted, "note.get", map[string]any{"id": page})
	for _, bad := range []map[string]any{{"name": ""}, {"name": strings.Repeat("x", 81)}, {"description": strings.Repeat("x", 501)}, {"visibility": "secret"}} {
		bad["id"] = sp
		d.code("invalid", admin, "space.update", bad)
	}
	d.ok(admin, "space.update", map[string]any{"id": sp, "description": nil})
	if d.scalar("select coalesce(description, 'none') from core.spaces where id = $1::uuid", sp) != "none" {
		t.Fatal("description not cleared")
	}
}

func TestArchiveStatesTheRealReason(t *testing.T) {
	d, owner := newDG(t)
	admin, editor := d.member(owner), d.member(owner)
	sp := d.space(owner, "ARC", "open")
	root := d.page(owner, sp, "Root", nil, "x")
	kid := d.child(owner, sp, "Kid", root)
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": admin.userID, "role": "admin"})
	d.code("forbidden", editor, "space.archive", map[string]any{"id": sp, "archived": true})

	if got := d.ok(admin, "space.archive", map[string]any{"id": sp, "archived": true}); got["archived"] != true {
		t.Fatalf("archive: %v", got)
	}
	for _, w := range []who{owner, admin, editor} {
		d.archivedRefusal(w, "note.update", map[string]any{"id": root, "title": "x"})
		// the real reason, not "Some pages here are restricted", for someone who can see the space
		for c, in := range map[string]map[string]any{
			"note.delete": {"id": kid}, "note.duplicate": {"id": kid}, "note.move": {"id": kid, "parent_id": nil},
			"note.reorder": {"id": kid, "before_id": nil},
		} {
			d.archivedRefusal(w, c, in)
		}
	}
	d.ok(editor, "note.get", map[string]any{"id": root})
	if d.spaceRow(editor, sp, false) != nil {
		t.Fatal("an archived space is listed")
	}
	if got := d.spaceRow(editor, sp, true); got == nil || got["archived"] != true {
		t.Fatalf("include_archived: %v", got)
	}
	// settings still work on an archived space, and it can come back; a non-admin cannot
	d.ok(admin, "space.update", map[string]any{"id": sp, "description": "Frozen"})
	d.ok(admin, "space.member_set", map[string]any{"space_id": sp, "user_id": editor.userID, "role": "viewer"})
	d.code("forbidden", editor, "space.archive", map[string]any{"id": sp, "archived": false})
	d.ok(admin, "space.archive", map[string]any{"id": sp, "archived": false})
	d.code("forbidden", editor, "note.update", map[string]any{"id": root, "title": "x"}) // viewer row now
	d.ok(owner, "note.update", map[string]any{"id": root, "title": "back"})

	// a restriction in an unarchived space still answers the generic text
	d.ok(owner, "note.restrict", map[string]any{"note_id": kid, "restriction": "view"})
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": editor.userID, "role": "editor"})
	d.refused(editor, "note.delete", map[string]any{"id": root})
}

func TestPublicLinksSwitch(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "PUB", "open")
	if d.ok(owner, "space.update", map[string]any{"id": sp, "public_links": true})["public_links"] != true {
		t.Fatal("on")
	}
	r := d.run(owner, "space.update", map[string]any{"id": sp, "public_links": false})
	if r.status != 200 {
		if strings.Contains(string(r.body), "not implemented") { // page_link.revoke_space is P2's; the switch itself is checked once it lands
			t.Skip("page_link.revoke_space is not registered yet")
		}
		t.Fatalf("switch off: %d %s", r.status, r.body)
	}
	if d.spaceRow(owner, sp, false)["public_links"] != false {
		t.Fatal("the switch is not reported")
	}
}
