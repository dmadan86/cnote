-- WP10: attendance, leave and holidays. No payroll, no salary. Employees are people records: a workspace member
-- (user_id) or an offline employee without a login (user_id null). The name is a snapshot so attendance survives the
-- membership being removed. Personal data under DPDP: exported by data.export_subject for members.
CREATE SCHEMA IF NOT EXISTS hr;
GRANT USAGE ON SCHEMA hr TO cnote_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA hr GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO cnote_app;

CREATE TABLE hr.employees (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id uuid,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  code text CHECK (length(code) BETWEEN 1 AND 30),
  weekly_off int[] NOT NULL DEFAULT '{0}' CHECK (weekly_off <@ ARRAY[0,1,2,3,4,5,6]),  -- 0 = Sunday
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, code),
  UNIQUE (workspace_id, user_id),
  FOREIGN KEY (workspace_id, user_id) REFERENCES core.memberships (workspace_id, user_id) ON DELETE SET NULL (user_id)
);

CREATE TABLE hr.attendance (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL,
  day date NOT NULL,                                   -- the IST calendar day
  status text NOT NULL CHECK (status IN ('present','half_day','absent','leave','holiday','week_off')),
  in_at timestamptz,
  out_at timestamptz,
  marked_by uuid,
  note text CHECK (length(note) <= 300),
  UNIQUE (workspace_id, employee_id, day),
  CHECK (out_at IS NULL OR in_at IS NULL OR out_at >= in_at),
  FOREIGN KEY (workspace_id, employee_id) REFERENCES hr.employees (workspace_id, id)
);
CREATE INDEX attendance_day ON hr.attendance (workspace_id, day);

CREATE TABLE hr.leave_types (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  yearly_quota numeric(4,1) CHECK (yearly_quota >= 0),   -- null = no limit (e.g. unpaid leave)
  paid boolean NOT NULL DEFAULT true,
  active boolean NOT NULL DEFAULT true,
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, name)
);

CREATE TABLE hr.leave_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL,
  leave_type_id uuid NOT NULL,
  from_day date NOT NULL,
  to_day date NOT NULL,
  days numeric(4,1) NOT NULL CHECK (days > 0),         -- working days: weekly offs and holidays excluded
  half_day boolean NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
  reason text CHECK (length(reason) <= 500),
  requested_by uuid,
  decided_by uuid,
  decided_at timestamptz,
  decision_note text CHECK (length(decision_note) <= 500),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (to_day >= from_day AND extract(year FROM from_day) = extract(year FROM to_day)),
  CHECK (NOT half_day OR from_day = to_day),
  FOREIGN KEY (workspace_id, employee_id) REFERENCES hr.employees (workspace_id, id),
  FOREIGN KEY (workspace_id, leave_type_id) REFERENCES hr.leave_types (workspace_id, id)
);
CREATE INDEX leave_requests_employee ON hr.leave_requests (workspace_id, employee_id, status);

CREATE TABLE hr.holidays (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  day date NOT NULL,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  kind text NOT NULL DEFAULT 'company' CHECK (kind IN ('national','festival','company')),
  UNIQUE (workspace_id, day)
);

SELECT core.enable_tenant_rls('hr.employees');
SELECT core.enable_tenant_rls('hr.attendance');
SELECT core.enable_tenant_rls('hr.leave_types');
SELECT core.enable_tenant_rls('hr.leave_requests');
SELECT core.enable_tenant_rls('hr.holidays');
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA hr TO cnote_app;
