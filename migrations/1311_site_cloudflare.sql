-- Cloudflare for SaaS reconciler (docs/design/site-builder-phase3.md 18.15 to 18.18). sites.cloudflare_reconciliation (1308) holds the
-- desired state per hostname. A tombstone has to outlive its site and its workspace's claim until the provider confirms the delete,
-- so the row no longer cascades from sites. The functions below cross workspaces (the sweep plans and picks work for everyone) and
-- are SECURITY DEFINER like 1302; the per-row work then runs inside the row's own workspace.
ALTER TABLE sites.cloudflare_reconciliation DROP CONSTRAINT IF EXISTS cloudflare_reconciliation_workspace_id_site_id_fkey;
ALTER TABLE sites.cloudflare_reconciliation
  ADD COLUMN ssl_status text,
  ADD COLUMN last_error text,
  ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN generation int NOT NULL DEFAULT 0,  -- bumped whenever the planner changes what the row wants; a run stores its result only for the generation it read
  ADD COLUMN reserved_at timestamptz;            -- a create is in flight: the slot counts against the budget until the run ends (or 10 minutes pass)
CREATE INDEX cloudflare_reconciliation_due ON sites.cloudflare_reconciliation (next_attempt_at);

-- A site that may hold provider hostnames: published, Pro, not taken down. Same test as sites.live_domains, without the routing flag.
-- Plans the whole table: present for every verified claim of such a site (at most p_per_ws per workspace, oldest first), absent for every
-- row that no longer qualifies (unpublish, takedown, Pro lapse, disconnect, lost claim, site deleted). An absent row that never
-- reached the provider is dropped; any other becomes a tombstone until the delete is confirmed. A hostname that stops being wanted
-- also stops being routed, so republishing waits for the provider again.
CREATE FUNCTION sites.cf_plan(p_per_ws int) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
BEGIN
  INSERT INTO sites.cloudflare_reconciliation AS r (hostname, workspace_id, site_id, desired_state, status, next_attempt_at)
  SELECT w.hostname, w.workspace_id, w.site_id, 'present', 'pending', now() FROM (
    SELECT d.hostname, d.workspace_id, d.site_id,
           row_number() OVER (PARTITION BY d.workspace_id ORDER BY d.verified_at, d.hostname) AS rn
    FROM sites.domain_claims d
    JOIN sites.sites s ON s.workspace_id = d.workspace_id AND s.id = d.site_id AND s.published_version_id IS NOT NULL
    JOIN sites.site_platform p ON p.site_id = s.id AND p.pro AND p.taken_down_at IS NULL
    WHERE d.status = 'verified') w
  WHERE w.rn <= p_per_ws
  ON CONFLICT (hostname) DO UPDATE SET workspace_id = EXCLUDED.workspace_id, site_id = EXCLUDED.site_id, desired_state = 'present',
      tombstone = false, last_error = NULL, next_attempt_at = now(), updated_at = now(), generation = r.generation + 1,
      status = CASE WHEN r.provider_id IS NULL THEN 'pending' ELSE r.status END
    WHERE r.desired_state <> 'present' OR r.workspace_id <> EXCLUDED.workspace_id OR r.site_id <> EXCLUDED.site_id;

  WITH gone AS (
    UPDATE sites.cloudflare_reconciliation r SET desired_state = 'absent', tombstone = (r.provider_id IS NOT NULL OR r.attempts > 0),
        next_attempt_at = now(), updated_at = now(), generation = r.generation + 1
    WHERE r.desired_state = 'present' AND NOT EXISTS (
      SELECT 1 FROM sites.domain_claims d
      JOIN sites.sites s ON s.workspace_id = d.workspace_id AND s.id = d.site_id AND s.published_version_id IS NOT NULL
      JOIN sites.site_platform p ON p.site_id = s.id AND p.pro AND p.taken_down_at IS NULL
      WHERE d.hostname = r.hostname AND d.workspace_id = r.workspace_id AND d.site_id = r.site_id AND d.status = 'verified')
    RETURNING r.hostname)
  UPDATE sites.domain_claims c SET routing_configured = false FROM gone WHERE c.hostname = gone.hostname AND c.routing_configured;

  DELETE FROM sites.cloudflare_reconciliation WHERE desired_state = 'absent' AND NOT tombstone;
END $$;

-- Rows whose turn has come (a lease from the sweep moves next_attempt_at forward, so two sweeps never work the same row).
CREATE FUNCTION sites.cf_due(p_limit int) RETURNS TABLE(workspace_id uuid, hostname text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT workspace_id, hostname FROM sites.cloudflare_reconciliation WHERE next_attempt_at <= now() ORDER BY next_attempt_at, hostname LIMIT p_limit $$;

-- Hostnames that count against the budget: those the provider holds for us (including tombstones not yet confirmed deleted) and
-- those a run has reserved a slot for.
CREATE FUNCTION sites.cf_used() RETURNS int
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT count(*)::int FROM sites.cloudflare_reconciliation WHERE provider_id IS NOT NULL OR reserved_at > now() - interval '10 minutes' $$;

-- Takes a slot for one create, atomically across workers (an advisory lock serialises the count and the claim). False when the budget is full.
CREATE FUNCTION sites.cf_reserve(p_host text, p_max int) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('sites.cf_reserve'));
  IF (SELECT count(*) FROM sites.cloudflare_reconciliation
       WHERE hostname <> p_host AND (provider_id IS NOT NULL OR reserved_at > now() - interval '10 minutes')) >= p_max THEN
    RETURN false;
  END IF;
  UPDATE sites.cloudflare_reconciliation SET reserved_at = now()
   WHERE hostname = p_host AND desired_state = 'present' AND provider_id IS NULL;
  RETURN FOUND;
END $$;

REVOKE ALL ON FUNCTION sites.cf_plan(int), sites.cf_due(int), sites.cf_used(), sites.cf_reserve(text, int) FROM PUBLIC;
REVOKE ALL ON FUNCTION sites.cf_plan(int) FROM cnote_app;
GRANT EXECUTE ON FUNCTION sites.cf_due(int), sites.cf_used(), sites.cf_reserve(text, int) TO cnote_app;
