-- Phase 3b abuse scan (design 18.9 to 18.14): scan results, the link-check ledger, reviewer actions, the Web Risk usage
-- counter, the public blocked-link list, the public report insert and the platform review functions. Nothing here is
-- reachable cross-tenant by the app role except the small SECURITY DEFINER functions granted below.

ALTER TABLE sites.abuse_cases ADD COLUMN unchecked jsonb NOT NULL DEFAULT '[]';   -- links the Web Risk cap left unchecked
ALTER TABLE sites.abuse_cases ADD COLUMN scan_score int NOT NULL DEFAULT 0;      -- rules + Web Risk; score adds the capped report points

-- One row per site: what the last scan covered. A newer publish or post change means the row no longer matches and the site is due.
CREATE TABLE sites.abuse_scans (
 workspace_id uuid NOT NULL, site_id uuid NOT NULL, published_version_id uuid NOT NULL, post_rev text NOT NULL,
 score int NOT NULL DEFAULT 0, scanned_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (workspace_id, site_id),
 FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites(workspace_id, id) ON DELETE CASCADE
);
-- Every link ever sent to Web Risk and when its answer expires (clean links too), so a scan checks new links first, then the oldest.
CREATE TABLE sites.abuse_links (
 workspace_id uuid NOT NULL, site_id uuid NOT NULL, canonical_url text NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 PRIMARY KEY (workspace_id, site_id, canonical_url),
 FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites(workspace_id, id) ON DELETE CASCADE
);
-- Reviewer decisions. The app role can only read them; the review functions below write them.
CREATE TABLE sites.abuse_actions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, site_id uuid NOT NULL, case_id uuid NOT NULL,
 action text NOT NULL CHECK (action IN ('dismiss','takedown','restore')), actor_user_id uuid NOT NULL REFERENCES core.users(id),
 reason text NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 500), version_id uuid, created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (workspace_id, site_id, case_id) REFERENCES sites.abuse_cases(workspace_id, site_id, id) ON DELETE CASCADE,
 UNIQUE (workspace_id, id)
);
ALTER TABLE sites.abuse_appeals ADD FOREIGN KEY (workspace_id, takedown_action_id) REFERENCES sites.abuse_actions(workspace_id, id) ON DELETE CASCADE;
ALTER TABLE sites.abuse_appeals ADD CHECK (char_length(message) BETWEEN 3 AND 1000);
-- Platform-wide Web Risk call counter per month (the daily one uses the shared rate bucket). Holds no tenant data.
CREATE TABLE sites.webrisk_usage (month text PRIMARY KEY, n int NOT NULL DEFAULT 0);
SELECT core.enable_tenant_rls('sites.abuse_scans');
SELECT core.enable_tenant_rls('sites.abuse_links');
SELECT core.enable_tenant_rls('sites.abuse_actions');
GRANT SELECT, INSERT, UPDATE, DELETE ON sites.abuse_scans, sites.abuse_links, sites.webrisk_usage TO cnote_app;
GRANT SELECT ON sites.abuse_actions TO cnote_app;

-- Owners and admins are told about a takedown or restore in the app (and by email).
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_kind_check;
ALTER TABLE core.notifications
  ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('mention','approval_requested','approval_decided','reminder','assigned','quote_decided','chat_mention','lead','site_abuse'));

-- ---- public reads and writes (no tenant is known) ----
-- Links hidden on a live site, as canonical URLs. Rows stay until a recheck clears them.
CREATE FUNCTION sites.public_blocked(p_site uuid) RETURNS SETOF text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT b.canonical_url FROM sites.blocked_links b WHERE b.site_id = p_site ORDER BY b.canonical_url LIMIT 500 $$;

