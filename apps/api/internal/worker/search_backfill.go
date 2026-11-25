package worker

import (
	"context"

	"github.com/cnote/api/internal/db"
)

const (
	searchBackfillBatch      = 200
	searchBackfillMaxBatches = 50 // per workspace per run; the sweep runs every minute, so a big workspace catches up over a few runs
)

// SearchPlainBackfill fills core.notes.search_plain for rows that predate migration 1331 (the trigger fills every
// new or edited page). One small transaction per batch, skipping rows another writer holds, so it never blocks editing.
func SearchPlainBackfill(ctx context.Context, w *Worker) error {
	_, err := SearchPlainBackfillRun(ctx, w, searchBackfillBatch, searchBackfillMaxBatches)
	return err
}

func SearchPlainBackfillRun(ctx context.Context, w *Worker, batch, maxBatches int) (int64, error) {
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, ws := range ids {
		for i := 0; i < maxBatches; i++ {
			n, err := searchPlainBatch(ctx, w, ws, batch)
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

func searchPlainBatch(ctx context.Context, w *Worker, ws string, batch int) (int64, error) {
	tx, err := db.BeginTenant(ctx, w.Pool, ws)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx,
		`update core.notes set search_plain = core.note_search_vector(title, content_md)
		 where id in (select id from core.notes where search_plain is null and deleted_at is null order by id limit $1 for update skip locked)`, batch)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}
