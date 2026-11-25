package tables

import (
	"errors"
	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

type Role string

const (
	RoleNone   Role = "none"
	RoleRead   Role = "read"
	RoleWrite  Role = "write"
	RoleManage Role = "manage"
)

func loadBase(c *command.Ctx, id string) (Base, error) {
	var b Base
	err := c.Tx.QueryRow(c.Context, `select id::text, workspace_id::text, space_id::text, name from tables.bases where workspace_id=$1::uuid and id=$2::uuid and deleted_at is null`, c.WorkspaceID(), id).Scan(&b.ID, &b.WorkspaceID, &b.SpaceID, &b.Name)
	return b, command.DB(err)
}
func loadTable(c *command.Ctx, id string) (Table, error) {
	var t Table
	err := c.Tx.QueryRow(c.Context, `select id::text, workspace_id::text, base_id::text, name from tables.tables where workspace_id=$1::uuid and id=$2::uuid and deleted_at is null`, c.WorkspaceID(), id).Scan(&t.ID, &t.WorkspaceID, &t.BaseID, &t.Name)
	return t, command.DB(err)
}
func loadFields(c *command.Ctx, tableID string) ([]Field, error) {
	rows, err := c.Tx.Query(c.Context, `select id::text, workspace_id::text, table_id::text, name, type, config, position, locked from tables.fields where workspace_id=$1::uuid and table_id=$2::uuid and deleted_at is null order by position, id`, c.WorkspaceID(), tableID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	fields := []Field{}
	for rows.Next() {
		var f Field
		if err := rows.Scan(&f.ID, &f.WorkspaceID, &f.TableID, &f.Name, &f.Type, &f.Config, &f.Position, &f.Locked); err != nil {
			return nil, command.DB(err)
		}
		fields = append(fields, f)
	}
	return fields, command.DB(rows.Err())
}

// rank orders roles so a command can ask for "at least write".
func (r Role) rank() int {
	switch r {
	case RoleRead:
		return 1
	case RoleWrite:
		return 2
	case RoleManage:
		return 3
	}
	return 0
}
func (r Role) AtLeast(min Role) bool { return r.rank() >= min.rank() }

// spaceRole is the one resolver (design 17.3): workspace role, space visibility and space membership, the same
// inputs access.AssertSpaceAccess uses, plus space admin. RoleNone means "not found" (a private space the caller
// is not in); nothing here reveals that the space exists.
//
//	workspace owner/admin           manage
//	guest in the space              read   (a guest outside the space: none)
//	space viewer                    read
//	space admin                     manage
//	space editor / any member of an open space   write
func spaceRole(c *command.Ctx, spaceID string) (Role, error) {
	if err := access.AssertSpaceAccess(c, spaceID, false); err != nil {
		var ce *command.Error
		if errors.As(err, &ce) && ce.Code == command.NotFound {
			return RoleNone, nil
		}
		return RoleNone, err
	}
	if c.Principal.Role >= command.Admin {
		return RoleManage, nil
	}
	var role *string
	err := c.Tx.QueryRow(c.Context, `select role from core.space_members where workspace_id=$1::uuid and space_id=$2::uuid and user_id=$3::uuid`, c.WorkspaceID(), spaceID, c.Principal.UserID).Scan(&role)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RoleNone, command.DB(err)
	}
	switch {
	case c.Principal.Role == command.Guest:
		return RoleRead, nil // AssertSpaceAccess already refused a guest who is not a member
	case role != nil && *role == "admin":
		return RoleManage, nil
	case role != nil && *role == "viewer":
		return RoleRead, nil
	}
	return RoleWrite, nil
}

// baseRole loads a live base and returns the caller's role in it. A missing base is a not-found error.
func baseRole(c *command.Ctx, baseID string) (Role, error) {
	b, err := loadBase(c, baseID)
	if err != nil {
		return RoleNone, err
	}
	return spaceRole(c, b.SpaceID)
}

// assertBases is the mixed-id bulk check: every base must exist and give the caller at least min, or the whole
// call is refused before anything is written. A base the caller cannot see and a base that does not exist look
// the same (not found), so the error never says which ids were real. Read-only but visible: forbidden.
func assertBases(c *command.Ctx, baseIDs []string, min Role) error {
	seen := map[string]bool{}
	denied := false
	for _, id := range baseIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		r, err := baseRole(c, id)
		var ce *command.Error
		if errors.As(err, &ce) && ce.Code == command.NotFound {
			r, err = RoleNone, nil
		}
		if err != nil {
			return err
		}
		if r == RoleNone {
			return command.ErrNotFound("base")
		}
		if !r.AtLeast(min) {
			denied = true
		}
	}
	if denied {
		return command.ErrForbidden("you do not have permission for this change in one of these bases")
	}
	return nil
}
