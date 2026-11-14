package auth

// Brute-force throttling: failures per key (account hash, client IP, MFA user) are counted in core.auth_throttle,
// shared by every API instance. After `free` failures the key locks for base * 2^(extra failures), capped; a
// quiet hour forgets the count. Checks run before any password hashing so a locked key costs the server nothing.

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const (
	throttleBase = 30 * time.Second
	throttleCap  = 15 * time.Minute
)

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

// Thresholds (failures allowed before the first lock). Env overrides exist for deployments behind shared NAT.
func freeAccount() int { return envInt("AUTH_ACCOUNT_FREE", 5) }
func freeIP() int      { return envInt("AUTH_IP_FREE", 20) }
func freeSignup() int  { return envInt("AUTH_SIGNUP_FREE", 200) }

// ThrottledError is a 429 carrying how long to wait; the text is generic so it says nothing about the account.
func throttled(wait time.Duration) *command.Error {
	e := command.NewError(command.RateLimited, "too many attempts, try again later")
	e.Details = map[string]int{"retry_after": int(wait.Seconds()) + 1}
	return e
}

// locked returns the longest remaining lock among keys.
func (a *Auth) locked(ctx context.Context, keys ...string) (time.Duration, error) {
	var secs float64
	err := a.Pool.QueryRow(ctx, `select coalesce(max(extract(epoch from locked_until - now())), 0)::float8 from core.auth_throttle
		where key = any($1) and locked_until > now()`, keys).Scan(&secs)
	return time.Duration(secs * float64(time.Second)), command.DB(err)
}

// fail records one failure and, past `free`, locks the key. It returns the lock length (0 when still free).
func (a *Auth) fail(ctx context.Context, key string, free int) (time.Duration, error) {
	var fails int
	err := a.Pool.QueryRow(ctx, `insert into core.auth_throttle (key, fails) values ($1, 1)
		on conflict (key) do update set
		  fails = case when core.auth_throttle.window_start < now() - interval '1 hour' and coalesce(core.auth_throttle.locked_until < now(), true) then 1 else core.auth_throttle.fails + 1 end,
		  window_start = case when core.auth_throttle.window_start < now() - interval '1 hour' and coalesce(core.auth_throttle.locked_until < now(), true) then now() else core.auth_throttle.window_start end,
		  updated_at = now()
		returning fails`, key).Scan(&fails)
	if err != nil || fails < free {
		return 0, command.DB(err)
	}
	d := throttleBase
	for i := free; i < fails && d < throttleCap; i++ {
		d *= 2
	}
	d = min(d, throttleCap)
	_, err = a.Pool.Exec(ctx, "update core.auth_throttle set locked_until = now() + make_interval(secs => $2) where key = $1", key, d.Seconds())
	return d, command.DB(err)
}

func (a *Auth) forget(ctx context.Context, key string) {
	_, _ = a.Pool.Exec(ctx, "delete from core.auth_throttle where key = $1", key)
}

// auditLockout writes auth.locked_out to the audit trail of every workspace the person belongs to. Unknown accounts and
// bare IP locks have no workspace, so they only reach the server log.
func (a *Auth) auditLockout(ctx context.Context, userID, scope string, d time.Duration) {
	slog.Warn("auth lockout", "scope", scope, "user", userID, "seconds", d.Seconds())
	if userID == "" {
		return
	}
	wss, err := a.UserWorkspaces(ctx, userID)
	if err != nil {
		return
	}
	for _, w := range wss {
		tx, err := db.BeginTenant(ctx, a.Pool, w.ID)
		if err != nil {
			continue
		}
		_, _ = tx.Exec(ctx, `insert into core.events (workspace_id, kind, actor_type, actor_id, payload) values ($1::uuid, 'auth.locked_out', 'system', null, $2)`,
			w.ID, `{"user_id":"`+userID+`","scope":"`+scope+`","seconds":`+strconv.Itoa(int(d.Seconds()))+`}`)
		_ = tx.Commit(ctx)
	}
}

// CheckSignup counts a sign-up attempt for the client IP and refuses once the allowance is spent.
func (a *Auth) CheckSignup(ctx context.Context, ip string) error {
	key := "signup:" + ip
	if d, err := a.locked(ctx, key); err != nil || d > 0 {
		if err != nil {
			return err
		}
		return throttled(d)
	}
	d, err := a.fail(ctx, key, freeSignup())
	if err == nil && d > 0 {
		a.auditLockout(ctx, "", "signup_ip", d)
	}
	return err
}
