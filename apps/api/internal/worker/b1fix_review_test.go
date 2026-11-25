package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/notes"
)

// Review fixes for docs Phase B1 (PR 23): mail after a revocation, seed and backfill racing events, reordered mention events,
// the digest per-run limit, at-most-once delivery.

// ---- committed fixture: real connections, so two transactions can race ----

type b1fix struct {
	e        *env
	smtp     *fakeSMTP
	tess, mo who
	note     string
}

func b1fixSetup(t *testing.T) b1fix {
	smtp := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+smtp.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "C Note <noreply@example.test>")
	t.Setenv("PUBLIC_WEB_URL", "https://app.example.test/")
	t.Setenv("CNOTE_MASTER_KEY", "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=")
	t.Setenv("MAIL_OUTBOX_DELAY", "1h") // the background worker must not run jobs these tests queue
	e := newEnv(t)
	tess, mo := e.signup("Tess"), e.signup("Mo")
	e.apiCmd(tess, "member.add", map[string]any{"email": mo.email, "role": "member"})
	e.settle(tess.ws)
	sp := str(e.apiCmd(tess, "space.create", map[string]any{"key": "DOC", "name": "Docs", "visibility": "open"}), "id")
	note := str(e.apiCmd(tess, "note.create", map[string]any{"space_id": sp, "title": "Plan", "content_md": "hi"}), "id")
	e.settle(tess.ws)
	t.Cleanup(func() { mailHook = nil; smtpTimeoutNS.Store(int64(20 * time.Second)) })
	return b1fix{e, smtp, tess, mo, note}
}

