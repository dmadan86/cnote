package marketplace

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/admin"
)

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ExchangeToken is exchange_token in cnote-core marketplace.rs (POST /api/marketplace/token): app credentials plus
// the one-time install code become a `cn_` key for the app's agent. ok=false for every failure (the caller answers
// invalid_grant without detail); clientID must already be a canonical lowercase UUID.
func ExchangeToken(ctx context.Context, pool *pgxpool.Pool, clientID, clientSecret, code string) (out map[string]any, ok bool, err error) {
	var storedHash, name string
	err = pool.QueryRow(ctx, "select client_secret_hash, name from core.market_apps where id = $1::uuid", clientID).Scan(&storedHash, &name)
	missing := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !missing {
		return nil, false, command.DB(err)
	}
	// Hash the presented secret even for unknown apps so both paths cost the same.
	presented := sha256Hex(clientSecret)
	if missing || subtle.ConstantTimeCompare([]byte(storedHash), []byte(presented)) != 1 {
		return nil, false, nil
	}
	var install, ws string
	err = pool.QueryRow(ctx,
		`update core.app_install_codes set used_at = now()
		 where code_hash = $1 and used_at is null and expires_at > now() returning install_id::text, workspace_id::text`,
		sha256Hex(code)).Scan(&install, &ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, command.DB(err)
	}
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return nil, false, command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var agent, user string
	scopes := []string{}
	err = tx.QueryRow(ctx, "select agent_id::text, installed_by::text, scopes from core.app_installs where id = $1::uuid and app_id = $2::uuid",
		install, clientID).Scan(&agent, &user, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, command.DB(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, command.DB(err)
	}
	prefix, key, hash, err := admin.NewKey()
	if err != nil {
		return nil, false, err
	}
	ktx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return nil, false, command.DB(err)
	}
	defer func() { _ = ktx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := ktx.Exec(ctx, `insert into core.api_keys (workspace_id, user_id, agent_id, name, prefix, key_hash, scopes)
		values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7)`, ws, user, agent, "Marketplace: "+name, prefix, hash, scopes); err != nil {
		return nil, false, command.DB(err)
	}
	if err := ktx.Commit(ctx); err != nil {
		return nil, false, command.DB(err)
	}
	return map[string]any{"access_token": key, "token_type": "bearer", "workspace_id": ws, "scopes": scopes}, true, nil
}
