package server_test

import (
	"testing"
	"time"
)

// Docs parity B1, P4 (docs-parity-b 4.4, 13.2.6): the task index and task.mine, through the real server and the background
// worker. The rebuild itself (retries, races, notifications) is tested in worker/pagetasks_test.go.

func taskMD(ws string, w who, text, due string) string {
	return "- [ ] " + text + " " + mention(ws, w.userID, "A") + ` <time datetime="` + due + `">d</time>` + "\n"
}

// mineEventually waits for the worker to index, then returns task.mine of w.
func (d dg) mine(w who, in map[string]any, want int) []map[string]any {
	d.t.Helper()
	var got []map[string]any
	for range 100 {
		settle(d.t, d.e, d.ws)
		if got = d.list(w, "task.mine", in); len(got) == want {
			return got
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.t.Fatalf("task.mine: want %d rows, got %v", want, got)
	return nil
}

func TestTaskMineListsOnlyWhatThePersonCanRead(t *testing.T) {
	d, owner := newDG(t)
	a, guest := d.member(owner), d.e.addMember(owner, d.e.signup("Gst", false), "guest")
	sp := d.space(owner, "OPN", "open")
	soon := d.page(owner, sp, "Soon", nil, taskMD(owner.ws, a, "Visible later", "2026-12-20"))
	secret := d.page(owner, sp, "Secret", nil, taskMD(owner.ws, a, "Restricted first", "2026-01-01"))
	d.page(owner, sp, "Nobody", nil, "- [ ] nobody's task\n")

	rows := d.mine(a, map[string]any{}, 2)
	if rows[0]["text"] != "Restricted first" || rows[0]["note_id"] != secret || rows[1]["note_id"] != soon {
		t.Fatalf("overdue first, then by date: %v", rows)
	}
	if rows[0]["due"] != "2026-01-01" || rows[0]["done"] != false || rows[0]["note_title"] != "Secret" || rows[0]["ord"].(float64) != 0 {
		t.Fatalf("row shape: %v", rows[0])
	}
	if got := d.list(owner, "task.mine", map[string]any{}); len(got) != 0 {
		t.Fatalf("task.mine is the caller's own: %v", got)
	}

	// Restricting the page hides the task before the limit applies: limit 1 must still return the readable one.
	d.ok(owner, "note.restrict", map[string]any{"note_id": secret, "restriction": "view"})
	got := d.list(a, "task.mine", map[string]any{"limit": 1})
	if len(got) != 1 || got[0]["note_id"] != soon {
		t.Fatalf("a restricted page's task never shows, and the limit counts only visible rows: %v", got)
	}
	// A guest assigned a task on a page not shared with them sees nothing; shared, they see it.
	d.ok(owner, "note.update", map[string]any{"id": soon, "content_md": taskMD(owner.ws, guest, "Guest job", "2026-12-21"), "schema": 2, "expected_epoch": 1})
	d.mine(owner, map[string]any{}, 0)
	for range 50 {
		settle(t, d.e, d.ws)
		if d.scalar("select count(*)::text from core.note_tasks where assignee_id = $1::uuid", guest.userID) == "1" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := d.list(guest, "task.mine", map[string]any{}); len(got) != 0 {
		t.Fatalf("a guest sees only pages shared with them: %v", got)
	}
	d.ok(owner, "note.share", map[string]any{"note_id": soon, "email": guest.email, "level": "view"})
	if got := d.list(guest, "task.mine", map[string]any{}); len(got) != 1 {
		t.Fatalf("shared with the guest: %v", got)
	}
	// In the trash: gone.
	d.ok(owner, "note.delete", map[string]any{"id": soon})
	if got := d.list(guest, "task.mine", map[string]any{}); len(got) != 0 {
		t.Fatalf("a deleted page's tasks are not listed: %v", got)
	}
}

func TestAssignmentReachesTheBellOnce(t *testing.T) {
	d, owner := newDG(t)
	a := d.member(owner)
	sp := d.space(owner, "OPN", "open")
	page := d.page(owner, sp, "Plan", nil, "x")
	d.ok(owner, "note.update", map[string]any{"id": page, "content_md": taskMD(owner.ws, a, "Do it", "2026-12-20"), "schema": 2, "expected_epoch": 1})
	d.mine(a, map[string]any{}, 1)
	settle(t, d.e, d.ws)
	// note.update's own mention path told them in the same request; the task index added nothing on top.
	if n := len(inbox(d.gw, a, "mention")) + len(inbox(d.gw, a, "assigned")); n != 1 {
		t.Fatalf("one edit, one notification, got %d", n)
	}
	d.ok(owner, "note.update", map[string]any{"id": page, "content_md": taskMD(owner.ws, a, "Do it", "2026-12-25") + "more\n", "schema": 2, "expected_epoch": 1})
	settle(t, d.e, d.ws)
	if n := len(inbox(d.gw, a, "mention")) + len(inbox(d.gw, a, "assigned")); n != 1 {
		t.Fatalf("a changed date and more text is not a new assignment, got %d", n)
	}
}
