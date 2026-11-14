// Package db: pool and tenant-scoped transactions (Rust: crates/cnote-db). The app connects as the
// non-owner role cnote_app, so row-level security applies; never connect with the admin URL here.
package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, url string, max int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = max
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return pool, pool.Ping(ctx)
}

// BeginTenant opens a transaction bound to one workspace. set_config(..., true) is transaction-local,
// so pooled connections never leak the tenant.
func BeginTenant(ctx context.Context, pool *pgxpool.Pool, workspaceID string) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "select set_config('app.workspace_id', $1, true)", workspaceID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
