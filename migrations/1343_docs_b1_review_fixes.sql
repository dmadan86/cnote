-- Docs B1 review fixes: at-most-once notification mail.
-- core.mail_sends is committed in its own short transaction BEFORE SMTP, so a crash or rollback after the send leaves proof that
-- the send may have happened; a retried job then marks its notifications sent_unknown instead of sending a second mail.
ALTER TABLE core.notifications DROP CONSTRAINT notifications_email_state_check;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_email_state_check
  CHECK (email_state IN ('queued','mailed','suppressed','sent_unknown')) NOT VALID;

CREATE TABLE core.mail_sends (
  message_id text PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id uuid NOT NULL,
  state text NOT NULL DEFAULT 'sending' CHECK (state IN ('sending','sent')),
  started_at timestamptz NOT NULL DEFAULT now()
);
SELECT core.enable_tenant_rls('core.mail_sends');
GRANT SELECT, INSERT, UPDATE ON core.mail_sends TO cnote_app;
