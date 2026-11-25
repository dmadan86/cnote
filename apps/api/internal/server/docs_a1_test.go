package server_test

import (
	"strings"
	"testing"
)

// Docs parity A1, W2: page status, icon, wide, labels, stars, recent and search filters. Every list applies the
// access predicate inside the query (7.6): restricted pages, private spaces, shares, guests, revocation, other tenants.

func labelCounts(d dg, w who, in map[string]any) map[string]float64 {
	out := map[string]float64{}
	for _, r := range d.list(w, "label.list", in) {
		out[r["label"].(string)] = r["count"].(float64)
	}
	return out
}

func mineIDs(d dg, w who, list string) []string {
	return ids(d.list(w, "note.mine", map[string]any{"list": list}))
}

func TestA1PageChromeFields(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	p := d.page(owner, sp, "Policy", nil, "text")
	n := d.ok(owner, "note.update", map[string]any{"id": p, "status": "in_review", "icon": "📘", "wide": true})
	if n["status"] != "in_review" || n["icon"] != "📘" || n["wide"] != true {
		t.Fatalf("fields: %v", n)
	}
	ch := d.hist(owner, p)
	for _, w := range []string{"status", "icon", "wide"} {
		if !strings.Contains(ch, `"`+w+`"`) {
			t.Fatalf("changed misses %s: %s", w, ch)
		}
	}
	d.code("invalid", owner, "note.update", map[string]any{"id": p, "status": "verified"})
	d.code("invalid", owner, "note.update", map[string]any{"id": p, "icon": strings.Repeat("a", 17)})
	d.code("invalid", owner, "note.update", map[string]any{"id": p, "wide": "yes"})
	if n := d.ok(owner, "note.update", map[string]any{"id": p, "status": nil, "icon": ""}); n["status"] != nil || n["icon"] != nil || n["wide"] != true {
		t.Fatalf("clear: %v", n)
	}
	// untouched fields stay when the update names none of them
	d.ok(owner, "note.update", map[string]any{"id": p, "status": "final"})
	if n := d.ok(member, "note.update", map[string]any{"id": p, "title": "Policy 2"}); n["status"] != "final" {
		t.Fatalf("status kept: %v", n)
	}
}

func TestA1LabelsNeverLeakRestrictedPages(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	open := d.space(owner, "OPN", "open")
	priv := d.space(owner, "SEC", "private")
	pub := d.page(owner, open, "Public", nil, "x")
	res := d.page(owner, open, "Restricted", nil, "x")
	hid := d.page(owner, priv, "Hidden", nil, "x")
	d.ok(owner, "note.restrict", map[string]any{"note_id": res, "restriction": "view"})
	for _, p := range []string{pub, res, hid} {
		d.ok(owner, "label.set", map[string]any{"note_id": p, "labels": []string{"Alpha", "alpha", "shared"}})
	}
	d.ok(owner, "label.set", map[string]any{"note_id": res, "labels": []string{"secret-plan", "shared"}})
	d.ok(owner, "label.set", map[string]any{"note_id": hid, "labels": []string{"vault"}})

	if got := d.ok(owner, "label.set", map[string]any{"note_id": pub, "labels": []string{"B", "a", "A"}})["labels"]; strings.Join(toStrings(got), ",") != "a,b" {
		t.Fatalf("normalised: %v", got)
	}
	d.ok(owner, "label.set", map[string]any{"note_id": pub, "labels": []string{"alpha", "shared"}})
	// the owner counts every readable page, the member neither the restricted nor the private-space page
	oc, mc := labelCounts(d, owner, map[string]any{}), labelCounts(d, member, map[string]any{})
	if oc["shared"] != 2 || oc["secret-plan"] != 1 || oc["vault"] != 1 {
		t.Fatalf("owner counts: %v", oc)
	}
	if mc["shared"] != 1 || mc["alpha"] != 1 || len(mc) != 2 {
		t.Fatalf("member counts leak: %v", mc)
	}
	if c := labelCounts(d, member, map[string]any{"prefix": "SEC"}); len(c) != 0 {
		t.Fatalf("prefix leak: %v", c)
	}
	// search by label and by status filter is access checked too
	if hits := ids(d.list(member, "search.notes", map[string]any{"label": "shared"})); len(hits) != 1 || hits[0] != pub {
		t.Fatalf("label search: %v", hits)
	}
	if hits := ids(d.list(member, "search.notes", map[string]any{"label": "vault"})); len(hits) != 0 {
		t.Fatalf("label search leak: %v", hits)
	}
	// edit rights: a viewer of the restricted page cannot label it
	d.code("not_found", member, "label.set", map[string]any{"note_id": res, "labels": []string{"x"}})
	if g := d.ok(owner, "note.get", map[string]any{"id": pub})["labels"]; strings.Join(toStrings(g), ",") != "alpha,shared" {
		t.Fatalf("note.get labels: %v", g)
	}
	d.code("invalid", owner, "label.set", map[string]any{"note_id": pub, "labels": []string{"has space"}})
	d.code("invalid", owner, "label.set", map[string]any{"note_id": pub, "labels": []string{"a,b"}})
}

