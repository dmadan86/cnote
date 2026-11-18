-- Data importers (docs/design/importers.md): contacts, items, opening documents and balances, Tally masters.

-- Opening documents: unpaid invoices and bills carried over from another system. They never post to the ledger on
-- their own (the opening entry carries one Dr 1100 / Cr 2100 sum) and are left out of every tax report.
ALTER TABLE billing.invoices ADD COLUMN opening boolean NOT NULL DEFAULT false;
ALTER TABLE billing.purchase_bills ADD COLUMN opening boolean NOT NULL DEFAULT false;

-- A cancelled opening invoice (an undone import) frees its number so the file can be imported again.
ALTER TABLE billing.invoices DROP CONSTRAINT invoices_workspace_id_number_key;
CREATE UNIQUE INDEX invoices_ws_number_live ON billing.invoices (workspace_id, number) WHERE NOT (opening AND status = 'cancelled');

-- Opening stock is an inventory receipt whose source is the import job.
ALTER TABLE inventory.movements DROP CONSTRAINT movements_source_type_check;
ALTER TABLE inventory.movements ADD CONSTRAINT movements_source_type_check CHECK (source_type = ANY (ARRAY['invoice'::text, 'purchase_bill'::text, 'import'::text]));

-- Import jobs: one table for the Jira/Notion/CSV page importers (kind 'notes') and the data importers.
ALTER TABLE importers.jobs DROP CONSTRAINT jobs_source_check;
ALTER TABLE importers.jobs ADD CONSTRAINT jobs_source_check CHECK (source = ANY (ARRAY['jira'::text, 'confluence'::text, 'notion'::text, 'trello'::text, 'csv'::text, 'data_import'::text]));
ALTER TABLE importers.jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE importers.jobs ADD CONSTRAINT jobs_status_check CHECK (status = ANY (ARRAY['preview'::text, 'queued'::text, 'running'::text, 'done'::text, 'failed'::text, 'undone'::text]));
ALTER TABLE importers.jobs ADD COLUMN kind text NOT NULL DEFAULT 'notes' CHECK (kind IN ('notes', 'contacts', 'items', 'opening_docs', 'tally'));
ALTER TABLE importers.jobs ADD COLUMN filename text;
ALTER TABLE importers.jobs ADD COLUMN undone_at timestamptz;
ALTER TABLE importers.jobs ADD CONSTRAINT jobs_ws_id_key UNIQUE (workspace_id, id);

-- One row per record of the uploaded file. raw is what the file said; fields is the record after column mapping and
-- cleaning. status: ready / fix / existing (decided at preview), then added / failed / skipped once the run applied it.
-- target_id is the record the row created, written in the same savepoint as the status so a rerun never repeats a row.
CREATE TABLE importers.rows (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  job_id uuid NOT NULL,
  row_no integer NOT NULL CHECK (row_no > 0),
  entity text NOT NULL DEFAULT 'row',
  raw jsonb NOT NULL DEFAULT '{}',
  fields jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'ready' CHECK (status IN ('ready', 'fix', 'existing', 'added', 'failed', 'skipped')),
  reason text,
  target_id uuid,
  target_kind text,
  applied_at timestamptz,
  PRIMARY KEY (job_id, row_no),
  FOREIGN KEY (workspace_id, job_id) REFERENCES importers.jobs(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX importers_rows_pending ON importers.rows (job_id, status);
SELECT core.enable_tenant_rls('importers.rows');
GRANT SELECT, INSERT, UPDATE, DELETE ON importers.rows TO cnote_app;

-- The opening-balances draft and its posting: one row per workspace. posted: entry_id is the live journal entry;
-- reversed: reversal_entry_id is its exact negation. Saving a reversed page makes it a draft again (a new post is allowed).
CREATE TABLE core.opening_balances (
  workspace_id uuid PRIMARY KEY REFERENCES core.workspaces(id) ON DELETE CASCADE,
  go_live date NOT NULL,
  lines jsonb NOT NULL DEFAULT '[]',
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'reversed')),
  entry_id uuid,
  reversal_entry_id uuid,
  difference_minor bigint,
  posted_at timestamptz,
  posted_by uuid REFERENCES core.users(id),
  updated_by uuid REFERENCES core.users(id),
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, entry_id) REFERENCES billing.journal_entries(workspace_id, id),
  FOREIGN KEY (workspace_id, reversal_entry_id) REFERENCES billing.journal_entries(workspace_id, id),
  CHECK ((status = 'draft') = (entry_id IS NULL) AND (status = 'reversed') = (reversal_entry_id IS NOT NULL))
);
SELECT core.enable_tenant_rls('core.opening_balances');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.opening_balances TO cnote_app;

-- 3900 Opening balance adjustments (equity): the difference an opening posting needs to balance.
INSERT INTO billing.accounts (workspace_id, code, name, kind)
SELECT id, '3900', 'Opening balance adjustments', 'equity' FROM core.workspaces ON CONFLICT DO NOTHING;
