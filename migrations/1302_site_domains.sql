-- Custom domains (docs/design/site-builder-phase2.md 17.1 to 17.3). Claims live in sites.domain_claims (migration 1300,
-- tenant RLS). Everything that must cross workspaces (claiming a name another workspace may hold, routing a request host,
-- the TLS ask, the daily due list) is a SECURITY DEFINER function below, following migrations 1280 and 1290.
ALTER TABLE sites.domain_claims ADD CONSTRAINT domain_claims_lower CHECK (hostname = lower(hostname));

-- Durable history for "each site may add 5 hostnames per 30 days": removing a claim does not give the slot back.
-- The app can read it (its own tenant); the only writer is sites.domain_claim.
CREATE TABLE sites.domain_additions (
  id bigserial PRIMARY KEY,
  workspace_id uuid NOT NULL,
  site_id uuid NOT NULL,
  hostname text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX domain_additions_site ON sites.domain_additions (site_id, created_at DESC);
SELECT core.enable_tenant_rls('sites.domain_additions');
GRANT SELECT ON sites.domain_additions TO cnote_app;

-- Claim a hostname for a site of the CURRENT workspace. Returns created, exists (this site already holds a live claim),
-- taken (another site holds it), limit (5 per site per 30 days) or missing (no such site here). A pending claim past its
-- expiry and a lost claim are reclaimable by anyone; a pending claim that has not expired is not.
CREATE FUNCTION sites.domain_claim(p_site uuid, p_host text, p_token text) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
DECLARE ws uuid := core.current_workspace(); n int; got int;
BEGIN
  IF ws IS NULL THEN RAISE EXCEPTION 'no workspace' USING ERRCODE = '42501'; END IF;
  PERFORM 1 FROM sites.sites WHERE id = p_site AND workspace_id = ws FOR UPDATE;   -- serialises adds per site
  IF NOT FOUND THEN RETURN 'missing'; END IF;
  IF EXISTS (SELECT 1 FROM sites.domain_claims WHERE hostname = p_host AND site_id = p_site
             AND (status = 'verified' OR (status = 'pending' AND expires_at > now()))) THEN RETURN 'exists'; END IF;
  SELECT count(*) INTO n FROM sites.domain_additions WHERE site_id = p_site AND created_at > now() - interval '30 days';
  IF n >= 5 THEN RETURN 'limit'; END IF;
  DELETE FROM sites.domain_claims WHERE hostname = p_host AND (status = 'lost' OR (status = 'pending' AND expires_at <= now()));
  INSERT INTO sites.domain_claims (workspace_id, site_id, hostname, txt_token) VALUES (ws, p_site, p_host, p_token) ON CONFLICT (hostname) DO NOTHING;
  GET DIAGNOSTICS got = ROW_COUNT;
  IF got = 0 THEN RETURN 'taken'; END IF;
  INSERT INTO sites.domain_additions (workspace_id, site_id, hostname) VALUES (ws, p_site, p_host);
  RETURN 'created';
END $$;

-- Platform-wide: at most 50 hostnames become verified per hour. True takes a slot (inside the caller's transaction).
CREATE FUNCTION sites.domain_verify_slot() RETURNS boolean
LANGUAGE plpgsql SET search_path = sites, core, pg_temp AS $$
DECLARE n int;
BEGIN
  INSERT INTO core.rate_buckets (key, bucket, n) VALUES ('sitedomain:verified', date_trunc('hour', now()), 1)
    ON CONFLICT (key, bucket) DO UPDATE SET n = core.rate_buckets.n + 1 WHERE core.rate_buckets.n < 50 RETURNING core.rate_buckets.n INTO n;
  RETURN FOUND;
END $$;

-- A live hostname: verified, its routing record seen, the site published, not taken down and Pro. Every public decision uses this.
CREATE VIEW sites.live_domains WITH (security_barrier = true) AS
  SELECT d.hostname, d.workspace_id, d.site_id, d.verified_at
  FROM sites.domain_claims d
  JOIN sites.sites s ON s.workspace_id = d.workspace_id AND s.id = d.site_id AND s.published_version_id IS NOT NULL
  JOIN sites.site_platform p ON p.site_id = s.id AND p.pro AND p.taken_down_at IS NULL
  WHERE d.status = 'verified' AND d.routing_configured;
REVOKE ALL ON sites.live_domains FROM PUBLIC;   -- owner-only; read through the functions below

CREATE FUNCTION sites.domain_ask(p_host text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM sites.live_domains WHERE hostname = p_host) $$;

-- The hostname a site is served on: www first, then the oldest verified. NULL when none is live.
CREATE FUNCTION sites.domain_canonical(p_site uuid) RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT hostname FROM sites.live_domains WHERE site_id = p_site ORDER BY (hostname LIKE 'www.%') DESC, verified_at, hostname LIMIT 1 $$;

CREATE FUNCTION sites.domain_lookup(p_host text)
RETURNS TABLE(workspace_id uuid, site_id uuid, version_id uuid, name text, data jsonb, pro boolean, published_at timestamptz,
              published_by uuid, form_paused boolean, lead_space_id uuid, subdomain text, canonical_host text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT s.workspace_id, s.id, s.published_version_id, s.name, v.data, true, s.published_at, s.published_by, s.form_paused, s.lead_space_id,
         s.subdomain, sites.domain_canonical(s.id)
  FROM sites.live_domains l
  JOIN sites.sites s ON s.workspace_id = l.workspace_id AND s.id = l.site_id
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  WHERE l.hostname = p_host $$;

-- Claims for the daily re-check; the sweep re-reads each one inside its own workspace.
CREATE FUNCTION sites.domains_due(p_limit int) RETURNS TABLE(workspace_id uuid, id uuid, hostname text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT workspace_id, id, hostname FROM sites.domain_claims
  WHERE status = 'verified' AND (checked_at IS NULL OR checked_at < now() - interval '23 hours')
  ORDER BY checked_at NULLS FIRST LIMIT p_limit $$;

-- Housekeeping: expired pending claims and addition history older than the 30-day window.
CREATE FUNCTION sites.domain_prune() RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  DELETE FROM sites.domain_claims WHERE status = 'pending' AND expires_at <= now();
  DELETE FROM sites.domain_additions WHERE created_at < now() - interval '60 days' $$;

REVOKE ALL ON FUNCTION sites.domain_claim(uuid, text, text), sites.domain_verify_slot(), sites.domain_ask(text), sites.domain_canonical(uuid),
  sites.domain_lookup(text), sites.domains_due(int), sites.domain_prune() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION sites.domain_claim(uuid, text, text), sites.domain_verify_slot(), sites.domain_ask(text), sites.domain_canonical(uuid),
  sites.domain_lookup(text), sites.domains_due(int), sites.domain_prune() TO cnote_app;
