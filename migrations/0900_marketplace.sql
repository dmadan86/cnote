-- App marketplace. market_apps and app_install_codes are global (like core.oauth_clients): the token endpoint reads them
-- before any tenant is known, so every command-side query filters explicitly. app_installs is a normal tenant table.
-- An installed app is an external agent (core.agents); uninstalling deletes the agent, which cascades to its API keys.
CREATE TABLE core.market_apps (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{2,39}$'),
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
  tagline text NOT NULL DEFAULT '' CHECK (char_length(tagline) <= 120),
  description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 4000),
  category text NOT NULL CHECK (category IN ('productivity','sales','finance','developer','communication','other')),
  icon_url text CHECK (icon_url IS NULL OR icon_url LIKE 'https://%'),
  homepage_url text CHECK (homepage_url IS NULL OR homepage_url LIKE 'https://%'),
  redirect_url text NOT NULL,
  scopes text[] NOT NULL CHECK (
    scopes <@ ARRAY['read','write_notes','write_items','manage_crm','manage_invoices','send_messages']::text[] AND 'read' = ANY(scopes)),
  publisher_workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  publisher_name text NOT NULL,
  client_secret_hash text NOT NULL,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','review','published','rejected')),
  review_note text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON core.market_apps (publisher_workspace_id);

CREATE TABLE core.app_installs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES core.market_apps(id) ON DELETE CASCADE,
  agent_id uuid NOT NULL REFERENCES core.agents(id) ON DELETE CASCADE,
  installed_by uuid NOT NULL REFERENCES core.users(id),
  scopes text[] NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, app_id)
);
SELECT core.enable_tenant_rls('core.app_installs');

-- workspace_id is kept here so the token endpoint can open the right tenant without reading app_installs blind.
CREATE TABLE core.app_install_codes (
  code_hash text PRIMARY KEY,
  install_id uuid NOT NULL REFERENCES core.app_installs(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at timestamptz
);

-- Install counts span tenants, which RLS hides; the count is public marketplace data.
CREATE OR REPLACE FUNCTION core.app_install_count(app uuid) RETURNS bigint
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS
$$ SELECT count(*) FROM core.app_installs WHERE app_id = app $$;
