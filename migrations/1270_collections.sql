-- Collections assistant: automatic payment reminders (docs/design/collections.md, "Binding revisions").
-- Operational only: nothing here is an accounting document. Invoices keep their immutability; pause/opt-out live in reminder_prefs.
CREATE TABLE billing.reminder_log (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  invoice_note_id uuid NOT NULL,
  step smallint NOT NULL CHECK (step BETWEEN 0 AND 4),   -- index into the workspace's five reminder steps
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sent','failed','cancelled')),
  message_id text NOT NULL,                              -- fixed at enqueue; a retried send repeats it
  to_email text NOT NULL,
  due_date date NOT NULL,                                -- the date the step was computed from (IST)
  error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  sent_at timestamptz,
  UNIQUE (workspace_id, invoice_note_id, step),          -- a step is never sent twice
  FOREIGN KEY (workspace_id, invoice_note_id) REFERENCES billing.invoices (workspace_id, note_id) ON DELETE CASCADE
);
CREATE INDEX reminder_log_invoice ON billing.reminder_log (workspace_id, invoice_note_id);
CREATE INDEX reminder_log_queued ON billing.reminder_log (workspace_id, invoice_note_id) WHERE status = 'queued';

-- note_id is an invoice or a contact page.
CREATE TABLE billing.reminder_prefs (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL,
  paused boolean NOT NULL DEFAULT true,
  PRIMARY KEY (workspace_id, note_id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes (workspace_id, id) ON DELETE CASCADE
);

-- One row per workspace per IST day: the claim that the sweep owns that day's work.
CREATE TABLE billing.reminder_runs (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  day date NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, day)
);

SELECT core.enable_tenant_rls('billing.reminder_log');
SELECT core.enable_tenant_rls('billing.reminder_prefs');
SELECT core.enable_tenant_rls('billing.reminder_runs');
GRANT SELECT, INSERT, UPDATE, DELETE ON billing.reminder_log, billing.reminder_prefs, billing.reminder_runs TO cnote_app;
