// Package security is the workspace sign-in policy (WP18): require MFA for admins and owners, and OpenID Connect
// single sign-on with an optional "enforce" switch. Go-only, owner-only, people only (never an agent or key): the policy
// decides who may sign in. The provider's client secret is encrypted with CNOTE_MASTER_KEY and never returned.
package security

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

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
	// Domains decide who SSO admits and enforces on, so changing them is owner-only like security.update.
	dom := func(name string, g command.Group, h command.Handler, min command.Role, confirm bool) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole, c.Confirm = min, confirm
		return c
	}
	return []command.Command{get, upd,
		dom("security.domain_list", command.GroupRead, domainList, command.Admin, false),
		dom("security.domain_add", command.GroupAdmin, domainAdd, command.Owner, true),
		dom("security.domain_verify", command.GroupAdmin, domainVerify, command.Owner, true),
		dom("security.domain_remove", command.GroupAdmin, domainRemove, command.Owner, true)}
}

type policy struct {
	RequireMFA bool
	SSOEnabled bool
	Issuer     string
	ClientID   string
	SecretEnc  []byte
	Enforce    bool
}

func load(c *command.Ctx) (policy, error) {
	var p policy
	var iss, cid *string
	err := c.Tx.QueryRow(c.Context, `select require_mfa_admins, sso_enabled, sso_issuer, sso_client_id, sso_client_secret_enc, sso_enforce
		from core.workspace_security where workspace_id = $1::uuid`, c.WorkspaceID()).Scan(&p.RequireMFA, &p.SSOEnabled, &iss, &cid, &p.SecretEnc, &p.Enforce)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, command.DB(err)
	}
	p.Issuer, p.ClientID = deref(iss), deref(cid)
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
		"has_client_secret": len(p.SecretEnc) > 0, "enforce": p.Enforce}}
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
	doms, err := domains(c)
	if err != nil {
		return nil, err
	}
	var slug string
	if err := c.Tx.QueryRow(c.Context, "select slug from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&slug); err != nil {
		return nil, command.DB(err)
	}
	return view(p, map[string]any{"domains": doms, "admins_without_mfa": without, "sso_login_path": "/api/auth/sso/start?workspace=" + slug, "sso_callback_path": "/api/auth/sso/callback"}), rows.Err()
}

type ssoIn struct {
	Enabled      *bool   `json:"enabled"`
	Issuer       *string `json:"issuer"`
	ClientID     *string `json:"client_id"`
	ClientSecret *string `json:"client_secret"`
	Enforce      *bool   `json:"enforce"`
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
			var verified int
			if err := c.Tx.QueryRow(c.Context, "select count(*) from core.sso_domains where workspace_id = $1::uuid and verified_at is not null", c.WorkspaceID()).Scan(&verified); err != nil {
				return nil, command.DB(err)
			}
			if p.Issuer == "" || p.ClientID == "" || len(p.SecretEnc) == 0 || verified == 0 {
				return nil, command.ErrInvalid("single sign-on needs an issuer URL, client id, client secret and at least one verified domain (security.domain_add, then security.domain_verify)")
			}
			if err := auth.ValidateIssuer(c.Context, p.Issuer); err != nil {
				return nil, command.ErrInvalid("the identity provider could not be reached: " + err.Error())
			}
		} else if p.Enforce {
			return nil, command.ErrInvalid("enforcing single sign-on needs it to be enabled")
		}
		changed = append(changed, "sso")
	}
	_, err = c.Tx.Exec(c.Context, `insert into core.workspace_security (workspace_id, require_mfa_admins, sso_enabled, sso_issuer, sso_client_id, sso_client_secret_enc, sso_enforce)
		values ($1::uuid,$2,$3,nullif($4,''),nullif($5,''),$6,$7)
		on conflict (workspace_id) do update set require_mfa_admins = excluded.require_mfa_admins, sso_enabled = excluded.sso_enabled, sso_issuer = excluded.sso_issuer,
		  sso_client_id = excluded.sso_client_id, sso_client_secret_enc = excluded.sso_client_secret_enc,
		  sso_enforce = excluded.sso_enforce, updated_at = now()`,
		c.WorkspaceID(), p.RequireMFA, p.SSOEnabled, p.Issuer, p.ClientID, p.SecretEnc, p.Enforce)
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

