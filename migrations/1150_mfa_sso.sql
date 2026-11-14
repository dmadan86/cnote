-- WP18: sign-in hardening. Throttle state for password and MFA guesses, TOTP enrolment per person, hashed recovery
-- codes, short-lived MFA challenges, and per-workspace security policy (require MFA for admins, OIDC single sign-on).
-- Per-person tables hold no workspace_id (a person spans workspaces), like core.users and core.sessions.

-- How a session was proven: 'pwd' (password only), 'mfa' (password + TOTP or recovery code), 'sso' (OpenID Connect,
-- the identity provider did the authentication). sso_workspace_id pins an SSO session to the workspace whose provider
-- vouched for the person, so a workspace-owned provider cannot reach the person's other workspaces.
ALTER TABLE core.sessions ADD COLUMN amr text NOT NULL DEFAULT 'pwd' CHECK (amr IN ('pwd','mfa','sso'));
ALTER TABLE core.sessions ADD COLUMN sso_workspace_id uuid REFERENCES core.workspaces(id) ON DELETE CASCADE;

CREATE TABLE core.auth_throttle (
  key text PRIMARY KEY,                       -- 'acct:<sha256 of email>', 'ip:<ip>', 'mfa:<user>', 'signup:<ip>'
  fails int NOT NULL DEFAULT 0,
  locked_until timestamptz,
  window_start timestamptz NOT NULL DEFAULT now(), -- failures older than an hour are forgotten
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.user_mfa (
  user_id uuid PRIMARY KEY REFERENCES core.users(id) ON DELETE CASCADE,
  secret_enc bytea NOT NULL,                  -- secretbox (CNOTE_MASTER_KEY), bound to the user id
  confirmed_at timestamptz,                   -- null while enrolment is pending
  last_step bigint NOT NULL DEFAULT 0,        -- newest accepted 30 s step: a code is good once
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.mfa_recovery_codes (
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  code_hash text NOT NULL,                    -- sha256 of the code; 50 bits of randomness, single use
  used_at timestamptz,
  PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE core.mfa_challenges (
  id text PRIMARY KEY,                        -- sha256 of the token handed to the browser
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  attempts int NOT NULL DEFAULT 0,
  expires_at timestamptz NOT NULL
);

CREATE TABLE core.workspace_security (
  workspace_id uuid PRIMARY KEY REFERENCES core.workspaces(id) ON DELETE CASCADE,
  require_mfa_admins boolean NOT NULL DEFAULT false,
  sso_enabled boolean NOT NULL DEFAULT false,
  sso_issuer text,
  sso_client_id text,
  sso_client_secret_enc bytea,
  sso_allowed_domains text[] NOT NULL DEFAULT '{}',
  sso_enforce boolean NOT NULL DEFAULT false, -- members of an allowed domain cannot use a password
  updated_at timestamptz NOT NULL DEFAULT now()
);
SELECT core.enable_tenant_rls('core.workspace_security');

GRANT SELECT, INSERT, UPDATE, DELETE ON core.auth_throttle, core.user_mfa, core.mfa_recovery_codes, core.mfa_challenges, core.workspace_security TO cnote_app;

-- Sign-in happens before any tenant is known: these read the policy across workspaces, returning only what they must.
CREATE OR REPLACE FUNCTION core.sso_by_slug(p_slug text)
RETURNS TABLE(workspace_id uuid, issuer text, client_id text, client_secret_enc bytea, allowed_domains text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT s.workspace_id, s.sso_issuer, s.sso_client_id, s.sso_client_secret_enc, s.sso_allowed_domains
  FROM core.workspace_security s JOIN core.workspaces w ON w.id = s.workspace_id
  WHERE w.slug = p_slug AND s.sso_enabled AND s.sso_issuer IS NOT NULL AND s.sso_client_id IS NOT NULL AND s.sso_client_secret_enc IS NOT NULL
$$;

-- Does a workspace this person belongs to enforce SSO for their email domain? Owners keep password login as the way back in
-- when the identity provider is down or misconfigured.
CREATE OR REPLACE FUNCTION core.sso_enforced_for(p_user uuid, p_domain text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM core.memberships m JOIN core.workspace_security s ON s.workspace_id = m.workspace_id
    WHERE m.user_id = p_user AND m.deactivated_at IS NULL AND m.role <> 'owner' AND s.sso_enabled AND s.sso_enforce AND lower(p_domain) = ANY (s.sso_allowed_domains))
$$;

-- Is this person an admin or owner of a workspace that requires MFA? (login: tells the browser to send them to set it up)
CREATE OR REPLACE FUNCTION core.mfa_required_for(p_user uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM core.memberships m JOIN core.workspace_security s ON s.workspace_id = m.workspace_id
    WHERE m.user_id = p_user AND m.deactivated_at IS NULL AND m.role IN ('admin','owner') AND s.require_mfa_admins)
$$;
