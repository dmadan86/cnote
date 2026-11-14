package company

import (
	"encoding/json"
	"errors"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/jackc/pgx/v5"
)

// Branches: a name, an address and an optional GSTIN. Invoices raised from a branch with a GSTIN print that GSTIN
// and use its state as the supplier state (billing.loadSeller); without one they use the workspace GSTIN.
func branchCommands() []command.Command {
	nc := command.NewCommand
	admin := func(c command.Command) command.Command { c.MinRole = command.Admin; return c }
	del := nc("branch.delete", command.GroupDestructive, branchDelete)
	del.Confirm = true
	list := nc("branch.list", command.GroupRead, branchList)
	list.MinRole = command.Member
	return []command.Command{list, admin(nc("branch.create", command.GroupAdmin, branchCreate)), admin(nc("branch.update", command.GroupAdmin, branchUpdate)), admin(del)}
}

const branchCols = `b.id::text, b.name, b.address, b.gstin, b.state_code,
	(select count(*) from core.memberships m where m.branch_id = b.id and m.deactivated_at is null)`

func scanBranch(r pgx.Row) (map[string]any, error) {
	var id, name string
	var addr, gstin, state *string
	var n int64
	if err := r.Scan(&id, &name, &addr, &gstin, &state, &n); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "name": name, "address": addr, "gstin": gstin, "state_code": state, "members": n}, nil
}

func branchList(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select "+branchCols+" from core.branches b order by b.name")
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		x, err := scanBranch(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, x)
	}
	return out, command.DB(rows.Err())
}

// branchFields validates what was sent; a present empty string clears address or gstin.
func branchFields(m in, creating bool) (name, addr, gstin string, hasName, hasAddr, hasGSTIN bool, err error) {
	if name, hasName, err = m.str("name", 80); err != nil {
		return
	}
	if (creating || hasName) && name == "" {
		err = command.ErrInvalid("name is required")
		return
	}
	if addr, hasAddr, err = m.str("address", 300); err != nil {
		return
	}
	if gstin, hasGSTIN, err = m.str("gstin", 15); err != nil {
		return
	}
	if gstin != "" {
		gstin, err = billing.ValidateGSTIN(gstin)
	}
	return
}

func branchCreate(c *command.Ctx, b json.RawMessage) (any, error) {
	if err := human(c); err != nil {
		return nil, err
	}
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	name, addr, gstin, _, _, _, err := branchFields(m, true)
	if err != nil {
		return nil, err
	}
	var id string
	err = c.Tx.QueryRow(c.Context, `insert into core.branches (workspace_id, name, address, gstin, state_code)
		values ($1::uuid, $2, nullif($3,''), nullif($4,''), nullif(left($4, 2),'')) on conflict (workspace_id, name) do nothing returning id::text`,
		c.WorkspaceID(), name, addr, gstin).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrConflict("a branch named " + name + " already exists")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err = c.Emit("branch.created", nil, map[string]any{"id": id, "name": name}); err != nil {
		return nil, err
	}
	return scanBranchByID(c, id)
}

func scanBranchByID(c *command.Ctx, id string) (any, error) {
	x, err := scanBranch(c.Tx.QueryRow(c.Context, "select "+branchCols+" from core.branches b where b.id = $1::uuid", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("branch")
	}
	return x, command.DB(err)
}

func branchUpdate(c *command.Ctx, b json.RawMessage) (any, error) {
	if err := human(c); err != nil {
		return nil, err
	}
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("id", true)
	if err != nil {
		return nil, err
	}
	name, addr, gstin, hasName, hasAddr, hasGSTIN, err := branchFields(m, false)
	if err != nil {
		return nil, err
	}
	if !hasName && !hasAddr && !hasGSTIN {
		return nil, command.ErrInvalid("nothing to update")
	}
	var taken bool
	if hasName {
		if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from core.branches where name = $1 and id <> $2::uuid)", name, id).Scan(&taken); err != nil {
			return nil, command.DB(err)
		}
		if taken {
			return nil, command.ErrConflict("a branch named " + name + " already exists")
		}
	}
	tag, err := c.Tx.Exec(c.Context, `update core.branches set name = case when $2 then $3 else name end,
		address = case when $4 then nullif($5,'') else address end,
		gstin = case when $6 then nullif($7,'') else gstin end, state_code = case when $6 then nullif(left($7, 2),'') else state_code end
		where id = $1::uuid`, id, hasName, name, hasAddr, addr, hasGSTIN, gstin)
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, command.ErrNotFound("branch")
	}
	if _, err = c.Emit("branch.updated", nil, map[string]any{"id": id}); err != nil {
		return nil, err
	}
	return scanBranchByID(c, id)
}

func branchDelete(c *command.Ctx, b json.RawMessage) (any, error) {
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("id", true)
	if err != nil {
		return nil, err
	}
	// Issued invoices are immutable, so a branch that raised one is kept (rename it instead).
	var issued bool
	if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from billing.invoices where branch_id = $1::uuid and status <> 'draft')", id).Scan(&issued); err != nil {
		return nil, command.DB(err)
	}
	if issued {
		return nil, command.ErrConflict("this branch has issued invoices and cannot be deleted; rename it instead")
	}
	// Members, drafts and expenses keep their rows; the FK only clears their branch.
	tag, err := c.Tx.Exec(c.Context, "delete from core.branches where id=$1::uuid", id)
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, command.ErrNotFound("branch")
	}
	_, err = c.Emit("branch.deleted", nil, map[string]any{"id": id})
	return map[string]any{"deleted": true}, err
}
