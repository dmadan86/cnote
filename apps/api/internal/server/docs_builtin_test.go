package server_test

import (
	"strings"
	"testing"
)

// Built-in templates (docs-parity 5.5, 7.6): ids, rendering, filtering and permissions.

func TestBuiltinTemplatesListAndRender(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "OPS", "open")
	parent := d.page(owner, space, "Handbook", nil, "")

	all := d.list(owner, "template.list", map[string]any{"include_content": true})
	var builtins int
	for _, x := range all {
		if x["builtin"] == true {
			builtins++
			if !strings.HasPrefix(x["id"].(string), "builtin:") || x["category"] == "" || x["content_md"] == "" {
				t.Fatalf("bad built-in row: %v", x)
			}
		}
	}
	if builtins != 12 {
		t.Fatalf("built-ins listed: %d", builtins)
	}
	// custom ones sit beside them; a board filter or a record filter leaves the built-ins out
	for _, in := range []map[string]any{{"kind": "board"}, {"source_kind": "deal"}} {
		for _, x := range d.list(owner, "template.list", in) {
			if x["builtin"] == true {
				t.Fatalf("%v listed a built-in", in)
			}
		}
	}
	if x := d.list(owner, "template.list", map[string]any{})[0]; x["content_md"] != nil {
		t.Fatal("content only on request")
	}

	// the id is read before UUID parsing; substitution is the one custom templates get
	r := d.ok(owner, "template.render", map[string]any{"template_id": "builtin:meeting-notes", "space_id": space, "parent_id": parent,
		"answers": map[string]any{"date": "3 Dec 2026", "attendees": "Asha"}})
	note := r["note"].(map[string]any)
	if len(r["unresolved"].([]any)) != 0 || note["parent_id"] != parent || !strings.HasPrefix(note["title"].(string), "Meeting notes ") ||
		strings.Contains(note["title"].(string), "{{") {
		t.Fatalf("render: %v", r)
	}
	body := d.ok(owner, "note.get", map[string]any{"id": note["id"]})["content_md"].(string)
	if strings.Contains(body, "{{") || !strings.Contains(body, "Tess") {
		t.Fatalf("body not substituted: %.200s", body)
	}
	// a parent alone is enough to place the page
	d.ok(owner, "template.render", map[string]any{"template_id": "builtin:sop", "parent_id": parent, "title": "Packing SOP"})
	// every built-in renders
	for _, x := range all {
		if x["builtin"] == true {
			d.ok(owner, "template.render", map[string]any{"template_id": x["id"], "space_id": space})
		}
	}
	d.code("not_found", owner, "template.render", map[string]any{"template_id": "builtin:nope", "space_id": space})
	d.code("invalid", owner, "template.render", map[string]any{"template_id": "not-a-uuid", "space_id": space})
	d.code("invalid", owner, "template.render", map[string]any{"template_id": "builtin:sop"})
	// refresh has nothing to refresh from
	d.code("invalid", owner, "template.refresh", map[string]any{"note_id": note["id"]})
}

func TestBuiltinTemplatesSpaceFilteringAndPermissions(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	private := d.space(owner, "HUSH", "private")
	open := d.space(owner, "OPN", "open")
	d.save(owner, private, "Board pack", "none", "salary bands", []string{})

	names := func(w who) (n, builtin int) {
		for _, x := range d.list(w, "template.list", map[string]any{}) {
			if x["builtin"] == true {
				builtin++
			} else if x["name"] == "Board pack" {
				n++
			}
		}
		return
	}
	if c, b := names(owner); c != 1 || b != 12 {
		t.Fatalf("owner sees %d custom, %d built-in", c, b)
	}
	if c, b := names(member); c != 0 || b != 12 {
		t.Fatalf("member sees %d custom, %d built-in", c, b)
	}

	// destination permission: no page in a space the caller cannot create in, nor under a page they cannot edit
	if d.run(member, "template.render", map[string]any{"template_id": "builtin:sop", "space_id": private}).status == 200 {
		t.Fatal("member created a page in a private space")
	}
	d.ok(member, "template.render", map[string]any{"template_id": "builtin:sop", "space_id": open})
	locked := d.page(owner, open, "Locked", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": locked, "restriction": "view"})
	if d.run(member, "template.render", map[string]any{"template_id": "builtin:sop", "parent_id": locked}).status == 200 {
		t.Fatal("member created a page under a restricted page")
	}
	guest := d.e.addMember(owner, d.e.signup("G", false), "guest")
	if d.run(guest, "template.render", map[string]any{"template_id": "builtin:sop", "space_id": open}).status == 200 {
		t.Fatal("guest created a page")
	}

	// saving a restricted page as a template stays refused
	d.code("forbidden", owner, "template.save", map[string]any{"note_id": locked, "name": "leaky", "source_kind": "none"})
}
