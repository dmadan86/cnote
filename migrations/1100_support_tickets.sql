-- WP12: support tickets. A ticket is a typed note ('ticket') so comments, access and search come for free; this table
-- holds its status, priority, assignee and SLA clock. The contact may be a person or a company (crm.contacts.kind).
CREATE TABLE crm.tickets (
  note_id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  contact_id uuid NOT NULL,
  invoice_id uuid,
  project_id uuid,                      -- a project is a space
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','pending','resolved','closed')),
  priority text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low','normal','high','urgent')),
  assignee_id uuid REFERENCES core.users(id),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 4000),
  source text NOT NULL DEFAULT 'internal' CHECK (source IN ('internal','form')),
  sla_due_at timestamptz NOT NULL,
  resolved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, contact_id) REFERENCES crm.contacts (workspace_id, note_id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, invoice_id) REFERENCES billing.invoices (workspace_id, note_id) ON DELETE SET NULL (invoice_id),
  FOREIGN KEY (workspace_id, project_id) REFERENCES core.spaces (workspace_id, id) ON DELETE SET NULL (project_id)
);
CREATE INDEX tickets_status ON crm.tickets(workspace_id, status, sla_due_at);
CREATE INDEX tickets_contact ON crm.tickets(workspace_id, contact_id);
SELECT core.enable_tenant_rls('crm.tickets');
GRANT SELECT, INSERT, UPDATE, DELETE ON crm.tickets TO cnote_app;

-- Per-workspace priority -> hours to resolve. A missing row means the default in code (urgent 4, high 8, normal 24, low 72).
CREATE TABLE crm.ticket_sla (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  priority text NOT NULL CHECK (priority IN ('low','normal','high','urgent')),
  hours integer NOT NULL CHECK (hours BETWEEN 1 AND 8760),
  PRIMARY KEY (workspace_id, priority)
);
SELECT core.enable_tenant_rls('crm.ticket_sla');
GRANT SELECT, INSERT, UPDATE, DELETE ON crm.ticket_sla TO cnote_app;
