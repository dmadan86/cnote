-- WP1 approvals and reminders. A human approval request is a core.confirmations row of kind 'approval': the same
-- stored command + input as an agent confirmation, decided by a named person instead of any admin. One rule per
-- command says from which amount a non-owner needs a decision, who decides, and who gets it after N hours.
ALTER TABLE core.confirmations DROP CONSTRAINT IF EXISTS confirmations_status_check;
ALTER TABLE core.confirmations
  ADD CONSTRAINT confirmations_status_check CHECK (status IN ('pending','confirmed','rejected','expired','cancelled')),
  ADD COLUMN kind text NOT NULL DEFAULT 'agent' CHECK (kind IN ('agent','approval')),
  ADD COLUMN note_id uuid,                                   -- the document being approved
  ADD COLUMN amount_minor bigint CHECK (amount_minor >= 0),  -- amount at request time; approve re-checks it
  ADD COLUMN approver_user_id uuid REFERENCES core.users(id), -- null = any admin or owner
  ADD COLUMN fallback_user_id uuid REFERENCES core.users(id),
  ADD COLUMN escalate_at timestamptz,
  ADD COLUMN escalated_at timestamptz,
  ADD COLUMN reason text;                                    -- reject reason or approve note
ALTER TABLE core.confirmations ADD CONSTRAINT confirmations_ws_note_fkey
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes (workspace_id, id) ON DELETE SET NULL (note_id);
CREATE INDEX confirmations_approvals ON core.confirmations(workspace_id, status, created_at DESC) WHERE kind = 'approval';
CREATE INDEX confirmations_approval_escalate ON core.confirmations(escalate_at) WHERE kind = 'approval' AND status = 'pending' AND escalated_at IS NULL;

CREATE TABLE core.approval_rules (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  command text NOT NULL,
  min_amount_minor bigint NOT NULL DEFAULT 0 CHECK (min_amount_minor >= 0),
  approver_user_id uuid REFERENCES core.users(id),
  approver_role text CHECK (approver_role IN ('owner','admin')),
  fallback_user_id uuid REFERENCES core.users(id),
  escalate_after_hours integer CHECK (escalate_after_hours BETWEEN 1 AND 168),
  enabled boolean NOT NULL DEFAULT true,
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, command),
  CHECK (approver_user_id IS NOT NULL OR approver_role IS NOT NULL),
  CHECK (escalate_after_hours IS NULL OR fallback_user_id IS NOT NULL)
);
SELECT core.enable_tenant_rls('core.approval_rules');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.approval_rules TO cnote_app;

-- Notifications beyond mentions. note_id is null for the aggregated daily reminders; (user, source, day) keeps one each.
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_kind_check;
ALTER TABLE core.notifications
  ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('mention','approval_requested','approval_decided','reminder','assigned')),
  ALTER COLUMN note_id DROP NOT NULL,
  ADD COLUMN source text,
  ADD COLUMN source_day date;
CREATE UNIQUE INDEX notifications_reminder_once ON core.notifications(user_id, source, source_day) WHERE source IS NOT NULL;
