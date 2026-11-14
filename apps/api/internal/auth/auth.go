// Package auth resolves the caller of a workspace-scoped request: a bearer API key (agents, MCP clients)
// or a session (cookie cn_session, or `cs_` bearer for native apps). Same SQL as crates/cnote-core/src/auth.rs
// and `principal_for` in crates/cnote-server/src/api.rs.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const (
	SessionCookie       = "cn_session"
	nativeSessionPrefix = "cs_"
	oauthAccessPrefix   = "cno_"
)

type Auth struct{ Pool *pgxpool.Pool }

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func cookie(h http.Header, name string) string {
	for _, line := range h.Values("Cookie") {
		for _, kv := range strings.Split(line, ";") {
			if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok && k == name {
				return v
			}
		}
	}
	return ""
}

func bearer(h http.Header) (string, bool) {
	v, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(v), ok
}

func sessionToken(h http.Header) string {
	if b, ok := bearer(h); ok {
		if t, ok := strings.CutPrefix(b, nativeSessionPrefix); ok {
			return t
		}
	}
	return cookie(h, SessionCookie)
}

// PrincipalFor mirrors principal_for. OAuth access tokens (cno_) depend on the Rust OAuth tables and the
// public base URL, so they return command.ErrFallback and the request is proxied until oauth is ported.
func (a *Auth) PrincipalFor(ctx context.Context, h http.Header, workspaceID string) (command.Principal, error) {
	if key, ok := bearer(h); ok && !strings.HasPrefix(key, nativeSessionPrefix) {
		if strings.HasPrefix(key, oauthAccessPrefix) {
			return command.Principal{}, command.ErrFallback
		}
		p, err := a.principalFromAPIKey(ctx, key)
		if err != nil {
			return p, err
		}
		if p.WorkspaceID == "" {
			return p, command.NewError(command.Unauthenticated, "invalid API key or access token")
		}
		if p.WorkspaceID != workspaceID {
			return p, command.ErrForbidden("API key belongs to another workspace")
		}
		return p, nil
	}
	token := sessionToken(h)
	if token == "" {
		return command.Principal{}, command.NewError(command.Unauthenticated, "sign in required")
	}
	var userID string
	err := a.Pool.QueryRow(ctx,
		`select u.id::text from core.sessions s join core.users u on u.id = s.user_id
		 where s.id = $1 and s.expires_at > now()`, sha256Hex(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.Principal{}, command.NewError(command.Unauthenticated, "session expired")
	}
	if err != nil {
		return command.Principal{}, command.DB(err)
	}
	return a.userPrincipal(ctx, userID, workspaceID)
}

func (a *Auth) userPrincipal(ctx context.Context, userID, workspaceID string) (command.Principal, error) {
	tx, err := db.BeginTenant(ctx, a.Pool, workspaceID)
	if err != nil {
		return command.Principal{}, command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var role string
	err = tx.QueryRow(ctx, "select role from core.memberships where user_id = $1::uuid", userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.Principal{}, command.ErrForbidden("not a member of this workspace")
	}
	if err != nil {
		return command.Principal{}, command.DB(err)
	}
	return command.Principal{Kind: command.KindUser, UserID: &userID, WorkspaceID: workspaceID, Role: command.ParseRole(role)}, nil
}

// principalFromAPIKey returns a zero Principal (empty WorkspaceID) when the key is unknown. Agents are
// capped by their owner's role and never act as owner.
func (a *Auth) principalFromAPIKey(ctx context.Context, key string) (command.Principal, error) {
	if !strings.HasPrefix(key, "cn_") {
		return command.Principal{}, nil
	}
	var ws, user string
	var agent *string
	var scopes []string
	err := a.Pool.QueryRow(ctx,
		"select workspace_id::text, user_id::text, agent_id::text, scopes from core.resolve_api_key($1)", sha256Hex(key)).
		Scan(&ws, &user, &agent, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.Principal{}, nil
	}
	if err != nil {
		return command.Principal{}, command.DB(err)
	}
	owner, err := a.userPrincipal(ctx, user, ws)
	if err != nil {
		return command.Principal{}, nil // owner left the workspace: key is dead (Rust: Ok(None))
	}
	p := command.Principal{Kind: command.KindAPIKey, UserID: &user, AgentID: agent, WorkspaceID: ws, Role: min(owner.Role, command.Admin)}
	if agent != nil {
		p.Kind = command.KindAgent
	}
	if len(scopes) > 0 {
		p.Scopes = scopes
	}
	return p, nil
}
