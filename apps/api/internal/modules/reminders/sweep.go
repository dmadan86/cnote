package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/mailer"
)

var ist = time.FixedZone("IST", 5*3600+1800)

// sendHour is the first IST hour of the day the sweep sends.
const sendHour = 10

// Sweep sends today's reminders for every workspace. Run it every few minutes: a workspace-day is claimed in one
// transaction with its work, so concurrent sweeps skip it and a crash retries it. It only ever handles the current
// IST day; a step whose day passed (server down, reminders switched on late) is skipped, never back-filled.
func Sweep(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, now time.Time) error {
	rows, err := pool.Query(ctx, `select core.all_workspace_ids()::text`)
	if err != nil {
		return err
	}
	wss, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var failures []error
	for _, ws := range wss {
		if err := sweepWorkspace(ctx, pool, reg, ws, now.In(ist)); err != nil {
			failures = append(failures, fmt.Errorf("reminders %s: %w", ws, err))
		}
	}
	return errors.Join(failures...)
}

func sweepWorkspace(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, ws string, now time.Time) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c := &command.Ctx{Context: ctx, Tx: tx}
	reg.Bind(c)
	c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: ws, Role: command.Owner}
	// A mail job that ran out of retries leaves its reminder queued: record the failure.
	if _, err := tx.Exec(ctx, `update billing.reminder_log l set status = 'failed', error = left(coalesce(j.last_error, 'send failed'), 300)
		from core.jobs j where l.status in ('queued','sending') and j.kind = 'mail.send' and j.status = 'failed' and j.payload->>'notify' = 'reminder:' || l.id::text`); err != nil {
		return err
	}
	if now.Hour() >= sendHour && mailer.Configured() {
		if err := sendToday(c, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// maxAttempts caps the same-day retries of an invoice that failed to enqueue.
const maxAttempts = 5

type due struct {
	id, number, email, contact string
	step                       int
}

func sendToday(c *command.Ctx, now time.Time) error {
	s, err := loadSettings(c)
	if err != nil || !s.Enabled {
		return err
	}
	today := now.Format(time.DateOnly)
	if _, err := c.Tx.Exec(c.Context, `insert into billing.reminder_runs (workspace_id, day) values ($1::uuid, $2::date) on conflict do nothing`, c.WorkspaceID(), today); err != nil {
		return command.DB(err)
	}
	var done bool
	err = c.Tx.QueryRow(c.Context, `select done from billing.reminder_runs where day = $1::date for update skip locked`, today).Scan(&done)
	if errors.Is(err, pgx.ErrNoRows) || done {
		return nil // finished today, or another sweep holds the day
	}
	if err != nil {
		return command.DB(err)
	}
	var owner string
	err = c.Tx.QueryRow(c.Context, `select user_id::text from core.memberships where workspace_id = $1::uuid and role = 'owner' order by created_at limit 1`, c.WorkspaceID()).Scan(&owner)
	if err != nil {
		return command.DB(err)
	}
	c.Principal.UserID = &owner
	var offsets []int32
	for _, st := range s.Steps {
		if st.Enabled {
			offsets = append(offsets, int32(st.offset()))
		}
	}
	// today - due = the step's offset
	rows, err := c.Tx.Query(c.Context, `select i.note_id::text, coalesce(i.number,''), ct.email, ct.note_id::text, ($1::date - coalesce(i.due_date, i.invoice_date))::int
		from `+eligibleFrom+` and ($1::date - coalesce(i.due_date, i.invoice_date)) = any($2::int[])
		  and not exists (select 1 from billing.reminder_log l where l.invoice_note_id = i.note_id and l.status <> 'cancelled' and l.due_date = $1::date)
		order by i.note_id`, today, offsets)
	if err != nil {
		return command.DB(err)
	}
	var todo []due
	for rows.Next() {
		var d due
		var delta int
		if err := rows.Scan(&d.id, &d.number, &d.email, &d.contact, &delta); err != nil {
			rows.Close()
			return command.DB(err)
		}
		for i, st := range s.Steps {
			if st.Enabled && st.offset() == delta {
				d.step = i
			}
		}
		todo = append(todo, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return command.DB(err)
	}
	retry := false
	for _, d := range todo {
		var tries int
		if err := c.Tx.QueryRow(c.Context, `select coalesce((select attempts from billing.reminder_attempts where invoice_note_id = $1::uuid and step = $2), 0)`, d.id, d.step).Scan(&tries); err != nil {
			return command.DB(err)
		}
		if tries >= maxAttempts {
			continue // given up for today
		}
		if err := enqueueOne(c, s, d, today); err != nil {
			slog.Warn("reminder enqueue failed", "invoice", d.id, "step", d.step, "attempt", tries+1, "error", err)
			if _, err := c.Tx.Exec(c.Context, `insert into billing.reminder_attempts (workspace_id, invoice_note_id, step, attempts, last_error)
				values ($1::uuid, $2::uuid, $3, 1, left($4, 300)) on conflict (workspace_id, invoice_note_id, step) do update set attempts = billing.reminder_attempts.attempts + 1, last_error = excluded.last_error`,
				c.WorkspaceID(), d.id, d.step, err.Error()); err != nil {
				return command.DB(err)
			}
			retry = retry || tries+1 < maxAttempts
		}
	}
	// The day stays open while a failed invoice has attempts left: the next sweep (5 minutes later) retries it.
	_, err = c.Tx.Exec(c.Context, `update billing.reminder_runs set done = $2 where day = $1::date`, today, !retry)
	return command.DB(err)
}

// enqueueOne writes the log row and the outbox mail in one savepoint.
func enqueueOne(c *command.Ctx, s Settings, d due, today string) (err error) {
	sp, err := c.Tx.Begin(c.Context)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = sp.Rollback(context.WithoutCancel(c.Context))
		}
	}()
	cc := *c
	cc.Tx = sp
	var logID, mid string
	if err = sp.QueryRow(c.Context, `select gen_random_uuid()::text, '<' || gen_random_uuid()::text || '@cnote>'`).Scan(&logID, &mid); err != nil {
		return err
	}
	tag, err := sp.Exec(c.Context, `insert into billing.reminder_log (id, workspace_id, invoice_note_id, step, message_id, contact_id, to_email, due_date)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid, $7, $8::date) on conflict (workspace_id, invoice_note_id, step) do nothing`,
		logID, c.WorkspaceID(), d.id, d.step, mid, d.contact, d.email, today)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return sp.Rollback(c.Context) // this step was already logged: never twice
	}
	var link struct {
		URL string `json:"url"`
	}
	b, err := cc.Call("share.create", map[string]any{"note_id": d.id, "expires_in_days": 90})
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &link); err != nil {
		return err
	}
	var r struct{ Subject, Text, HTML, FromName, ReplyTo string }
	b, err = cc.Call("email_template.render", map[string]any{"event": s.Steps[d.step].Event, "note_id": d.id, "link": link.URL})
	if err != nil {
		return err
	}
	var rj map[string]string
	if err = json.Unmarshal(b, &rj); err != nil {
		return err
	}
	r.Subject, r.Text, r.HTML, r.FromName, r.ReplyTo = rj["subject"], rj["text"], rj["html"], rj["from_name"], rj["reply_to"]
	if err = mailer.Enqueue(c.Context, sp, c.WorkspaceID(), mailer.Mail{To: d.email, ReplyTo: r.ReplyTo, FromName: r.FromName, Subject: r.Subject,
		Text: r.Text, HTML: r.HTML, MessageID: mid, Notify: "reminder:" + logID}); err != nil {
		return err
	}
	return sp.Commit(c.Context)
}

