package server_test

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cnote/api/internal/unsub"
)

// Docs parity B1, step 0 (docs-parity-b 13.2.2 and 13.2.5): the archived-space write gate on every route, and the
// one-click unsubscribe endpoint. Real Postgres and the real Go server.

const archivedMsg = "This space is archived"

func (d dg) archive(space string, on bool) {
	d.t.Helper()
	q := "update core.spaces set archived_at = now() where id = $1::uuid"
	if !on {
		q = "update core.spaces set archived_at = null where id = $1::uuid"
	}
	d.sql(q, space)
}

func (d dg) archivedRefusal(w who, cmd string, in any) {
	d.t.Helper()
	if code, msg := d.errOf(w, cmd, in); code != "forbidden" || msg != archivedMsg {
		d.t.Fatalf("%s %v: want forbidden %q, got %s %q", cmd, in, archivedMsg, code, msg)
	}
}

func TestArchivedSpaceRefusesWritesOnEveryRoute(t *testing.T) {
	d, owner := newDG(t)
	editor := d.member(owner)
	open := d.space(owner, "OPN", "open")
	page := d.page(owner, open, "Runbook", nil, "restart")
	other := d.page(owner, open, "Other", nil, "x")
	comment := d.ok(owner, "comment.add", map[string]any{"note_id": page, "body": "first"})["id"].(string)
	if r := d.run(editor, "note.update", map[string]any{"id": page, "title": "Runbook v2"}); r.status != 200 {
		t.Fatalf("an editor writes before the archive: %d %s", r.status, r.body)
	}

	gone := d.page(owner, open, "Gone", nil, "x")
	deletion := d.ok(owner, "note.delete", map[string]any{"id": gone})["deletion_id"].(string)

	d.archive(open, true)

	// Workspace owner (admin) and a plain member: the same refusal, because archived is read-only for everyone.
	for name, w := range map[string]who{"owner": owner, "member": editor} {
		t.Run(name, func(t *testing.T) {
			d.archivedRefusal(w, "note.update", map[string]any{"id": page, "title": "x"})
			d.archivedRefusal(w, "note.create", map[string]any{"space_id": open, "title": "new"})
			// Subtree commands answer with the generic refusal of 7.9 whatever the cause, so it names nothing.
			for c, in := range map[string]map[string]any{
				"note.delete": {"id": other}, "note.duplicate": {"id": other},
				"note.move": {"id": other, "parent_id": page}, "note.reorder": {"id": other, "before_id": nil},
			} {
				if code, msg := d.errOf(w, c, in); code != "forbidden" || (msg != archivedMsg && msg != restricted) {
					t.Fatalf("%s: %s %q", c, code, msg)
				}
			}
			d.archivedRefusal(w, "label.set", map[string]any{"note_id": page, "labels": []string{"a"}})
			d.archivedRefusal(w, "comment.add", map[string]any{"note_id": page, "body": "second"})
			d.archivedRefusal(w, "comment.resolve", map[string]any{"id": comment})
			// Tables caps the caller's role to read in an archived space, so its own message comes back.
			if code, _ := d.errOf(w, "base.create", map[string]any{"space_id": open, "name": "Orders"}); code != "forbidden" {
				t.Fatalf("base.create in an archived space: %s", code)
			}
		})
	}
	// A file, and a Projects item (a note in the same space).
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))
	d.archivedRefusal(owner, "file.upload", map[string]any{"note_id": page, "filename": "a.png", "data_base64": png})
	d.archivedRefusal(editor, "file.upload", map[string]any{"note_id": page, "filename": "a.png", "data_base64": png})
	d.archivedRefusal(owner, "item.create", map[string]any{"space_id": open, "title": "Ship it"})
	d.archivedRefusal(owner, "note.share", map[string]any{"note_id": page, "user_id": editor.userID, "level": "view"})
	d.archivedRefusal(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})

	// A trashed page stays in the trash: the restore is refused (it answers not found, as for anyone who may not edit it).
	if r := d.run(owner, "note.undelete", map[string]any{"deletion_id": deletion}); r.status == 200 {
		t.Fatalf("undelete in an archived space: %s", r.body)
	}
	if d.scalar("select (deleted_at is not null)::text from core.notes where id = $1::uuid", gone) != "true" {
		t.Fatal("the page came back")
	}

	// Reading is untouched.
	d.ok(editor, "note.get", map[string]any{"id": page})
	d.list(editor, "comment.list", map[string]any{"note_id": page})
	if n := len(d.list(editor, "search.notes", map[string]any{"q": "runbook"})); n != 1 {
		t.Fatalf("an archived space stays searchable, got %d hits", n)
	}

	d.archive(open, false)
	if r := d.run(editor, "note.update", map[string]any{"id": page, "title": "again"}); r.status != 200 {
		t.Fatalf("writes come back with the space: %d %s", r.status, r.body)
	}
}

