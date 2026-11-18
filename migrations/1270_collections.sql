-- Collections assistant: automatic payment reminders (docs/design/collections.md, "Binding revisions").
-- Operational only: nothing here is an accounting document. Invoices keep their immutability; pause/opt-out live in reminder_prefs.
CREATE TABLE billing.reminder_log (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  invoice_note_id uuid NOT NULL,
  step smallint NOT NULL CHECK (step BETWEEN 0 AND 4),   -- index into the workspace's five reminder steps
  -- 'sending' is committed right before the SMTP call: a retry that finds it old (a crash after the send) marks the reminder sent, not resent.
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sending','sent','failed','cancelled')),
  sending_at timestamptz,
  message_id text NOT NULL,                              -- fixed at enqueue; a retried send repeats it
  contact_id uuid NOT NULL,                              -- the recipient intended at enqueue; dispatch cancels if the invoice's contact or its email changed
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
  done boolean NOT NULL DEFAULT false,                   -- false while an invoice failed to enqueue: the next sweep retries it the same day
  PRIMARY KEY (workspace_id, day)
);

-- Failed enqueue attempts per invoice step (capped by the sweep).
CREATE TABLE billing.reminder_attempts (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  invoice_note_id uuid NOT NULL,
  step smallint NOT NULL,
  attempts smallint NOT NULL DEFAULT 0,
  last_error text,
  PRIMARY KEY (workspace_id, invoice_note_id, step),
  FOREIGN KEY (workspace_id, invoice_note_id) REFERENCES billing.invoices (workspace_id, note_id) ON DELETE CASCADE
);

SELECT core.enable_tenant_rls('billing.reminder_log');
SELECT core.enable_tenant_rls('billing.reminder_prefs');
SELECT core.enable_tenant_rls('billing.reminder_runs');
SELECT core.enable_tenant_rls('billing.reminder_attempts');
GRANT SELECT, INSERT, UPDATE, DELETE ON billing.reminder_log, billing.reminder_prefs, billing.reminder_runs, billing.reminder_attempts TO cnote_app;
