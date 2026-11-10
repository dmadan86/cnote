-- Per-Type property tables (ADR-002) and module tables. Each property table is keyed by note_id.

-- ===== projects (ADR-014) =====
CREATE TABLE projects.sprints (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid NOT NULL REFERENCES core.spaces(id) ON DELETE CASCADE,
  name text NOT NULL,
  goal text,
  state text NOT NULL DEFAULT 'future' CHECK (state IN ('future','active','closed')),
  start_date date,
  end_date date,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects.versions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid NOT NULL REFERENCES core.spaces(id) ON DELETE CASCADE,
  name text NOT NULL,
  released boolean NOT NULL DEFAULT false,
  release_date date,
  UNIQUE (space_id, name)
);

CREATE TABLE projects.components (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid NOT NULL REFERENCES core.spaces(id) ON DELETE CASCADE,
  name text NOT NULL,
  lead_id uuid REFERENCES core.users(id),
  UNIQUE (space_id, name)
);

-- Item = Note with an item Type (task, bug, story, epic, subtask, ...)
CREATE TABLE projects.items (
  note_id uuid PRIMARY KEY REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid NOT NULL REFERENCES core.spaces(id) ON DELETE CASCADE,
  number integer NOT NULL,
  key text NOT NULL,                         -- ENG-42
  item_type text NOT NULL DEFAULT 'task',
  status text NOT NULL,
  status_category text NOT NULL DEFAULT 'todo' CHECK (status_category IN ('todo','in_progress','done')),
  priority text NOT NULL DEFAULT 'medium' CHECK (priority IN ('urgent','high','medium','low','none')),
  assignee_id uuid REFERENCES core.users(id),
  assignee_agent_id uuid REFERENCES core.agents(id),
  reporter_id uuid REFERENCES core.users(id),
  sprint_id uuid REFERENCES projects.sprints(id) ON DELETE SET NULL,
  fix_version_id uuid REFERENCES projects.versions(id) ON DELETE SET NULL,
  component_id uuid REFERENCES projects.components(id) ON DELETE SET NULL,
  estimate numeric,                          -- story points
  due_date date,
  labels text[] NOT NULL DEFAULT '{}',
  custom jsonb NOT NULL DEFAULT '{}',        -- custom fields from the Space's Type schema
  rank text NOT NULL DEFAULT 'm',
  workflow_version integer NOT NULL DEFAULT 1,
  triage boolean NOT NULL DEFAULT false,
  resolved_at timestamptz,
  UNIQUE (workspace_id, key)
);
CREATE INDEX items_space_status ON projects.items(space_id, status_category);
CREATE INDEX items_assignee ON projects.items(assignee_id);

-- ===== workflow & automation (ADR-007) =====
CREATE TABLE projects.workflows (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid REFERENCES core.spaces(id) ON DELETE CASCADE,
  type_name text NOT NULL,                   -- item, deal, invoice, design
  version integer NOT NULL DEFAULT 1,
  definition jsonb NOT NULL,                 -- {statuses, transitions, guards, postActions}
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, space_id, type_name, version)
);

CREATE TABLE projects.automations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid REFERENCES core.spaces(id) ON DELETE CASCADE,
  name text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  draft boolean NOT NULL DEFAULT false,       -- imported Jira rules arrive as drafts (ADR-012)
  trigger jsonb NOT NULL,                     -- {kind:'event'|'schedule'|'webhook'|'tool', ...}
  conditions jsonb NOT NULL DEFAULT '[]',
  actions jsonb NOT NULL DEFAULT '[]',
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects.automation_runs (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  automation_id uuid NOT NULL REFERENCES projects.automations(id) ON DELETE CASCADE,
  event_id bigint,
  status text NOT NULL CHECK (status IN ('success','skipped','failed','loop_blocked')),
  log jsonb NOT NULL DEFAULT '[]',
  created_at timestamptz NOT NULL DEFAULT now()
);

-- ===== docs (ADR-013) =====
CREATE TABLE docs.freshness_flags (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  reason text NOT NULL CHECK (reason IN ('binding_changed','date_lapsed','imported')),
  source_note_id uuid REFERENCES core.notes(id) ON DELETE SET NULL,
  source_event_id bigint,
  created_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);

CREATE TABLE docs.edit_proposals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  flag_id uuid REFERENCES docs.freshness_flags(id) ON DELETE SET NULL,
  proposed_md text NOT NULL,
  rationale text NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
  decided_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  decided_at timestamptz
);

CREATE TABLE docs.capture_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  question text NOT NULL,
  asked_by uuid REFERENCES core.users(id),
  routed_to uuid REFERENCES core.users(id),
  answer_note_id uuid REFERENCES core.notes(id) ON DELETE SET NULL,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','answered','dismissed')),
  created_at timestamptz NOT NULL DEFAULT now()
);

-- ===== crm =====
CREATE TABLE crm.contacts (
  note_id uuid PRIMARY KEY REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  kind text NOT NULL DEFAULT 'person' CHECK (kind IN ('person','company')),
  email text,
  phone text,
  company text,
  gstin text,
  state_code text,                           -- GST state code, e.g. 29 Karnataka
  billing_address text
);

