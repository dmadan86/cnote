package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/jobs"
)

// docs-parity-b 4.3 and 13.2.5: the daily digest.

// istAt returns the next 09:00 IST at least an hour from now, so created_at (real now) is inside the 48 hour window.
func istAt(hour int) time.Time {
	n := time.Now().In(digestZone)
	t := time.Date(n.Year(), n.Month(), n.Day(), hour, 0, 0, 0, digestZone)
	for t.Before(n.Add(time.Hour)) {
		t = t.Add(24 * time.Hour)
	}
	return t
}

func (f mailFixture) comment(t *testing.T, title string) {
	t.Helper()
	f.exec(t, `insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title) values ($1::uuid,$2::uuid,'comment',$3::uuid,$4::uuid,$5)`,
		f.tess.ws, f.mo.userID, f.note, f.tess.userID, title)
}

func (f mailFixture) digest(t *testing.T, at time.Time) {
	t.Helper()
	if err := digestSweepTx(context.Background(), f.g.c.Tx, f.tess.ws, at); err != nil {
		t.Fatal(err)
	}
}

func (f mailFixture) lastDigest(t *testing.T) *string {
	t.Helper()
	var d *string
	err := f.g.c.Tx.QueryRow(f.g.c.Context, "select last_digest_on::text from core.notify_prefs where user_id = $1::uuid", f.mo.userID).Scan(&d)
	if err != nil && err != pgx.ErrNoRows {
		t.Fatal(err)
	}
	return d
}

