-- WP23: SAML 2.0 SSO as an alternative to OIDC, and device sessions.
-- (1) A workspace picks one protocol. SAML keeps the IdP metadata (pasted, or fetched once from a URL through the SSRF
-- guard) and optionally the attribute that carries the email. (2) A sign-in request id is stored when the browser is sent to
-- the IdP and consumed by the response (InResponseTo); an assertion id is remembered until it expires so it cannot be
-- replayed. (3) Sessions get a public id for the device list, the client's address and user agent, and a last_seen_at.
ALTER TABLE core.workspace_security ADD COLUMN sso_protocol text NOT NULL DEFAULT 'oidc' CHECK (sso_protocol IN ('oidc','saml'));
ALTER TABLE core.workspace_security ADD COLUMN saml_idp_metadata text;
ALTER TABLE core.workspace_security ADD COLUMN saml_idp_metadata_url text;
ALTER TABLE core.workspace_security ADD COLUMN saml_email_attribute text;

CREATE TABLE core.saml_requests (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  request_id text NOT NULL,
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (workspace_id, request_id)
);
CREATE TABLE core.saml_assertions (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  assertion_id text NOT NULL,
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (workspace_id, assertion_id)
);
SELECT core.enable_tenant_rls('core.saml_requests');
SELECT core.enable_tenant_rls('core.saml_assertions');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.saml_requests, core.saml_assertions TO cnote_app;

ALTER TABLE core.sessions ADD COLUMN public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE;
ALTER TABLE core.sessions ADD COLUMN user_agent text;
ALTER TABLE core.sessions ADD COLUMN ip text;
ALTER TABLE core.sessions ADD COLUMN last_seen_at timestamptz;
CREATE INDEX sessions_user_idx ON core.sessions (user_id);

-- OIDC sign-in only sees workspaces that chose OIDC.
CREATE OR REPLACE FUNCTION core.sso_by_slug(p_slug text)
RETURNS TABLE(workspace_id uuid, issuer text, client_id text, client_secret_enc bytea, allowed_domains text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT s.workspace_id, s.sso_issuer, s.sso_client_id, s.sso_client_secret_enc,
         coalesce((SELECT array_agg(d.domain) FROM core.sso_domains d WHERE d.workspace_id = s.workspace_id AND d.verified_at IS NOT NULL), '{}')
  FROM core.workspace_security s JOIN core.workspaces w ON w.id = s.workspace_id
  WHERE w.slug = p_slug AND s.sso_enabled AND s.sso_protocol = 'oidc' AND s.sso_issuer IS NOT NULL AND s.sso_client_id IS NOT NULL AND s.sso_client_secret_enc IS NOT NULL
$$;

CREATE OR REPLACE FUNCTION core.saml_by_slug(p_slug text)
RETURNS TABLE(workspace_id uuid, idp_metadata text, email_attribute text, allowed_domains text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT s.workspace_id, s.saml_idp_metadata, coalesce(s.saml_email_attribute, ''),
         coalesce((SELECT array_agg(d.domain) FROM core.sso_domains d WHERE d.workspace_id = s.workspace_id AND d.verified_at IS NOT NULL), '{}')
  FROM core.workspace_security s JOIN core.workspaces w ON w.id = s.workspace_id
  WHERE w.slug = p_slug AND s.sso_enabled AND s.sso_protocol = 'saml' AND s.saml_idp_metadata IS NOT NULL
$$;
GRANT EXECUTE ON FUNCTION core.saml_by_slug(text) TO cnote_app;