CREATE TABLE crm.deals (
  note_id uuid PRIMARY KEY REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  stage text NOT NULL DEFAULT 'lead',
  amount_minor bigint NOT NULL DEFAULT 0,
  currency char(3) NOT NULL DEFAULT 'INR',
  contact_id uuid REFERENCES crm.contacts(note_id) ON DELETE SET NULL,
  owner_id uuid REFERENCES core.users(id),
  expected_close date,
  probability integer NOT NULL DEFAULT 10 CHECK (probability BETWEEN 0 AND 100),
  closed_at timestamptz
);

-- ===== billing (ADR-008) =====
CREATE TABLE billing.accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  code text NOT NULL,                        -- 1100 receivables, 4000 revenue, 2201 CGST output ...
  name text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('asset','liability','equity','revenue','expense')),
  UNIQUE (workspace_id, code)
);

CREATE TABLE billing.invoices (
  note_id uuid PRIMARY KEY REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  kind text NOT NULL DEFAULT 'invoice' CHECK (kind IN ('invoice','credit_note')),
  number text,                               -- assigned on issue
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','issued','irn_generated','sent','partly_paid','paid','cancelled')),
  contact_id uuid REFERENCES crm.contacts(note_id),
  original_invoice_id uuid REFERENCES billing.invoices(note_id),
  currency char(3) NOT NULL DEFAULT 'INR',
  supplier_gstin text,
  supplier_state text,
  buyer_gstin text,
  place_of_supply text,
  reverse_charge boolean NOT NULL DEFAULT false,
  invoice_date date,
  due_date date,
  subtotal_minor bigint NOT NULL DEFAULT 0,
  cgst_minor bigint NOT NULL DEFAULT 0,
  sgst_minor bigint NOT NULL DEFAULT 0,
  igst_minor bigint NOT NULL DEFAULT 0,
  total_minor bigint NOT NULL DEFAULT 0,
  paid_minor bigint NOT NULL DEFAULT 0,
  irn text,
  ack_no text,
  ack_date timestamptz,
  signed_qr text,
  irn_cancelled_at timestamptz,
  issued_at timestamptz,
  cancelled_at timestamptz,
  UNIQUE (workspace_id, number)
);

CREATE TABLE billing.invoice_lines (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  invoice_id uuid NOT NULL REFERENCES billing.invoices(note_id) ON DELETE CASCADE,
  position integer NOT NULL,
  description text NOT NULL,
  hsn_sac text,
  quantity numeric NOT NULL DEFAULT 1,
  unit_price_minor bigint NOT NULL,
  gst_rate_bp integer NOT NULL DEFAULT 1800,  -- basis points, 1800 = 18%
  taxable_minor bigint NOT NULL,
  cgst_minor bigint NOT NULL DEFAULT 0,
  sgst_minor bigint NOT NULL DEFAULT 0,
  igst_minor bigint NOT NULL DEFAULT 0,
  item_note_id uuid REFERENCES core.notes(id) ON DELETE SET NULL  -- "bills" link to work
);

CREATE TABLE billing.payments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  invoice_id uuid NOT NULL REFERENCES billing.invoices(note_id),
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  method text NOT NULL DEFAULT 'upi',
  reference text,
  received_at timestamptz NOT NULL DEFAULT now()
);

-- Append-only journal; every posting balances (enforced by a deferred constraint trigger in 0100).
CREATE TABLE billing.journal_entries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  source_type text NOT NULL,                 -- invoice, credit_note, payment, write_off
  source_id uuid NOT NULL,
  memo text,
  posted_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE billing.journal_lines (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  entry_id uuid NOT NULL REFERENCES billing.journal_entries(id),
  account_code text NOT NULL,
  debit_minor bigint NOT NULL DEFAULT 0 CHECK (debit_minor >= 0),
  credit_minor bigint NOT NULL DEFAULT 0 CHECK (credit_minor >= 0),
  currency char(3) NOT NULL DEFAULT 'INR',
  CHECK ((debit_minor = 0) <> (credit_minor = 0))
);
CREATE TRIGGER journal_entries_append_only BEFORE UPDATE OR DELETE ON billing.journal_entries
  FOR EACH ROW EXECUTE FUNCTION core.forbid_mutation();
CREATE TRIGGER journal_lines_append_only BEFORE UPDATE OR DELETE ON billing.journal_lines
  FOR EACH ROW EXECUTE FUNCTION core.forbid_mutation();

CREATE TABLE billing.sequences (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL,                        -- e.g. 'invoice:2026-27'
  next_value integer NOT NULL DEFAULT 1,
  PRIMARY KEY (workspace_id, name)
);

-- ===== ai (ADR-009) =====
CREATE TABLE ai.chunks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  model text NOT NULL,                       -- indexes are versioned by embedding model
  ordinal integer NOT NULL,
  content text NOT NULL,
  embedding vector(1536),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chunks_note ON ai.chunks(note_id);

CREATE TABLE ai.audit (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  actor_id text,
  provider text NOT NULL,
  model text NOT NULL,
  purpose text NOT NULL,
  prompt jsonb NOT NULL,
  response jsonb,
  tokens_in integer,
  tokens_out integer,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- ===== importers (ADR-012) =====
CREATE TABLE importers.jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  source text NOT NULL CHECK (source IN ('jira','confluence','notion','trello','csv')),
  space_id uuid REFERENCES core.spaces(id) ON DELETE SET NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed')),
  cursor jsonb NOT NULL DEFAULT '{}',        -- resumability
  report jsonb NOT NULL DEFAULT '{}',        -- fidelity report
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);
