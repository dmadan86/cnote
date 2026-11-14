package server_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Ports of crates/cnote-core/tests/page_permissions.rs. Sharing commands run on the Go server; commands not
// ported yet (note.list, note.update, ...) go through it too and are proxied to Rust, so the whole flow is
// exercised end to end against the same database.
type sh struct {
	*env
	owner who
}

func newSh(t *testing.T) *sh {
	e := newEnv(t)
	return &sh{e, e.signup("Olive", true)}
}

func (s *sh) call(w who, cmd string, in any) resp {
	s.t.Helper()
	return s.cmd(s.gosv.URL, w, s.owner.ws, cmd, in, nil)
}

func (s *sh) ok(w who, cmd string, in any) any {
	s.t.Helper()
	r := s.call(w, cmd, in)
	if r.status != 200 {
		s.t.Fatalf("%s %v: %d %s", cmd, in, r.status, r.body)
	}
	var v any
	_ = json.Unmarshal(r.body, &v)
	return v
}

func (s *sh) errStatus(w who, cmd string, in any, status int, code string) {
	s.t.Helper()
	r := s.call(w, cmd, in)
	if r.status != status || !strings.Contains(string(r.body), `"code":"`+code+`"`) {
		s.t.Fatalf("%s %v: want %d %s, got %d %s", cmd, in, status, code, r.status, r.body)
	}
}

func (s *sh) member(role string) who { return s.addMember(s.owner, s.signup("M", false), role) }

func (s *sh) space(key, vis string) string {
	return s.ok(s.owner, "space.create", map[string]any{"key": key, "name": key, "visibility": vis}).(map[string]any)["id"].(string)
}

func (s *sh) page(space, title, parent, body string) string {
	in := map[string]any{"space_id": space, "title": title, "content_md": body}
	if parent != "" {
		in["parent_id"] = parent
	}
	return s.ok(s.owner, "note.create", in).(map[string]any)["id"].(string)
}