func (f mailFixture) mailJob(t *testing.T) error {
	t.Helper()
	var j jobs.Job
	err := f.g.c.Tx.QueryRow(f.g.c.Context, `select id, workspace_id::text, kind, payload, attempts, max_attempts from core.jobs
		where workspace_id = $1::uuid and kind = $2 and status = 'queued' order by id desc limit 1`, f.tess.ws, NotifyMailJob).
		Scan(&j.ID, &j.WorkspaceID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	return f.g.e.w.Handle(f.g.c, j)
}

func TestDigestIsSkippedWhenEmpty(t *testing.T) {
	f := mailSetup(t) // the only notification is a mention: a direct kind, not digest material in smart mode
	f.digest(t, istAt(9))
	f.g.drain(100)
	if n := len(f.mailsTo(f.mo.email)); n != 0 {
		t.Fatalf("an empty digest must not be sent, got %d", n)
	}
	if d := f.lastDigest(t); d != nil {
		t.Fatalf("an empty digest must not claim the day, got %s", *d)
	}
	f.comment(t, "Plan")
	f.digest(t, istAt(7)) // before the digest hour (08:00)
	f.g.drain(100)
	if n := len(f.mailsTo(f.mo.email)); n != 0 {
		t.Fatalf("not before the digest hour, got %d", n)
	}
}

func TestDigestOncePerDayWithoutCommentText(t *testing.T) {
	f := mailSetup(t)
	f.comment(t, "Plan")
	f.comment(t, "Plan")
	at := istAt(9)
	f.digest(t, at)
	f.g.drain(100)
	f.comment(t, "Plan")
	f.digest(t, at) // a second sweep the same day, and a retry of the first
	f.digest(t, at)
	f.g.drain(100)
	got := f.mailsTo(f.mo.email)
	if len(got) != 1 {
		t.Fatalf("one digest per day, got %d", len(got))
	}
	text := unfold(got[0])
	if !strings.Contains(text, "Subject: 2 updates in") || strings.Contains(text, "secret body") {
		t.Fatalf("digest:\n%s", text)
	}
	f.digest(t, at.Add(24*time.Hour)) // tomorrow's digest takes the one that came after
	f.g.drain(100)
	if n := len(f.mailsTo(f.mo.email)); n != 2 {
		t.Fatalf("the next day sends the next digest, got %d", n)
	}
}

func TestDigestAccessLostBetweenClaimAndSend(t *testing.T) {
	f := mailSetup(t)
	f.comment(t, "Plan")
	f.digest(t, istAt(9))
	f.exec(t, "update core.notes set restriction = 'view' where id = $1::uuid", f.note) // restricted, and Mo has no grant
	f.g.drain(100)
	if n := len(f.mailsTo(f.mo.email)); n != 0 {
		t.Fatalf("a person who lost the page must get nothing about it, got %d", n)
	}
}

func TestDigestOptOutWhileQueued(t *testing.T) {
	f := mailSetup(t)
	f.comment(t, "Plan")
	f.digest(t, istAt(9))
	f.exec(t, "update core.notify_prefs set digest_off = true where user_id = $1::uuid", f.mo.userID) // the one-click link
	f.g.drain(100)
	if n := len(f.mailsTo(f.mo.email)); n != 0 {
		t.Fatalf("opted out while queued, got %d", n)
	}
}

func TestDigestRetryAfterAccessLoss(t *testing.T) {
	f := mailSetup(t)
	f.comment(t, "Plan")
	f.digest(t, istAt(9))
	t.Setenv("SMTP_URL", "smtp://127.0.0.1:1") // nothing listens: the send fails and the job is retried
	if err := f.mailJob(t); err == nil {
		t.Fatal("the send must fail")
	}
	f.exec(t, "update core.notes set restriction = 'view' where id = $1::uuid", f.note)
	t.Setenv("SMTP_URL", "smtp://"+f.smtp.ln.Addr().String())
	if err := f.mailJob(t); err != nil {
		t.Fatal(err)
	}
	if n := len(f.mailsTo(f.mo.email)); n != 0 {
		t.Fatalf("the retry must recheck access, got %d", n)
	}
}

func TestDigestSpaceNotificationsFollowMembership(t *testing.T) {
	for _, member := range []bool{false, true} {
		f := mailSetup(t)
		var sp string
		if err := f.g.c.Tx.QueryRow(f.g.c.Context, `insert into core.spaces (workspace_id, key, name, visibility) values ($1::uuid,'PRV','Private','private') returning id::text`, f.tess.ws).Scan(&sp); err != nil {
			t.Fatal(err)
		}
		if member {
			f.exec(t, "insert into core.space_members (workspace_id, space_id, user_id) values ($1::uuid,$2::uuid,$3::uuid)", f.tess.ws, sp, f.mo.userID)
		}
		f.exec(t, `insert into core.notifications (workspace_id, user_id, kind, actor_user_id, title, source) values ($1::uuid,$2::uuid,'page_updated',$3::uuid,'Private',$4)`,
			f.tess.ws, f.mo.userID, f.tess.userID, "space:"+sp)
		f.digest(t, istAt(9))
		f.g.drain(100)
		if n := len(f.mailsTo(f.mo.email)); (n == 1) != member {
			t.Fatalf("member=%v: got %d mails", member, n)
		}
	}
}

func TestDigestSubjectNeverHasATitle(t *testing.T) {
	f := mailSetup(t)
	f.exec(t, "update core.notes set restriction = 'view' where id = $1::uuid", f.note)
	f.exec(t, "insert into core.note_permissions (note_id, workspace_id, user_id, level, granted_by) values ($1::uuid,$2::uuid,$3::uuid,'view',$4::uuid)", f.note, f.tess.ws, f.mo.userID, f.tess.userID)
	f.comment(t, "Plan")
	f.digest(t, istAt(9))
	f.g.drain(100)
	got := f.mailsTo(f.mo.email)
	if len(got) != 1 {
		t.Fatalf("want one digest, got %d", len(got))
	}
	head := strings.SplitN(unfold(got[0]), "\n\n", 2)[0]
	if !strings.Contains(head, "Subject: 1 updates in") || strings.Contains(head, "Plan") {
		t.Fatalf("the digest subject must not hold a title:\n%s", head)
	}
}

func TestAccessRequestBatchingHook(t *testing.T) {
	f := mailSetup(t)
	day := istAt(9).Format("2006-01-02")
	if ok, err := AccessRequestMailedToday(f.g.c.Context, f.g.c.Tx, f.mo.userID, day); err != nil || ok {
		t.Fatalf("nothing mailed yet: %v %v", ok, err)
	}
	f.exec(t, `insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title, emailed_at, email_state) values ($1::uuid,$2::uuid,'access_request',$3::uuid,$4::uuid,'Plan',$5,'mailed')`,
		f.tess.ws, f.mo.userID, f.note, f.tess.userID, istAt(9).Add(-time.Hour))
	if ok, err := AccessRequestMailedToday(f.g.c.Context, f.g.c.Tx, f.mo.userID, day); err != nil || !ok {
		t.Fatalf("one went out: %v %v", ok, err)
	}
	// The second request of the day waits for the digest, which takes it.
	f.exec(t, `insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title) values ($1::uuid,$2::uuid,'access_request',$3::uuid,$4::uuid,'Plan')`,
		f.tess.ws, f.mo.userID, f.note, f.tess.userID)
	f.digest(t, istAt(9))
	f.g.drain(100)
	if n := len(f.mailsTo(f.mo.email)); n != 1 {
		t.Fatalf("the batched request reaches the digest, got %d", n)
	}
}

func TestDigestListsOverdueTasksOnReadablePagesOnly(t *testing.T) {
	for _, readable := range []bool{true, false} {
		f := mailSetup(t)
		f.comment(t, "Plan")
		f.exec(t, `insert into core.note_tasks (workspace_id, note_id, ord, text, assignee_id, due, done, indexed_version) values ($1::uuid,$2::uuid,0,'Ship it',$3::uuid,'2000-01-01',false,1)`,
			f.tess.ws, f.note, f.mo.userID)
		if !readable { // the comment still reads the page at the sweep; the page closes before the send
			f.digest(t, istAt(9))
			f.exec(t, "update core.notes set restriction = 'view' where id = $1::uuid", f.note)
		} else {
			f.digest(t, istAt(9))
		}
		f.g.drain(100)
		got := f.mailsTo(f.mo.email)
		if readable != (len(got) == 1 && strings.Contains(unfold(got[0]), "Task due: Ship it (overdue since 2000-01-01)")) {
			t.Fatalf("readable=%v: %d mails", readable, len(got))
		}
	}
}

func TestDigestSentForDueTasksAloneAndDirectSweepLeavesDigestModeRows(t *testing.T) {
	f := mailSetup(t)
	f.exec(t, "insert into core.notify_prefs (workspace_id, user_id, email_mode) values ($1::uuid,$2::uuid,'digest')", f.tess.ws, f.mo.userID)
	f.sweep(t) // digest mode: the direct sweep claims nothing, the digest takes the row
	if s := f.state(t); s != "" && s != "pending" {
		t.Fatalf("the direct sweep must leave a digest-mode row alone, state %q", s)
	}
	f.exec(t, "update core.notifications set read_at = now() where id = $1::uuid", f.notifID) // nothing unread, only a task
	f.exec(t, `insert into core.note_tasks (workspace_id, note_id, ord, text, assignee_id, due, done, indexed_version) values ($1::uuid,$2::uuid,0,'Ship it',$3::uuid,'2000-01-01',false,1)`,
		f.tess.ws, f.note, f.mo.userID)
	f.digest(t, istAt(9))
	f.g.drain(100)
	got := f.mailsTo(f.mo.email)
	if len(got) != 1 || !strings.Contains(unfold(got[0]), "Task due: Ship it") {
		t.Fatalf("a person with only a due task gets the digest, got %d mails", len(got))
	}
}
