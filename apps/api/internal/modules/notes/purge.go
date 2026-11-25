package notes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

// Trash purge and retention (docs C1, design 5.6 as revised by 13.3). purgeDeletion is the one place a deletion is hard-deleted,
// for note.purge and for the sweep. Irreversible: the only undo is a database backup.

const (
	DefaultRetentionDays = 30
	purgeBatchPerRun     = 200 // deletions per workspace per sweep run
	purgeLockTimeout     = "5s"
)

func init() {
	c1Handlers["note.purge"] = notePurge
	c1Handlers["trash.retention_get"] = retentionGet
	c1Handlers["trash.retention_set"] = retentionSet
}

// retention is the workspace's rule. Days 0 means never; From nil means purge is disabled (a workspace stamped by no build).
type retention struct {
	Days int
	From *time.Time
	// Running is true once a full period has passed since From, so the rule may remove anything old enough.
	Running bool
}

func readRetention(c *command.Ctx, lock bool) (retention, error) {
	clause := ""
	if lock {
		clause = " for share"
	}
	var r retention
	err := c.Tx.QueryRow(c.Context,
		`select coalesce((settings->>'trash_retention_days')::int, $2), trash_purge_from from core.workspaces where id = $1::uuid`+clause,
		c.WorkspaceID(), DefaultRetentionDays).Scan(&r.Days, &r.From)
	if err != nil {
		return r, command.DB(err)
	}
	if r.Days > 0 && r.From != nil {
		err = c.Tx.QueryRow(c.Context, "select now() >= $1::timestamptz + make_interval(days => $2)", *r.From, r.Days).Scan(&r.Running)
	}
	return r, command.DB(err)
}

func retentionOut(r retention) map[string]any {
	var from any
	if r.From != nil {
		from = command.Ts(*r.From)
	}
	return map[string]any{"days": r.Days, "purge_from": from}
}

func retentionGet(c *command.Ctx, input json.RawMessage) (any, error) {
	r, err := readRetention(c, false)
	return retentionOut(r), err
}

func retentionSet(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	n, err := o.optInt("days", 32)
	if err != nil {
		return nil, err
	}
	if n == nil || (*n != 0 && *n != 30 && *n != 90 && *n != 365) {
		return nil, command.ErrInvalid("days must be 0, 30, 90 or 365")
	}
	days := int(*n)
	// the row lock (no key update, so page inserts are not held up) serialises two changes and waits for a purge holding it shared; the clock restarts only when the rule really changes
	var old int
	if err := c.Tx.QueryRow(c.Context,
		`select coalesce((settings->>'trash_retention_days')::int, $2) from core.workspaces where id = $1::uuid for no key update`,
		c.WorkspaceID(), DefaultRetentionDays).Scan(&old); err != nil {
		return nil, command.DB(err)
	}
	if old != days {
		if _, err := c.Tx.Exec(c.Context,
			`update core.workspaces set settings = settings || jsonb_build_object('trash_retention_days', $2::int), trash_purge_from = now() where id = $1::uuid`,
			c.WorkspaceID(), days); err != nil {
			return nil, command.DB(err)
		}
		if _, err := c.Emit("workspace.settings_updated", nil, map[string]any{"keys": []string{"trash_retention_days"}}); err != nil {
			return nil, err
		}
	}
	r, err := readRetention(c, false)
	return retentionOut(r), err
}

type purgeResult struct {
	Purged bool
	Skip   string // why a sweep left it alone
	Pages  int
	Files  int
	Bytes  int64
}