func idsOf(v any) []string {
	out := []string{}
	for _, n := range v.([]any) {
		out = append(out, n.(map[string]any)["id"].(string))
	}
	return out
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSharingViewRestrictionHidesPageAndChildrenInherit(t *testing.T) {
	w := newSh(t)
	bob := w.member("member")
	sp := w.space("VR", "open")
	secret := w.page(sp, "Layoff plan", "", "zebrafish budget")
	child := w.page(sp, "Layoff child", secret, "zebrafish details")
	open := w.page(sp, "Lunch menu", "", "zebrafish tacos")
	w.ok(w.owner, "comment.add", map[string]any{"note_id": secret, "body": "hush"})
	w.ok(w.owner, "relation.add", map[string]any{"from_id": open, "to_id": secret, "kind": "relates"})
	eq(t, len(idsOf(w.ok(bob, "note.list", map[string]any{"space_id": sp}))), 3)

	w.ok(w.owner, "note.restrict", map[string]any{"note_id": secret, "restriction": "view"})
	for _, id := range []string{secret, child} {
		w.errStatus(bob, "note.get", map[string]any{"id": id}, 404, "not_found")
		w.errStatus(bob, "comment.list", map[string]any{"note_id": id}, 404, "not_found")
		w.errStatus(bob, "note.history", map[string]any{"id": id}, 404, "not_found")
		w.errStatus(bob, "note.update", map[string]any{"id": id, "title": "x"}, 404, "not_found")
		w.errStatus(bob, "comment.add", map[string]any{"note_id": id, "body": "x"}, 404, "not_found")
	}
	eq(t, idsOf(w.ok(bob, "note.list", map[string]any{"space_id": sp})), []string{open})
	eq(t, len(idsOf(w.ok(bob, "note.list", map[string]any{"parent_id": secret}))), 0)
	eq(t, idsOf(w.ok(bob, "search.notes", map[string]any{"q": "zebrafish"})), []string{open})
	detail := w.ok(bob, "note.get", map[string]any{"id": open}).(map[string]any)
	eq(t, len(detail["relations"].([]any)), 0)
	w.errStatus(bob, "note.create", map[string]any{"space_id": sp, "title": "t", "parent_id": secret}, 404, "not_found")
	w.errStatus(bob, "note.update", map[string]any{"id": open, "parent_id": secret}, 404, "not_found")
	w.errStatus(bob, "note.permissions", map[string]any{"note_id": secret}, 404, "not_found")
	w.errStatus(bob, "note.share", map[string]any{"note_id": secret, "user_id": bob.userID, "level": "edit"}, 404, "not_found")
	eq(t, len(idsOf(w.ok(w.owner, "note.list", map[string]any{"space_id": sp}))), 3)
}

func TestSharingEditRestrictionAllowsReadingBlocksWriting(t *testing.T) {
	w := newSh(t)
	bob := w.member("member")
	sp := w.space("ER", "open")
	page := w.page(sp, "Policy", "", "rules")
	child := w.page(sp, "Policy FAQ", page, "faq")
	w.ok(w.owner, "note.restrict", map[string]any{"note_id": page, "restriction": "edit"})

	w.ok(bob, "note.get", map[string]any{"id": page})
	w.errStatus(bob, "note.update", map[string]any{"id": page, "title": "x"}, 403, "forbidden")
	w.errStatus(bob, "note.update", map[string]any{"id": child, "title": "x"}, 403, "forbidden")
	w.errStatus(bob, "note.delete", map[string]any{"id": child}, 403, "forbidden")
	w.ok(bob, "comment.add", map[string]any{"note_id": page, "body": "question"})
	w.errStatus(bob, "note.restrict", map[string]any{"note_id": page, "restriction": "none"}, 403, "forbidden")
	perms := w.ok(bob, "note.permissions", map[string]any{"note_id": child}).(map[string]any)
	eq(t, perms["can_manage"], false)
	eq(t, perms["inherited_from"].(map[string]any)["id"], page)

	w.ok(w.owner, "note.share", map[string]any{"note_id": page, "email": strings.ToUpper(bob.email), "level": "edit"})
	w.ok(bob, "note.update", map[string]any{"id": child, "title": "FAQ v2"})
}

func TestSharingIntoPrivateSpaceGrantsOnlyThatPageAndChildren(t *testing.T) {
	w := newSh(t)
	bob, guest, carol := w.member("member"), w.member("guest"), w.member("member")
	sp := w.space("PR", "private")
	shared := w.page(sp, "Roadmap", "", "quokka plan")
	kid := w.page(sp, "Roadmap Q3", shared, "quokka q3")
	other := w.page(sp, "Payroll", "", "quokka salaries")

	for _, p := range []who{bob, guest} {
		w.errStatus(p, "note.get", map[string]any{"id": shared}, 404, "not_found")
		eq(t, w.call(p, "note.shared_with_me", map[string]any{}).body, []byte("[]"))
	}
	w.ok(w.owner, "note.share", map[string]any{"note_id": shared, "user_id": bob.userID, "level": "view"})
	w.ok(w.owner, "note.share", map[string]any{"note_id": shared, "email": guest.email, "level": "view"})
	w.ok(w.owner, "note.share", map[string]any{"note_id": shared, "user_id": carol.userID, "level": "edit"})

	for _, p := range []who{bob, guest, carol} {
		w.ok(p, "note.get", map[string]any{"id": shared})
		w.ok(p, "note.get", map[string]any{"id": kid})
		w.errStatus(p, "note.get", map[string]any{"id": other}, 404, "not_found")
		eq(t, len(idsOf(w.ok(p, "note.list", map[string]any{"space_id": sp}))), 0)
		eq(t, idsOf(w.ok(p, "note.shared_with_me", map[string]any{})), []string{shared})
		eq(t, idsOf(w.ok(p, "note.list", map[string]any{"parent_id": shared})), []string{kid})
		eq(t, idsOf(w.ok(p, "search.notes", map[string]any{"q": "quokka"})), []string{shared})
		for _, s := range idsOf(w.ok(p, "space.list", map[string]any{})) {
			if s == sp {
				t.Fatal("private space listed")
			}
		}
		if r := w.call(p, "note.create", map[string]any{"space_id": sp, "title": "x"}); r.status == 200 {
			t.Fatal("create in private space succeeded")
		}
	}
	w.errStatus(bob, "note.update", map[string]any{"id": kid, "title": "x"}, 403, "forbidden")
	w.ok(carol, "note.update", map[string]any{"id": kid, "title": "edited by carol"})
	w.errStatus(carol, "note.update", map[string]any{"id": other, "title": "x"}, 404, "not_found")

	w.ok(w.owner, "note.unshare", map[string]any{"note_id": shared, "user_id": bob.userID})
	w.errStatus(bob, "note.get", map[string]any{"id": kid}, 404, "not_found")
	eq(t, w.call(bob, "note.shared_with_me", map[string]any{}).body, []byte("[]"))
}

func TestSharingRulesLockoutMembershipAndAdminBypass(t *testing.T) {
	w := newSh(t)
	alice, admin := w.member("member"), w.member("admin")
	sp := w.space("LK", "open")
	page := w.page(sp, "Plan", "", "alpaca")

	w.ok(alice, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	perms := w.ok(alice, "note.permissions", map[string]any{"note_id": page}).(map[string]any)
	eq(t, perms["restriction"], "view")
	eq(t, perms["people"].([]any)[0].(map[string]any)["level"], "edit")
	eq(t, perms["can_manage"], true)
	w.ok(w.owner, "note.get", map[string]any{"id": page})
	w.ok(admin, "note.update", map[string]any{"id": page, "title": "admin edit"})

	w.errStatus(alice, "note.unshare", map[string]any{"note_id": page, "user_id": alice.userID}, 409, "conflict")
	w.errStatus(alice, "note.share", map[string]any{"note_id": page, "user_id": alice.userID, "level": "view"}, 409, "conflict")
	w.ok(alice, "note.update", map[string]any{"id": page, "title": "still mine"}) // rolled back: still editor

	w.errStatus(alice, "note.share", map[string]any{"note_id": page, "email": "nobody@t.local", "level": "view"}, 400, "invalid")
	stranger := w.signup("S", false)
	w.errStatus(alice, "note.share", map[string]any{"note_id": page, "user_id": stranger.userID, "level": "view"}, 400, "invalid")
	w.errStatus(alice, "note.restrict", map[string]any{"note_id": page, "restriction": "bogus"}, 400, "invalid")

	w.ok(alice, "note.restrict", map[string]any{"note_id": page, "restriction": "none"})
	bob := w.member("member")
	w.ok(bob, "note.update", map[string]any{"id": page, "title": "open again"})
}

func TestSharingRawAccessFunction(t *testing.T) {
	w := newSh(t)
	bob := w.member("member")
	sp := w.space("AG", "open")
	page := w.page(sp, "Salary bands", "", "wombat")
	w.ok(w.owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})
	eq(t, len(w.ok(bob, "view.list", map[string]any{}).([]any)), 0)
	tx, err := dbTenant(w.env, w.owner.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var lvl string
	if err := tx.QueryRow(context.Background(), "select core.note_access($1::uuid, $2::uuid, false, array[$3]::uuid[])", page, bob.userID, sp).Scan(&lvl); err != nil || lvl != "none" {
		t.Fatalf("%v %s", err, lvl)
	}
	if err := tx.QueryRow(context.Background(), "select core.note_access($1::uuid, null, true, '{}')", page).Scan(&lvl); err != nil || lvl != "edit" {
		t.Fatalf("%v %s", err, lvl)
	}
}

func TestSharingGuestSharedAsEditorCanSave(t *testing.T) {
	w := newSh(t)
	guest := w.member("guest")
	for _, vis := range []string{"open", "private"} {
		key := "GO"
		if vis == "private" {
			key = "GP"
		}
		sp := w.space(key, vis)
		page := w.page(sp, "Brief", "", "draft")
		w.ok(w.owner, "note.share", map[string]any{"note_id": page, "email": guest.email, "level": "edit"})
		got := w.ok(guest, "note.get", map[string]any{"id": page}).(map[string]any)
		eq(t, got["level"], "edit")
		w.ok(guest, "note.update", map[string]any{"id": page, "title": "Brief v2", "content_md": "guest wrote this"})
		w.ok(guest, "comment.add", map[string]any{"note_id": page, "body": "looks good"})
		other := w.page(sp, "Other", "", "x")
		w.errStatus(guest, "note.update", map[string]any{"id": other, "title": "x"}, 404, "not_found")
		w.ok(w.owner, "note.share", map[string]any{"note_id": other, "email": guest.email, "level": "view"})
		w.errStatus(guest, "note.update", map[string]any{"id": other, "title": "x"}, 403, "forbidden")
	}
}

// Idempotent replay and gates are applied by the command layer; check them for a sharing command anyway.
func TestSharingGatesAndEvents(t *testing.T) {
	w := newSh(t)
	guest, bob := w.member("guest"), w.member("member")
	sp := w.space("GT", "open")
	page := w.page(sp, "P", "", "x")
	w.errStatus(guest, "note.restrict", map[string]any{"note_id": page, "restriction": "view"}, 403, "forbidden")
	w.errStatus(who{}, "note.permissions", map[string]any{"note_id": page}, 401, "unauthenticated")
	w.errStatus(w.signup("X", true), "note.permissions", map[string]any{"note_id": page}, 403, "forbidden")
	key := map[string]string{"Idempotency-Key": rnd()}
	a := w.cmd(w.gosv.URL, w.owner, w.owner.ws, "note.share", map[string]any{"note_id": page, "user_id": bob.userID, "level": "view"}, key)
	b := w.cmd(w.gosv.URL, w.owner, w.owner.ws, "note.share", map[string]any{"note_id": page, "user_id": bob.userID, "level": "view"}, key)
	if a.status != 200 || string(a.body) != string(b.body) {
		t.Fatalf("%s / %s", a.body, b.body)
	}
	w.ok(w.owner, "note.unshare", map[string]any{"note_id": page, "user_id": bob.userID})
	n := w.dbStrings(w.owner.ws, "select kind from core.events where note_id = $1::uuid and kind in ('note.shared','note.unshared') order by id", page)
	eq(t, n, []string{"note.shared", "note.unshared"}) // exactly one note.shared despite the replay
}

// dbStrings runs a one-column query inside the tenant (RLS applies) and returns the values as text.
func (e *env) dbStrings(ws, sql string, args ...any) []string {
	e.t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(context.Background(), sql, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}
