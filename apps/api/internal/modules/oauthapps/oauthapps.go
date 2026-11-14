// Package oauthapps: "connected apps", the OAuth grants a person approved in this workspace, and revoking them
// (crates/cnote-mcp/src/apps.rs). People only: an agent must not list or revoke its own (or another app's) access.
package oauthapps

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/cnote/api/internal/command"
)

func Commands() []command.Command {
	return []command.Command{
		command.NewCommand("oauth_app.list", command.GroupAdmin, list),
		command.NewCommand("oauth_app.revoke", command.GroupAdmin, revoke),
	}
}

func person(c *command.Ctx) (string, error) {
	if c.Principal.Kind == command.KindUser && c.Principal.UserID != nil {
		return *c.Principal.UserID, nil
	}
	return "", command.ErrForbidden("only people can manage connected apps")
}

type app struct {
	ID         string      `json:"id"`
	ClientID   string      `json:"client_id"`
	Name       string      `json:"name"`
	CreatedAt  command.Ts  `json:"created_at"`
	LastUsedAt *command.Ts `json:"last_used_at"`
}

func list(c *command.Ctx, input json.RawMessage) (any, error) {
	var obj map[string]json.RawMessage // `Empty {}` accepts any object and ignores fields
	if json.Unmarshal(input, &obj) != nil || obj == nil {
		return nil, command.ErrFallback
	}
	user, err := person(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select g.id::text, g.client_id, c.name, g.created_at, g.last_used_at
		from core.oauth_grants g join core.oauth_clients c on c.client_id = g.client_id
		where g.user_id = $1::uuid and g.workspace_id = $2::uuid and g.revoked_at is null
		order by g.created_at desc`, user, c.WorkspaceID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []app{}
	for rows.Next() {
		var a app
		var created time.Time
		var used *time.Time
		if err := rows.Scan(&a.ID, &a.ClientID, &a.Name, &created, &used); err != nil {
			return nil, err
		}
		a.CreatedAt, a.LastUsedAt = command.Ts(created), command.TsPtr(used)
		out = append(out, a)
	}
	return out, rows.Err()
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func revoke(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		ID *string `json:"id"`
	}
	if json.Unmarshal(input, &in) != nil || in.ID == nil || !uuidRe.MatchString(*in.ID) {
		return nil, command.ErrFallback
	}
	user, err := person(c)
	if err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context, `update core.oauth_grants set revoked_at = now()
		where id = $1::uuid and user_id = $2::uuid and workspace_id = $3::uuid and revoked_at is null`, *in.ID, user, c.WorkspaceID())
	if err != nil {
		return nil, err
	}
	n := tag.RowsAffected()
	if n == 1 {
		if _, err := c.Tx.Exec(c.Context, "delete from core.oauth_tokens where grant_id = $1::uuid", *in.ID); err != nil {
			return nil, err
		}
		if _, err := c.Emit("oauth_app.revoked", nil, map[string]any{"grant_id": *in.ID}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"revoked": n == 1}, nil
}
