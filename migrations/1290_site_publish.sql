-- Website builder phase 1 (docs/design/site-builder-phase1.md sections 2, 17): publish, platform controls, site images,
-- the public contact form and email verification.

-- ---- publishing: a pointer to an immutable version, plus where enquiries land ----
ALTER TABLE sites.sites
  ADD COLUMN subdomain text UNIQUE CHECK (subdomain ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(subdomain) BETWEEN 3 AND 40),
  ADD COLUMN published_version_id uuid,
  ADD COLUMN published_at timestamptz,
  ADD COLUMN published_by uuid REFERENCES core.users(id),
  ADD COLUMN lead_space_id uuid,                       -- fixed at first publish: the workspace's oldest space (no FK: the space may be deleted later)
  ADD COLUMN form_count int NOT NULL DEFAULT 0,        -- accepted contact-form submissions
  ADD COLUMN form_paused boolean NOT NULL DEFAULT false,
  ADD FOREIGN KEY (workspace_id, id, published_version_id) REFERENCES sites.site_versions (workspace_id, site_id, id);
ALTER TABLE sites.site_turns ADD COLUMN image_ids uuid[] NOT NULL DEFAULT '{}';

-- ---- site images hang on a site instead of a page ----
ALTER TABLE core.attachments ALTER COLUMN note_id DROP NOT NULL,
  ADD COLUMN site_id uuid,
  ADD FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE,
  ADD CHECK ((note_id IS NULL) <> (site_id IS NULL));
CREATE INDEX attachments_site ON core.attachments (workspace_id, site_id) WHERE site_id IS NOT NULL;

-- Website Pro, takedown and rename are platform decisions. The app role can read this table (its own tenant) but has no
-- INSERT/UPDATE/DELETE: the only writer is sites.platform_set below.
CREATE TABLE sites.site_platform (
  workspace_id uuid NOT NULL,
  site_id uuid PRIMARY KEY,
  pro boolean NOT NULL DEFAULT false,
  taken_down_at timestamptz,
  taken_down_reason text,
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('sites.site_platform');
GRANT SELECT ON sites.site_platform TO cnote_app;

CREATE TABLE sites.platform_actions (                  -- audit; no grant to cnote_app
  id bigserial PRIMARY KEY,
  actor_user_id uuid NOT NULL REFERENCES core.users(id),
  actor_email text NOT NULL,
  workspace_id uuid NOT NULL,
  site_id uuid NOT NULL,
  action text NOT NULL CHECK (action IN ('pro_on','pro_off','takedown','restore','rename')),
  old_subdomain text, new_subdomain text,
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 500),
  created_at timestamptz NOT NULL DEFAULT now()
);

-- The address can be set once by the app (the first publish) and changed only by sites.platform_set (which runs as the owner).
CREATE FUNCTION sites.guard_subdomain() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.subdomain IS NOT NULL AND NEW.subdomain IS DISTINCT FROM OLD.subdomain AND current_user = 'cnote_app' THEN
    RAISE EXCEPTION 'the address of a published site can only be changed by the platform' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER sites_guard_subdomain BEFORE UPDATE OF subdomain ON sites.sites FOR EACH ROW EXECUTE FUNCTION sites.guard_subdomain();

