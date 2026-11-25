package command

import "encoding/json"

// Exists builds an internal, admin-only command that answers whether a module has any row of one kind, for the Home
// "Get set up" guide (setup.status). sql is `select 1 from ... where workspace_id = $1::uuid ...`; the owning
// module writes it, so no other module reads its tables. Result: {"exists": bool}.
func Exists(name, sql string) Command {
	c := NewCommand(name, GroupRead, func(c *Ctx, _ json.RawMessage) (any, error) {
		var ok bool
		err := c.Tx.QueryRow(c.Context, "select exists ("+sql+")", c.WorkspaceID()).Scan(&ok)
		return map[string]bool{"exists": ok}, DB(err)
	})
	c.MinRole = Admin
	c.Internal = true
	return c
}
