// Package security is the workspace sign-in policy (WP18): require MFA for admins and owners, and OpenID Connect
// single sign-on with an optional "enforce" switch. Go-only, owner-only, people only (never an agent or key): the policy
// decides who may sign in. The provider's client secret is encrypted with CNOTE_MASTER_KEY and never returned.
package security

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/secretbox"
)

func Commands() []command.Command {
	get := command.NewCommand("security.get", command.GroupRead, get)
	get.MinRole = command.Admin
	upd := command.NewCommand("security.update", command.GroupAdmin, update)
	upd.MinRole, upd.Confirm = command.Owner, true
	return []command.Command{get, upd}
}

type policy struct {
	RequireMFA bool
	SSOEnabled bool
	Issuer     string
	ClientID   string
	SecretEnc  []byte
	Domains    []string
	Enforce    bool
}

func load(c *command.Ctx) (policy, error) {
	var p policy
	var iss, cid *string
	err := c.Tx.QueryRow(c.Context, `select require_mfa_admins, sso_enabled, sso_issuer, sso_client_id, sso_client_secret_enc, sso_allowed_domains, sso_enforce
		from core.workspace_security where workspace_id = $1::uuid`, c.WorkspaceID()).Scan(&p.RequireMFA, &p.SSOEnabled, &iss, &cid, &p.SecretEnc, &p.Domains, &p.Enforce)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, command.DB(err)
	}
	p.Issuer, p.ClientID = deref(iss), deref(cid)
	if p.Domains == nil {
		p.Domains = []string{}
	}
	return p, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func view(p policy, extra map[string]any) map[string]any {
	out := map[string]any{"require_mfa_admins": p.RequireMFA, "sso": map[string]any{"enabled": p.SSOEnabled, "issuer": p.Issuer, "client_id": p.ClientID,
		"has_client_secret": len(p.SecretEnc) > 0, "allowed_domains": p.Domains, "enforce": p.Enforce}}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func get(c *command.Ctx, _ json.RawMessage) (any, error) {
	p, err := load(c)
	if err != nil {
		return nil, err
	}
	// admins and owners who could not sign in once the MFA rule is on
	rows, err := c.Tx.Query(c.Context, `select u.id::text, u.email from core.memberships m join core.users u on u.id = m.user_id
		where m.deactivated_at is null and m.role in ('admin','owner')
		  and not exists (select 1 from core.user_mfa f where f.user_id = u.id and f.confirmed_at is not null) order by u.email`)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	without := []map[string]string{}
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			return nil, command.DB(err)
		}
		without = append(without, map[string]string{"user_id": id, "email": email})
	}
	var slug string
	if err := c.Tx.QueryRow(c.Context, "select slug from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&slug); err != nil {
		return nil, command.DB(err)
	}
	return view(p, map[string]any{"admins_without_mfa": without, "sso_login_path": "/api/auth/sso/start?workspace=" + slug, "sso_callback_path": "/api/auth/sso/callback"}), rows.Err()
}

type ssoIn struct {
	Enabled        *bool     `json:"enabled"`
	Issuer         *string   `json:"issuer"`
	ClientID       *string   `json:"client_id"`
	ClientSecret   *string   `json:"client_secret"`
	AllowedDomains *[]string `json:"allowed_domains"`
	Enforce        *bool     `json:"enforce"`
}

