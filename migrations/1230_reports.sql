-- Report builder: saved reports and dashboards. The spec is re-validated against the registry on every run with the
-- caller's rights, so nothing here grants access to data. private = owner only, workspace = every member.
CREATE TABLE core.reports (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
  dataset text NOT NULL,
  spec jsonb NOT NULL,
  visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','workspace')),
  created_by uuid NOT NULL REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id)
);
CREATE INDEX reports_ws ON core.reports (workspace_id, updated_at DESC);

CREATE TABLE core.dashboards (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
  tiles jsonb NOT NULL DEFAULT '[]',
  period jsonb,
  visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','workspace')),
  created_by uuid NOT NULL REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id)
);
CREATE INDEX dashboards_ws ON core.dashboards (workspace_id, updated_at DESC);

SELECT core.enable_tenant_rls('core.reports');
SELECT core.enable_tenant_rls('core.dashboards');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.reports, core.dashboards TO cnote_app;
