-- WP15: branches (optionally with their own GSTIN) and multi-step approval chains.
CREATE TABLE core.branches (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  address text CHECK (length(address) <= 300),
  gstin text,                 -- null: invoices raised from this branch use the workspace GSTIN
  state_code text,            -- derived from the GSTIN when set
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name),
  UNIQUE (workspace_id, id)
);
SELECT core.enable_tenant_rls('core.branches');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.branches TO cnote_app;

ALTER TABLE core.memberships ADD COLUMN branch_id uuid,
  ADD FOREIGN KEY (workspace_id, branch_id) REFERENCES core.branches (workspace_id, id) ON DELETE SET NULL (branch_id);
ALTER TABLE billing.invoices ADD COLUMN branch_id uuid,
  ADD FOREIGN KEY (workspace_id, branch_id) REFERENCES core.branches (workspace_id, id) ON DELETE SET NULL (branch_id);
ALTER TABLE billing.expenses ADD COLUMN branch_id uuid,
  ADD FOREIGN KEY (workspace_id, branch_id) REFERENCES core.branches (workspace_id, id) ON DELETE SET NULL (branch_id);

-- Approval chains. A rule's steps are an ordered jsonb array:
--   [{"mode":"any|all","min_amount_minor":0,"approvers":[{"kind":"role","role":"admin"}|{"kind":"user","user_id":"..."}|{"kind":"department_head"}]}]
-- A request snapshots the steps that apply to its amount (threshold skip), with department heads resolved, so
-- later rule edits never change a request in flight. Existing single-approver rules become one-step chains.
ALTER TABLE core.approval_rules ADD COLUMN steps jsonb;
UPDATE core.approval_rules SET steps = jsonb_build_array(jsonb_build_object('mode', 'any', 'min_amount_minor', 0, 'approvers',
  jsonb_build_array(CASE WHEN approver_user_id IS NOT NULL THEN jsonb_build_object('kind', 'user', 'user_id', approver_user_id::text)
                         ELSE jsonb_build_object('kind', 'role', 'role', approver_role) END)));
ALTER TABLE core.approval_rules ALTER COLUMN steps SET NOT NULL;

ALTER TABLE core.confirmations ADD COLUMN steps jsonb, ADD COLUMN step_idx integer NOT NULL DEFAULT 0,
  ADD CONSTRAINT confirmations_ws_id_key UNIQUE (workspace_id, id);
UPDATE core.confirmations SET steps = jsonb_build_array(jsonb_build_object('mode', 'any', 'min_amount_minor', 0, 'approvers',
  jsonb_build_array(CASE WHEN approver_user_id IS NOT NULL THEN jsonb_build_object('kind', 'user', 'user_id', approver_user_id::text)
                         ELSE jsonb_build_object('kind', 'role', 'role', 'admin') END)))
  WHERE kind = 'approval';

CREATE TABLE core.approval_decisions (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  confirmation_id uuid NOT NULL,
  step_idx integer NOT NULL,
  slot integer NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id),
  decided_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (confirmation_id, step_idx, slot),
  UNIQUE (confirmation_id, step_idx, user_id),
  FOREIGN KEY (workspace_id, confirmation_id) REFERENCES core.confirmations (workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('core.approval_decisions');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.approval_decisions TO cnote_app;
