package approvals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sweep escalates approvals past their deadline to the fallback approver and writes the daily reminders
// (one notification per person, source and day; re-running the sweep the same day adds nothing).
// today is the IST calendar date. Each workspace runs in its own tenant transaction.
func Sweep(ctx context.Context, pool *pgxpool.Pool, today time.Time) error {
	rows, err := pool.Query(ctx, `select core.all_workspace_ids()::text`)
	if err != nil {
		return err
	}
	workspaces, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var failures []error
	for _, ws := range workspaces {
		if err := sweepOne(ctx, pool, ws, today.Format(time.DateOnly)); err != nil {
			failures = append(failures, fmt.Errorf("approvals %s: %w", ws, err))
		}
	}
	return errors.Join(failures...)
}

func sweepOne(ctx context.Context, pool *pgxpool.Pool, ws, day string) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c := &command.Ctx{Context: ctx, Tx: tx, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: ws}}

	// The approver becomes the fallback; a request escalates once.
	rows, err := tx.Query(ctx,
		`update core.confirmations set approver_user_id = fallback_user_id, escalated_at = now()
		  where kind = 'approval' and status = 'pending' and escalated_at is null and escalate_at <= now() and fallback_user_id is not null
		  returning id::text, command, note_id::text, fallback_user_id::text, requested_by`)
	if err != nil {
		return err
	}
	type esc struct {
		id, cmd, requester, fallback string
		note                         *string
	}
	var escs []esc
	for rows.Next() {
		var e esc
		if err := rows.Scan(&e.id, &e.cmd, &e.note, &e.fallback, &e.requester); err != nil {
			rows.Close()
			return err
		}
		escs = append(escs, e)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	for _, e := range escs {
		if _, err := c.Emit("approval.escalated", e.note, map[string]any{"approval_id": e.id, "command": e.cmd, "to": e.fallback}); err != nil {
			return err
		}
		if err := command.NotifyApprovers(c, &e.fallback, nil, "approval_requested", e.note, e.requester); err != nil {
			return err
		}
	}

	const ins = `insert into core.notifications (workspace_id, user_id, kind, title, source, source_day)
		select $1::uuid, r.user_id, 'reminder', r.n::text || $3::text, $4::text, $2::date from (`
	const tail = `) r on conflict (user_id, source, source_day) where source is not null do nothing`
	for _, q := range []struct{ sql, title, source string }{
		// approvals waiting more than a day, per person who can decide them
		{`select m.user_id, count(*) n from core.confirmations p
		    join core.memberships m on m.workspace_id = p.workspace_id and (p.approver_user_id = m.user_id or (p.approver_user_id is null and m.role in ('owner','admin')))
		   where p.kind = 'approval' and p.status = 'pending' and p.created_at < now() - interval '24 hours' and m.role <> 'guest' group by m.user_id`,
			" approval(s) waiting for more than a day", "approvals"},
		// tasks due today, per assignee
		{`select i.assignee_id as user_id, count(*) n from projects.items i join core.notes n on n.id = i.note_id
		   where i.assignee_id is not null and i.due_date = $2::date and i.status_category <> 'done' and n.deleted_at is null group by i.assignee_id`,
			" task(s) due today", "tasks_due"},
		// overdue invoices, for owners and admins
		{`select m.user_id, count(*) n from billing.invoices i join core.notes n on n.id = i.note_id
		    join core.memberships m on m.workspace_id = i.workspace_id and m.role in ('owner','admin')
		   where i.kind = 'invoice' and i.status in ('issued','irn_generated','sent','partly_paid') and i.due_date < $2::date
		     and i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor > 0 and n.deleted_at is null group by m.user_id`,
			" invoice(s) overdue", "invoices_overdue"},
	} {
		if _, err := tx.Exec(ctx, ins+q.sql+tail, ws, day, q.title, q.source); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func Start(ctx context.Context, pool *pgxpool.Pool, period time.Duration, ist func() time.Time) {
	if period <= 0 {
		return
	}
	go func() {
		timer := time.NewTicker(period)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if err := Sweep(ctx, pool, ist()); err != nil {
					slog.Error("approval sweep", "error", err)
				}
			}
		}
	}()
}
