-- In-app notifications (currently only kind 'mention'). One row per recipient.
CREATE TABLE core.notifications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id),
  kind text NOT NULL CHECK (kind IN ('mention')),
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  actor_user_id uuid REFERENCES core.users(id),
  title text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  read_at timestamptz
);
CREATE INDEX notifications_user_unread ON core.notifications(user_id, read_at);
SELECT core.enable_tenant_rls('core.notifications');
