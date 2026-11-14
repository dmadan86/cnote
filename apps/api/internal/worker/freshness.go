package worker

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
)

// Port of the event side and the sweep of crates/cnote-docs/src/freshness.rs. The docs.propose_edit job
// (it calls the AI provider) stays in Rust: this package only enqueues it.

const (
	lapseGraceDays = 14
	graveyardDays  = 90
)

// Event kinds that say nothing about the content of the note they are attached to.
var ignoredKinds = []string{"doc.", "relation.", "comment.", "space.", "note.created", "note.deleted"}

func flag(c *command.Ctx, noteID, reason string, source *string, event *int64) (string, error) {
	var id string
	err := c.Tx.QueryRow(c.Context,
		`insert into docs.freshness_flags (workspace_id, note_id, reason, source_note_id, source_event_id)
		 values ($1::uuid,$2::uuid,$3,$4::uuid,$5) returning id::text`, c.WorkspaceID(), noteID, reason, source, event).Scan(&id)
	return id, err
}

// freshnessOnEvent: any event on note X makes pages bound to X stale, with a flag and an edit proposal job.
func freshnessOnEvent(c *command.Ctx, ev jobs.Event) error {
	if ev.NoteID == nil {
		return nil
	}
	for _, p := range ignoredKinds {
		if strings.HasPrefix(ev.Kind, p) {
			return nil
		}
	}
	if ev.Kind == "note.updated" {
		var p struct {
			Changed []any `json:"changed"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		if !slices.Contains(p.Changed, any("content")) && !slices.Contains(p.Changed, any("title")) {
			return nil
		}
	}
	src := *ev.NoteID
	rows, err := c.Tx.Query(c.Context,
		`select distinct r.from_id::text from core.relations r join core.notes n on n.id = r.from_id
		 where r.to_id = $1::uuid and r.kind = 'binds' and r.from_id <> $1::uuid
		   and cardinality(n.types) = 0 and n.deleted_at is null and n.freshness <> 'archived'`, src)
	if err != nil {
		return err
	}
	pages, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, page := range pages {
		// One open flag per (page, source): a burst of events on X must not pile up proposals.
		var open bool
		if err := c.Tx.QueryRow(c.Context,
			`select exists (select 1 from docs.freshness_flags where note_id = $1::uuid and source_note_id = $2::uuid
			   and reason = 'binding_changed' and resolved_at is null)`, page, src).Scan(&open); err != nil {
			return err
		}
		if open {
			continue
		}
		flagID, err := flag(c, page, "binding_changed", &src, &ev.ID)
		if err != nil {
			return err
		}
		if _, err := c.Tx.Exec(c.Context, "update core.notes set freshness = 'stale' where id = $1::uuid and freshness = 'fresh'", page); err != nil {
			return err
		}
		if _, err := c.Emit("doc.stale", &page, map[string]any{"reason": "binding_changed", "source_note_id": src, "source_event": ev.Kind, "flag_id": flagID}); err != nil {
			return err
		}
		if err := enqueue(c, "docs.propose_edit", map[string]any{"flag_id": flagID}, nil); err != nil {
			return err
		}
	}
	return nil
}

// enqueue is Rust events::enqueue.
func enqueue(c *command.Ctx, kind string, payload any, runAt *time.Time) error {
	b, err := command.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = c.Tx.Exec(c.Context,
		"insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,$2,$3,coalesce($4, now()))",
		c.WorkspaceID(), kind, json.RawMessage(b), runAt)
	return err
}

// allWorkspaceIDs lists every workspace through the SECURITY DEFINER helper.
func allWorkspaceIDs(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, "select core.all_workspace_ids()::text")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// systemCtx opens a tenant transaction with the system principal (Principal::system(ws)).
func (w *Worker) systemCtx(ctx context.Context, ws string) (*command.Ctx, error) {
	tx, err := db.BeginTenant(ctx, w.Pool, ws)
	if err != nil {
		return nil, err
	}
	c := &command.Ctx{Context: ctx, Tx: tx, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: ws, Role: command.Owner}}
	if w.Registry != nil {
		w.Registry.Bind(c)
	}
	return c, nil
}

// FreshnessSweep is the daily sweep over every workspace (docs.freshness_sweep).
func FreshnessSweep(ctx context.Context, w *Worker) error {
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return err
	}
	for _, ws := range ids {
		if err := func() error {
			c, err := w.systemCtx(ctx, ws)
			if err != nil {
				return err
			}
			defer func() { _ = c.Tx.Rollback(context.WithoutCancel(ctx)) }()
			if err := FreshnessSweepWorkspace(c, time.Now().UTC()); err != nil {
				return err
			}
			return c.Tx.Commit(ctx)
		}(); err != nil {
			return err
		}
	}
	return nil
}

func FreshnessSweepWorkspace(c *command.Ctx, today time.Time) error {
	day := today.UTC().Format(time.DateOnly)
	// Graveyard mode: imported pages nobody claimed.
	rows, err := c.Tx.Query(c.Context,
		`update core.notes set freshness = 'archived', archived_at = now()
		 where freshness = 'unverified' and cardinality(types) = 0 and deleted_at is null
		   and created_at <= now() - make_interval(days => $1)
		 returning id::text, owner_id::text`, int32(graveyardDays))
	if err != nil {
		return err
	}
	type idOwner struct {
		id    string
		owner *string
	}
	unclaimed, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (idOwner, error) {
		var x idOwner
		return x, r.Scan(&x.id, &x.owner)
	})
	if err != nil {
		return err
	}
	for _, u := range unclaimed {
		if _, err := c.Emit("doc.archived", &u.id, map[string]any{"reason": "unclaimed_import", "owner_id": u.owner}); err != nil {
			return err
		}
	}
	rows, err = c.Tx.Query(c.Context,
		`select id::text, owner_id::text, lapse_count from core.notes
		 where cardinality(types) = 0 and deleted_at is null and freshness in ('fresh','stale') and verify_by < $1::date`, day)
	if err != nil {
		return err
	}
	type lapsedRow struct {
		id    string
		owner *string
		count int32
	}
	lapsed, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (lapsedRow, error) {
		var x lapsedRow
		return x, r.Scan(&x.id, &x.owner, &x.count)
	})
	if err != nil {
		return err
	}
	reverify := today.UTC().AddDate(0, 0, lapseGraceDays).Format(time.DateOnly)
	for _, l := range lapsed {
		count := l.count + 1
		if _, err := flag(c, l.id, "date_lapsed", nil, nil); err != nil {
			return err
		}
		if count >= 2 {
			if _, err := c.Tx.Exec(c.Context,
				"update core.notes set freshness = 'archived', archived_at = now(), lapse_count = $2 where id = $1::uuid", l.id, count); err != nil {
				return err
			}
			if _, err := c.Emit("doc.archived", &l.id, map[string]any{"reason": "lapsed_twice", "owner_id": l.owner}); err != nil {
				return err
			}
		} else {
			if _, err := c.Tx.Exec(c.Context,
				"update core.notes set freshness = 'stale', lapse_count = $2, verify_by = $3::date where id = $1::uuid", l.id, count, reverify); err != nil {
				return err
			}
			if _, err := c.Emit("doc.lapsed", &l.id, map[string]any{"owner_id": l.owner, "lapse_count": count, "reverify_by": reverify}); err != nil {
				return err
			}
		}
	}
	return nil
}

// IRNWindowSweep (finance.irn_window): for workspaces with AATO above Rs 10 crore, warn once per un-reported B2B
// invoice that is 20+ days old. Returns the number of warnings emitted.
func IRNWindowSweep(ctx context.Context, w *Worker, today time.Time) (int, error) {
	const warnDay, blockDay = 20, 29
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return 0, err
	}
	emitted := 0
	for _, ws := range ids {
		n, err := func() (int, error) {
			c, err := w.systemCtx(ctx, ws)
			if err != nil {
				return 0, err
			}
			defer func() { _ = c.Tx.Rollback(context.WithoutCancel(ctx)) }()
			n, err := IRNWarnWorkspace(c, today)
			if err != nil {
				return 0, err
			}
			return n, c.Tx.Commit(ctx)
		}()
		if err != nil {
			return emitted, err
		}
		emitted += n
	}
	return emitted, nil
}

// IRNWarnWorkspace is the per-workspace body of the sweep; the caller commits.
func IRNWarnWorkspace(c *command.Ctx, today time.Time) (int, error) {
	const warnDay, blockDay = 20, 29
	var settings map[string]any
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select settings from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&raw); err != nil {
		return 0, err
	}
	_ = json.Unmarshal(raw, &settings)
	if settings["aato_above_10cr"] != true {
		return 0, nil
	}
	day := today.Format(time.DateOnly)
	rows, err := c.Tx.Query(c.Context,
		`select i.note_id::text, i.number, i.invoice_date::text from billing.invoices i
				 where i.status in ('issued','sent','partly_paid','paid') and i.irn is null and i.buyer_gstin is not null
				   and $1::date - i.invoice_date >= $2
				   and not exists (select 1 from core.events e where e.note_id = i.note_id and e.kind = 'invoice.irn_window_warning')`, day, int32(warnDay))
	if err != nil {
		return 0, err
	}
	type due struct {
		id     string
		number *string
		date   string
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.id, &d.number, &d.date)
	})
	if err != nil {
		return 0, err
	}
	for _, d := range list {
		dt, err := time.Parse(time.DateOnly, d.date)
		if err != nil {
			return 0, err
		}
		days := int64(today.Sub(dt) / (24 * time.Hour))
		if _, err := c.Emit("invoice.irn_window_warning", &d.id, map[string]any{"number": d.number, "days_elapsed": days, "days_left": 30 - days, "blocked": days >= blockDay}); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}
