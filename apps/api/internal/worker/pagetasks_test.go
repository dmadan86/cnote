package worker

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules/notes"
)

// docs-parity-b 4.4 and 13.2.6: the task index and the assignment notification, for the API path and the gateway path.

func taskLine(ws, user, text, due string) string {
	s := "- [ ] " + text
	if user != "" {
		s += " " + mentionLink(ws, user)
	}
	if due != "" {
		s += ` <time datetime="` + due + `">` + due + `</time>`
	}
	return s
}

func (s *goSide) assignedCount(note, user string) int {
	s.e.t.Helper()
	var n int
	if err := s.c.Tx.QueryRow(s.c.Context, "select count(*) from core.notifications where note_id = $1::uuid and user_id = $2::uuid and kind = 'assigned'", note, user).Scan(&n); err != nil {
		s.e.t.Fatal(err)
	}
	return n
}

func (s *goSide) taskRows(note string) (n int, version int) {
	s.e.t.Helper()
	if err := s.c.Tx.QueryRow(s.c.Context, "select count(*) from core.note_tasks where note_id = $1::uuid", note).Scan(&n); err != nil {
		s.e.t.Fatal(err)
	}
	_ = s.c.Tx.QueryRow(s.c.Context, "select coalesce((select indexed_version from core.note_task_state where note_id = $1::uuid), 0)", note).Scan(&version)
	return
}

func TestGatewayAssignmentNotifiesOnceAndDoesNotDoubleTheMention(t *testing.T) {
	_, tess, mo, g, note := mentionFixture(t)
	g.drain(100) // the page was created: its (empty) index exists

	g.gatewaySave(note, taskLine(tess.ws, mo.userID, "Send the GST summary", "2026-12-01"))
	g.drain(100)
	if n, _ := g.taskRows(note); n != 1 {
		t.Fatalf("a co-edited page must be indexed, got %d rows", n)
	}
	// The same edit mentioned Mo, so the mention path told them; the assignment must not tell them a second time.
	if g.mentionCount(note, mo.userID) != 1 || g.assignedCount(note, mo.userID) != 0 {
		t.Fatalf("one edit, one notification: mention %d assigned %d", g.mentionCount(note, mo.userID), g.assignedCount(note, mo.userID))
	}
}

func TestAssignmentOnAnAlreadyMentionedPersonNotifiesOnceAndOnlyForNewTasks(t *testing.T) {
	_, tess, mo, g, note := mentionFixture(t)
	g.drain(100)
	m := mentionLink(tess.ws, mo.userID)
	g.gatewaySave(note, "cc "+m) // mentioned in plain text first
	g.drain(100)
	g.gatewaySave(note, "cc "+m+"\n"+taskLine(tess.ws, mo.userID, "Book the venue", ""))
	g.drain(100)
	if g.assignedCount(note, mo.userID) != 1 {
		t.Fatalf("a new task for someone already mentioned is an assignment, got %d", g.assignedCount(note, mo.userID))
	}
	// Edits elsewhere on the page, and moving the task, do not announce it again.
	g.gatewaySave(note, "cc "+m+" and more words\n"+taskLine(tess.ws, mo.userID, "Book the venue", "2026-12-05"))
	g.drain(100)
	g.gatewaySave(note, taskLine(tess.ws, mo.userID, "Book the venue", "2026-12-05")+"\nintro")
	g.drain(100)
	if g.assignedCount(note, mo.userID) != 1 {
		t.Fatalf("editing elsewhere must not notify again, got %d", g.assignedCount(note, mo.userID))
	}
	// A different task text is a different assignment.
	g.gatewaySave(note, taskLine(tess.ws, mo.userID, "Book the venue and caterer", ""))
	g.drain(100)
	if g.assignedCount(note, mo.userID) != 2 {
		t.Fatalf("a new task text is a new assignment, got %d", g.assignedCount(note, mo.userID))
	}
	// Whitespace and case are not a new text.
	g.gatewaySave(note, taskLine(tess.ws, mo.userID, "book  the venue AND caterer", ""))
	g.drain(100)
	if g.assignedCount(note, mo.userID) != 2 {
		t.Fatalf("normalised text is the same assignment, got %d", g.assignedCount(note, mo.userID))
	}
}

func TestSelfAssignmentAndStrangersAreNotNotified(t *testing.T) {
	e, tess, mo, g, note := mentionFixture(t)
	g.drain(100)
	outsider := e.signup("Out")
	g.gatewaySave(note, taskLine(tess.ws, tess.userID, "Mine", "")+"\n"+taskLine(tess.ws, outsider.userID, "Theirs", ""))
	g.drain(100)
	if g.assignedCount(note, tess.userID) != 0 || g.assignedCount(note, outsider.userID) != 0 {
		t.Fatal("the editor and a non-member are never notified")
	}
	var assigned *string
	if err := g.c.Tx.QueryRow(g.c.Context, "select assignee_id::text from core.note_tasks where note_id = $1::uuid and text = 'Theirs'", note).Scan(&assigned); err != nil || assigned != nil {
		t.Fatalf("an assignee outside the workspace is stored as null: %v %v", assigned, err)
	}
	_ = mo
}

