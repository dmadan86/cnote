-- Site analytics (design 17.8): the public beacon writes the daily counters through this one function. The app role has
-- no INSERT/UPDATE on the counter tables; the function runs as the table owner, so it crosses tenants, and it checks
-- everything itself: the site is published and not taken down, the page exists in the published version, the referrer is
-- a plausible host, and at most 50 distinct referrer hosts are kept per site-day (the rest count as 'other').
CREATE FUNCTION sites.hit(p_site uuid, p_slug text, p_ref text, p_suffix text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
DECLARE
  ws uuid; sub text; canon text; doc jsonb;
  d date := (now() AT TIME ZONE 'Asia/Kolkata')::date;
  h text := nullif(lower(p_ref), '');
  entry boolean;
  mine boolean;
BEGIN
  SELECT s.workspace_id, s.subdomain, s.canonical_host, v.data INTO ws, sub, canon, doc
  FROM sites.sites s
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE s.id = p_site AND s.published_version_id IS NOT NULL AND p.taken_down_at IS NULL;
  IF NOT FOUND THEN RETURN; END IF;
  IF p_slug IS NULL OR (p_slug <> '' AND NOT jsonb_path_exists(doc, '$.pages[*] ? (@.slug == $s)', jsonb_build_object('s', p_slug))) THEN RETURN; END IF;
  IF h IS NOT NULL AND (char_length(h) > 100 OR h NOT LIKE '%.%' OR h !~ '^[a-z0-9.-]+$') THEN h := NULL; END IF;
  -- a referrer that is one of this site's own hosts is navigation inside the site: not an entry, not a referrer
  mine := coalesce(h IS NOT NULL AND (h = sub || '.' || p_suffix OR h = canon
    OR EXISTS (SELECT 1 FROM sites.domain_claims c WHERE c.site_id = p_site AND c.status = 'verified' AND c.hostname = h)), false);
  entry := NOT mine;
  INSERT INTO sites.site_stats (workspace_id, site_id, day, kind, key, n) VALUES (ws, p_site, d, 'page', p_slug, 1)
    ON CONFLICT (site_id, day, kind, key) DO UPDATE SET n = site_stats.n + 1;
  IF NOT entry THEN RETURN; END IF;
  INSERT INTO sites.site_stats (workspace_id, site_id, day, kind, key, n) VALUES (ws, p_site, d, 'visit', '', 1)
    ON CONFLICT (site_id, day, kind, key) DO UPDATE SET n = site_stats.n + 1;
  IF h IS NULL THEN RETURN; END IF;
  -- one lock per site-day makes the count check and the insert one atomic step
  PERFORM pg_advisory_xact_lock(hashtextextended(p_site::text || d::text, 0));
  IF NOT EXISTS (SELECT 1 FROM sites.site_referrers WHERE site_id = p_site AND day = d AND host = h)
     AND (SELECT count(*) FROM sites.site_referrers WHERE site_id = p_site AND day = d AND host <> 'other') >= 50 THEN
    h := 'other';
  END IF;
  INSERT INTO sites.site_referrers (workspace_id, site_id, day, host, n) VALUES (ws, p_site, d, h, 1)
    ON CONFLICT (site_id, day, host) DO UPDATE SET n = site_referrers.n + 1;
END $$;

REVOKE ALL ON FUNCTION sites.hit(uuid, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION sites.hit(uuid, text, text, text) TO cnote_app;
