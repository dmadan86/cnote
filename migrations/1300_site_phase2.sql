-- Phase 2 shared storage. Public routing and counter writers are added with their features.
ALTER TABLE sites.site_versions DROP CONSTRAINT site_versions_data_check,
  ADD CONSTRAINT site_versions_data_check CHECK (octet_length(data::text) < 262144),
  DROP CONSTRAINT site_versions_source_check,
  ADD CONSTRAINT site_versions_source_check CHECK (source IN ('template','chat','edit','editor','restore')),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
GRANT UPDATE ON sites.site_versions TO cnote_app;
ALTER TABLE sites.sites ADD COLUMN canonical_host text;
CREATE TABLE sites.domain_claims (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL,
  site_id uuid NOT NULL,
  hostname text NOT NULL UNIQUE CHECK (char_length(hostname) <= 253),
  txt_token text NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','verified','lost')),
  routing_configured boolean NOT NULL DEFAULT false,
  failures int NOT NULL DEFAULT 0 CHECK (failures >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL DEFAULT (now() + interval '7 days'),
  checked_at timestamptz,
  verified_at timestamptz,
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX domain_claims_site ON sites.domain_claims (workspace_id, site_id);
CREATE INDEX domain_claims_due ON sites.domain_claims (checked_at) WHERE status = 'verified';
SELECT core.enable_tenant_rls('sites.domain_claims');
GRANT SELECT, INSERT, UPDATE, DELETE ON sites.domain_claims TO cnote_app;
CREATE TABLE sites.site_stats (
  workspace_id uuid NOT NULL, site_id uuid NOT NULL, day date NOT NULL,
  kind text NOT NULL CHECK (kind IN ('page','visit')),
  key text NOT NULL CHECK (char_length(key) <= 100), n int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, kind, key),
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE
);
CREATE TABLE sites.site_referrers (
  workspace_id uuid NOT NULL, site_id uuid NOT NULL, day date NOT NULL,
  host text NOT NULL CHECK (char_length(host) BETWEEN 1 AND 100), n int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, day, host),
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('sites.site_stats');
SELECT core.enable_tenant_rls('sites.site_referrers');
GRANT SELECT, DELETE ON sites.site_stats, sites.site_referrers TO cnote_app;
