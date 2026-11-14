-- Vendors and purchase bills (accounts payable). A vendor is a CRM contact flagged is_vendor. A purchase bill is a
-- typed note (types = ['purchase_bill']) plus these rows. Recording posts Dr expense + Dr input GST (ITC) / Cr accounts
-- payable (2100); paying posts Dr 2100 / Cr bank. The purchase accounts (2100, 1301-1303, 5000) are seeded lazily per
-- workspace on the first recorded bill, so trial balances of workspaces without bills do not change.
ALTER TABLE crm.contacts ADD COLUMN is_vendor boolean NOT NULL DEFAULT false;

CREATE TABLE billing.purchase_bills (
  note_id uuid PRIMARY KEY REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  vendor_id uuid NOT NULL REFERENCES crm.contacts(note_id),
  vendor_bill_number text NOT NULL CHECK (btrim(vendor_bill_number) <> ''),  -- the supplier's own number
  bill_date date NOT NULL,
  due_date date NOT NULL,
  supplier_gstin text,
  supplier_state text,
  place_of_supply text,                      -- where the goods or services are received (our state by default)
  status text NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft','recorded','partly_paid','paid','cancelled')),
  currency char(3) NOT NULL DEFAULT 'INR',
  subtotal_minor bigint NOT NULL DEFAULT 0,
  cgst_minor bigint NOT NULL DEFAULT 0,      -- cgst + sgst + igst = input tax credit
  sgst_minor bigint NOT NULL DEFAULT 0,
  igst_minor bigint NOT NULL DEFAULT 0,
  total_minor bigint NOT NULL DEFAULT 0,
  paid_minor bigint NOT NULL DEFAULT 0 CHECK (paid_minor >= 0 AND paid_minor <= total_minor),
  recorded_at timestamptz,
  cancelled_at timestamptz
);
-- A cancelled bill frees the supplier's number.
CREATE UNIQUE INDEX purchase_bills_vendor_number ON billing.purchase_bills(workspace_id, vendor_id, vendor_bill_number)
  WHERE status <> 'cancelled';
CREATE INDEX purchase_bills_ws_status ON billing.purchase_bills(workspace_id, status);

CREATE TABLE billing.purchase_bill_lines (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  bill_id uuid NOT NULL REFERENCES billing.purchase_bills(note_id) ON DELETE CASCADE,
  position integer NOT NULL,
  description text NOT NULL,
  hsn_sac text,
  quantity numeric NOT NULL DEFAULT 1,
  unit_price_minor bigint NOT NULL,
  gst_rate_bp integer NOT NULL DEFAULT 1800,
  taxable_minor bigint NOT NULL,
  cgst_minor bigint NOT NULL DEFAULT 0,
  sgst_minor bigint NOT NULL DEFAULT 0,
  igst_minor bigint NOT NULL DEFAULT 0,
  item_note_id uuid REFERENCES core.notes(id) ON DELETE SET NULL,
  account_code text                          -- expense account; null = 5000 Purchases and expenses
);
CREATE INDEX purchase_bill_lines_bill ON billing.purchase_bill_lines(bill_id, position);

CREATE TABLE billing.purchase_payments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  bill_id uuid NOT NULL REFERENCES billing.purchase_bills(note_id) ON DELETE CASCADE,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  method text NOT NULL DEFAULT 'bank',
  reference text,
  paid_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX purchase_payments_bill ON billing.purchase_payments(bill_id);

SELECT core.enable_tenant_rls('billing.purchase_bills');
SELECT core.enable_tenant_rls('billing.purchase_bill_lines');
SELECT core.enable_tenant_rls('billing.purchase_payments');
GRANT SELECT, INSERT, UPDATE, DELETE ON billing.purchase_bills, billing.purchase_bill_lines, billing.purchase_payments TO cnote_app;