func (f b1fix) tenant(t *testing.T, fn func(tx pgx.Tx) error) error {
	t.Helper()
	tx, err := db.BeginTenant(context.Background(), f.e.pool, f.tess.ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(context.Background())
}

func (f b1fix) exec(t *testing.T, q string, a ...any) {
	t.Helper()
	if err := f.tenant(t, func(tx pgx.Tx) error { _, err := tx.Exec(context.Background(), q, a...); return err }); err != nil {
		t.Fatal(err)
	}
}

// queued adds a committed, queued mention notification for Mo and returns the job payload that references it.
func (f b1fix) queued(t *testing.T) (notif string, payload json.RawMessage) {
	t.Helper()
	if err := f.tenant(t, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title, emailed_at, email_state)
			 values ($1::uuid,$2::uuid,'mention',$3::uuid,$4::uuid,'Plan', now(), 'queued') returning id::text`, f.tess.ws, f.mo.userID, f.note, f.tess.userID).Scan(&notif)
	}); err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(notifyMailRef{UserID: f.mo.userID, Mode: ModeDirect, Notifications: []string{notif}, MessageID: "<" + rnd() + "@cnote>"})
	return
}

// run is one execution of the mail job in its own transaction, committed when the job returns nil.
func (f b1fix) run(t *testing.T, payload json.RawMessage) error {
	t.Helper()
	tx, err := db.BeginTenant(context.Background(), f.e.pool, f.tess.ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	c := &command.Ctx{Context: context.Background(), Tx: tx, Pool: f.e.pool, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: f.tess.ws, Role: command.Owner}}
	f.e.reg.Bind(c)
	if err := notifyMailJob(c, payload); err != nil {
		return err
	}
	return tx.Commit(context.Background())
}

func (f b1fix) mails() int {
	f.smtp.mu.Lock()
	defer f.smtp.mu.Unlock()
	n := 0
	for _, m := range f.smtp.msgs {
		if m.to == f.mo.email {
			n++
		}
	}
	return n
}

func (f b1fix) state(t *testing.T, notif string) (s string) {
	t.Helper()
	_ = f.tenant(t, func(tx pgx.Tx) error {
		var p *string
		err := tx.QueryRow(context.Background(), "select email_state from core.notifications where id = $1::uuid", notif).Scan(&p)
		if p != nil {
			s = *p
		}
		return err
	})
	return
}

// raceWhileSending starts the job, stops it right before SMTP, runs `change` in a second transaction and checks that the change
// waits for the job (it must not complete while the mail is in flight) and completes afterwards. It returns the job's error.
func (f b1fix) raceWhileSending(t *testing.T, payload json.RawMessage, change func(tx pgx.Tx) error) {
	t.Helper()
	atSend, release := make(chan struct{}), make(chan struct{})
	mailHook = func(stage string) error {
		if stage == "send" {
			close(atSend)
			<-release
		}
		return nil
	}
	jobDone, changeDone := make(chan error, 1), make(chan error, 1)
	go func() { jobDone <- f.run(t, payload) }()
	select {
	case <-atSend:
	case err := <-jobDone:
		t.Fatalf("the job ended before SMTP: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("the job never reached SMTP")
	}
	go func() { changeDone <- f.tenant(t, change) }()
	select {
	case err := <-changeDone:
		close(release)
		<-jobDone
		t.Fatalf("a removal or unsubscribe committed while the mail was in flight (err %v): it must wait for the send", err)
	case <-time.After(700 * time.Millisecond):
	}
	close(release)
	if err := <-jobDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-changeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the change never completed after the job committed")
	}
}

// ---- fix 1 ----

func TestUnsubscribeWaitsForSendInFlight(t *testing.T) {
	f := b1fixSetup(t)
	f.exec(t, "insert into core.notify_prefs (workspace_id, user_id) values ($1::uuid,$2::uuid)", f.tess.ws, f.mo.userID) // a person who has saved settings once
	_, payload := f.queued(t)
	f.raceWhileSending(t, payload, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`insert into core.notify_prefs (workspace_id, user_id, direct_off) values ($1::uuid,$2::uuid,true)
			 on conflict (workspace_id, user_id) do update set direct_off = true`, f.tess.ws, f.mo.userID)
		return err
	})
	if f.mails() != 1 {
		t.Fatalf("the job checked before the unsubscribe committed and sent once, got %d", f.mails())
	}
}

func TestMembershipRemovalWaitsForSendInFlight(t *testing.T) {
	f := b1fixSetup(t)
	_, payload := f.queued(t)
	f.raceWhileSending(t, payload, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "delete from core.memberships where user_id = $1::uuid and workspace_id = $2::uuid", f.mo.userID, f.tess.ws)
		return err
	})
	if f.mails() != 1 {
		t.Fatalf("got %d mails", f.mails())
	}
}

func TestMailNotSentWhenAccessRemovedJustBeforeTheFinalCheck(t *testing.T) {
	f := b1fixSetup(t)
	notif, payload := f.queued(t)
	mailHook = func(stage string) error {
		if stage == "final" { // page restrictions are not locked: this change commits in the gap the final check closes
			f.exec(t, "update core.notes set restriction = 'view' where id = $1::uuid", f.note)
		}
		return nil
	}
	if err := f.run(t, payload); err != nil {
		t.Fatal(err)
	}
	if f.mails() != 0 {
		t.Fatalf("a person who lost the page just before the send must get nothing, got %d", f.mails())
	}
	if s := f.state(t, notif); s != "suppressed" {
		t.Fatalf("state %q", s)
	}
}

func TestSMTPIsBoundedByATimeout(t *testing.T) {
	f := b1fixSetup(t)
	f.smtp.stall.Store(true)
	smtpTimeoutNS.Store(int64(300 * time.Millisecond))
	_, payload := f.queued(t)
	start := time.Now()
	if err := f.run(t, payload); err == nil {
		t.Fatal("a stalled server must fail the send")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("SMTP must be cut off by the timeout, took %v", time.Since(start))
	}
}

// ---- fix 6 ----

func TestCrashBetweenSMTPAndCommitSendsNoSecondMail(t *testing.T) {
	f := b1fixSetup(t)
	notif, payload := f.queued(t)
	crash := true
	mailHook = func(stage string) error {
		if stage == "sent" && crash {
			crash = false
			return errors.New("crash after SMTP, before commit")
		}
		return nil
	}
	if err := f.run(t, payload); err == nil {
		t.Fatal("the first run must fail")
	}
	if f.mails() != 1 {
		t.Fatalf("first run sent %d", f.mails())
	}
	if err := f.run(t, payload); err != nil { // the queue's retry
		t.Fatal(err)
	}
	if f.mails() != 1 {
		t.Fatalf("a retried job must not send a second mail, got %d", f.mails())
	}
	if s := f.state(t, notif); s != "mailed" {
		t.Fatalf("state %q", s)
	}
}

func TestOldSendingMarkerIsSentUnknownAndNotResent(t *testing.T) {
	f := b1fixSetup(t)
	notif, payload := f.queued(t)
	var ref notifyMailRef
	_ = json.Unmarshal(payload, &ref)
	f.exec(t, "insert into core.mail_sends (message_id, workspace_id, user_id, started_at) values ($1,$2::uuid,$3::uuid, now() - interval '5 minutes')", ref.MessageID, f.tess.ws, f.mo.userID)
	if err := f.run(t, payload); err != nil {
		t.Fatal(err)
	}
	if f.mails() != 0 {
		t.Fatalf("a send of unknown outcome is not repeated, got %d", f.mails())
	}
	if s := f.state(t, notif); s != "sent_unknown" {
		t.Fatalf("state %q", s)
	}
}

func TestFreshSendingMarkerWaitsForTheOtherRun(t *testing.T) {
	f := b1fixSetup(t)
	notif, payload := f.queued(t)
	var ref notifyMailRef
	_ = json.Unmarshal(payload, &ref)
	f.exec(t, "insert into core.mail_sends (message_id, workspace_id, user_id) values ($1,$2::uuid,$3::uuid)", ref.MessageID, f.tess.ws, f.mo.userID)
	if err := f.run(t, payload); err == nil {
		t.Fatal("a send that may still be running must make this run wait (error, retried later)")
	}
	if f.mails() != 0 || f.state(t, notif) != "queued" {
		t.Fatalf("nothing changes: %d mails, state %q", f.mails(), f.state(t, notif))
	}
}

// ---- fixes 2 and 3 (inside one test transaction, where the pending job is visible) ----

func TestSeedSweepLeavesAPendingGatewayMentionAlone(t *testing.T) {
	_, tess, mo, g, note := mentionFixture(t)
	g.drain(100)
	m := mentionLink(tess.ws, mo.userID)
	if _, err := g.c.Tx.Exec(g.c.Context, "delete from core.note_mention_state where note_id = $1::uuid", note); err != nil {
		t.Fatal(err)
	}
	g.gatewaySave(note, "hi "+m) // committed by the gateway, its note.updated not yet handled
	if n, err := mentionSeedRun(context.Background(), g.c.Tx, 500); err != nil || n != 0 {
		t.Fatalf("the sweep must not look at a page with a pending event: %d %v", n, err)
	}
	g.drain(100)
	if c := g.mentionCount(note, mo.userID); c != 1 {
		t.Fatalf("the mention must still be delivered, got %d", c)
	}
	// With nothing pending any more, a page that has no state is seeded as before.
	if _, err := g.c.Tx.Exec(g.c.Context, "delete from core.note_mention_state where note_id = $1::uuid", note); err != nil {
		t.Fatal(err)
	}
	if n, err := mentionSeedRun(context.Background(), g.c.Tx, 500); err != nil || n < 1 {
		t.Fatalf("seed: %d %v", n, err)
	}
}

func TestBackfillLeavesAPendingEventAlone(t *testing.T) {
	_, tess, mo, g, _ := mentionFixture(t)
	g.drain(100)
	sp := str(g.cmd("space.create", map[string]any{"key": "TSK", "name": "Tasks", "visibility": "open"}), "id")
	note := str(g.cmd("note.create", map[string]any{"space_id": sp, "title": "Plan", "content_md": taskLine(tess.ws, mo.userID, "Ship it", "")}), "id")
	g.c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: tess.ws, Role: command.Owner}
	if _, err := TaskBackfillRun(context.Background(), g.c.Tx, tess.ws, 500); err != nil {
		t.Fatal(err)
	}
	if n, v := g.taskRows(note); n != 0 || v != 0 {
		t.Fatalf("the backfill must not index a page whose note.created is pending, got %d rows, version %d", n, v)
	}
	g.drain(100)
	if c := g.assignedCount(note, mo.userID); c != 1 {
		t.Fatalf("the assignee is notified exactly once, got %d", c)
	}
}

// ---- fix 4 ----

func (s *goSide) gatewaySaveAs(note, md, user string) {
	s.e.t.Helper()
	s.c.Principal = command.Principal{Kind: command.KindUser, UserID: &user, WorkspaceID: s.w.ws, Role: command.Owner}
	s.gatewaySave(note, md)
	s.c.Principal = s.user
}

func TestReorderedGatewayEventsAttributeMentionsToTheVersionAuthor(t *testing.T) {
	_, alice, bob, g, note := mentionFixture(t)
	carol := e2(t, g, alice)
	g.drain(100)
	g.gatewaySaveAs(note, "hi from Alice", alice.userID)                             // v2, Alice
	g.gatewaySaveAs(note, "hi from Alice "+mentionLink(alice.ws, carol), bob.userID) // v3, Bob adds Carol
	g.drain(100)                                                                     // Alice's older event runs first, on Bob's body
	var actor string
	if err := g.c.Tx.QueryRow(g.c.Context, "select actor_user_id::text from core.notifications where note_id = $1::uuid and user_id = $2::uuid and kind = 'mention'", note, carol).Scan(&actor); err != nil {
		t.Fatalf("Carol must be notified: %v", err)
	}
	if actor != bob.userID {
		t.Fatalf("Carol was mentioned by Bob, not by the actor of the event being dispatched: %q", actor)
	}
}

func TestStaleEventOfTheMentionedPersonDoesNotSwallowTheirMention(t *testing.T) {
	_, alice, bob, g, note := mentionFixture(t)
	g.drain(100)
	g.gatewaySaveAs(note, "Alice typed", alice.userID)                                    // v2, Alice
	g.gatewaySaveAs(note, "Alice typed "+mentionLink(alice.ws, alice.userID), bob.userID) // v3, Bob mentions Alice
	g.drain(100)                                                                          // Alice's stale event runs first
	if c := g.mentionCount(note, alice.userID); c != 1 {
		t.Fatalf("Alice must be told Bob mentioned her, got %d", c)
	}
	// A real self-mention (the mentioned person wrote the version) is still skipped.
	g.gatewaySaveAs(note, "Alice typed "+mentionLink(alice.ws, alice.userID)+" "+mentionLink(alice.ws, bob.userID), bob.userID) // Bob mentions himself
	g.drain(100)
	if c := g.mentionCount(note, bob.userID); c != 0 {
		t.Fatalf("a self-mention notifies nobody, got %d", c)
	}
}

// ---- fix 5 ----

func (f b1fix) digestRows(t *testing.T, n int) {
	t.Helper()
	f.exec(t, `insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title)
		select $1::uuid, $2::uuid, 'comment', $3::uuid, $4::uuid, 'Plan' from generate_series(1, $5)`, f.tess.ws, f.mo.userID, f.note, f.tess.userID, n)
}

func (f b1fix) digestJobs(t *testing.T) (jobs, rows int) {
	t.Helper()
	_ = f.tenant(t, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`select count(*), coalesce(sum(jsonb_array_length(payload->'notifications')), 0) from core.jobs
			 where workspace_id = $1::uuid and kind = 'notify.mail' and payload->>'user_id' = $2 and payload->>'mode' = 'digest'`, f.tess.ws, f.mo.userID).Scan(&jobs, &rows)
	})
	return
}

func TestDigestTakesAllOfOnePersonsRowsAndSurvivesConcurrentSweeps(t *testing.T) {
	f := b1fixSetup(t)
	const rows = 2105 // more than the old per-run row limit of 2000
	f.digestRows(t, rows)
	at := istAt(9)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- digestSweepWorkspace(context.Background(), f.e.w, f.tess.ws, at)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if jobs, got := f.digestJobs(t); jobs != 1 || got != rows {
		t.Fatalf("one digest holding every row: %d jobs, %d rows, want 1 and %d", jobs, got, rows)
	}
}

// ---- task assignments use the same version author (lead's follow-up to fix 4) ----

func TestReorderedEventsAttributeTaskAssignmentsToTheVersionAuthor(t *testing.T) {
	_, alice, bob, g, note := mentionFixture(t)
	carol := e2(t, g, alice)
	g.drain(100)
	g.gatewaySaveAs(note, "hi from Alice", alice.userID)                                                                               // v2, Alice
	g.gatewaySaveAs(note, taskLine(alice.ws, carol, "Ship it", "")+"\n"+taskLine(alice.ws, alice.userID, "Review it", ""), bob.userID) // v3, Bob assigns Carol and Alice
	// Alice's stale event runs first: the index is built from Bob's version with Alice as the dispatching actor.
	if err := notes.IndexTasks(context.Background(), g.c.Tx, alice.ws, note, &alice.userID, false); err != nil {
		t.Fatal(err)
	}
	var actor string
	if err := g.c.Tx.QueryRow(g.c.Context, "select actor_user_id::text from core.notifications where note_id = $1::uuid and user_id = $2::uuid and kind = 'assigned'", note, carol).Scan(&actor); err != nil {
		t.Fatalf("Carol must be notified as assigned: %v", err)
	}
	if actor != bob.userID {
		t.Fatalf("Carol was assigned by Bob, not by the actor of the event being dispatched: %q", actor)
	}
	if c := g.assignedCount(note, alice.userID); c != 1 {
		t.Fatalf("Bob assigned Alice, so Alice's own stale event must not skip her, got %d", c)
	}
}
