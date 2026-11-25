-- Tables Phase 1b public forms (docs/design/tables.md 18.2, revised by 18.8).
-- The token is 16 random bytes (128 bits), base64url; only its sha256 (hex) is stored. config is the form (design 18.2).
CREATE TABLE tables.form_links (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL,
  base_id uuid NOT NULL,
  table_id uuid NOT NULL,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
  token_hash text NOT NULL UNIQUE,
  config jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 16384),
  enabled boolean NOT NULL DEFAULT true,
  submissions integer NOT NULL DEFAULT 0,
  last_submission_at timestamptz,
  created_by uuid NOT NULL REFERENCES core.users (id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, base_id) REFERENCES tables.bases (workspace_id, id) ON DELETE CASCADE,
  -- the table must belong to that base (tables has UNIQUE (workspace_id, base_id, id))
  FOREIGN KEY (workspace_id, base_id, table_id) REFERENCES tables.tables (workspace_id, base_id, id) ON DELETE CASCADE
);
CREATE INDEX form_links_table ON tables.form_links (workspace_id, table_id);
SELECT core.enable_tenant_rls('tables.form_links');
GRANT SELECT, INSERT, UPDATE, DELETE ON tables.form_links TO cnote_app;

-- The one lookup an unauthenticated request may make: no tenant is known yet. Disabled, rotated-away, unknown and
-- trashed (form, table or base) all return zero rows. search_path is pinned and every object is schema-qualified, so
-- a caller cannot shadow anything; execute is for cnote_app only.
CREATE FUNCTION tables.resolve_form(p_hash text)
RETURNS TABLE (id uuid, workspace_id uuid, base_id uuid, table_id uuid, config jsonb, created_by uuid, plan text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT f.id, f.workspace_id, f.base_id, f.table_id, f.config, f.created_by, w.plan
  FROM tables.form_links f
  JOIN tables.bases b ON b.workspace_id = f.workspace_id AND b.id = f.base_id AND b.deleted_at IS NULL
  JOIN tables.tables t ON t.workspace_id = f.workspace_id AND t.base_id = b.id AND t.id = f.table_id AND t.deleted_at IS NULL
  JOIN core.workspaces w ON w.id = f.workspace_id
  WHERE f.token_hash = p_hash AND f.enabled $$;
REVOKE ALL ON FUNCTION tables.resolve_form(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION tables.resolve_form(text) TO cnote_app;