-- A visitor report. Only a live site takes one; it joins the open case when there is one.
CREATE FUNCTION sites.abuse_report_add(p_site uuid, p_ip_hash text, p_reason text) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
DECLARE s sites.sites; cid uuid;
BEGIN
  SELECT x.* INTO s FROM sites.sites x LEFT JOIN sites.site_platform p ON p.site_id = x.id
   WHERE x.id = p_site AND x.published_version_id IS NOT NULL AND p.taken_down_at IS NULL;
  IF NOT FOUND THEN RETURN 'not_found'; END IF;
  PERFORM pg_advisory_xact_lock(hashtext('sites.abuse_report_add'));
  IF (SELECT count(*) FROM sites.abuse_reports WHERE site_id = p_site AND ip_hash = p_ip_hash AND created_at > now() - interval '1 day') >= 3
     OR (SELECT count(*) FROM sites.abuse_reports WHERE created_at > now() - interval '1 hour') >= 500 THEN
    RETURN 'rate_limited';
  END IF;
  SELECT id INTO cid FROM sites.abuse_cases WHERE site_id = p_site AND status = 'open';
  INSERT INTO sites.abuse_reports (workspace_id, site_id, case_id, ip_hash, reason) VALUES (s.workspace_id, s.id, cid, p_ip_hash, left(p_reason, 500));
  RETURN 'ok';
END $$;

-- ---- platform review (called over the admin connection, like sites.platform_set) ----
CREATE FUNCTION sites.abuse_review_list(p_status text, p_limit int) RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT coalesce(jsonb_agg(r ORDER BY (r->>'score')::int DESC, r->>'updated_at' DESC), '[]'::jsonb) FROM (
    SELECT jsonb_build_object('id', c.id, 'site_id', c.site_id, 'status', c.status, 'score', c.score, 'scan_score', c.scan_score,
      'site_name', s.name, 'subdomain', s.subdomain, 'workspace_slug', w.slug, 'updated_at', c.updated_at, 'created_at', c.created_at,
      'signals', (SELECT count(*) FROM sites.abuse_signals g WHERE g.case_id = c.id),
      'reports', (SELECT count(*) FROM sites.abuse_reports q WHERE q.case_id = c.id),
      'unchecked', jsonb_array_length(c.unchecked),
      'appealed', EXISTS (SELECT 1 FROM sites.abuse_appeals a WHERE a.case_id = c.id)) AS r
    FROM sites.abuse_cases c JOIN sites.sites s ON s.workspace_id = c.workspace_id AND s.id = c.site_id
    JOIN core.workspaces w ON w.id = c.workspace_id
    WHERE p_status IS NULL OR c.status = p_status
    ORDER BY c.score DESC, c.updated_at DESC LIMIT greatest(least(p_limit, 200), 1)) q $$;

CREATE FUNCTION sites.abuse_review_get(p_case uuid) RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT jsonb_build_object('id', c.id, 'site_id', c.site_id, 'status', c.status, 'score', c.score, 'scan_score', c.scan_score,
    'published_version_id', c.published_version_id, 'current_version_id', s.published_version_id, 'post_rev', c.post_rev,
    'dismissed_for_version', c.dismissed_for_version, 'site_name', s.name, 'subdomain', s.subdomain, 'workspace_slug', w.slug,
    'created_at', c.created_at, 'updated_at', c.updated_at, 'unchecked', c.unchecked,
    'taken_down', p.taken_down_at IS NOT NULL,
    'signals', (SELECT coalesce(jsonb_agg(jsonb_build_object('rule', g.rule, 'location', g.location, 'snippet', g.snippet, 'created_at', g.created_at) ORDER BY g.created_at, g.id), '[]') FROM sites.abuse_signals g WHERE g.case_id = c.id),
    'reports', (SELECT coalesce(jsonb_agg(jsonb_build_object('reason', q.reason, 'created_at', q.created_at) ORDER BY q.created_at DESC), '[]') FROM (SELECT * FROM sites.abuse_reports WHERE case_id = c.id ORDER BY created_at DESC LIMIT 50) q),
    'actions', (SELECT coalesce(jsonb_agg(jsonb_build_object('id', a.id, 'action', a.action, 'reason', a.reason, 'created_at', a.created_at) ORDER BY a.created_at), '[]') FROM sites.abuse_actions a WHERE a.case_id = c.id),
    'appeals', (SELECT coalesce(jsonb_agg(jsonb_build_object('takedown_action_id', x.takedown_action_id, 'message', x.message, 'created_at', x.created_at) ORDER BY x.created_at), '[]') FROM sites.abuse_appeals x WHERE x.case_id = c.id))
  FROM sites.abuse_cases c JOIN sites.sites s ON s.workspace_id = c.workspace_id AND s.id = c.site_id
  JOIN core.workspaces w ON w.id = c.workspace_id LEFT JOIN sites.site_platform p ON p.site_id = c.site_id
  WHERE c.id = p_case $$;

