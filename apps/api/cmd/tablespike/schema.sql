-- Throwaway scratch schema for the Tables perf spike. Not a migration.
CREATE SCHEMA core;
CREATE SCHEMA spike;
CREATE FUNCTION core.current_workspace() RETURNS uuid
LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('app.workspace_id', true), '')::uuid $$;
CREATE FUNCTION core.enable_tenant_rls(tbl regclass) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %s', tbl);
  EXECUTE format('CREATE POLICY tenant_isolation ON %s USING (workspace_id = core.current_workspace()) WITH CHECK (workspace_id = core.current_workspace())', tbl);
END $$;

CREATE EXTENSION pg_trgm SCHEMA spike;

-- Leakproof helpers. Under RLS the planner only uses an index for a user qual if every function in
-- it is LEAKPROOF; the built-in jsonb ->>, numeric compares, jsonb @> and ILIKE are not.
CREATE FUNCTION spike.jt(jsonb, text) RETURNS text AS 'jsonb_object_field_text' LANGUAGE internal IMMUTABLE STRICT LEAKPROOF PARALLEL SAFE;
-- float8 (not numeric): float8 comparisons are leakproof, numeric ones are not. Never raises.
CREATE FUNCTION spike.jf(j jsonb, k text) RETURNS float8 LANGUAGE plpgsql IMMUTABLE STRICT LEAKPROOF PARALLEL SAFE AS
$$ BEGIN IF jsonb_typeof(j->k) = 'number' THEN RETURN (j->k)::float8; END IF; RETURN NULL; END $$;
CREATE FUNCTION spike.jcontains(jsonb, jsonb) RETURNS bool AS 'jsonb_contains' LANGUAGE internal IMMUTABLE STRICT LEAKPROOF PARALLEL SAFE;
-- RESTRICT/JOIN matter: without them the planner guesses a default selectivity and ignores the GIN index.
CREATE OPERATOR spike.@@> (LEFTARG = jsonb, RIGHTARG = jsonb, FUNCTION = spike.jcontains, RESTRICT = matchingsel, JOIN = matchingjoinsel);
CREATE FUNCTION spike.jilike(text, text) RETURNS bool AS 'texticlike' LANGUAGE internal IMMUTABLE STRICT LEAKPROOF PARALLEL SAFE;
CREATE OPERATOR spike.~~~ (LEFTARG = text, RIGHTARG = text, FUNCTION = spike.jilike, RESTRICT = iclikesel, JOIN = iclikejoinsel);
CREATE OPERATOR CLASS spike.jsonb_lp_ops FOR TYPE jsonb USING gin AS
  OPERATOR 7 spike.@@>(jsonb, jsonb), FUNCTION 1 btint4cmp(int4, int4),
  FUNCTION 2 gin_extract_jsonb_path(jsonb, internal, internal),
  FUNCTION 3 gin_extract_jsonb_query_path(jsonb, internal, int2, internal, internal, internal, internal),
  FUNCTION 4 gin_consistent_jsonb_path(internal, int2, jsonb, int4, internal, internal, internal, internal),
  FUNCTION 6 gin_triconsistent_jsonb_path(internal, int2, jsonb, int4, internal, internal, internal), STORAGE int4;
CREATE OPERATOR CLASS spike.trgm_lp_ops FOR TYPE text USING gin AS
  OPERATOR 4 spike.~~~(text, text), FUNCTION 1 btint4cmp(int4, int4),
  FUNCTION 2 spike.gin_extract_value_trgm(text, internal),
  FUNCTION 3 spike.gin_extract_query_trgm(text, internal, int2, internal, internal, internal, internal),
  FUNCTION 4 spike.gin_trgm_consistent(internal, int2, text, int4, internal, internal, internal, internal),
  FUNCTION 6 spike.gin_trgm_triconsistent(internal, int2, text, int4, internal, internal, internal), STORAGE int4;

CREATE TABLE spike.records (
  workspace_id uuid NOT NULL, table_id uuid NOT NULL, id uuid NOT NULL, data jsonb NOT NULL,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, deleted_at timestamptz,
  PRIMARY KEY (workspace_id, table_id, id)) WITH (fillfactor = 80); -- free space per page lets non-indexed updates be HOT
-- default view order; every strategy has it
CREATE INDEX ix_base_created ON spike.records (workspace_id, table_id, created_at, id) WHERE deleted_at IS NULL;
CREATE TABLE spike.links (
  workspace_id uuid NOT NULL, from_record uuid NOT NULL, field_id text NOT NULL, to_record uuid NOT NULL,
  PRIMARY KEY (from_record, field_id, to_record));
CREATE INDEX ix_links_rev ON spike.links (to_record, field_id, from_record);
SELECT core.enable_tenant_rls('spike.records');
SELECT core.enable_tenant_rls('spike.links');
GRANT USAGE ON SCHEMA core, spike TO cnote_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON spike.records, spike.links TO cnote_app;
