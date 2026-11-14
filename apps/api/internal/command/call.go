package command

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/db"
)

// Call runs another command inside the same transaction with the full gate (role, scopes, rate limit, agent
// groups, usage), like Rust `ctx.call`. The result is the serialized response.
func (c *Ctx) Call(name string, input any) (json.RawMessage, error) {
	b, err := Marshal(input)
	if err != nil {
		return nil, ErrInvalid(err.Error())
	}
	out, err := c.reg.run(c, name, b, ExecOptions{})
	return out, err
}

// Bind attaches this registry to a context whose tenant transaction is owned by the caller.
func (r *Registry) Bind(c *Ctx) { c.reg = r }

// WithTx runs fn in one tenant transaction (committed when fn succeeds) with this registry bound, so fn can use
// Ctx.Call. Used where Rust opens its own Ctx outside a command (MCP resources).
func (r *Registry) WithTx(ctx context.Context, pool *pgxpool.Pool, p Principal, fn func(*Ctx) error) error {
	tx, err := db.BeginTenant(ctx, pool, p.WorkspaceID)
	if err != nil {
		return DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c := &Ctx{Context: ctx, Tx: tx, Principal: p, reg: r}
	if err := fn(c); err != nil {
		return err
	}
	return DB(tx.Commit(ctx))
}
