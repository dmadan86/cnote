-- Phase 3b shared storage. Cross-tenant access remains unavailable to the app role.
ALTER TABLE sites.posts ADD COLUMN lang text NOT NULL DEFAULT 'en';
CREATE TABLE sites.blocked_links (
 workspace_id uuid NOT NULL, site_id uuid NOT NULL, canonical_url text NOT NULL,
 threat_type text NOT NULL, expires_at timestamptz NOT NULL, checked_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (workspace_id, site_id, canonical_url),
 FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites(workspace_id,id) ON DELETE CASCADE
);
CREATE TABLE sites.abuse_cases (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, site_id uuid NOT NULL,
 status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','dismissed','taken_down','restored')),
 score int NOT NULL DEFAULT 0, published_version_id uuid, post_rev int,
 dismissed_for_version uuid, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,site_id,id),
 FOREIGN KEY (workspace_id,site_id) REFERENCES sites.sites(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id,site_id,published_version_id) REFERENCES sites.site_versions(workspace_id,site_id,id),
 FOREIGN KEY (workspace_id,site_id,dismissed_for_version) REFERENCES sites.site_versions(workspace_id,site_id,id)
);
CREATE UNIQUE INDEX abuse_cases_one_open ON sites.abuse_cases(site_id) WHERE status = 'open';
CREATE TABLE sites.abuse_signals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, site_id uuid NOT NULL, case_id uuid NOT NULL,
 rule text NOT NULL, location text NOT NULL, snippet text NOT NULL CHECK (char_length(snippet) <= 200),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (workspace_id,site_id,case_id) REFERENCES sites.abuse_cases(workspace_id,site_id,id) ON DELETE CASCADE
);
CREATE TABLE sites.abuse_appeals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, site_id uuid NOT NULL, case_id uuid NOT NULL,
 takedown_action_id uuid NOT NULL UNIQUE, message text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (workspace_id,site_id,case_id) REFERENCES sites.abuse_cases(workspace_id,site_id,id) ON DELETE CASCADE
);
CREATE TABLE sites.abuse_reports (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, site_id uuid NOT NULL, case_id uuid,
 ip_hash text NOT NULL, reason text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (workspace_id,site_id) REFERENCES sites.sites(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id,site_id,case_id) REFERENCES sites.abuse_cases(workspace_id,site_id,id) ON DELETE CASCADE
);
CREATE TABLE sites.cloudflare_reconciliation (
 hostname text PRIMARY KEY, workspace_id uuid NOT NULL, site_id uuid NOT NULL,
 desired_state text NOT NULL CHECK (desired_state IN ('present','absent')), provider_id text,
 status text NOT NULL DEFAULT 'pending', tombstone boolean NOT NULL DEFAULT false,
 attempts int NOT NULL DEFAULT 0 CHECK (attempts >= 0), created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), last_attempt_at timestamptz,
 FOREIGN KEY (workspace_id,site_id) REFERENCES sites.sites(workspace_id,id) ON DELETE CASCADE
);
CREATE TABLE sites.consent_texts (
 id text NOT NULL, lang text NOT NULL, version text NOT NULL, text text NOT NULL,
 PRIMARY KEY (id,lang,version)
);
INSERT INTO sites.consent_texts(id,lang,version,text) VALUES
 ('enquiry-v1','en','v1','I agree that {site_name} may use these details to reply to my enquiry.');
SELECT core.enable_tenant_rls('sites.blocked_links');
SELECT core.enable_tenant_rls('sites.abuse_cases');
SELECT core.enable_tenant_rls('sites.abuse_signals');
SELECT core.enable_tenant_rls('sites.abuse_appeals');
SELECT core.enable_tenant_rls('sites.abuse_reports');
SELECT core.enable_tenant_rls('sites.cloudflare_reconciliation');
GRANT SELECT, INSERT, UPDATE, DELETE ON sites.blocked_links, sites.abuse_cases, sites.abuse_signals, sites.abuse_appeals, sites.abuse_reports, sites.cloudflare_reconciliation TO cnote_app;
GRANT SELECT ON sites.consent_texts TO cnote_app;
