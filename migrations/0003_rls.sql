-- ADR-003: RLS on every tenant table. The app connects as cnote_app (no BYPASSRLS, not table owner),
-- so ENABLE (not FORCE) is enough; SECURITY DEFINER functions run as the owner for the few cross-tenant paths.
CREATE OR REPLACE FUNCTION core.enable_tenant_rls(tbl regclass) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %s', tbl);
  EXECUTE format('CREATE POLICY tenant_isolation ON %s USING (workspace_id = core.current_workspace()) WITH CHECK (workspace_id = core.current_workspace())', tbl);
END $$;

DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.table_schema, c.table_name FROM information_schema.columns c
    JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
    WHERE c.column_name = 'workspace_id'
      AND c.table_schema IN ('core','projects','docs','crm','billing','ai','importers')
  LOOP
    PERFORM core.enable_tenant_rls(format('%I.%I', r.table_schema, r.table_name)::regclass);
  END LOOP;
END $$;

-- Workspaces: a member sees their workspace row when it is the current tenant.
ALTER TABLE core.workspaces ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON core.workspaces USING (id = core.current_workspace());
-- Workspace creation and membership listing happen through SECURITY DEFINER functions below.

CREATE OR REPLACE FUNCTION core.create_workspace(p_name text, p_slug text, p_owner uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = core, pg_temp AS $$
DECLARE ws uuid; s text := p_slug;
BEGIN
  LOOP
    BEGIN
      INSERT INTO core.workspaces(name, slug) VALUES (p_name, s) RETURNING id INTO ws;
      EXIT;
    EXCEPTION WHEN unique_violation THEN
      s := p_slug || '-' || substr(md5(random()::text), 1, 4);  -- concurrent sign-ups with the same name
    END;
  END LOOP;
  INSERT INTO core.memberships(workspace_id, user_id, role) VALUES (ws, p_owner, 'owner');
  RETURN ws;
END $$;

CREATE OR REPLACE FUNCTION core.user_workspaces(p_user uuid)
RETURNS TABLE(id uuid, name text, slug text, role text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT w.id, w.name, w.slug, m.role FROM core.workspaces w JOIN core.memberships m ON m.workspace_id = w.id
  WHERE m.user_id = p_user ORDER BY w.created_at
$$;

-- API key lookup must happen before the tenant is known.
CREATE OR REPLACE FUNCTION core.resolve_api_key(p_hash text)
RETURNS TABLE(id uuid, workspace_id uuid, user_id uuid, agent_id uuid, scopes text[])
LANGUAGE sql SECURITY DEFINER SET search_path = core, pg_temp AS $$
  UPDATE core.api_keys SET last_used_at = now() WHERE key_hash = p_hash AND revoked_at IS NULL
  RETURNING id, workspace_id, user_id, agent_id, scopes
$$;

-- Workers sweep all tenants: list workspace ids only.
CREATE OR REPLACE FUNCTION core.all_workspace_ids() RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$ SELECT id FROM core.workspaces $$;

-- Job claim across tenants (jobs carry their workspace_id; the worker then sets the tenant).
CREATE OR REPLACE FUNCTION core.claim_jobs(p_kinds text[], p_limit int)
RETURNS SETOF core.jobs LANGUAGE sql SECURITY DEFINER SET search_path = core, pg_temp AS $$
  UPDATE core.jobs j SET status = 'running', attempts = attempts + 1, updated_at = now()
  WHERE j.id IN (
    SELECT id FROM core.jobs WHERE status = 'queued' AND run_at <= now() AND (p_kinds IS NULL OR kind = ANY(p_kinds))
    ORDER BY run_at, id FOR UPDATE SKIP LOCKED LIMIT p_limit)
  RETURNING j.*
$$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cnote_app') THEN
    CREATE ROLE cnote_app LOGIN NOSUPERUSER NOBYPASSRLS; -- password set out of band (scripts/setup-db.sh reads DB_APP_PASSWORD)
  END IF;
END $$;
GRANT USAGE ON SCHEMA core, projects, docs, crm, billing, ai, importers TO cnote_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA core, projects, docs, crm, billing, ai, importers TO cnote_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA core, projects, docs, crm, billing, ai, importers TO cnote_app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA core TO cnote_app;
-- users/sessions are global: the app may read/write them (no workspace_id).
-- Tables created by later migrations (run by the same owner role) are granted automatically,
-- but must still call core.enable_tenant_rls('schema.table') themselves.
ALTER DEFAULT PRIVILEGES IN SCHEMA core, projects, docs, crm, billing, ai, importers
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO cnote_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA core, projects, docs, crm, billing, ai, importers
  GRANT USAGE, SELECT ON SEQUENCES TO cnote_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA core GRANT EXECUTE ON FUNCTIONS TO cnote_app;
