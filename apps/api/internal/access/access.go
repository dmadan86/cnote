// Package access is the port of crates/cnote-core/src/access.rs: space-level access on top of RLS,
// page-level access via the SQL function core.note_access.
package access

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

// ArchivedMessage is the refusal for any write into an archived space (design 13.2.2). Archived is read-only for everyone,
// workspace admins included, whatever route reached the page: space access, a direct share, the gateway, a comment.
const ArchivedMessage = "This space is archived"

func AssertSpaceAccess(c *command.Ctx, spaceID string, write bool) error {
	p := c.Principal
	var visibility string
	var spaceRole *string
	var archived bool
	err := c.Tx.QueryRow(c.Context,
		`select s.visibility, sm.role, s.archived_at is not null from core.spaces s
		 left join core.space_members sm on sm.space_id = s.id and sm.user_id = $2::uuid
		 where s.id = $1::uuid`, spaceID, p.UserID).Scan(&visibility, &spaceRole, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("space")
	}
	if err != nil {
		return command.DB(err)
	}
	if p.Role >= command.Admin {
		if write && archived {
			return command.ErrForbidden(ArchivedMessage)
		}
		return nil
	}
	if (visibility == "private" || p.Role == command.Guest) && spaceRole == nil {
		return command.ErrNotFound("space")
	}
	if write && archived {
		return command.ErrForbidden(ArchivedMessage)
	}
	if write && ((spaceRole != nil && *spaceRole == "viewer") || (p.Role == command.Guest && spaceRole == nil)) {
		return command.ErrForbidden("read-only access to this space")
	}
	return nil
}

// ReadableSpaceIDs lists the spaces the caller may read; filter list queries with `= any($n::text[]::uuid[])`.
func ReadableSpaceIDs(c *command.Ctx) ([]string, error) {
	p := c.Principal
	rows, err := c.Tx.Query(c.Context,
		`select s.id::text from core.spaces s
		 left join core.space_members sm on sm.space_id = s.id and sm.user_id = $1::uuid
		 where $2 or sm.user_id is not null or (s.visibility = 'open' and $3)`,
		p.UserID, p.Role >= command.Admin, p.Role != command.Guest)
	if err != nil {
		return nil, command.DB(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if ids == nil {
		ids = []string{}
	}
	return ids, command.DB(err)
}

// Viewer is the (user, is_admin) pair bound to core.note_access.
func Viewer(c *command.Ctx) (*string, bool) {
	return c.Principal.UserID, c.Principal.Role >= command.Admin
}

// NoteLevel is the caller's page-level access: none | view | edit.
func NoteLevel(c *command.Ctx, noteID string, spaces []string) (string, error) {
	uid, admin := Viewer(c)
	var level string
	err := c.Tx.QueryRow(c.Context, "select core.note_access($1::uuid,$2::uuid,$3,$4::text[]::uuid[])",
		noteID, uid, admin, spaces).Scan(&level)
	return level, command.DB(err)
}

// AssertNoteAccess returns the note's space id. A page the caller cannot read is not found; a read-only
// page is forbidden for writes. A shared page is readable without any other access to its space.
func AssertNoteAccess(c *command.Ctx, noteID string, write bool) (string, error) {
	var space string
	var archived bool
	err := c.Tx.QueryRow(c.Context,
		`select n.space_id::text, s.archived_at is not null from core.notes n join core.spaces s on s.id = n.space_id
		 where n.id = $1::uuid and n.deleted_at is null`, noteID).Scan(&space, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("note")
	}
	if err != nil {
		return "", command.DB(err)
	}
	spaces, err := ReadableSpaceIDs(c)
	if err != nil {
		return "", err
	}
	level, err := NoteLevel(c, noteID, spaces)
	if err != nil {
		return "", err
	}
	if level == "none" {
		return "", command.ErrNotFound("note")
	}
	for _, s := range spaces {
		if s == space {
			if err := AssertSpaceAccess(c, space, write); err != nil {
				return "", err
			}
			break
		}
	}
	// A page reached by a direct share is in a space the caller cannot read, so the loop above skipped it: the archive
	// rule must not depend on that.
	if write && archived {
		return "", command.ErrForbidden(ArchivedMessage)
	}
	if write && level != "edit" {
		return "", command.ErrForbidden("you can only view this page")
	}
	return space, nil
}

// AssertSpaceWritable refuses with ArchivedMessage when the space is archived. For code that writes into a space without
// going through AssertSpaceAccess (jobs, imports, derived data a command writes for someone else).
func AssertSpaceWritable(c *command.Ctx, spaceID string) error {
	var archived bool
	err := c.Tx.QueryRow(c.Context, "select archived_at is not null from core.spaces where id = $1::uuid", spaceID).Scan(&archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("space")
	}
	if err != nil {
		return command.DB(err)
	}
	if archived {
		return command.ErrForbidden(ArchivedMessage)
	}
	return nil
}

// AssertSpaceAdmin passes for a workspace admin or owner, and for a member whose row in core.space_members has role admin.
// It is about the space only: a page restriction still applies to a space admin (design 13.2.1), so it never replaces
// AssertNoteAccess. A space the caller cannot read is not found.
func AssertSpaceAdmin(c *command.Ctx, spaceID string) error {
	p := c.Principal
	if err := AssertSpaceAccess(c, spaceID, false); err != nil {
		return err
	}
	if p.Role >= command.Admin {
		return nil
	}
	var admin bool
	err := c.Tx.QueryRow(c.Context,
		"select exists(select 1 from core.space_members where space_id = $1::uuid and user_id = $2::uuid and role = 'admin')",
		spaceID, p.UserID).Scan(&admin)
	if err != nil {
		return command.DB(err)
	}
	if !admin {
		return command.ErrForbidden("only a space admin can do this")
	}
	return nil
}
