-- Phase 3a catalogue, blog and share images (docs/design/site-builder-phase3.md sections 1, 17.1, 17.5, 17.7).

-- Generated share images are stored as site images but are not photos the owner uploaded: they do not count toward the
-- photo limit and are not listed in the photo library.
ALTER TABLE core.attachments ADD COLUMN generated boolean NOT NULL DEFAULT false;
ALTER TABLE sites.posts ADD COLUMN ai boolean NOT NULL DEFAULT false;  -- drafted by the AI

-- Public reads gain the catalogue snapshot and the share-image ids. A changed return type needs drop and create, with grants.
DROP FUNCTION sites.host_lookup(text);
DROP FUNCTION sites.public_by_id(uuid);
DROP FUNCTION sites.domain_lookup(text);

CREATE FUNCTION sites.host_lookup(p_subdomain text)
RETURNS TABLE(workspace_id uuid, site_id uuid, version_id uuid, name text, data jsonb, pro boolean, published_at timestamptz,
              published_by uuid, form_paused boolean, lead_space_id uuid, catalog_published jsonb, og_images jsonb)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT s.workspace_id, s.id, s.published_version_id, s.name, v.data, coalesce(p.pro, false), s.published_at, s.published_by, s.form_paused, s.lead_space_id,
         s.catalog_published, s.page_og_images
  FROM sites.sites s
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE s.subdomain = p_subdomain AND s.published_version_id IS NOT NULL AND p.taken_down_at IS NULL $$;

CREATE FUNCTION sites.public_by_id(p_site uuid)
RETURNS TABLE(workspace_id uuid, site_id uuid, version_id uuid, name text, data jsonb, pro boolean, published_at timestamptz,
              published_by uuid, form_paused boolean, lead_space_id uuid, catalog_published jsonb, og_images jsonb)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT s.workspace_id, s.id, s.published_version_id, s.name, v.data, coalesce(p.pro, false), s.published_at, s.published_by, s.form_paused, s.lead_space_id,
         s.catalog_published, s.page_og_images
  FROM sites.sites s
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE s.id = p_site AND s.published_version_id IS NOT NULL AND p.taken_down_at IS NULL $$;

CREATE FUNCTION sites.domain_lookup(p_host text)
RETURNS TABLE(workspace_id uuid, site_id uuid, version_id uuid, name text, data jsonb, pro boolean, published_at timestamptz,
              published_by uuid, form_paused boolean, lead_space_id uuid, subdomain text, canonical_host text,
              catalog_published jsonb, og_images jsonb)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT s.workspace_id, s.id, s.published_version_id, s.name, v.data, true, s.published_at, s.published_by, s.form_paused, s.lead_space_id,
         s.subdomain, sites.domain_canonical(s.id), s.catalog_published, s.page_og_images
  FROM sites.live_domains l
  JOIN sites.sites s ON s.workspace_id = l.workspace_id AND s.id = l.site_id
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  WHERE l.hostname = p_host $$;

-- A public image: this site's, and used by the published version, the live catalogue snapshot, a generated page share
-- image, or a live published post (its cover, share image or body). Never a draft or a pending copy (17.7).
CREATE OR REPLACE FUNCTION sites.public_image(p_site uuid, p_image uuid)
RETURNS TABLE(workspace_id uuid, content_type text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT a.workspace_id, a.content_type
  FROM core.attachments a
  JOIN sites.sites s ON s.workspace_id = a.workspace_id AND s.id = a.site_id
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE a.id = p_image AND a.site_id = p_site AND p.taken_down_at IS NULL
    AND (jsonb_path_exists(v.data, '$.** ? (@ == $img)', jsonb_build_object('img', p_image::text))
      OR jsonb_path_exists(coalesce(s.catalog_published, '[]'::jsonb), '$.** ? (@ == $img)', jsonb_build_object('img', p_image::text))
      OR jsonb_path_exists(s.page_og_images, '$.** ? (@ == $img)', jsonb_build_object('img', p_image::text))
      OR EXISTS (SELECT 1 FROM sites.posts x WHERE x.workspace_id = s.workspace_id AND x.site_id = s.id AND x.status = 'published'
                 AND (x.cover_image_id = p_image OR x.og_image_id = p_image
                      OR jsonb_path_exists(x.body, '$.** ? (@ == $img)', jsonb_build_object('img', p_image::text))))) $$;

-- Published posts of a live site, newest first, without bodies. total is the count of all published posts.
CREATE FUNCTION sites.public_posts(p_site uuid, p_limit int, p_offset int)
RETURNS TABLE(id uuid, slug text, title text, description text, cover_image_id uuid, og_image_id uuid, published_at timestamptz,
              updated_at timestamptz, total bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT x.id, x.slug, x.title, x.description, x.cover_image_id, x.og_image_id, x.published_at, x.updated_at, count(*) OVER ()
  FROM sites.posts x
  JOIN sites.sites s ON s.workspace_id = x.workspace_id AND s.id = x.site_id AND s.published_version_id IS NOT NULL
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE x.site_id = p_site AND x.status = 'published' AND p.taken_down_at IS NULL
  ORDER BY x.published_at DESC, x.id
  LIMIT greatest(p_limit, 0) OFFSET greatest(p_offset, 0) $$;

CREATE FUNCTION sites.public_post(p_site uuid, p_slug text)
RETURNS TABLE(id uuid, slug text, title text, description text, cover_image_id uuid, og_image_id uuid, body jsonb, published_at timestamptz,
              updated_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT x.id, x.slug, x.title, x.description, x.cover_image_id, x.og_image_id, x.body, x.published_at, x.updated_at
  FROM sites.posts x
  JOIN sites.sites s ON s.workspace_id = x.workspace_id AND s.id = x.site_id AND s.published_version_id IS NOT NULL
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE x.site_id = p_site AND x.slug = p_slug AND x.status = 'published' AND p.taken_down_at IS NULL $$;

REVOKE ALL ON FUNCTION sites.host_lookup(text), sites.public_by_id(uuid), sites.domain_lookup(text),
  sites.public_posts(uuid, int, int), sites.public_post(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION sites.host_lookup(text), sites.public_by_id(uuid), sites.domain_lookup(text),
  sites.public_posts(uuid, int, int), sites.public_post(uuid, text) TO cnote_app;