// purgeDeletion removes one deletion for good, in the caller's transaction. manual is a person's note.purge: the caller must have
// been able to list the deletion in Trash and be a workspace or space admin. Otherwise it is the sweep, which also needs the
// retention rule to allow it. Every rule is read again here, under the space's structural lock, so a restore that won the lock
// first (or a page edited since the sweep chose it) is simply not purged.
func purgeDeletion(c *command.Ctx, did string, manual bool) (purgeResult, error) {
	var res purgeResult
	var space string
	if err := c.Tx.QueryRow(c.Context, "select space_id::text from core.notes where deletion_id = $1::uuid limit 1", did).Scan(&space); err == pgx.ErrNoRows {
		res.Skip = "gone"
		return res, nil
	} else if err != nil {
		return res, command.DB(err)
	}
	if err := lockTree(c, space); err != nil {
		return res, err
	}
	// Serialize the archive check with space.archive, which updates this row.
	if _, err := c.Tx.Exec(c.Context, "select id from core.spaces where id = $1::uuid for share", space); err != nil {
		return res, command.DB(err)
	}
	if err := access.AssertSpaceWritable(c, space); err != nil {
		if manual {
			return res, err
		}
		res.Skip = "archived space"
		return res, nil
	}
	rt, err := readRetention(c, true)
	if err != nil {
		return res, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, title, space_id::text, parent_id::text, coalesce(cardinality(types), 0), deleted_at, deleted_by::text,
		        freshness = 'archived', deleted_at is not null and $3::int > 0 and deleted_at < now() - make_interval(days => $3::int)
		 from core.notes where deletion_id = $1::uuid and workspace_id = $2::uuid order by id for update`, did, c.WorkspaceID(), rt.Days)
	if err != nil {
		return res, command.DB(err)
	}
	var pages []trashPage
	var ids []string
	typed, archived, old := false, false, true
	for rows.Next() {
		var p trashPage
		var nt int
		var at *time.Time
		var ok, pageArchived bool
		if err := rows.Scan(&p.ID, &p.Title, &p.Space, &p.Parent, &nt, &at, &p.DeletedBy, &pageArchived, &ok); err != nil {
			rows.Close()
			return res, command.DB(err)
		}
		if at == nil {
			ok = false
		} else {
			p.DeletedAt = *at
		}
		typed = typed || nt > 0
		archived = archived || pageArchived
		old = old && ok
		pages = append(pages, p)
		ids = append(ids, p.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, command.DB(err)
	}
	if len(pages) == 0 {
		res.Skip = "gone" // a restore (or another purge) got the lock first
		return res, nil
	}
	for _, p := range pages {
		if p.DeletedAt.IsZero() {
			res.Skip = "not deleted"
			return res, nil
		}
	}
	if manual {
		ps, err := loadDeletions(c, []string{did})
		if err != nil {
			return res, err
		}
		if ok, err := listable(c, ps[did], map[string]bool{}); err != nil {
			return res, err
		} else if !ok {
			return res, command.ErrNotFound("note") // a deletion the caller could not list does not exist for them
		}
		if ok, err := canManage(c, space, nil); err != nil {
			return res, err
		} else if !ok {
			return res, command.ErrForbidden("only workspace and space admins can delete pages for good")
		}
	} else if rt.Days == 0 || !rt.Running || !old {
		res.Skip = "not due"
		return res, nil
	}
	if archived {
		res.Skip = "archived page"
		if manual {
			return res, access.AssertPageNotArchived(true)
		}
		return res, nil
	}
	if typed { // business records are never purged: their cascade would destroy invoices, deals and items
		res.Skip = "typed page"
		return res, nil
	}
	if pend, err := pendingPages(c, ids); err != nil {
		return res, err
	} else if len(pend) > 0 {
		res.Skip = "unsaved edits"
		if manual {
			return res, command.ErrConflict(blockedSaving)
		}
		return res, nil
	}
	var source bool
	if err := c.Tx.QueryRow(c.Context,
		"select exists(select 1 from docs.template_bindings where source_id = any($1::text[]::uuid[]) and note_id <> all($1::text[]::uuid[]))", ids).Scan(&source); err != nil {
		return res, command.DB(err)
	}
	if source {
		res.Skip = "template source"
		if manual {
			return res, command.ErrConflict("Other pages were made from a page in this deletion. Remove their template link first.")
		}
		return res, nil
	}
	var links int
	if err := c.Tx.QueryRow(c.Context,
		`select (select count(*) from core.attachments where note_id = any($1::text[]::uuid[])),
		        (select coalesce(sum(size_bytes), 0) from core.attachments where note_id = any($1::text[]::uuid[])),
		        (select count(*) from core.public_links where note_id = any($1::text[]::uuid[]))`, ids).Scan(&res.Files, &res.Bytes, &links); err != nil {
		return res, command.DB(err)
	}
	root := deletionRoot(pages)
	reason, days := "retention", rt.Days
	if manual {
		reason = "manual"
	}
	deletedBy := any(nil)
	if root.DeletedBy != nil {
		deletedBy = *root.DeletedBy
	}
	// The audit event holds ids and the root title, never a page body; note_id is null because the page goes.
	if _, err := c.Emit("note.purged", nil, map[string]any{
		"deletion_id": did, "space_id": space, "root_id": root.ID, "root_title": root.Title, "page_ids": ids, "count": len(ids),
		"files": res.Files, "bytes": res.Bytes, "public_links": links, "reason": reason, "retention_days": days,
		"deleted_at": command.Ts(root.DeletedAt), "deleted_by": deletedBy,
	}); err != nil {
		return res, err
	}
	// Cascades remove everything owned by the pages; page files queue their objects through the attachments_gc trigger.
	if _, err := c.Tx.Exec(c.Context, "delete from core.notes where id = any($1::text[]::uuid[])", ids); err != nil {
		return res, command.DB(err)
	}
	res.Purged, res.Pages = true, len(ids)
	return res, nil
}

func notePurge(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	did, err := o.reqUUID("deletion_id")
	if err != nil {
		return nil, err
	}
	res, err := purgeDeletion(c, did, true)
	if err != nil {
		return nil, err
	}
	if !res.Purged {
		return nil, command.ErrNotFound("note") // restored or purged by someone else first
	}
	return map[string]any{"purged": true, "pages": res.Pages, "files": res.Files, "bytes": res.Bytes}, nil
}

// PurgeDue is the sweep for one workspace: up to purgeBatchPerRun due deletions, oldest first, each in its own short
// transaction. A deletion that fails or times out on the lock is logged and left for the next run.
func PurgeDue(ctx context.Context, pool *pgxpool.Pool, ws string) (int, error) {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return 0, err
	}
	var days int
	var running bool
	err = tx.QueryRow(ctx,
		`select d, trash_purge_from is not null and d > 0 and now() >= trash_purge_from + make_interval(days => d)
		 from (select coalesce((settings->>'trash_retention_days')::int, $2) d, trash_purge_from from core.workspaces where id = $1::uuid) w`,
		ws, DefaultRetentionDays).Scan(&days, &running)
	var dids []string
	if err == nil && running {
		var rows pgx.Rows
		if rows, err = tx.Query(ctx,
			`select deletion_id::text from core.notes where workspace_id = $1::uuid and deletion_id is not null and deleted_at is not null
			 group by deletion_id having max(deleted_at) < now() - make_interval(days => $2::int)
			 and (max(purge_skipped_at) is null or max(purge_skipped_at) < now() - interval '1 day')
			 order by min(deleted_at), deletion_id limit $3`, ws, days, purgeBatchPerRun); err == nil {
			dids, err = pgx.CollectRows(rows, pgx.RowTo[string])
		}
	}
	_ = tx.Rollback(context.WithoutCancel(ctx))
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, did := range dids {
		if ctx.Err() != nil {
			break
		}
		done, err := purgeOne(ctx, pool, ws, did)
		if err != nil {
			slog.Error("trash purge failed", "workspace", ws, "deletion", did, "error", err)
			errs = append(errs, fmt.Errorf("deletion %s: %w", did, err))
		}
		if done {
			n++
		}
	}
	return n, errors.Join(errs...)
}

func purgeOne(ctx context.Context, pool *pgxpool.Pool, ws, did string) (bool, error) {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "set local lock_timeout = '"+purgeLockTimeout+"'"); err != nil {
		return false, err
	}
	c := &command.Ctx{Context: ctx, Tx: tx, Pool: pool, Principal: command.Principal{Kind: command.KindSystem, WorkspaceID: ws, Role: command.Owner}}
	res, err := purgeDeletion(c, did, false)
	if err != nil {
		return false, err
	}
	if !res.Purged {
		if _, err := tx.Exec(ctx, "update core.notes set purge_skipped_at = now() where workspace_id = $1::uuid and deletion_id = $2::uuid", ws, did); err != nil {
			return false, err
		}
	}
	return res.Purged, tx.Commit(ctx)
}
