package notifications

import (
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/secretbox"
)

func init() {
	b1Handlers["notify_prefs.get"] = prefsGet
	b1Handlers["notify_prefs.set"] = prefsSet
}

// prefsOut reads the caller's row; no row means the defaults. Mail also needs the master key (the unsubscribe link signs with it).
func prefsOut(c *command.Ctx, uid string) (map[string]any, error) {
	mode, hour, directOff, digestOff := "smart", 8, false, false
	err := c.Tx.QueryRow(c.Context, `select email_mode, digest_hour::int, direct_off, digest_off from core.notify_prefs where user_id = $1::uuid`, uid).
		Scan(&mode, &hour, &directOff, &digestOff)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	_, keyErr := secretbox.MasterKey()
	return map[string]any{"email_mode": mode, "digest_hour": hour, "direct_off": directOff, "digest_off": digestOff,
		"email_configured": mailer.Configured() && keyErr == nil}, nil
}

func prefsGet(c *command.Ctx, _ json.RawMessage) (any, error) {
	uid, err := me(c)
	if err != nil {
		return nil, err
	}
	return prefsOut(c, uid)
}

func prefsSet(c *command.Ctx, input json.RawMessage) (any, error) {
	uid, err := me(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		Mode      *string `json:"email_mode"`
		Hour      *int    `json:"digest_hour"`
		DirectOff *bool   `json:"direct_off"`
		DigestOff *bool   `json:"digest_off"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil, command.ErrInvalid("invalid input")
	}
	if in.Mode != nil && *in.Mode != "smart" && *in.Mode != "digest" && *in.Mode != "off" {
		return nil, command.ErrInvalid("email_mode must be smart, digest or off")
	}
	if in.Hour != nil && (*in.Hour < 0 || *in.Hour > 23) {
		return nil, command.ErrInvalid("digest_hour must be 0 to 23")
	}
	// An upsert that keeps what was not sent.
	if _, err := c.Tx.Exec(c.Context, `insert into core.notify_prefs (workspace_id, user_id, email_mode, digest_hour, direct_off, digest_off)
		values ($1::uuid, $2::uuid, coalesce($3::text, 'smart'), coalesce($4::int, 8), coalesce($5::bool, false), coalesce($6::bool, false))
		on conflict (workspace_id, user_id) do update set email_mode = coalesce($3::text, core.notify_prefs.email_mode),
		  digest_hour = coalesce($4::int, core.notify_prefs.digest_hour), direct_off = coalesce($5::bool, core.notify_prefs.direct_off),
		  digest_off = coalesce($6::bool, core.notify_prefs.digest_off), updated_at = now()`,
		c.WorkspaceID(), uid, in.Mode, in.Hour, in.DirectOff, in.DigestOff); err != nil {
		return nil, err
	}
	return prefsOut(c, uid)
}
