package server_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// C1 R2 (docs-parity-c 5.3, 13.6): note.cover_set. Real Postgres and the real Go server.

func TestC1CoverSetRolesFilesAndArchive(t *testing.T) {
	d, owner := newDG(t)
	editor := d.member(owner)
	viewer := d.member(owner)
	guest := d.e.addMember(owner, d.e.signup("Gst", false), "guest")
	sp := d.space(owner, "COV", "open")
	d.ok(owner, "space.member_set", map[string]any{"space_id": sp, "user_id": viewer.userID, "role": "viewer"})
	page := d.page(owner, sp, "Cover page", nil, "x")
	other := d.page(owner, sp, "Other page", nil, "x")
	img := d.pageFile(owner, page)
	theirs := d.pageFile(owner, other)
	pdf := d.ok(owner, "file.upload", map[string]any{"note_id": page, "filename": "a.pdf",
		"data_base64": base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n"))})["id"].(string)

	cover := func(r map[string]any) map[string]any { c, _ := r["cover"].(map[string]any); return c }
	// set (default position 50), reposition, read back through note.get, clear
	if c := cover(d.ok(owner, "note.cover_set", map[string]any{"note_id": page, "file_id": img})); c["file_id"] != img || c["y"] != float64(50) {
		t.Fatalf("set: %v", c)
	}
	if c := cover(d.ok(editor, "note.cover_set", map[string]any{"note_id": page, "file_id": img, "y": 20})); c["y"] != float64(20) {
		t.Fatalf("reposition: %v", c)
	}
	if c := cover(d.ok(owner, "note.get", map[string]any{"id": page})); c["file_id"] != img || c["y"] != float64(20) {
		t.Fatalf("note.get: %v", c)
	}
	// the file must be an image attached to this page, and y must be in range
	d.code("invalid", owner, "note.cover_set", map[string]any{"note_id": page, "file_id": theirs})
	d.code("invalid", owner, "note.cover_set", map[string]any{"note_id": page, "file_id": pdf})
	d.code("invalid", owner, "note.cover_set", map[string]any{"note_id": page, "file_id": "00000000-0000-4000-8000-000000000000"})
	d.code("invalid", owner, "note.cover_set", map[string]any{"note_id": page, "file_id": img, "y": 101})
	// a viewer cannot set; a guest only with edit access to the page
	if r := d.run(viewer, "note.cover_set", map[string]any{"note_id": page, "file_id": img}); r.status == 200 {
		t.Fatalf("a viewer set a cover: %s", r.body)
	}
	if r := d.run(guest, "note.cover_set", map[string]any{"note_id": page, "file_id": nil}); r.status == 200 {
		t.Fatalf("a guest without access cleared a cover: %s", r.body)
	}
	d.ok(owner, "note.share", map[string]any{"note_id": page, "email": guest.email, "level": "edit"})
	d.ok(guest, "note.cover_set", map[string]any{"note_id": page, "file_id": img, "y": 70})
	// duplicate carries the cover to the copied file
	dup := d.ok(owner, "note.duplicate", map[string]any{"id": page})["id"].(string)
	c := cover(d.ok(owner, "note.get", map[string]any{"id": dup}))
	if c == nil || c["file_id"] == img || c["y"] != float64(70) {
		t.Fatalf("duplicate cover: %v", c)
	}
	// clearing, and a cover whose file was deleted
	if r := d.ok(owner, "note.cover_set", map[string]any{"note_id": page, "file_id": nil}); r["cover"] != nil {
		t.Fatalf("clear: %v", r)
	}
	if d.scalar("select count(*)::text from core.notes where id = $1::uuid and cover_file_id is null and cover_y is null", page) != "1" {
		t.Fatal("clear left columns behind")
	}
	// an archived page is read-only, cover included
	d.ok(owner, "note.cover_set", map[string]any{"note_id": other, "file_id": theirs})
	d.ok(owner, "doc.archive", map[string]any{"id": other})
	for _, in := range []map[string]any{{"note_id": other, "file_id": nil}, {"note_id": other, "file_id": theirs, "y": 5}} {
		if code, msg := d.errOf(owner, "note.cover_set", in); code != "forbidden" || msg != "This page is archived" {
			t.Fatalf("archived page: %s %q", code, msg)
		}
	}
	// an archived space too
	sp2 := d.space(owner, "ARC", "open")
	p2 := d.page(owner, sp2, "P2", nil, "x")
	f2 := d.pageFile(owner, p2)
	d.archive(sp2, true)
	d.archivedRefusal(owner, "note.cover_set", map[string]any{"note_id": p2, "file_id": f2})
}

func TestC1PublicPageServesTheCoverOnly(t *testing.T) {
	x := newPG(t)
	sp := x.space(x.owner, "PCV", "open")
	page := x.page(x.owner, sp, "Public cover", nil, "just text")
	cov, other := x.pageFile(x.owner, page), x.pageFile(x.owner, page)
	x.ok(x.owner, "note.cover_set", map[string]any{"note_id": page, "file_id": cov, "y": 30})
	token := x.link(x.owner, page)
	s, _, b := x.get(token, "")
	if s != 200 {
		t.Fatalf("%d %s", s, b)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(b), &m)
	want := "/api/public/page/" + token + "/files/" + cov
	if m["cover_url"] != want || m["cover_y"] != float64(30) {
		t.Fatalf("cover fields: %v %v", m["cover_url"], m["cover_y"])
	}
	if strings.Contains(b, x.ws) || strings.Contains(b, other) {
		t.Fatalf("public page leaks: %s", b)
	}
	if s, h, _ := x.get(token+"/files/"+cov, ""); s != 200 || h.Get("Content-Type") != "image/png" {
		t.Fatalf("cover file: %d %v", s, h)
	}
	x.expectGone(token + "/files/" + other) // attached, but in neither the body nor the cover
	x.ok(x.owner, "note.cover_set", map[string]any{"note_id": page, "file_id": nil})
	x.expectGone(token + "/files/" + cov)
	if _, _, b := x.get(token, ""); !strings.Contains(b, `"cover_url":null`) {
		t.Fatalf("cleared cover still shown: %s", b)
	}
}
