-- WP8: expenses and TDS on vendor payments.
-- An expense is a typed note (types = ['expense']) plus one billing.expenses row, so receipts (attachments), comments and
-- history attach to the note. A claim (employee paid) flows draft -> submitted -> approved -> reimbursed, or rejected;
-- a direct expense (company paid, no vendor bill) is posted at once. Postings use account codes seeded lazily per
-- workspace (2150 employee reimbursements, 2400 TDS payable, 52xx expense accounts), so a workspace without expenses
-- or TDS keeps its chart and trial balance unchanged.
CREATE TABLE billing.expenses (
  note_id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('claim','direct')),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','submitted','approved','rejected','reimbursed','posted')),
  spent_on date NOT NULL,
  category text NOT NULL CHECK (btrim(category) <> ''),
  account_code text NOT NULL CHECK (account_code ~ '^[0-9]{4,10}$'),
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  gst_minor bigint NOT NULL DEFAULT 0 CHECK (gst_minor >= 0 AND gst_minor <= amount_minor),   -- informational: no ITC is taken
  vendor_name text CHECK (length(vendor_name) <= 200),
  paid_by_user_id uuid,                        -- the claimant; null for a direct expense
  method text NOT NULL DEFAULT 'bank',         -- direct: how the company paid
  reference text CHECK (length(reference) <= 200),
  submitted_at timestamptz,
  decided_by uuid,
  decided_at timestamptz,
  decision_reason text CHECK (length(decision_reason) <= 500),
  reimbursed_at timestamptz,
  reimbursed_method text,
  reimbursed_reference text CHECK (length(reimbursed_reference) <= 200),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'direct') = (status = 'posted')),
  CHECK (kind = 'direct' OR paid_by_user_id IS NOT NULL),
  UNIQUE (workspace_id, note_id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, paid_by_user_id) REFERENCES core.memberships (workspace_id, user_id) ON DELETE SET NULL (paid_by_user_id)
);
CREATE INDEX expenses_status ON billing.expenses(workspace_id, status);
CREATE INDEX expenses_claimant ON billing.expenses(workspace_id, paid_by_user_id, status);
CREATE INDEX expenses_spent ON billing.expenses(workspace_id, spent_on);

-- Statutory rates are data, reviewed each April (CA review). Seeded per workspace by the application on first use.
CREATE TABLE billing.tds_rates (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  code text NOT NULL CHECK (code ~ '^[a-z0-9_]{1,40}$'),
  label text NOT NULL CHECK (length(label) BETWEEN 1 AND 200),
  rate_bp integer NOT NULL CHECK (rate_bp BETWEEN 0 AND 10000),
  no_pan_rate_bp integer NOT NULL DEFAULT 2000 CHECK (no_pan_rate_bp BETWEEN 0 AND 10000),   -- applies when the vendor has no PAN
  single_limit_minor bigint CHECK (single_limit_minor >= 0),    -- deduct when one payment exceeds this
  annual_limit_minor bigint CHECK (annual_limit_minor >= 0),    -- or when the financial year's total to the vendor exceeds this
  active boolean NOT NULL DEFAULT true,
  UNIQUE (workspace_id, code)
);
SELECT core.enable_tenant_rls('billing.tds_rates');

ALTER TABLE billing.purchase_payments
  ADD COLUMN tds_code text,
  ADD COLUMN tds_rate_bp integer NOT NULL DEFAULT 0,
  ADD COLUMN tds_base_minor bigint NOT NULL DEFAULT 0 CHECK (tds_base_minor >= 0),   -- payment value counted toward the thresholds (GST excluded)
  ADD COLUMN tds_on_minor bigint NOT NULL DEFAULT 0 CHECK (tds_on_minor >= 0),       -- value TDS was actually deducted on (catch-up included)
  ADD COLUMN tds_minor bigint NOT NULL DEFAULT 0 CHECK (tds_minor >= 0 AND tds_minor <= amount_minor);

ALTER TABLE crm.contacts
  ADD COLUMN pan text CHECK (pan ~ '^[A-Z]{5}[0-9]{4}[A-Z]$'),
  ADD COLUMN default_tds_code text;

SELECT core.enable_tenant_rls('billing.expenses');
GRANT SELECT, INSERT, UPDATE, DELETE ON billing.expenses, billing.tds_rates TO cnote_app;