-- dismiss | takedown | restore. Locks the case, applies the platform function for takedown and restore, writes the action and
-- tells the owners and admins (in-app row and an outbox mail per person). p_title, p_subject and p_body are the server's own text.
CREATE FUNCTION sites.abuse_review_act(p_actor uuid, p_case uuid, p_action text, p_reason text, p_title text, p_subject text, p_body text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
DECLARE c sites.abuse_cases; s sites.sites; aid uuid; r record;
BEGIN
  IF p_action NOT IN ('dismiss','takedown','restore') THEN RAISE EXCEPTION 'unknown action' USING ERRCODE = '22023'; END IF;
  IF NOT EXISTS (SELECT 1 FROM core.users WHERE id = p_actor) THEN RAISE EXCEPTION 'unknown actor' USING ERRCODE = '42501'; END IF;
  SELECT x.* INTO c FROM sites.abuse_cases x WHERE x.id = p_case FOR UPDATE;
  IF NOT FOUND THEN RETURN NULL; END IF;
  SELECT x.* INTO s FROM sites.sites x WHERE x.workspace_id = c.workspace_id AND x.id = c.site_id FOR UPDATE;
  IF p_action IN ('dismiss','takedown') AND c.status <> 'open' THEN RAISE EXCEPTION 'this case is not open' USING ERRCODE = '22023'; END IF;
  IF p_action = 'restore' AND c.status <> 'taken_down' THEN RAISE EXCEPTION 'this site is not taken down for this case' USING ERRCODE = '22023'; END IF;
  IF p_action = 'takedown' THEN
    IF s.subdomain IS NULL THEN RAISE EXCEPTION 'this site has no address' USING ERRCODE = '22023'; END IF;
    PERFORM 1 FROM sites.platform_set(p_actor, s.subdomain, NULL, 'takedown', p_reason, NULL);
  ELSIF p_action = 'restore' THEN
    PERFORM 1 FROM sites.platform_set(p_actor, s.subdomain, NULL, 'restore', p_reason, NULL);
  END IF;
  INSERT INTO sites.abuse_actions (workspace_id, site_id, case_id, action, actor_user_id, reason, version_id)
    VALUES (c.workspace_id, c.site_id, c.id, p_action, p_actor, p_reason, s.published_version_id) RETURNING id INTO aid;
  UPDATE sites.abuse_cases SET updated_at = now(),
    status = CASE p_action WHEN 'dismiss' THEN 'dismissed' WHEN 'takedown' THEN 'taken_down' ELSE 'restored' END,
    dismissed_for_version = CASE WHEN p_action = 'takedown' THEN dismissed_for_version ELSE s.published_version_id END
    WHERE id = c.id;
  IF p_action IN ('takedown','restore') THEN
    FOR r IN SELECT m.user_id, u.email FROM core.memberships m JOIN core.users u ON u.id = m.user_id
             WHERE m.workspace_id = c.workspace_id AND m.role IN ('owner','admin') AND m.deactivated_at IS NULL ORDER BY m.created_at LIMIT 20 LOOP
      INSERT INTO core.notifications (workspace_id, user_id, kind, title) VALUES (c.workspace_id, r.user_id, 'site_abuse', left(p_title, 200)) ON CONFLICT DO NOTHING;
      INSERT INTO core.jobs (workspace_id, kind, payload) VALUES (c.workspace_id, 'mail.send',
        jsonb_build_object('to', r.email, 'subject', p_subject, 'text', p_body, 'message_id', '<' || gen_random_uuid() || '@cnote>'));
    END LOOP;
  END IF;
  RETURN jsonb_build_object('case_id', c.id, 'site_id', c.site_id, 'workspace_id', c.workspace_id, 'action_id', aid, 'action', p_action, 'subdomain', s.subdomain);
END $$;

REVOKE ALL ON FUNCTION sites.abuse_review_list(text, int), sites.abuse_review_get(uuid), sites.abuse_review_act(uuid, uuid, text, text, text, text, text) FROM PUBLIC, cnote_app;
REVOKE ALL ON FUNCTION sites.public_blocked(uuid), sites.abuse_report_add(uuid, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION sites.public_blocked(uuid), sites.abuse_report_add(uuid, text, text) TO cnote_app;
