-- Phase 3a shared catalogue and blog storage.
CREATE TABLE sites.catalog_items (
 workspace_id uuid NOT NULL, site_id uuid NOT NULL, item_id uuid NOT NULL,
 position int NOT NULL CHECK (position >= 0),
 category text NOT NULL DEFAULT '' CHECK (char_length(category) <= 40),
 description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 300),
 image_id uuid,
 PRIMARY KEY (workspace_id, site_id, item_id),
 FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE
);
ALTER TABLE sites.sites
 ADD COLUMN catalog_price text NOT NULL DEFAULT 'show_incl' CHECK (catalog_price IN ('show_incl','show_plus','hide')),
 ADD COLUMN catalog_published jsonb CHECK (octet_length(catalog_published::text) < 65536),
 ADD COLUMN catalog_synced_at timestamptz,
 ADD COLUMN page_og_images jsonb NOT NULL DEFAULT '{}'::jsonb;
CREATE TABLE sites.posts (
 workspace_id uuid NOT NULL, site_id uuid NOT NULL, id uuid NOT NULL DEFAULT gen_random_uuid(),
 slug text NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(slug) <= 60),
 title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
 description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 160),
 cover_image_id uuid, og_image_id uuid,
 body jsonb NOT NULL CHECK (octet_length(body::text) < 102400),
 pending jsonb CHECK (octet_length(pending::text) < 110000),
 status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published')),
 published_at timestamptz, rev int NOT NULL DEFAULT 0 CHECK (rev >= 0),
 created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (workspace_id, site_id, id), UNIQUE (workspace_id, site_id, slug),
 FOREIGN KEY (workspace_id, site_id) REFERENCES sites.sites (workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('sites.catalog_items');
SELECT core.enable_tenant_rls('sites.posts');
GRANT SELECT, INSERT, UPDATE, DELETE ON sites.catalog_items, sites.posts TO cnote_app;