// Pool is where BeforeSend commits the `sending` mark on its own connection (set by the worker). Nil (tests that drive
// the hook inside one transaction): the mark rides in the job's transaction.
var Pool *pgxpool.Pool

// sendingStale is how long a `sending` mark may sit before a retry reads it as "the send probably happened".
const sendingStale = 10 * time.Minute

// markSending sets status 'sending' (or back to 'queued') in a transaction of its own that commits at once.
func markSending(c *command.Ctx, ref, from, to string) error {
	q := `update billing.reminder_log set status = $3, sending_at = case when $3 = 'sending' then now() end where id = $1::uuid and status = $2`
	if Pool == nil {
		_, err := c.Tx.Exec(c.Context, q, ref, from, to)
		return command.DB(err)
	}
	ctx := context.WithoutCancel(c.Context)
	tx, err := db.BeginTenant(ctx, Pool, c.WorkspaceID())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, q, ref, from, to); err != nil {
		return command.DB(err)
	}
	return tx.Commit(ctx)
}

// BeforeSend is the mail.send hook: the last eligibility check, in the job's own transaction. It locks the invoice
// row first, so a payment either committed before this check (the reminder is cancelled) or waits for this job. Not
// eligible, or the recipient changed since enqueue: the reminder is cancelled and nothing is sent. Then `sending` is
// committed before the SMTP call.
func BeforeSend(c *command.Ctx, ref string) (bool, error) {
	var inv, status, toEmail string
	var contact string
	var sendingAt *time.Time
	err := c.Tx.QueryRow(c.Context, `select invoice_note_id::text, status, to_email, contact_id::text, sending_at from billing.reminder_log where id = $1::uuid`, ref).
		Scan(&inv, &status, &toEmail, &contact, &sendingAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, `select 1 from billing.invoices where note_id = $1::uuid for update`, inv); err != nil {
		return false, command.DB(err)
	}
	switch status {
	case "queued":
	case "sending": // a retry after a crash that may have come after the SMTP send: prefer a missed duplicate to a double send
		if sendingAt != nil && time.Since(*sendingAt) < sendingStale {
			return false, errors.New("reminder is being sent")
		}
		_, err = c.Tx.Exec(c.Context, `update billing.reminder_log set status = 'sent', sent_at = now(), error = 'possibly sent: retried after a crash' where id = $1::uuid and status = 'sending'`, ref)
		return false, command.DB(err)
	default:
		return false, nil
	}
	cancel := func(reason string) (bool, error) {
		_, err := c.Tx.Exec(c.Context, `update billing.reminder_log set status = 'cancelled', error = $2 where id = $1::uuid`, ref, reason)
		return false, command.DB(err)
	}
	ok, err := eligible(c, inv)
	if err != nil {
		return false, err
	}
	if ok {
		s, err := loadSettings(c)
		if err != nil {
			return false, err
		}
		ok = s.Enabled
	}
	if !ok {
		return cancel("no longer eligible")
	}
	var curContact, curEmail string
	if err := c.Tx.QueryRow(c.Context, `select ct.note_id::text, coalesce(ct.email,'') from billing.invoices i join crm.contacts ct on ct.note_id = i.contact_id where i.note_id = $1::uuid`, inv).Scan(&curContact, &curEmail); err != nil {
		return false, command.DB(err)
	}
	if curContact != contact || !strings.EqualFold(curEmail, toEmail) {
		return cancel("recipient changed since the reminder was queued")
	}
	return true, markSending(c, ref, "queued", "sending")
}

// SendFailed is the mail.send hook for a send that failed: the reminder goes back to queued so the retry sends it.
// (A crash never reaches here, which is how a retry tells "failed" from "maybe sent".)
func SendFailed(c *command.Ctx, ref string) error { return markSending(c, ref, "sending", "queued") }

// AfterSend marks the reminder sent and emits invoice.reminder_sent, in the transaction that completes the job.
func AfterSend(c *command.Ctx, ref, messageID string) error {
	var inv string
	var step int
	err := c.Tx.QueryRow(c.Context, `update billing.reminder_log set status = 'sent', sent_at = now(), error = null where id = $1::uuid returning invoice_note_id::text, step`, ref).Scan(&inv, &step)
	if err != nil {
		return command.DB(err)
	}
	_, err = c.Emit("invoice.reminder_sent", &inv, map[string]any{"step": step, "message_id": messageID})
	return err
}
