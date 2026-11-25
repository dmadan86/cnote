package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
)

// docs-parity-b 13.2.10: a mention typed while co-editing notified nobody, because the gateway saves the page and emits
// note.updated but never runs note.update. These tests play both writers against one page and count notifications.

func mentionLink(ws, user string) string { return "[@Mo](/w/" + ws + "/people/" + user + ")" }

func (s *goSide) mentionCount(note, user string) int {
	s.e.t.Helper()
	var n int
	if err := s.c.Tx.QueryRow(s.c.Context, "select count(*) from core.notifications where note_id = $1::uuid and user_id = $2::uuid and kind = 'mention'", note, user).Scan(&n); err != nil {
		s.e.t.Fatal(err)
	}
	return n
}

// gatewaySave is what apps/collab does on a debounce: write the snapshot, bump the version, emit note.updated as the editor.
func (s *goSide) gatewaySave(note, md string) {
	s.e.t.Helper()
	var v int
	if err := s.c.Tx.QueryRow(s.c.Context, "update core.notes set content_md = $2, version = version + 1, updated_at = now() where id = $1::uuid returning version", note, md).Scan(&v); err != nil {
		s.e.t.Fatal(err)
	}
	if _, err := s.c.Emit("note.updated", &note, map[string]any{"changed": []string{"content"}, "version": v}); err != nil {
		s.e.t.Fatal(err)
	}
}

func mentionFixture(t *testing.T) (*env, who, who, *goSide, string) {
	e := newEnv(t)
	tess, mo := e.signup("Tess"), e.signup("Mo")
	e.apiCmd(tess, "member.add", map[string]any{"email": mo.email, "role": "member"})
	e.settle(tess.ws)
	g := e.newGoSide(tess)
	sp := str(g.cmd("space.create", map[string]any{"key": "DOC", "name": "Docs", "visibility": "open"}), "id")
	note := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "Plan", "content_md": "hi"}), "id")
	return e, tess, mo, g, note
}

func TestGatewayMentionNotifiesOnceAndOnlyWhenTypedAgain(t *testing.T) {
	_, tess, mo, g, note := mentionFixture(t)
	m := mentionLink(tess.ws, mo.userID)

	g.gatewaySave(note, "hi "+m)
	g.drain(100)
	if n := g.mentionCount(note, mo.userID); n != 1 {
		t.Fatalf("a mention typed in a co-edited page must notify once, got %d", n)
	}
	var actor string
	_ = g.c.Tx.QueryRow(g.c.Context, "select actor_user_id::text from core.notifications where note_id = $1::uuid and kind = 'mention'", note).Scan(&actor)
	if actor != tess.userID {
		t.Fatalf("the editor is the actor, got %q", actor)
	}

	// A retried or duplicated event for a version already handled does nothing.
	g.c.Principal = command.Principal{Kind: command.KindUser, UserID: &tess.userID, WorkspaceID: tess.ws, Role: command.Owner}
	ev := jobs.Event{NoteID: &note, Payload: json.RawMessage(`{"changed":["content"]}`)}
	for range 3 {
		if err := mentionsOnNoteUpdated(g.c, ev); err != nil {
			t.Fatal(err)
		}
	}
	// More typing around the same mention is not a new mention.
	g.gatewaySave(note, "hi "+m+" and more")
	g.drain(100)
	if n := g.mentionCount(note, mo.userID); n != 1 {
		t.Fatalf("an edit next to a mention must not notify again, got %d", n)
	}
	// Taking it out and typing it again is a new mention, as before.
	g.gatewaySave(note, "hi")
	g.drain(100)
	g.gatewaySave(note, "hi "+m)
	g.drain(100)
	if n := g.mentionCount(note, mo.userID); n != 2 {
		t.Fatalf("a mention typed again notifies again, got %d", n)
	}
}

func TestApiAndGatewayPathsShareOneNotification(t *testing.T) {
	_, tess, mo, g, note := mentionFixture(t)
	m := mentionLink(tess.ws, mo.userID)

	// note.update notifies in its own transaction; the note.updated event it emits then reaches the worker handler.
	g.cmd("note.update", map[string]any{"id": note, "schema": 2, "expected_epoch": 1, "content_md": "hi " + m})
	if n := g.mentionCount(note, mo.userID); n != 1 {
		t.Fatalf("note.update must notify, got %d", n)
	}
	g.drain(100)
	if n := g.mentionCount(note, mo.userID); n != 1 {
		t.Fatalf("the worker must not notify again for the same version, got %d", n)
	}
	// A gateway save that adds a second person: only that person is new.
	ravi := e2(t, g, tess)
	g.gatewaySave(note, "hi "+m+" "+mentionLink(tess.ws, ravi))
	g.drain(100)
	if g.mentionCount(note, mo.userID) != 1 || g.mentionCount(note, ravi) != 1 {
		t.Fatal("each person is notified once across both paths")
	}
}

// e2 adds a second member to the workspace and returns their user id.
func e2(t *testing.T, g *goSide, owner who) string {
	t.Helper()
	p := g.e.signup("Ravi")
	g.e.apiCmd(owner, "member.add", map[string]any{"email": p.email, "role": "member"})
	return p.userID
}

func TestMentionOfSomeoneWhoCannotReadIsSkipped(t *testing.T) {
	e, tess, _, g, note := mentionFixture(t)
	outsider := e.signup("Out") // a member of a different workspace
	g.gatewaySave(note, "hi "+mentionLink(tess.ws, outsider.userID))
	g.drain(100)
	if n := g.mentionCount(note, outsider.userID); n != 0 {
		t.Fatalf("a person outside the workspace is never notified, got %d", n)
	}
}

func TestMentionSeedSweepMarksOldMentionsAsHandled(t *testing.T) {
	e, tess, mo, g, note := mentionFixture(t)
	g.drain(100) // the page's own note.created is handled first: the seed leaves a page with a pending event alone
	m := mentionLink(tess.ws, mo.userID)
	// A page that predates the store: body with a mention, no state row.
	if _, err := g.c.Tx.Exec(g.c.Context, "update core.notes set content_md = $2 where id = $1::uuid", note, "old "+m); err != nil {
		t.Fatal(err)
	}
	if _, err := g.c.Tx.Exec(g.c.Context, "delete from core.note_mention_state where note_id = $1::uuid", note); err != nil {
		t.Fatal(err)
	}
	_ = e
	n, err := mentionSeedRun(context.Background(), g.c.Tx, 500)
	if err != nil || n < 1 {
		t.Fatalf("seed: %d %v", n, err)
	}
	g.gatewaySave(note, "old "+m+" and a later edit")
	g.drain(100)
	if c := g.mentionCount(note, mo.userID); c != 0 {
		t.Fatalf("a mention that was there before the store is not new, got %d", c)
	}
}
