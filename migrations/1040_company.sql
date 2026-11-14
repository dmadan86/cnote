-- Company and people foundation: departments, a profile on each membership (who they report to, when they joined or
-- left), a finance-access flag and deactivation. The legal profile itself lives in workspaces.settings.company.
CREATE TABLE core.departments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  head_user_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name),
  UNIQUE (workspace_id, id)
);
SELECT core.enable_tenant_rls('core.departments');

ALTER TABLE core.memberships
  ADD COLUMN department_id uuid,
  ADD COLUMN manager_user_id uuid,
  ADD COLUMN designation text CHECK (length(designation) <= 80),
  ADD COLUMN phone text CHECK (length(phone) <= 20),
  ADD COLUMN joined_on date,
  ADD COLUMN left_on date,
  ADD COLUMN finance_access boolean NOT NULL DEFAULT true,
  ADD COLUMN deactivated_at timestamptz,
  ADD FOREIGN KEY (workspace_id, department_id) REFERENCES core.departments (workspace_id, id) ON DELETE SET NULL (department_id),
  ADD FOREIGN KEY (workspace_id, manager_user_id) REFERENCES core.memberships (workspace_id, user_id) ON DELETE SET NULL (manager_user_id);

ALTER TABLE core.departments
  ADD FOREIGN KEY (workspace_id, head_user_id) REFERENCES core.memberships (workspace_id, user_id) ON DELETE SET NULL (head_user_id);