// LookupTXT resolves TXT records; tests replace it so no real DNS is used.
var LookupTXT = func(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

type domainRow struct {
	Domain     string  `json:"domain"`
	Token      string  `json:"token"`
	Record     string  `json:"record"`
	RecordName string  `json:"record_name"`
	Verified   bool    `json:"verified"`
	VerifiedAt *string `json:"verified_at"`
}

func domains(c *command.Ctx) ([]domainRow, error) {
	rows, err := c.Tx.Query(c.Context, "select domain, token, to_char(verified_at at time zone 'utc', 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"') from core.sso_domains where workspace_id = $1::uuid order by domain", c.WorkspaceID())
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []domainRow{}
	for rows.Next() {
		var d domainRow
		if err := rows.Scan(&d.Domain, &d.Token, &d.VerifiedAt); err != nil {
			return nil, command.DB(err)
		}
		d.Verified, d.Record, d.RecordName = d.VerifiedAt != nil, "cnote-verification="+d.Token, "_cnote."+d.Domain
		out = append(out, d)
	}
	return out, rows.Err()
}

func domainList(c *command.Ctx, _ json.RawMessage) (any, error) { return domains(c) }

func domainArg(b json.RawMessage) (string, error) {
	var in struct {
		Domain string `json:"domain"`
	}
	if json.Unmarshal(b, &in) != nil {
		return "", command.ErrInvalid("input must be a JSON object with domain")
	}
	d := strings.ToLower(strings.TrimSpace(in.Domain))
	if !auth.ValidDomain(d) {
		return "", command.ErrInvalid("domain " + d + " is not valid: use your company domain, not a public mailbox provider")
	}
	return d, nil
}

// domainAdd registers a domain and returns the TXT record that proves it: cnote-verification=<token> at the domain or
// at _cnote.<domain>. Until that record is seen the domain is ignored by single sign-on.
func domainAdd(c *command.Ctx, b json.RawMessage) (any, error) {
	d, err := domainArg(b)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	if _, err := c.Tx.Exec(c.Context, "insert into core.sso_domains (workspace_id, domain, token) values ($1::uuid,$2,$3) on conflict do nothing", c.WorkspaceID(), d, hex.EncodeToString(buf)); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("security.domain_added", nil, map[string]any{"domain": d}); err != nil {
		return nil, err
	}
	all, err := domains(c)
	for _, r := range all {
		if r.Domain == d {
			return r, err
		}
	}
	return nil, err
}

func domainVerify(c *command.Ctx, b json.RawMessage) (any, error) {
	d, err := domainArg(b)
	if err != nil {
		return nil, err
	}
	var token string
	if err := c.Tx.QueryRow(c.Context, "select token from core.sso_domains where workspace_id = $1::uuid and domain = $2", c.WorkspaceID(), d).Scan(&token); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("domain")
		}
		return nil, command.DB(err)
	}
	ctx, cancel := context.WithTimeout(c.Context, 5*time.Second)
	defer cancel()
	want, found := "cnote-verification="+token, false
	for _, name := range []string{d, "_cnote." + d} {
		txt, _ := LookupTXT(ctx, name) // a failed lookup is just "not found"
		for _, t := range txt {
			found = found || strings.TrimSpace(t) == want
		}
	}
	if !found {
		return nil, command.ErrInvalid("the TXT record was not found: add " + want + " as a TXT record at " + d + " or _cnote." + d + ", then try again (DNS can take a while)")
	}
	if _, err := c.Tx.Exec(c.Context, "update core.sso_domains set verified_at = coalesce(verified_at, now()) where workspace_id = $1::uuid and domain = $2", c.WorkspaceID(), d); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("security.domain_verified", nil, map[string]any{"domain": d}); err != nil {
		return nil, err
	}
	all, err := domains(c)
	for _, r := range all {
		if r.Domain == d {
			return r, err
		}
	}
	return nil, err
}

func domainRemove(c *command.Ctx, b json.RawMessage) (any, error) {
	d, err := domainArg(b)
	if err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context, "delete from core.sso_domains where workspace_id = $1::uuid and domain = $2", c.WorkspaceID(), d)
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() == 1 {
		if _, err := c.Emit("security.domain_removed", nil, map[string]any{"domain": d}); err != nil {
			return nil, err
		}
	}
	return map[string]bool{"removed": tag.RowsAffected() == 1}, nil
}
