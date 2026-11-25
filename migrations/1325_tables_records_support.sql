-- Tables package 2: batch id lists behind record.batch events (17.5) and CSV import uploads/jobs (17.6).
CREATE TABLE tables.batches (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, table_id uuid NOT NULL,
 op text NOT NULL CHECK (op IN ('created','updated','deleted','restored')), record_ids uuid[] NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (workspace_id, id), FOREIGN KEY (workspace_id, table_id) REFERENCES tables.tables(workspace_id, id) ON DELETE CASCADE
);
CREATE TABLE tables.imports (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, table_id uuid NOT NULL, filename text NOT NULL DEFAULT '',
 data bytea NOT NULL, status text NOT NULL DEFAULT 'uploaded' CHECK (status IN ('uploaded','queued','running','done','failed')),
 mapping jsonb, next_row integer NOT NULL DEFAULT 0, counts jsonb NOT NULL DEFAULT '{"created":0,"skipped":0,"failed":0}', error text,
 created_by uuid REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (workspace_id, id), FOREIGN KEY (workspace_id, table_id) REFERENCES tables.tables(workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('tables.batches');
SELECT core.enable_tenant_rls('tables.imports');
GRANT SELECT, INSERT, UPDATE, DELETE ON tables.batches, tables.imports TO cnote_app;
