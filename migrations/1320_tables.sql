CREATE SCHEMA IF NOT EXISTS tables;
GRANT USAGE ON SCHEMA tables TO cnote_app;

CREATE TABLE tables.bases (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
 space_id uuid NOT NULL, name text NOT NULL, icon text, template text, record_count integer NOT NULL DEFAULT 0,
 created_by uuid REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
 UNIQUE (workspace_id, id), FOREIGN KEY (workspace_id, space_id) REFERENCES core.spaces(workspace_id, id) ON DELETE CASCADE
);
CREATE TABLE tables.tables (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, base_id uuid NOT NULL, name text NOT NULL,
 position integer NOT NULL DEFAULT 0, primary_field_id uuid, next_seq bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
 UNIQUE (workspace_id, id), UNIQUE (workspace_id, base_id, id),
 FOREIGN KEY (workspace_id, base_id) REFERENCES tables.bases(workspace_id, id) ON DELETE CASCADE
);
CREATE TABLE tables.fields (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, table_id uuid NOT NULL, name text NOT NULL,
 type text NOT NULL CHECK (type IN ('text','long_text','number','percent','currency','date','checkbox','single_select','multi_select','phone','email','url','user','attachment','link','autonumber','created_time','modified_time','created_by','modified_by')),
 config jsonb NOT NULL DEFAULT '{}', position integer NOT NULL DEFAULT 0, locked boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
 UNIQUE (workspace_id, id), UNIQUE (workspace_id, table_id, id),
 FOREIGN KEY (workspace_id, table_id) REFERENCES tables.tables(workspace_id, id) ON DELETE CASCADE
);
ALTER TABLE tables.tables ADD FOREIGN KEY (workspace_id, id, primary_field_id) REFERENCES tables.fields(workspace_id, table_id, id);
CREATE TABLE tables.records (
 id uuid NOT NULL DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, table_id uuid NOT NULL, seq bigint NOT NULL,
 data jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data) = 'object' AND octet_length(data::text) <= 65536),
 created_by uuid REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT now(),
 updated_by uuid REFERENCES core.users(id), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
 PRIMARY KEY (workspace_id, table_id, id), UNIQUE (workspace_id, id), UNIQUE (table_id, seq),
 FOREIGN KEY (workspace_id, table_id) REFERENCES tables.tables(workspace_id, id) ON DELETE CASCADE
) WITH (fillfactor = 80);
CREATE INDEX records_default_order ON tables.records(workspace_id, table_id, created_at, id) WHERE deleted_at IS NULL;
CREATE TABLE tables.links (
 workspace_id uuid NOT NULL, base_id uuid NOT NULL, field_id uuid NOT NULL, from_record uuid NOT NULL, to_record uuid NOT NULL,
 PRIMARY KEY (workspace_id, field_id, from_record, to_record),
 FOREIGN KEY (workspace_id, base_id) REFERENCES tables.bases(workspace_id, id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id, field_id) REFERENCES tables.fields(workspace_id, id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id, from_record) REFERENCES tables.records(workspace_id, id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id, to_record) REFERENCES tables.records(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX links_reverse ON tables.links(to_record, field_id);
CREATE TABLE tables.views (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, table_id uuid NOT NULL, name text NOT NULL,
 type text NOT NULL CHECK (type IN ('grid','kanban','form')), config jsonb NOT NULL DEFAULT '{}' CHECK (octet_length(config::text) <= 16384),
 position integer NOT NULL DEFAULT 0, created_by uuid REFERENCES core.users(id),
 UNIQUE (workspace_id, id), FOREIGN KEY (workspace_id, table_id) REFERENCES tables.tables(workspace_id, id) ON DELETE CASCADE
);
CREATE TABLE tables.record_history (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, workspace_id uuid NOT NULL, table_id uuid NOT NULL, record_id uuid NOT NULL,
 actor_type text NOT NULL, actor_id uuid, at timestamptz NOT NULL DEFAULT now(), change jsonb NOT NULL,
 FOREIGN KEY (workspace_id, table_id, record_id) REFERENCES tables.records(workspace_id, table_id, id) ON DELETE CASCADE
);
-- history lookups and the cascade on purge; field names unique per table among live fields
CREATE INDEX record_history_record ON tables.record_history(workspace_id, record_id, at);
CREATE UNIQUE INDEX fields_name_unique ON tables.fields(table_id, lower(name)) WHERE deleted_at IS NULL;
SELECT core.enable_tenant_rls('tables.bases');
SELECT core.enable_tenant_rls('tables.tables');
SELECT core.enable_tenant_rls('tables.fields');
SELECT core.enable_tenant_rls('tables.records');
SELECT core.enable_tenant_rls('tables.links');
SELECT core.enable_tenant_rls('tables.views');
SELECT core.enable_tenant_rls('tables.record_history');
GRANT SELECT, INSERT, UPDATE, DELETE ON tables.bases, tables.tables, tables.fields, tables.records, tables.links, tables.views TO cnote_app;
GRANT SELECT, INSERT, DELETE ON tables.record_history TO cnote_app;  -- append-only; the retention sweep deletes
