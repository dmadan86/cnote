-- Tables Phase 1b record attachments (docs/design/tables.md section 18, revised by 18.8).

-- ---- attachments hang on a record's attachment field ----
-- A staged upload is a 'pending' row: it has the field, table and base but no record yet, and is made 'attached' inside
-- the record write. Pending rows older than 24 h are swept. staged_by is 'u:<user id>' or 'a:<agent id>', taken from the
-- authenticated principal: only that caller can attach the handle.
-- Ownership is a chain of composite foreign keys, so a row can never mix a field, table, base or record of another
-- parent, in the same workspace or in another one: workspace -> base -> table -> field and record.
ALTER TABLE tables.bases ADD COLUMN attachment_bytes bigint NOT NULL DEFAULT 0 CHECK (attachment_bytes >= 0);

ALTER TABLE core.attachments
  ADD COLUMN base_id uuid,
  ADD COLUMN table_id uuid,
  ADD COLUMN field_id uuid,
  ADD COLUMN record_id uuid,
  ADD COLUMN status text NOT NULL DEFAULT 'attached' CHECK (status IN ('pending', 'attached')),
  ADD COLUMN staged_by text,
  ADD CONSTRAINT attachments_table_fk FOREIGN KEY (workspace_id, base_id, table_id) REFERENCES tables.tables (workspace_id, base_id, id) ON DELETE CASCADE,
  ADD CONSTRAINT attachments_field_fk FOREIGN KEY (workspace_id, table_id, field_id) REFERENCES tables.fields (workspace_id, table_id, id) ON DELETE CASCADE,
  ADD CONSTRAINT attachments_record_fk FOREIGN KEY (workspace_id, table_id, record_id) REFERENCES tables.records (workspace_id, table_id, id) ON DELETE CASCADE;
-- exactly one owner; a record file carries base, table and field together; pending exactly when it has no record
ALTER TABLE core.attachments DROP CONSTRAINT attachments_check,
  ADD CONSTRAINT attachments_owner CHECK (
    num_nonnulls(note_id, site_id, field_id) = 1
    AND num_nonnulls(base_id, table_id, field_id) IN (0, 3)
    AND (record_id IS NULL OR field_id IS NOT NULL)
    AND (field_id IS NULL OR ((status = 'pending') = (record_id IS NULL) AND staged_by IS NOT NULL))
    AND (field_id IS NOT NULL OR status = 'attached'));
CREATE INDEX attachments_record ON core.attachments (workspace_id, record_id) WHERE record_id IS NOT NULL;
CREATE INDEX attachments_base ON core.attachments (workspace_id, base_id) WHERE base_id IS NOT NULL;
CREATE INDEX attachments_pending ON core.attachments (workspace_id, base_id, staged_by, created_at) WHERE status = 'pending';
CREATE INDEX attachments_pending_age ON core.attachments (created_at) WHERE status = 'pending' AND field_id IS NOT NULL;

-- ---- object lifecycle ----
-- An S3 object is announced here (state 'intent', committed in its own transaction) before it is written, and becomes
-- 'live' when its row is attached to a record. Keys are random UUIDs and never reused. No FK: these rows must outlive
-- the rows and the workspace they describe.
CREATE TABLE core.file_objects (
  key text PRIMARY KEY,
  workspace_id uuid NOT NULL,
  state text NOT NULL CHECK (state IN ('intent', 'live')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX file_objects_intent ON core.file_objects (created_at) WHERE state = 'intent';
SELECT core.enable_tenant_rls('core.file_objects');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.file_objects TO cnote_app;

-- Objects to delete. A record-file row that disappears for any reason (detach, cascade from a purged record, field,
-- table, base or workspace, the pending sweep) leaves its object key here, once. files.RecordSweep claims rows with
-- FOR UPDATE SKIP LOCKED, deletes an object only when no attachment row has its id, and keeps a failing row (tries
-- and last_error are kept; it is retried with a growing delay and logged from the 10th failure). No FK, by design.
CREATE TABLE core.file_gc (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id uuid NOT NULL,
  object_key text NOT NULL UNIQUE,
  tries integer NOT NULL DEFAULT 0,
  last_error text,
  next_try_at timestamptz NOT NULL DEFAULT now(),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX file_gc_due ON core.file_gc (next_try_at);
SELECT core.enable_tenant_rls('core.file_gc');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.file_gc TO cnote_app;

CREATE FUNCTION core.attachment_gc() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  INSERT INTO core.file_gc (workspace_id, object_key) VALUES (OLD.workspace_id, OLD.workspace_id::text || '/' || OLD.id::text)
    ON CONFLICT (object_key) DO NOTHING;
  RETURN OLD;
END $$;
CREATE TRIGGER attachments_gc BEFORE DELETE ON core.attachments FOR EACH ROW
  WHEN (OLD.field_id IS NOT NULL AND OLD.storage_key = 's3') EXECUTE FUNCTION core.attachment_gc();

-- The sweep visits every workspace that has work, including workspaces that were deleted (their queue rows stay).
CREATE FUNCTION core.file_sweep_workspaces() RETURNS SETOF uuid LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT workspace_id FROM core.file_gc WHERE next_try_at <= now()
  UNION SELECT workspace_id FROM core.file_objects WHERE state = 'intent' AND created_at < now() - interval '24 hours'
  UNION SELECT workspace_id FROM core.attachments WHERE status = 'pending' AND field_id IS NOT NULL AND created_at < now() - interval '24 hours'
$$;
