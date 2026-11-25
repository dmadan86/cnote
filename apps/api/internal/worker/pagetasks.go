package worker

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules/notes"
)

// The task index (docs-parity-b 4.4, 13.2.6). Every page create and update, from the API and from gateway snapshots alike, rebuilds
// that page's rows in core.note_tasks through notes.IndexTasks, which locks per page and applies only a newer version, so a retried
// or reordered event, or the backfill sweep, can run at any time. Registered after the mention consumer (wire.go), so a person
// the mention path already told about this edit is not told twice.
func init() {
	RegisterEvent("note.created", tasksOnNoteEvent)
	RegisterEvent("note.updated", tasksOnNoteEvent)
	RegisterSweep(Sweep{"docs.task_backfill", time.Minute, TaskBackfillSweep})
}

func tasksOnNoteEvent(c *command.Ctx, ev jobs.Event) error {
	if ev.NoteID == nil {
		return nil
	}
	// A new page announces its tasks; a copy of a page does not re-announce the original's.
	created := ev.Kind == "note.created" && !strings.Contains(string(ev.Payload), `"duplicate_of"`)
	return notes.IndexTasks(c.Context, c.Tx, ev.WorkspaceID, *ev.NoteID, c.Principal.UserID, created)
}

const taskBackfillBatch = 500

// TaskBackfillSweep indexes pages that have no task index yet (pages that predate the table, or whose events were lost), in
// batches of 500 and silently: nobody is notified about a task that already existed. A live edit racing it is safe, see IndexTasks.
func TaskBackfillSweep(ctx context.Context, w *Worker) error {
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return err
	}
	for _, ws := range ids {
		for range 20 {
			n, err := taskBackfillBatchRun(ctx, w, ws, taskBackfillBatch)
			if err != nil {
				return err
			}
			if n < taskBackfillBatch {
				break
			}
		}
	}
	return nil
}

func taskBackfillBatchRun(ctx context.Context, w *Worker, ws string, batch int) (int, error) {
	tx, err := db.BeginTenant(ctx, w.Pool, ws)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	n, err := TaskBackfillRun(ctx, tx, ws, batch)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// TaskBackfillRun indexes up to batch pages of the transaction's workspace that have no index state.
func TaskBackfillRun(ctx context.Context, tx pgx.Tx, ws string, batch int) (int, error) {
	rows, err := tx.Query(ctx,
		`select n.id::text from core.notes n where n.deleted_at is null
		   and not exists (select 1 from core.note_task_state s where s.note_id = n.id) order by n.id limit $1`, batch)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := notes.IndexTasks(ctx, tx, ws, id, nil, false); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
