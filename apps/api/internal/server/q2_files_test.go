package server_test

import (
	"encoding/base64"
	"testing"
)

// Docs B2 Q2: the per-page file cap, the uploader name, and the two lists the editor chrome relies on (the page link
// picker and the Sub-pages list) never showing a page the caller cannot read. Real Postgres and the real Go server.

func TestQ2FileCapIsFiftyOnPlainPagesAndTenOnTypedNotes(t *testing.T) {
	d, owner := newDG(t)
	sp := d.space(owner, "OPN", "open")
	page := d.page(owner, sp, "Plain", nil, "x")
	typed := d.page(owner, sp, "Typed", nil, "x")
	d.sql("update core.notes set types = '{expense}' where id = $1::uuid", typed)
	b64 := base64.StdEncoding.EncodeToString(png)
	up := func(note string) map[string]any {
		return map[string]any{"note_id": note, "filename": "a.png", "data_base64": b64}
	}
	for i := 0; i < 50; i++ {
		d.ok(owner, "file.upload", up(page))
	}
	d.code("conflict", owner, "file.upload", up(page))
	for i := 0; i < 10; i++ {
		d.ok(owner, "file.upload", up(typed))
	}
	d.code("conflict", owner, "file.upload", up(typed))

	rows := d.list(owner, "file.list", map[string]any{"note_id": page})
	if len(rows) != 50 || rows[0]["created_by_name"] != "Tess" {
		t.Fatalf("want 50 files by Tess, got %d %v", len(rows), rows[0])
	}
	// a reader without access to the page gets not_found, not the list
	stranger := d.e.addMember(owner, d.e.signup("Str", false), "member")
	d.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	d.code("not_found", stranger, "file.list", map[string]any{"note_id": page})
}

func TestQ2PickerAndSubPagesHideRestrictedPages(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	parent := d.page(owner, sp, "Handbook", nil, "x")
	d.child(owner, sp, "Zebra visible", parent)
	hidden := d.child(owner, sp, "Zebra secret", parent)
	d.ok(owner, "note.restrict", map[string]any{"note_id": hidden, "restriction": "view"})

	titles := func(rows []map[string]any) (out []string) {
		for _, r := range rows {
			out = append(out, r["title"].(string))
		}
		return
	}
	pick := titles(d.list(member, "search.notes", map[string]any{"q": "Zebra", "title_only": true, "limit": 8}))
	kids := titles(d.list(member, "note.list", map[string]any{"space_id": sp, "parent_id": parent}))
	for name, got := range map[string][]string{"picker": pick, "sub-pages": kids} {
		if len(got) != 1 || got[0] != "Zebra visible" {
			t.Fatalf("%s for a member: %v", name, got)
		}
	}
}
