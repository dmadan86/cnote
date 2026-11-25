package worker

import (
	"context"

	"github.com/cnote/api/internal/db"
)

const a2BackfillBatch = 500

// A2DeletionBackfill gives every page deleted before docs A2 (migration 1334) its own deletion_id, so Trash can list it
// (docs-parity 7.10.7). Batches of 500, one small transaction each, skipping rows another writer holds. Such a page has no
// restore evidence (deleted_restricted_by stays NULL): only an admin may restore it. New deletions set the columns themselves.
func A2DeletionBackfill(ctx context.Context, w *Worker) error {
	_, err := A2DeletionBackfillRun(ctx, w, a2BackfillBatch, 50)
	return err
}

func A2DeletionBackfillRun(ctx context.Context, w *Worker, batch, maxBatches int) (int64, error) {
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, ws := range ids {
		for i := 0; i < maxBatches; i++ {
			n, err := a2BackfillBatchRun(ctx, w, ws, batch)
			if err != nil {
				return total, err
			}
			total += n
			if n < int64(batch) {
				break
			}
		}
	}
	return total, nil
}

func a2BackfillBatchRun(ctx context.Context, w *Worker, ws string, batch int) (int64, error) {
	tx, err := db.BeginTenant(ctx, w.Pool, ws)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx,
		`update core.notes set deletion_id = gen_random_uuid()
		 where id in (select id from core.notes where deleted_at is not null and deletion_id is null order by id limit $1 for update skip locked)`, batch)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}
