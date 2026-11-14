package billing

import (
	"github.com/cnote/api/internal/command"
)

// ResolveBranch is the branch a new invoice or expense belongs to: the one asked for (it must exist in this
// workspace), else the caller's own branch, else none.
func ResolveBranch(c *command.Ctx, explicit *string) (*string, error) {
	var id *string
	if explicit != nil {
		if err := c.Tx.QueryRow(c.Context, "select id::text from core.branches where id = $1::uuid", *explicit).Scan(&id); err != nil {
			return nil, command.ErrInvalid("branch not found")
		}
		return id, nil
	}
	if c.Principal.UserID == nil {
		return nil, nil
	}
	if err := c.Tx.QueryRow(c.Context, "select branch_id::text from core.memberships where user_id = $1::uuid", *c.Principal.UserID).Scan(&id); err != nil {
		return nil, nil // not a member row (system caller): no branch
	}
	return id, nil
}
