CREATE SCHEMA IF NOT EXISTS sites;
GRANT USAGE ON SCHEMA sites TO cnote_app;

CREATE TABLE sites.sites (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
 template text NOT NULL,
 draft_version_id uuid,
 created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (workspace_id, id)
);
CREATE UNIQUE INDEX sites_one_per_workspace ON sites.sites (workspace_id);
CREATE TABLE sites.site_versions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 site_id uuid NOT NULL,
 seq int NOT NULL,
 parent_id uuid,
 source text NOT NULL CHECK (source IN ('template','chat','edit')),
 turn_id uuid,
 summary text NOT NULL DEFAULT '',
 data jsonb NOT NULL CHECK (octet_length(data::text) < 65536),
 created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (site_id, seq), UNIQUE (workspace_id, id), UNIQUE (workspace_id, site_id, id),
 FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id, site_id, parent_id) REFERENCES sites.site_versions (workspace_id, site_id, id)
);
ALTER TABLE sites.sites ADD FOREIGN KEY (workspace_id, id, draft_version_id) REFERENCES sites.site_versions (workspace_id, site_id, id);
CREATE TABLE sites.site_turns (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 site_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES core.users(id),
 message text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 2000),
 block_id text,
 tier text NOT NULL DEFAULT 'routine' CHECK (tier IN ('routine','reasoning')),
 status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed','cancelled')),
 base_version_id uuid NOT NULL,
 version_id uuid,
 reply text, error text,
 steps jsonb NOT NULL DEFAULT '[]',
 tokens_in int NOT NULL DEFAULT 0, tokens_out int NOT NULL DEFAULT 0, cache_read int NOT NULL DEFAULT 0,
 provider text, model text,
 attempt int NOT NULL DEFAULT 0,
 calls int NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
 UNIQUE (workspace_id, site_id, id),
 FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id, site_id, base_version_id) REFERENCES sites.site_versions (workspace_id, site_id, id),
 FOREIGN KEY (workspace_id, site_id, version_id) REFERENCES sites.site_versions (workspace_id, site_id, id)
);
ALTER TABLE sites.site_versions ADD FOREIGN KEY (workspace_id, site_id, turn_id) REFERENCES sites.site_turns (workspace_id, site_id, id);
CREATE UNIQUE INDEX site_turns_active ON sites.site_turns (site_id) WHERE status IN ('queued','running');
CREATE INDEX site_turns_site ON sites.site_turns (workspace_id, site_id, created_at);
SELECT core.enable_tenant_rls('sites.sites');
SELECT core.enable_tenant_rls('sites.site_versions');
SELECT core.enable_tenant_rls('sites.site_turns');
GRANT SELECT, INSERT, UPDATE, DELETE ON sites.sites, sites.site_turns TO cnote_app;
GRANT SELECT, INSERT, DELETE ON sites.site_versions TO cnote_app;
