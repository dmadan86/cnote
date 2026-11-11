-- MCP OAuth 2.1 (ADR-004). Like core.users/core.sessions these tables are looked up BEFORE the tenant is known
-- (token exchange, bearer resolution), so they carry no tenant RLS; every command-side query filters by
-- user_id + workspace_id explicitly. Only SHA-256 hashes of codes and tokens are stored.
CREATE TABLE core.oauth_clients (
  client_id text PRIMARY KEY,                      -- cnc_<random> (RFC 7591) or an https metadata-document URL
  kind text NOT NULL CHECK (kind IN ('registered','metadata')),
  name text NOT NULL,
  redirect_uris text[] NOT NULL,
  fetched_at timestamptz NOT NULL DEFAULT now(),   -- metadata documents are re-fetched when stale
  created_at timestamptz NOT NULL DEFAULT now()
);

-- One row per user approval: the "connected app". Revoking it kills every token of the family.
CREATE TABLE core.oauth_grants (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  client_id text NOT NULL REFERENCES core.oauth_clients(client_id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  resource text NOT NULL,
  scope text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at timestamptz
);
CREATE INDEX ON core.oauth_grants (user_id, workspace_id) WHERE revoked_at IS NULL;

CREATE TABLE core.oauth_codes (
  code_hash text PRIMARY KEY,
  client_id text NOT NULL REFERENCES core.oauth_clients(client_id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  redirect_uri text NOT NULL,
  code_challenge text NOT NULL,
  resource text NOT NULL,
  scope text NOT NULL DEFAULT '',
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  grant_id uuid REFERENCES core.oauth_grants(id) ON DELETE CASCADE  -- set on exchange; a replayed code revokes it
);

CREATE TABLE core.oauth_tokens (
  token_hash text PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('access','refresh')),
  grant_id uuid NOT NULL REFERENCES core.oauth_grants(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,                             -- refresh tokens rotate; presenting a used one is theft
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON core.oauth_tokens (grant_id);
CREATE INDEX ON core.oauth_codes (expires_at);
CREATE INDEX ON core.oauth_tokens (expires_at);
