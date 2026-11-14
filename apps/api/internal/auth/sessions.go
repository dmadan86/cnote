package auth

// Device sessions (WP23): who is signed in where. The client's address and user agent are recorded when a session is
// created (the server puts them on the request context); last_seen_at is refreshed at most every few minutes so the
// hot request path does not write on every call.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

type clientKey struct{}

type client struct{ ip, ua string }

// WithClient records the caller's address and user agent for any session created under ctx.
func WithClient(ctx context.Context, ip, ua string) context.Context {
	if len(ua) > 300 {
		ua = ua[:300]
	}
	return context.WithValue(ctx, clientKey{}, client{ip, ua})
}

func clientOf(ctx context.Context) client {
	c, _ := ctx.Value(clientKey{}).(client)
	return c
}

// seenEvery is how stale last_seen_at may get before a request refreshes it.
const seenEvery = 5 * time.Minute

func (a *Auth) touch(ctx context.Context, tokenHash string) {
	_, _ = a.Pool.Exec(context.WithoutCancel(ctx), "update core.sessions set last_seen_at = now() where id = $1", tokenHash)
}

// SessionInfo is one row of the device list. ID is the public handle, never the credential's hash.
type SessionInfo struct {
	ID        string     `json:"id"`
	UserAgent string     `json:"user_agent"`
	IP        string     `json:"ip"`
	AMR       string     `json:"amr"`
	CreatedAt time.Time  `json:"created_at"`
	LastSeen  *time.Time `json:"last_seen_at"`
	Current   bool       `json:"current"`
}

func (a *Auth) ListSessions(ctx context.Context, userID, currentToken string) ([]SessionInfo, error) {
	rows, err := a.Pool.Query(ctx, `select public_id::text, coalesce(user_agent,''), coalesce(ip,''), amr, created_at, last_seen_at, id = $2
		from core.sessions where user_id = $1::uuid and expires_at > now() order by coalesce(last_seen_at, created_at) desc`, userID, sha256Hex(currentToken))
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var s SessionInfo
		if err := rows.Scan(&s.ID, &s.UserAgent, &s.IP, &s.AMR, &s.CreatedAt, &s.LastSeen, &s.Current); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, s)
	}
	return out, command.DB(rows.Err())
}

// RevokeSession ends one of the person's other sessions (the current one is ended by signing out).
func (a *Auth) RevokeSession(ctx context.Context, userID, currentToken, publicID string) error {
	var id string
	err := a.Pool.QueryRow(ctx, "select id from core.sessions where public_id::text = $1 and user_id = $2::uuid", strings.ToLower(publicID), userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("session")
	}
	if err != nil {
		return command.DB(err)
	}
	if id == sha256Hex(currentToken) {
		return command.ErrInvalid("this is the session you are using: sign out instead")
	}
	_, err = a.Pool.Exec(ctx, "delete from core.sessions where id = $1", id)
	return command.DB(err)
}

// RevokeOtherSessions ends every session of the person except the current one and returns how many.
func (a *Auth) RevokeOtherSessions(ctx context.Context, userID, currentToken string) (int64, error) {
	tag, err := a.Pool.Exec(ctx, "delete from core.sessions where user_id = $1::uuid and id <> $2", userID, sha256Hex(currentToken))
	return tag.RowsAffected(), command.DB(err)
}