-- Public reads: no tenant is known, so these cross workspaces and return only published, not taken down sites.
CREATE FUNCTION sites.host_lookup(p_subdomain text)
RETURNS TABLE(workspace_id uuid, site_id uuid, version_id uuid, name text, data jsonb, pro boolean, published_at timestamptz,
              published_by uuid, form_paused boolean, lead_space_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT s.workspace_id, s.id, s.published_version_id, s.name, v.data, coalesce(p.pro, false), s.published_at, s.published_by, s.form_paused, s.lead_space_id
  FROM sites.sites s
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE s.subdomain = p_subdomain AND s.published_version_id IS NOT NULL AND p.taken_down_at IS NULL $$;

CREATE FUNCTION sites.public_by_id(p_site uuid)
RETURNS TABLE(workspace_id uuid, site_id uuid, version_id uuid, name text, data jsonb, pro boolean, published_at timestamptz,
              published_by uuid, form_paused boolean, lead_space_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT s.workspace_id, s.id, s.published_version_id, s.name, v.data, coalesce(p.pro, false), s.published_at, s.published_by, s.form_paused, s.lead_space_id
  FROM sites.sites s
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE s.id = p_site AND s.published_version_id IS NOT NULL AND p.taken_down_at IS NULL $$;

-- One query decides a public image: it belongs to this site and workspace, and the published version references it.
CREATE FUNCTION sites.public_image(p_site uuid, p_image uuid)
RETURNS TABLE(workspace_id uuid, content_type text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT a.workspace_id, a.content_type
  FROM core.attachments a
  JOIN sites.sites s ON s.workspace_id = a.workspace_id AND s.id = a.site_id
  JOIN sites.site_versions v ON v.workspace_id = s.workspace_id AND v.site_id = s.id AND v.id = s.published_version_id
  LEFT JOIN sites.site_platform p ON p.site_id = s.id
  WHERE a.id = p_image AND a.site_id = p_site AND p.taken_down_at IS NULL
    AND jsonb_path_exists(v.data, '$.** ? (@ == $img)', jsonb_build_object('img', p_image::text)) $$;

CREATE FUNCTION sites.subdomain_taken(p_subdomain text, p_except uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM sites.sites WHERE subdomain = p_subdomain AND id IS DISTINCT FROM p_except) $$;

-- The only writer of sites.site_platform. Gated in Go (platform admin by user id); p_actor is the command principal.
CREATE FUNCTION sites.platform_set(p_actor uuid, p_subdomain text, p_workspace_slug text, p_action text, p_reason text, p_new_subdomain text)
RETURNS TABLE(workspace_id uuid, site_id uuid, subdomain text, pro boolean, taken_down boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = sites, core, pg_temp AS $$
#variable_conflict use_column
DECLARE s sites.sites; mail text; old_sub text;
BEGIN
  IF p_action NOT IN ('pro_on','pro_off','takedown','restore','rename') THEN RAISE EXCEPTION 'unknown action' USING ERRCODE = '22023'; END IF;
  SELECT email INTO mail FROM core.users WHERE id = p_actor;
  IF mail IS NULL THEN RAISE EXCEPTION 'unknown actor' USING ERRCODE = '42501'; END IF;
  SELECT x.* INTO s FROM sites.sites x
   WHERE (p_subdomain IS NOT NULL AND x.subdomain = p_subdomain)
      OR (p_subdomain IS NULL AND x.workspace_id = (SELECT w.id FROM core.workspaces w WHERE w.slug = p_workspace_slug))
   LIMIT 1 FOR UPDATE;
  IF NOT FOUND THEN RETURN; END IF;
  old_sub := s.subdomain;
  INSERT INTO sites.site_platform (workspace_id, site_id) VALUES (s.workspace_id, s.id) ON CONFLICT (site_id) DO NOTHING;
  IF p_action = 'pro_on' THEN UPDATE sites.site_platform SET pro = true, updated_at = now() WHERE site_platform.site_id = s.id;
  ELSIF p_action = 'pro_off' THEN UPDATE sites.site_platform SET pro = false, updated_at = now() WHERE site_platform.site_id = s.id;
  ELSIF p_action = 'takedown' THEN UPDATE sites.site_platform SET taken_down_at = now(), taken_down_reason = p_reason, updated_at = now() WHERE site_platform.site_id = s.id;
  ELSIF p_action = 'restore' THEN UPDATE sites.site_platform SET taken_down_at = NULL, taken_down_reason = NULL, updated_at = now() WHERE site_platform.site_id = s.id;
  ELSE
    IF old_sub IS NULL OR p_new_subdomain IS NULL THEN RAISE EXCEPTION 'this site has no address to rename' USING ERRCODE = '22023'; END IF;
    UPDATE sites.sites SET subdomain = p_new_subdomain, updated_at = now() WHERE sites.id = s.id;
  END IF;
  INSERT INTO sites.platform_actions (actor_user_id, actor_email, workspace_id, site_id, action, old_subdomain, new_subdomain, reason)
    VALUES (p_actor, mail, s.workspace_id, s.id, p_action, old_sub, CASE WHEN p_action = 'rename' THEN p_new_subdomain END, p_reason);
  RETURN QUERY SELECT s.workspace_id, s.id, (SELECT x.subdomain FROM sites.sites x WHERE x.id = s.id), p.pro, p.taken_down_at IS NOT NULL
    FROM sites.site_platform p WHERE p.site_id = s.id;
END $$;

REVOKE ALL ON FUNCTION sites.platform_set(uuid, text, text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION sites.platform_set(uuid, text, text, text, text, text) TO cnote_app;
GRANT EXECUTE ON FUNCTION sites.host_lookup(text), sites.public_by_id(uuid), sites.public_image(uuid, uuid), sites.subdomain_taken(text, uuid) TO cnote_app;

-- ---- the contact form: one row per accepted submission (duplicate suppression, the 7-day lead count) ----
CREATE TABLE sites.form_log (
  id bigserial PRIMARY KEY,
  workspace_id uuid NOT NULL,
  site_id uuid NOT NULL,
  fingerprint text NOT NULL,                           -- sha256 of email, phone and message
  deal_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX form_log_site ON sites.form_log (site_id, created_at DESC);
SELECT core.enable_tenant_rls('sites.form_log');
GRANT SELECT, INSERT, DELETE ON sites.form_log TO cnote_app;
GRANT USAGE, SELECT ON SEQUENCE sites.form_log_id_seq TO cnote_app;

-- Shared hourly/daily counters for public endpoints and sends (keyed by a keyed hash, never a raw address). No tenant.
CREATE TABLE core.rate_buckets (
  key text NOT NULL,
  bucket timestamptz NOT NULL,
  n int NOT NULL DEFAULT 0,
  PRIMARY KEY (key, bucket)
);
GRANT SELECT, INSERT, UPDATE, DELETE ON core.rate_buckets TO cnote_app;

-- ---- email verification (design 17.8) ----
ALTER TABLE core.users ADD COLUMN email_verified_at timestamptz;
CREATE TABLE core.email_codes (
  user_id uuid PRIMARY KEY REFERENCES core.users(id) ON DELETE CASCADE,
  email text NOT NULL,                                 -- the address the code was sent to
  code_hash text NOT NULL,                             -- HMAC of user, email and code
  expires_at timestamptz NOT NULL,
  attempts int NOT NULL DEFAULT 0
);
GRANT SELECT, INSERT, UPDATE, DELETE ON core.email_codes TO cnote_app;

-- ---- notifications: website enquiries ----
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_kind_check;
ALTER TABLE core.notifications
  ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('mention','approval_requested','approval_decided','reminder','assigned','quote_decided','chat_mention','lead'));
