-- Customer messages: a workspace's own subject and body for one templated email event (invoice.sent, ...).
-- No row, or customised = false, means the default in code (internal/modules/emailtpl). Reset keeps the row and bumps
-- version so a stale editor can never overwrite after a reset.
CREATE TABLE core.email_templates (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  event text NOT NULL CHECK (char_length(event) BETWEEN 1 AND 40),
  subject text CHECK (char_length(subject) BETWEEN 1 AND 200),
  body text CHECK (char_length(body) BETWEEN 1 AND 10000),
  customised boolean NOT NULL DEFAULT true,
  version integer NOT NULL DEFAULT 1,
  updated_by uuid REFERENCES core.users(id),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (NOT customised OR (subject IS NOT NULL AND body IS NOT NULL)),
  PRIMARY KEY (workspace_id, event)
);
SELECT core.enable_tenant_rls('core.email_templates');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.email_templates TO cnote_app;
