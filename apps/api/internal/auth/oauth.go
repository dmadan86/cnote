package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

// PublicBase mirrors cnote_mcp::public_base: PUBLIC_BASE_URL (set, even empty) wins, else a loopback Host is echoed,
// else there is no issuer and OAuth is unavailable (a spoofed Host can never mint issuer or resource URLs).
func PublicBase(host string) (string, bool) {
	if v, ok := os.LookupEnv("PUBLIC_BASE_URL"); ok {
		return strings.TrimRight(v, "/"), true
	}
	if host == "" {
		return "", false
	}
	name := host
	if rest, ok := strings.CutPrefix(host, "["); ok {
		name, _, _ = strings.Cut(rest, "]")
	} else {
		name, _, _ = strings.Cut(host, ":")
	}
	if name == "localhost" || name == "127.0.0.1" || name == "::1" {
		return "http://" + host, true
	}
	return "", false
}

// MCPResource mirrors cnote_mcp::mcp_resource: the audience OAuth tokens are issued for, "" without a base.
func MCPResource(host string) string {
	if b, ok := PublicBase(host); ok {
		return b + "/mcp"
	}
	return ""
}

// BearerToken is the `Authorization: Bearer` value, trimmed (cnote_mcp::authenticate); ok=false for no or non-text header.
func BearerToken(h http.Header) (string, bool) {
	v := h.Get("Authorization")
	for i := 0; i < len(v); i++ {
		if v[i] < 32 && v[i] != '\t' || v[i] > 126 { // HeaderValue::to_str accepts visible ASCII only
			return "", false
		}
	}
	return bearer(h)
}

// SessionUser is the person behind a session cookie or `cs_` bearer (session_user in api.rs).
type SessionUser struct{ ID, Email, Name string }

func (a *Auth) SessionUser(ctx context.Context, h http.Header) (SessionUser, error) {
	token := sessionToken(h)
	if token == "" {
		return SessionUser{}, command.NewError(command.Unauthenticated, "sign in required")
	}
	var u SessionUser
	err := a.Pool.QueryRow(ctx,
		`select u.id::text, u.email, u.name from core.sessions s join core.users u on u.id = s.user_id
		 where s.id = $1 and s.expires_at > now()`, sha256Hex(token)).Scan(&u.ID, &u.Email, &u.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, command.NewError(command.Unauthenticated, "session expired")
	}
	return u, command.DB(err)
}

// Workspace is one row of core.user_workspaces.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

func (a *Auth) UserWorkspaces(ctx context.Context, userID string) ([]Workspace, error) {
	rows, err := a.Pool.Query(ctx, "select id::text, name, slug, role from core.user_workspaces($1::uuid)", userID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.Role); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, w)
	}
	return out, command.DB(rows.Err())
}

// UserPrincipal is the person's principal in a workspace; Forbidden when they are not (or no longer) a member.
func (a *Auth) UserPrincipal(ctx context.Context, userID, workspaceID string) (command.Principal, error) {
	return a.userPrincipal(ctx, userID, workspaceID)
}

// PrincipalForRequest is PrincipalFor plus native OAuth access tokens (`cno_`), which need the request Host to know
// the audience they must have been issued for (principal_for in api.rs).
func (a *Auth) PrincipalForRequest(ctx context.Context, h http.Header, host, workspaceID string) (command.Principal, error) {
	if key, ok := bearer(h); ok && strings.HasPrefix(key, oauthAccessPrefix) {
		p, ok, err := a.MCPPrincipal(ctx, h, MCPResource(host))
		if err != nil {
			return p, err
		}
		if !ok {
			return p, command.NewError(command.Unauthenticated, "invalid API key or access token")
		}
		if p.WorkspaceID != workspaceID {
			return p, command.ErrForbidden("API key belongs to another workspace")
		}
		return p, nil
	}
	return a.PrincipalFor(ctx, h, workspaceID)
}
