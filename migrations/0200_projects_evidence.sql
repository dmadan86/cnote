-- ADR-014: status from evidence. Signals from outside the app (PR opened/merged, deploy, payment) attach to items
-- and satisfy `evidence` guards in the workflow.
CREATE TABLE projects.evidence (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  source text NOT NULL,                       -- github, stripe, ...
  kind text NOT NULL,                         -- pr_opened, pr_merged, deploy_succeeded, payment_received, ...
  url text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX evidence_note_kind ON projects.evidence(note_id, kind);
SELECT core.enable_tenant_rls('projects.evidence');

CREATE INDEX automation_runs_recent ON projects.automation_runs(automation_id, created_at DESC);
CREATE INDEX automations_ws_enabled ON projects.automations(workspace_id) WHERE enabled AND NOT draft;
CREATE INDEX workflows_space ON projects.workflows(space_id, type_name, version);
