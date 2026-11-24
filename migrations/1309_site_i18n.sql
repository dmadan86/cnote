-- Phase 3b languages (design 18): the translate turn kind and per-language public post reads.
ALTER TABLE sites.site_turns DROP CONSTRAINT site_turns_kind_check;
ALTER TABLE sites.site_turns ADD CONSTRAINT site_turns_kind_check CHECK (kind IN ('page','post','translate'));
ALTER TABLE sites.site_turns ADD COLUMN lang text, ADD COLUMN scope text, ADD COLUMN translate_page text;

-- Published posts of a live site in one language (null: every language), newest first, without bodies.
DROP FUNCTION sites.public_posts(uuid, int, int);
CREATE FUNCTION sites.public_posts(p_site uuid, p_limit int, p_offset int, p_lang text)
RETURNS TABLE(id uuid, slug text, title text, description text, cover_image_id uuid, og_image_id uuid, published_at timestamptz,
              updated_at timestamptz, total bigint, lang text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT x.id, x.slug, x.title, x.description, x.cover_image_id, x.og_image_id, x.published_at, x.updated_at, count(*) OVER (), x.lang
  FROM sites.posts x
  JOIN sites.sites s ON s.workspace_id = x.workspace_id AND s.id = x.site_id AND s.published_version_id IS NOT NULL
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE x.site_id = p_site AND x.status = 'published' AND p.taken_down_at IS NULL AND (p_lang IS NULL OR x.lang = p_lang)
  ORDER BY x.published_at DESC, x.id
  LIMIT greatest(p_limit, 0) OFFSET greatest(p_offset, 0) $$;

DROP FUNCTION sites.public_post(uuid, text);
CREATE FUNCTION sites.public_post(p_site uuid, p_slug text)
RETURNS TABLE(id uuid, slug text, title text, description text, cover_image_id uuid, og_image_id uuid, body jsonb, published_at timestamptz,
              updated_at timestamptz, lang text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT x.id, x.slug, x.title, x.description, x.cover_image_id, x.og_image_id, x.body, x.published_at, x.updated_at, x.lang
  FROM sites.posts x
  JOIN sites.sites s ON s.workspace_id = x.workspace_id AND s.id = x.site_id AND s.published_version_id IS NOT NULL
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE x.site_id = p_site AND x.slug = p_slug AND x.status = 'published' AND p.taken_down_at IS NULL $$;

REVOKE ALL ON FUNCTION sites.public_posts(uuid, int, int, text), sites.public_post(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION sites.public_posts(uuid, int, int, text), sites.public_post(uuid, text) TO cnote_app;
