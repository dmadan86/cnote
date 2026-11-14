package recurring

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

// Sweep uses only the application pool. Workspace discovery is unscoped; all template reads/writes
// use BeginTenant. A locked schedule is skipped, and its entire catch-up batch commits atomically.
func Sweep(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, today time.Time) error {
	rows, e := pool.Query(ctx, `select core.all_workspace_ids()::text`)
	if e != nil {
		return e
	}
	workspaces, e := pgx.CollectRows(rows, pgx.RowTo[string])
	if e != nil {
		return e
	}
	var failures []error
	for _, ws := range workspaces {
		tx, e := db.BeginTenant(ctx, pool, ws)
		if e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `select r.note_id::text from billing.recurring_invoices r join core.notes n on n.id=r.note_id where r.status='active' and r.next_run<=$1::date and n.deleted_at is null order by r.next_run,r.note_id`, today.Format(time.DateOnly))
		if e != nil {
			_ = tx.Rollback(ctx)
			return e
		}
		ids, e := pgx.CollectRows(rows, pgx.RowTo[string])
		_ = tx.Rollback(ctx)
		if e != nil {
			return e
		}
		for _, id := range ids {
			if e = sweepOne(ctx, pool, reg, ws, id, today.Format(time.DateOnly)); e != nil {
				failures = append(failures, fmt.Errorf("recurring %s: %w", id, e))
			}
		}
	}
	return errors.Join(failures...)
}
func sweepOne(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, ws, id, today string) error {
	tx, e := db.BeginTenant(ctx, pool, ws)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c := &command.Ctx{Context: ctx, Tx: tx}
	reg.Bind(c)
	s, e := scan(tx.QueryRow(ctx, `select `+cols+` from billing.recurring_invoices r join core.notes n on n.id=r.note_id where r.note_id=$1::uuid and r.status='active' and r.next_run<=$2::date and n.deleted_at is null for update of r skip locked`, id, today))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var role string
	e = tx.QueryRow(ctx, `select role from core.memberships where user_id=$1::uuid`, s.Creator).Scan(&role)
	if e != nil {
		return e
	}
	// System actor on behalf of the creator, with the creator's CURRENT role and page access.
	c.Principal = command.Principal{Kind: command.KindSystem, WorkspaceID: ws, UserID: &s.Creator, Role: command.ParseRole(role)}
	if c.Principal.Role < command.Member {
		return command.ErrForbidden("creator can no longer manage invoices")
	}
	if _, e = checked(c, mustJSON(map[string]any{"id": id}), true); e != nil {
		return e
	}
	for i := 0; i < 12 && s.Status == "active" && s.Next <= today; i++ {
		if _, e = runPeriod(c, s, today); e != nil {
			return e
		}
		s, e = load(c, id, false)
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

func Start(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, period time.Duration) {
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
				if e := Sweep(ctx, pool, reg, time.Now().UTC().Add(330*time.Minute)); e != nil {
					slog.Error("recurring sweep", "error", e)
				}
			}
		}
	}()
}
