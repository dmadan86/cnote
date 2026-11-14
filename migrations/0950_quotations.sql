-- Quotations (estimates): inquiry -> quotation -> approval -> project/order -> invoice -> payment.
-- A quotation is a typed note (types = ['quotation']) plus these rows. It never posts to the ledger and never
-- takes an invoice number: its number comes from its own gap-free counter in billing.sequences (series 'quote'),
-- assigned when the quotation is sent.
CREATE TABLE billing.quotations (
  note_id uuid PRIMARY KEY REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  number text,                               -- assigned on send
  status text NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft','sent','accepted','rejected','expired','converted')),
  contact_id uuid REFERENCES crm.contacts(note_id),
  currency char(3) NOT NULL DEFAULT 'INR',
  supplier_gstin text,                       -- frozen on send
  supplier_state text,
  buyer_gstin text,
  place_of_supply text,
  reverse_charge boolean NOT NULL DEFAULT false,
  quote_date date,
  valid_until date,
  subtotal_minor bigint NOT NULL DEFAULT 0,
  cgst_minor bigint NOT NULL DEFAULT 0,
  sgst_minor bigint NOT NULL DEFAULT 0,
  igst_minor bigint NOT NULL DEFAULT 0,
  total_minor bigint NOT NULL DEFAULT 0,
  sent_at timestamptz,
  decided_at timestamptz,
  converted_invoice_id uuid REFERENCES billing.invoices(note_id) ON DELETE SET NULL,
  converted_item_id uuid REFERENCES core.notes(id) ON DELETE SET NULL,
  UNIQUE (workspace_id, number)
);
CREATE INDEX quotations_ws_status ON billing.quotations(workspace_id, status);

CREATE TABLE billing.quotation_lines (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  quotation_id uuid NOT NULL REFERENCES billing.quotations(note_id) ON DELETE CASCADE,
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
  item_note_id uuid REFERENCES core.notes(id) ON DELETE SET NULL
);
CREATE INDEX quotation_lines_quote ON billing.quotation_lines(quotation_id, position);

SELECT core.enable_tenant_rls('billing.quotations');
SELECT core.enable_tenant_rls('billing.quotation_lines');
GRANT SELECT, INSERT, UPDATE, DELETE ON billing.quotations, billing.quotation_lines TO cnote_app;
