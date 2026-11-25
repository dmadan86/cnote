package tables

import (
	"context"
	"strings"

	"github.com/cnote/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	purgeAfterDays = 30
	purgeBatch     = 1000
	purgeRounds    = 50 // batches per workspace per run; the sweep is daily, so a huge trash drains over days
)

// PurgeSweep hard-deletes what has been in the trash for 30 days, per workspace and in small batches: records,
// fields (stripping their key from the records first), tables (and tables of trashed bases), bases, then old
// history, batch lists and finished imports. Links go with their records and fields by cascade.
func PurgeSweep(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, "select core.all_workspace_ids()::text")
	if err != nil {
		return err
	}
	wss, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, ws := range wss {
		if err := purgeWorkspace(ctx, pool, ws); err != nil {
			return err
		}
	}
	return nil
}

func purgeWorkspace(ctx context.Context, pool *pgxpool.Pool, ws string) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Lock order is base, then table, then record: take the bases that hold files first (in id order), so the cascades
	// below delete files under the same base lock that every counter change needs, and reconcile the counters at the end.
	if _, err := attachmentBases(ctx, tx, true); err != nil {
		return err
	}
	cut := "now() - make_interval(days => $1)"
	// records, in batches
	for i := 0; i < purgeRounds; i++ {
		tag, err := tx.Exec(ctx, `delete from tables.records where (workspace_id, table_id, id) in (select workspace_id, table_id, id from tables.records where deleted_at < `+cut+` limit $2)`, purgeAfterDays, purgeBatch)
		if err != nil {
			return err
		}
		if tag.RowsAffected() < purgeBatch {
			break
		}
	}
	// fields: strip the key from the table's records, then drop the field (its links cascade)
	frows, err := tx.Query(ctx, `select id::text, table_id::text from tables.fields where deleted_at < `+cut+` limit 100`, purgeAfterDays)
	if err != nil {
		return err
	}
	type fld struct{ id, table string }
	var fs []fld
	for frows.Next() {
		var f fld
		if err := frows.Scan(&f.id, &f.table); err != nil {
			frows.Close()
			return err
		}
		fs = append(fs, f)
	}
	frows.Close()
	for _, f := range fs {
		clean := true
		for i := 0; i < purgeRounds; i++ {
			tag, err := tx.Exec(ctx, `update tables.records set data = data - $1::text where (workspace_id, table_id, id) in (select workspace_id, table_id, id from tables.records where table_id=$2::uuid and data ? $1::text limit $3)`, f.id, f.table, purgeBatch)
			if err != nil {
				return err
			}
			if tag.RowsAffected() < purgeBatch {
				break
			}
			if i == purgeRounds-1 {
				clean = false
			}
		}
		if clean {
			if _, err := tx.Exec(ctx, `update tables.tables set primary_field_id = null where primary_field_id=$1::uuid`, f.id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `delete from tables.fields where id=$1::uuid`, f.id); err != nil {
				return err
			}
		}
	}
	// records of trashed tables go in batches (the table row stays until it is empty)
	if _, err := tx.Exec(ctx, `delete from tables.records where (workspace_id, table_id, id) in (select r.workspace_id, r.table_id, r.id from tables.records r join tables.tables t on t.id=r.table_id
		where t.deleted_at < `+cut+` or exists (select 1 from tables.bases b where b.id=t.base_id and b.deleted_at < `+cut+`) limit $2)`, purgeAfterDays, purgeBatch*purgeRounds/5); err != nil {
		return err
	}
	// tables (own trash or trashed base): empty them in batches, then drop the empty ones
	if _, err := tx.Exec(ctx, `delete from tables.tables t where (t.deleted_at < `+cut+` or exists (select 1 from tables.bases b where b.id=t.base_id and b.deleted_at < `+cut+`))
		and not exists (select 1 from tables.records r where r.table_id=t.id)`, purgeAfterDays); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from tables.bases b where b.deleted_at < `+cut+` and not exists (select 1 from tables.tables t where t.base_id=b.id)`, purgeAfterDays); err != nil {
		return err
	}
	for _, q := range []string{
		`delete from tables.record_history where at < now() - make_interval(days => ` + itoa(historyRetention) + `)`,
		`delete from tables.batches where created_at < ` + cut,
		`delete from tables.imports where created_at < ` + cut,
	} {
		args := []any{}
		if strings.Contains(q, "$1") {
			args = append(args, purgeAfterDays)
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return err
		}
	}
	if err := ReconcileAttachmentBytes(ctx, tx, ws); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