func TestRetriedAndReorderedEventsApplyTheNewestVersionOnce(t *testing.T) {
	_, tess, mo, g, note := mentionFixture(t)
	g.drain(100)
	// Two saves before the worker looks: the first event already finds the second version's body.
	g.gatewaySave(note, taskLine(tess.ws, mo.userID, "First draft", ""))
	g.gatewaySave(note, taskLine(tess.ws, mo.userID, "Final text", "2026-12-02"))
	var v int
	_ = g.c.Tx.QueryRow(g.c.Context, "select version from core.notes where id = $1::uuid", note).Scan(&v)
	g.drain(100)
	n, got := g.taskRows(note)
	if n != 1 || got != v {
		t.Fatalf("index at the latest version %d, got %d rows at %d", v, n, got)
	}
	if told := g.assignedCount(note, mo.userID) + g.mentionCount(note, mo.userID); told != 1 {
		t.Fatalf("one notification for the text that survived (mention path or assignment), got %d", told)
	}
	// Retried and stale events change nothing.
	g.c.Principal = command.Principal{Kind: command.KindUser, UserID: &tess.userID, WorkspaceID: tess.ws, Role: command.Owner}
	for _, p := range []string{`{"changed":["content"],"version":2}`, `{"changed":["content"],"version":%d}`, `{"changed":["content"],"version":1}`} {
		ev := jobs.Event{WorkspaceID: tess.ws, Kind: "note.updated", NoteID: &note, Payload: json.RawMessage(p)}
		for range 2 {
			if err := tasksOnNoteEvent(g.c, ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n2, v2 := g.taskRows(note); n2 != 1 || v2 != v || g.assignedCount(note, mo.userID)+g.mentionCount(note, mo.userID) != 1 {
		t.Fatalf("retries changed state: rows %d version %d", n2, v2)
	}
	// Removing the task removes its row.
	g.gatewaySave(note, "nothing left")
	g.drain(100)
	if n, _ := g.taskRows(note); n != 0 {
		t.Fatalf("a removed task leaves the index, got %d", n)
	}
}

func TestBackfillIsSilentAndRacesLiveEditsSafely(t *testing.T) {
	e, tess, mo, g, note := mentionFixture(t)
	g.drain(100)
	// A page that predates the index: a task, no index state.
	g.gatewaySave(note, taskLine(tess.ws, mo.userID, "Old task", "2026-11-01"))
	g.drain(100) // its event is handled (a backfill never runs ahead of a pending event, see TestBackfillLeavesAPendingEventAlone)
	told := g.assignedCount(note, mo.userID)
	if _, err := g.c.Tx.Exec(g.c.Context, "delete from core.note_task_state where note_id = $1::uuid", note); err != nil {
		t.Fatal(err)
	}
	g.c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: tess.ws, Role: command.Owner}
	if n, err := TaskBackfillRun(context.Background(), g.c.Tx, tess.ws, 500); err != nil || n < 1 {
		t.Fatalf("backfill: %d %v", n, err)
	}
	if n, _ := g.taskRows(note); n != 1 {
		t.Fatalf("backfill indexes the page, got %d", n)
	}
	g.drain(100)
	if g.assignedCount(note, mo.userID) != told {
		t.Fatalf("a task that already existed is not announced again, got %d", g.assignedCount(note, mo.userID))
	}
	g.gatewaySave(note, taskLine(tess.ws, mo.userID, "Old task", "2026-11-01")+"\n"+taskLine(tess.ws, mo.userID, "New task", ""))
	g.drain(100)
	if g.assignedCount(note, mo.userID) != told+1 {
		t.Fatalf("only the task added after the backfill is announced, got %d", g.assignedCount(note, mo.userID))
	}
	_ = e
}

// Committed rows and separate connections: a backfill and live rebuilds of the same page at the same time.
func TestConcurrentRebuildsOfOnePageNotifyOnce(t *testing.T) {
	e := newEnv(t)
	tess, mo := e.signup("Tess"), e.signup("Mo")
	e.apiCmd(tess, "member.add", map[string]any{"email": mo.email, "role": "member"})
	e.settle(tess.ws)
	sp := str(e.apiCmd(tess, "space.create", map[string]any{"key": "DOC", "name": "Docs", "visibility": "open"}), "id")
	note := str(e.apiCmd(tess, "note.create", map[string]any{"space_id": sp, "title": "Plan", "content_md": "hi"}), "id")
	e.settle(tess.ws)

	tenant := func(f func(tx pgx.Tx) error) error {
		tx, err := db.BeginTenant(context.Background(), e.pool, tess.ws)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if err := f(tx); err != nil {
			return err
		}
		return tx.Commit(context.Background())
	}
	// A new body at version + 1 with no event: only the rebuilds below look at it.
	if err := tenant(func(x pgx.Tx) error {
		_, err := x.Exec(context.Background(), "update core.notes set content_md = $2, version = version + 1 where id = $1::uuid", note, taskLine(tess.ws, mo.userID, "Race me", ""))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- tenant(func(x pgx.Tx) error {
				if i%3 == 0 {
					_, err := TaskBackfillRun(context.Background(), x, tess.ws, 500)
					return err
				}
				return notes.IndexTasks(context.Background(), x, tess.ws, note, &tess.userID, false)
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var rows, told int
	if err := tenant(func(x pgx.Tx) error {
		if err := x.QueryRow(context.Background(), "select count(*) from core.note_tasks where note_id = $1::uuid", note).Scan(&rows); err != nil {
			return err
		}
		return x.QueryRow(context.Background(), "select count(*) from core.notifications where note_id = $1::uuid and kind = 'assigned' and user_id = $2::uuid", note, mo.userID).Scan(&told)
	}); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || told != 1 {
		t.Fatalf("racing rebuilds: %d rows, %d notifications, want 1 and 1", rows, told)
	}
}