func update(c *command.Ctx, b json.RawMessage) (any, error) {
	var in struct {
		RequireMFA *bool  `json:"require_mfa_admins"`
		SSO        *ssoIn `json:"sso"`
	}
	if json.Unmarshal(b, &in) != nil {
		return nil, command.ErrInvalid("input must be a JSON object with require_mfa_admins and/or sso")
	}
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return nil, command.ErrForbidden("sign-in policy is changed by a person")
	}
	p, err := load(c)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	if in.RequireMFA != nil && *in.RequireMFA != p.RequireMFA {
		if *in.RequireMFA {
			var on bool
			if err := c.Tx.QueryRow(c.Context, "select exists (select 1 from core.user_mfa where user_id = $1::uuid and confirmed_at is not null)", *c.Principal.UserID).Scan(&on); err != nil {
				return nil, command.DB(err)
			}
			if !on {
				return nil, command.ErrInvalid("turn on multi-factor authentication for your own account first, or you would lock yourself out")
			}
		}
		p.RequireMFA = *in.RequireMFA
		changed = append(changed, "require_mfa_admins")
	}
	if s := in.SSO; s != nil {
		if s.Enabled != nil {
			p.SSOEnabled = *s.Enabled
		}
		if s.Issuer != nil {
			p.Issuer = strings.TrimSpace(*s.Issuer)
		}
		if s.ClientID != nil {
			p.ClientID = strings.TrimSpace(*s.ClientID)
		}
		if s.AllowedDomains != nil {
			p.Domains = []string{}
			for _, d := range *s.AllowedDomains {
				d = strings.ToLower(strings.TrimSpace(d))
				if !auth.ValidDomain(d) {
					return nil, command.ErrInvalid("allowed domain " + d + " is not valid: use your company domain, not a public mailbox provider")
				}
				if !slices.Contains(p.Domains, d) {
					p.Domains = append(p.Domains, d)
				}
			}
			if len(p.Domains) > 20 {
				return nil, command.ErrInvalid("at most 20 allowed domains")
			}
		}
		if s.ClientSecret != nil && *s.ClientSecret != "" {
			key, err := secretbox.MasterKey()
			if err != nil {
				return nil, err
			}
			if p.SecretEnc, err = secretbox.Encrypt(key, c.WorkspaceID(), *s.ClientSecret); err != nil {
				return nil, err
			}
		}
		if s.Enforce != nil {
			p.Enforce = *s.Enforce
		}
		if p.SSOEnabled {
			if p.Issuer == "" || p.ClientID == "" || len(p.SecretEnc) == 0 || len(p.Domains) == 0 {
				return nil, command.ErrInvalid("single sign-on needs an issuer URL, client id, client secret and at least one allowed domain")
			}
			if err := auth.ValidateIssuer(c.Context, p.Issuer); err != nil {
				return nil, command.ErrInvalid("the identity provider could not be reached: " + err.Error())
			}
		} else if p.Enforce {
			return nil, command.ErrInvalid("enforcing single sign-on needs it to be enabled")
		}
		changed = append(changed, "sso")
	}
	_, err = c.Tx.Exec(c.Context, `insert into core.workspace_security (workspace_id, require_mfa_admins, sso_enabled, sso_issuer, sso_client_id, sso_client_secret_enc, sso_allowed_domains, sso_enforce)
		values ($1::uuid,$2,$3,nullif($4,''),nullif($5,''),$6,$7,$8)
		on conflict (workspace_id) do update set require_mfa_admins = excluded.require_mfa_admins, sso_enabled = excluded.sso_enabled, sso_issuer = excluded.sso_issuer,
		  sso_client_id = excluded.sso_client_id, sso_client_secret_enc = excluded.sso_client_secret_enc, sso_allowed_domains = excluded.sso_allowed_domains,
		  sso_enforce = excluded.sso_enforce, updated_at = now()`,
		c.WorkspaceID(), p.RequireMFA, p.SSOEnabled, p.Issuer, p.ClientID, p.SecretEnc, p.Domains, p.Enforce)
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("security.updated", nil, map[string]any{"changed": changed, "require_mfa_admins": p.RequireMFA, "sso_enabled": p.SSOEnabled, "sso_enforce": p.Enforce}); err != nil {
		return nil, err
	}
	return view(p, nil), nil
}

// CheckPromotion refuses to make someone an admin or owner without MFA when the workspace requires it.
func CheckPromotion(c *command.Ctx, userID, role string) error {
	if role != "admin" && role != "owner" {
		return nil
	}
	var blocked bool
	err := c.Tx.QueryRow(c.Context, `select coalesce((select require_mfa_admins from core.workspace_security where workspace_id = $1::uuid), false)
		and not exists (select 1 from core.user_mfa where user_id = $2::uuid and confirmed_at is not null)`, c.WorkspaceID(), userID).Scan(&blocked)
	if err != nil {
		return command.DB(err)
	}
	if blocked {
		return command.ErrInvalid("this workspace requires multi-factor authentication for admins: ask this person to turn it on in Account security before promoting them")
	}
	return nil
}
