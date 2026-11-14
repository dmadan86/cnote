-- WP7 audit log and data protection. core.events stays the one append-only audit trail (no second table);
-- this adds the indexes the audit viewer filters on and a ledger of DPDP export/erase requests.
CREATE INDEX events_ws_actor ON core.events(workspace_id, actor_id, id);
CREATE INDEX events_ws_time ON core.events(workspace_id, created_at);

CREATE TABLE core.data_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  subject_note_id uuid,                       -- the contact; set null if the note is ever hard-deleted
  subject_user_id uuid REFERENCES core.users(id),
  kind text NOT NULL CHECK (kind IN ('export','erase')),
  status text NOT NULL CHECK (status IN ('pending','done','refused')),
  reason text CHECK (length(reason) <= 1000),
  result jsonb NOT NULL DEFAULT '{}',          -- counts and what was kept and why; never the exported data itself
  requested_by uuid REFERENCES core.users(id),
  decided_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  done_at timestamptz,
  FOREIGN KEY (workspace_id, subject_note_id) REFERENCES core.notes (workspace_id, id) ON DELETE SET NULL (subject_note_id)
);
CREATE INDEX data_requests_ws ON core.data_requests(workspace_id, created_at DESC);
SELECT core.enable_tenant_rls('core.data_requests');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.data_requests TO cnote_app;
