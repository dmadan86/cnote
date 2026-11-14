-- WP9: time tracking and milestone billing.
-- A time entry is a row on a work item (volume, no note). Billed entries carry the draft invoice they went into and are
-- then immutable (enforced by the commands, which lock the rows first). A billing milestone is a projects.versions row
-- extended with money: the amount is always stored (taxable value, before GST); a percent is turned into an amount
-- against the accepted quotation when it is set.
CREATE TABLE projects.time_entries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  item_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id),
  worked_on date NOT NULL,
  minutes integer NOT NULL CHECK (minutes BETWEEN 1 AND 1440),
  note text,
  billable boolean NOT NULL DEFAULT true,
  invoice_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, item_id) REFERENCES core.notes (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, invoice_id) REFERENCES billing.invoices (workspace_id, note_id) ON DELETE SET NULL (invoice_id)
);
CREATE INDEX time_entries_item ON projects.time_entries(workspace_id, item_id);
CREATE INDEX time_entries_user_day ON projects.time_entries(workspace_id, user_id, worked_on);
CREATE INDEX time_entries_unbilled ON projects.time_entries(workspace_id) WHERE invoice_id IS NULL AND billable;
SELECT core.enable_tenant_rls('projects.time_entries');
GRANT SELECT, INSERT, UPDATE, DELETE ON projects.time_entries TO cnote_app;

ALTER TABLE projects.versions
  ADD COLUMN bill_amount_minor bigint CHECK (bill_amount_minor > 0),
  ADD COLUMN bill_percent_bp integer CHECK (bill_percent_bp BETWEEN 1 AND 10000),
  ADD COLUMN quotation_id uuid,
  ADD COLUMN invoice_id uuid,
  ADD FOREIGN KEY (workspace_id, quotation_id) REFERENCES billing.quotations (workspace_id, note_id) ON DELETE SET NULL (quotation_id),
  ADD FOREIGN KEY (workspace_id, invoice_id) REFERENCES billing.invoices (workspace_id, note_id) ON DELETE SET NULL (invoice_id);
CREATE INDEX versions_quotation ON projects.versions(workspace_id, quotation_id) WHERE quotation_id IS NOT NULL;
