package docs

import (
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// Manual archive (docs-parity-b 5.5). It sets the columns the freshness sweep sets, on the page and its live sub-pages, under the
// same space lock as the A2 subtree commands (lock order: space lock, then rows by id).

const (
	maxArchivePages = 500
	restrictedMsg   = "Some pages here are restricted" // notes.RestrictedMessage: names, counts and hints at nothing
)

func init() { b2Handlers["doc.archive"] = docArchive }

func lockTree(c *command.Ctx, space string) error {
	_, err := c.Tx.Exec(c.Context, "select pg_advisory_xact_lock(hashtextextended('notes.tree:' || $1::text, 0))", space)
	return command.DB(err)
}

type treePage struct {
	id       string
	plain    bool
	archived bool
}

// liveSubtree is the page and its live descendants, at most maxArchivePages, root included.
func liveSubtree(c *command.Ctx, root string) ([]treePage, error) {
	rows, err := c.Tx.Query(c.Context,
		`with recursive s as (
		   select id, cardinality(types) = 0 plain, freshness = 'archived' arch from core.notes where id = $1::uuid and deleted_at is null
		   union
		   select c.id, cardinality(c.types) = 0, c.freshness = 'archived' from core.notes c join s on c.parent_id = s.id where c.deleted_at is null)
		 select id::text, plain, arch from s order by id limit $2`, root, maxArchivePages+1)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []treePage
	for rows.Next() {
		var p treePage
		if err := rows.Scan(&p.id, &p.plain, &p.archived); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if len(out) > maxArchivePages {
		return nil, command.ErrInvalid("This page has too many sub-pages for one step; move or delete them in parts")
	}
	return out, nil
}

// editableAll: every id gives the caller edit access. Space write access is the caller's job.
func editableAll(c *command.Ctx, ids []string) (bool, error) {
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return false, err
	}
	uid, admin := access.Viewer(c)
	var bad int
	err = c.Tx.QueryRow(c.Context,
		`select count(*) from unnest($1::text[]::uuid[]) t(id) where core.note_access(t.id, $2::uuid, $3::bool, $4::text[]::uuid[]) <> 'edit'`,
		ids, uid, admin, spaces).Scan(&bad)
	return bad == 0, command.DB(err)
}

func docArchive(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(input, &in) != nil || in.ID == "" {
		return nil, command.ErrInvalid("id must be a UUID")
	}
	space, err := access.AssertNoteAccessArchivedOK(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if err := lockTree(c, space); err != nil {
		return nil, err
	}
	sub, err := liveSubtree(c, in.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(sub))
	for i, p := range sub {
		ids[i] = p.id
		if !p.plain {
			return nil, command.ErrInvalid("Only plain pages can be archived")
		}
	}
	if ok, err := editableAll(c, ids); err != nil {
		return nil, err
	} else if !ok {
		return nil, command.ErrForbidden(restrictedMsg)
	}
	if _, err := c.Tx.Exec(c.Context, "select 1 from core.notes where id = any($1::text[]::uuid[]) order by id for update", ids); err != nil {
		return nil, command.DB(err)
	}
	rows, err := c.Tx.Query(c.Context,
		`update core.notes set freshness = 'archived', archived_at = now()
		 where id = any($1::text[]::uuid[]) and freshness <> 'archived' returning id::text`, ids)
	if err != nil {
		return nil, command.DB(err)
	}
	done, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, command.DB(err)
	}
	if len(done) == 0 {
		return nil, command.ErrConflict("page is already archived")
	}
	for _, id := range done {
		id := id
		if _, err := c.Emit("doc.archived", &id, map[string]any{"reason": "manual", "root_id": in.ID}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"archived": len(done)}, nil
}

// restoreChildren brings back the archived pages below root that the caller can edit; the others are skipped without a word.
func restoreChildren(c *command.Ctx, root string) (int, error) {
	var space string
	if err := c.Tx.QueryRow(c.Context, "select space_id::text from core.notes where id = $1::uuid", root).Scan(&space); err != nil {
		return 0, command.DB(err)
	}
	if err := lockTree(c, space); err != nil {
		return 0, err
	}
	sub, err := liveSubtree(c, root)
	if err != nil {
		return 0, err
	}
	var ids []string
	for _, p := range sub {
		if p.archived && p.id != root {
			ids = append(ids, p.id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return 0, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select id::text from core.notes where id = any($1::text[]::uuid[])
		   and core.note_access(id, $2::uuid, $3::bool, $4::text[]::uuid[]) = 'edit' order by id for update`, ids, uid, admin, spaces)
	if err != nil {
		return 0, command.DB(err)
	}
	ok, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, command.DB(err)
	}
	if len(ok) == 0 {
		return 0, nil
	}
	if _, err := c.Tx.Exec(c.Context,
		`update core.notes set freshness = 'stale', archived_at = null, lapse_count = 0, verify_by = $2::date
		 where id = any($1::text[]::uuid[]) and freshness = 'archived'`, ok, todayPlus(lapseGraceDays)); err != nil {
		return 0, command.DB(err)
	}
	for _, id := range ok {
		id := id
		if _, err := c.Tx.Exec(c.Context,
			`insert into docs.freshness_flags (workspace_id, note_id, reason, source_note_id, source_event_id)
			 values ($1::uuid,$2::uuid,'date_lapsed',null,null)`, c.WorkspaceID(), id); err != nil {
			return 0, command.DB(err)
		}
		if _, err := c.Emit("doc.restored", &id, map[string]any{"root_id": root}); err != nil {
			return 0, err
		}
	}
	return len(ok), nil
}