// A page shared straight to someone who cannot read its space never goes through the space rule, so the gate must not
// depend on it.
func TestArchivedSpaceRefusesWritesThroughADirectShare(t *testing.T) {
	d, owner := newDG(t)
	sharee := d.member(owner)
	private := d.space(owner, "PRV", "private")
	page := d.page(owner, private, "Plan", nil, "x")
	d.ok(owner, "note.share", map[string]any{"note_id": page, "user_id": sharee.userID, "level": "edit"})
	if r := d.run(sharee, "note.update", map[string]any{"id": page, "title": "by share"}); r.status != 200 {
		t.Fatalf("the sharee edits: %d %s", r.status, r.body)
	}
	d.archive(private, true)
	d.archivedRefusal(sharee, "note.update", map[string]any{"id": page, "title": "after"})
	d.archivedRefusal(sharee, "comment.add", map[string]any{"note_id": page, "body": "late"})
	d.ok(sharee, "note.get", map[string]any{"id": page})
}

func (e *env) unsubReq(method, token string, form url.Values) (int, string) {
	e.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, e.gosv.URL+"/api/public/unsubscribe/"+token, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if res.StatusCode != 405 && (res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Referrer-Policy") != "no-referrer") {
		e.t.Fatalf("headers: %v", res.Header)
	}
	return res.StatusCode, b.String()
}

func TestOneClickUnsubscribeNeedsNoLoginAndChangesOnlyOnPost(t *testing.T) {
	t.Setenv("CNOTE_MASTER_KEY", "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=")
	d, owner := newDG(t)
	flags := func() string {
		return d.scalar(`select coalesce((select direct_off::text || ',' || digest_off::text from core.notify_prefs where user_id = $1::uuid), 'none')`, owner.userID)
	}
	direct, err := unsub.Token(owner.ws, owner.userID, unsub.Direct)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := unsub.Token(owner.ws, owner.userID, unsub.Digest)

	// A mail scanner following the link changes nothing.
	if st, body := d.e.unsubReq("GET", direct, nil); st != 200 || !strings.Contains(body, "<form") || flags() != "none" {
		t.Fatalf("GET must only show a button: %d %q flags=%s", st, body, flags())
	}
	// The mail client's one-click POST (RFC 8058) switches direct mail off.
	if st, _ := d.e.unsubReq("POST", direct, url.Values{"List-Unsubscribe": {"One-Click"}}); st != 200 || flags() != "true,false" {
		t.Fatalf("direct POST: %d flags=%s", st, flags())
	}
	// The digest is its own kind.
	if st, _ := d.e.unsubReq("POST", digest, nil); st != 200 || flags() != "true,true" {
		t.Fatalf("digest POST: %d flags=%s", st, flags())
	}
	// Not valid: edited, truncated, and a person who is not a member of that workspace.
	for _, tok := range []string{direct[:len(direct)-3] + "AAA", direct[:10], "x"} {
		if st, _ := d.e.unsubReq("POST", tok, nil); st != 404 {
			t.Fatalf("a bad token must 404, got %d", st)
		}
	}
	stranger := d.e.signup("Out", true)
	tok, _ := unsub.Token(owner.ws, stranger.userID, unsub.Direct)
	if st, _ := d.e.unsubReq("POST", tok, nil); st != 200 {
		t.Fatalf("a valid token for a non-member answers normally: %d", st)
	}
	if n := d.scalar("select count(*)::text from core.notify_prefs where user_id = $1::uuid", stranger.userID); n != "0" {
		t.Fatal("a non-member gets no preference row")
	}
	if st, _ := d.e.unsubReq("DELETE", direct, nil); st != 405 {
		t.Fatalf("DELETE: %d", st)
	}
}