func toStrings(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestA1StarsAndRecentFollowAccess(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	open := d.space(owner, "OPN", "open")
	page := d.page(owner, open, "Handbook", nil, "x")
	other := d.page(owner, open, "Other", nil, "x")
	d.ok(member, "note.star", map[string]any{"note_id": page, "starred": true})
	d.ok(member, "note.star", map[string]any{"note_id": other, "starred": true})
	d.ok(member, "note.viewed", map[string]any{"note_id": page})
	d.ok(member, "note.viewed", map[string]any{"note_id": page}) // idempotent
	if got := mineIDs(d, member, "starred"); len(got) != 2 || got[0] != other {
		t.Fatalf("starred newest first: %v", got)
	}
	if got := mineIDs(d, member, "recent"); len(got) != 1 || got[0] != page {
		t.Fatalf("recent: %v", got)
	}
	if got := mineIDs(d, owner, "starred"); len(got) != 0 {
		t.Fatalf("stars are per person: %v", got)
	}
	d.ok(member, "note.star", map[string]any{"note_id": other, "starred": false})
	if got := mineIDs(d, member, "starred"); len(got) != 1 || got[0] != page {
		t.Fatalf("unstar: %v", got)
	}
	// restricting the page later removes it from both lists, before the limit applies
	d.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	if len(mineIDs(d, member, "starred")) != 0 || len(mineIDs(d, member, "recent")) != 0 {
		t.Fatal("restricted page still listed")
	}
	d.code("not_found", member, "note.star", map[string]any{"note_id": page, "starred": true})
	d.code("not_found", member, "note.viewed", map[string]any{"note_id": page})
	// deleted pages drop out
	d.ok(owner, "note.delete", map[string]any{"id": page})
	if len(mineIDs(d, owner, "recent")) != 0 {
		t.Fatal("deleted page listed")
	}
}

func TestA1PrivateSpaceAndSpaceRemoval(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	priv := d.space(owner, "SEC", "private")
	p := d.page(owner, priv, "Vault", nil, "x")
	d.ok(owner, "note.star", map[string]any{"note_id": p, "starred": true})
	d.code("not_found", member, "note.star", map[string]any{"note_id": p, "starred": true})
	d.code("not_found", member, "note.viewed", map[string]any{"note_id": p})
	// a person who loses space membership loses the page from their lists at once
	d.sql("insert into core.space_members(workspace_id,space_id,user_id,role) values($1::uuid,$2::uuid,$3::uuid,'editor')", owner.ws, priv, member.userID)
	d.ok(member, "note.star", map[string]any{"note_id": p, "starred": true})
	if len(mineIDs(d, member, "starred")) != 1 {
		t.Fatal("member should see own star")
	}
	d.sql("delete from core.space_members where space_id = $1::uuid and user_id = $2::uuid", priv, member.userID)
	if got := mineIDs(d, member, "starred"); len(got) != 0 {
		t.Fatalf("revoked space still listed: %v", got)
	}
}

func TestA1GuestSeesOnlySharedPagesAndRevocationRemovesThem(t *testing.T) {
	d, owner := newDG(t)
	guest := d.e.addMember(owner, d.e.signup("Gst", false), "guest")
	open := d.space(owner, "OPN", "open")
	shared := d.page(owner, open, "Shared", nil, "findme")
	unshared := d.page(owner, open, "Unshared", nil, "findme")
	d.ok(owner, "label.set", map[string]any{"note_id": shared, "labels": []string{"team"}})
	d.ok(owner, "label.set", map[string]any{"note_id": unshared, "labels": []string{"team", "internal"}})
	d.ok(owner, "note.share", map[string]any{"note_id": shared, "email": guest.email, "level": "view"})
	d.code("not_found", guest, "note.star", map[string]any{"note_id": unshared, "starred": true})
	d.ok(guest, "note.star", map[string]any{"note_id": shared, "starred": true})
	d.ok(guest, "note.viewed", map[string]any{"note_id": shared})
	if c := labelCounts(d, guest, map[string]any{}); len(c) != 1 || c["team"] != 1 {
		t.Fatalf("guest label counts: %v", c)
	}
	if hits := ids(d.list(guest, "search.notes", map[string]any{"q": "findme"})); len(hits) != 1 || hits[0] != shared {
		t.Fatalf("guest search: %v", hits)
	}
	if got := mineIDs(d, guest, "starred"); len(got) != 1 || got[0] != shared {
		t.Fatalf("guest starred: %v", got)
	}
	d.ok(owner, "note.unshare", map[string]any{"note_id": shared, "user_id": guest.userID})
	if len(labelCounts(d, guest, map[string]any{})) != 0 || len(mineIDs(d, guest, "starred")) != 0 || len(mineIDs(d, guest, "recent")) != 0 {
		t.Fatal("revoked share still visible")
	}
	if hits := d.list(guest, "search.notes", map[string]any{"label": "team"}); len(hits) != 0 {
		t.Fatalf("revoked share found by label: %v", hits)
	}
}

func TestA1CrossWorkspaceIDsAreRefused(t *testing.T) {
	d, a := newDG(t)
	b := d.e.signup("Tess", true)
	sp := d.space(a, "OPN", "open")
	p := d.page(a, sp, "Mine", nil, "findme")
	d.ok(a, "label.set", map[string]any{"note_id": p, "labels": []string{"x"}})
	d.code("not_found", b, "label.set", map[string]any{"note_id": p, "labels": []string{"y"}})
	d.code("not_found", b, "note.star", map[string]any{"note_id": p, "starred": true})
	d.code("not_found", b, "note.viewed", map[string]any{"note_id": p})
	d.code("not_found", b, "note.update", map[string]any{"id": p, "status": "final"})
	if len(labelCounts(d, b, map[string]any{})) != 0 || len(mineIDs(d, b, "recent")) != 0 {
		t.Fatal("tenant leak")
	}
	if got := d.list(b, "search.notes", map[string]any{"label": "x", "space_id": sp}); len(got) != 0 {
		t.Fatalf("search leak: %v", got)
	}
}

func TestA1SearchFiltersAndPlainTextIndex(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	s1, s2 := d.space(owner, "AAA", "open"), d.space(owner, "BBB", "open")
	panel := d.page(owner, s1, "Panel page", nil, "> [!WARNING]\n> remember the **gstin** number\n\n<span data-status=\"green\">launchzone</span>")
	other := d.page(member, s2, "Other", nil, "gstin elsewhere")
	d.ok(owner, "note.update", map[string]any{"id": panel, "status": "final"})

	if got := ids(d.list(owner, "search.notes", map[string]any{"q": "gstin"})); len(got) != 2 {
		t.Fatalf("plain search: %v", got)
	}
	// tag and marker words are not indexed, the text inside them is
	if got := d.list(owner, "search.notes", map[string]any{"q": "span"}); len(got) != 0 {
		t.Fatalf("markup indexed: %v", got)
	}
	if got := d.list(owner, "search.notes", map[string]any{"q": "launchzone"}); len(got) != 1 {
		t.Fatalf("status text not indexed: %v", got)
	}
	cases := []struct {
		in   map[string]any
		want []string
	}{
		{map[string]any{"q": "gstin", "space_id": s2}, []string{other}},
		{map[string]any{"status": "final"}, []string{panel}},
		{map[string]any{"q": "gstin", "owner_id": member.userID}, []string{other}}, // the creator counts as a contributor
		{map[string]any{"q": "Other", "title_only": true}, []string{other}},
		{map[string]any{"q": "gstin", "title_only": true}, nil},
		{map[string]any{"q": "gstin", "updated_after": "2999-01-01T00:00:00Z"}, nil},
		{map[string]any{"updated_after": "2000-01-01T00:00:00Z", "space_id": s1}, []string{panel}},
	}
	for _, c := range cases {
		got := ids(d.list(owner, "search.notes", c.in))
		if len(got) != len(c.want) || (len(got) == 1 && got[0] != c.want[0]) {
			t.Fatalf("%v: got %v want %v", c.in, got, c.want)
		}
	}
	d.code("invalid", owner, "search.notes", map[string]any{})
	d.code("invalid", owner, "search.notes", map[string]any{"q": "x", "status": "bogus"})
	d.code("invalid", owner, "search.notes", map[string]any{"q": "x", "updated_after": "yesterday"})
}
