-- WP18 hardening. (1) An SSO allow-list entry only counts once the workspace has proven it controls the domain (a DNS TXT
-- record), so a provider a workspace admin controls cannot vouch for someone else's addresses. (2) The first SSO sign-in
-- links (workspace, issuer, subject) to a person; later sign-ins follow the link, not the email. (3) SSO enforcement is
-- per workspace and applied to the session at the workspace, not to the global password login.
CREATE TABLE core.sso_domains (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  domain text NOT NULL CHECK (domain = lower(domain) AND domain LIKE '%.%'),
  token text NOT NULL,                        -- the value that goes in the TXT record: cnote-verification=<token>
  verified_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, domain)
);
SELECT core.enable_tenant_rls('core.sso_domains');

CREATE TABLE core.sso_identities (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  issuer text NOT NULL,
  sub text NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, issuer, sub),
  UNIQUE (workspace_id, issuer, user_id)
);
SELECT core.enable_tenant_rls('core.sso_identities');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.sso_domains, core.sso_identities TO cnote_app;

-- domains configured under 1150 were never verified: keep them, unverified, each with its own token
INSERT INTO core.sso_domains (workspace_id, domain, token)
  SELECT workspace_id, lower(d), replace(gen_random_uuid()::text || gen_random_uuid()::text, '-', '')
  FROM core.workspace_security, unnest(sso_allowed_domains) AS d WHERE d LIKE '%.%' ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION core.sso_by_slug(p_slug text)
RETURNS TABLE(workspace_id uuid, issuer text, client_id text, client_secret_enc bytea, allowed_domains text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT s.workspace_id, s.sso_issuer, s.sso_client_id, s.sso_client_secret_enc,
         coalesce((SELECT array_agg(d.domain) FROM core.sso_domains d WHERE d.workspace_id = s.workspace_id AND d.verified_at IS NOT NULL), '{}')
  FROM core.workspace_security s JOIN core.workspaces w ON w.id = s.workspace_id
  WHERE w.slug = p_slug AND s.sso_enabled AND s.sso_issuer IS NOT NULL AND s.sso_client_id IS NOT NULL AND s.sso_client_secret_enc IS NOT NULL
$$;

DROP FUNCTION core.sso_enforced_for(uuid, text);
ALTER TABLE core.workspace_security DROP COLUMN sso_allowed_domains;
