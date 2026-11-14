-- WP4: advance receipts. A receipt voucher (GST Rule 50) or a refund voucher (Rule 51) is a typed note
-- (types = ['receipt_voucher']) plus one billing.receipts row. An issued voucher is immutable: only the running totals
-- applied_minor / refunded_minor and the status derived from them may change. Applying an advance to an invoice
-- is an append-only billing.receipt_applications row; it stores the tax share released so GSTR-1 table 11B needs no maths.
-- The ledger accounts (2300 customer advances, 1310 GST paid on advances) are seeded lazily per workspace on the first
-- receipt, so the trial balance of a workspace without receipts does not change.
CREATE TABLE billing.receipts (
  note_id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('receipt','refund')),
  number text NOT NULL CHECK (btrim(number) <> ''),
  status text NOT NULL DEFAULT 'issued' CHECK (status IN ('issued','partly_applied','applied','refunded')),
  contact_id uuid NOT NULL,
  quotation_id uuid,
  original_receipt_id uuid,                  -- set on a refund voucher
  supply_type text NOT NULL CHECK (supply_type IN ('services','goods')),
  supplier_gstin text NOT NULL,
  supplier_state text NOT NULL,
  buyer_gstin text,
  place_of_supply text,                      -- null = unknown, taxed as inter-state
  description text,
  gst_rate_bp integer NOT NULL DEFAULT 1800 CHECK (gst_rate_bp BETWEEN 0 AND 2800),
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),      -- gross, tax included
  taxable_minor bigint NOT NULL CHECK (taxable_minor >= 0),
  cgst_minor bigint NOT NULL DEFAULT 0 CHECK (cgst_minor >= 0),
  sgst_minor bigint NOT NULL DEFAULT 0 CHECK (sgst_minor >= 0),
  igst_minor bigint NOT NULL DEFAULT 0 CHECK (igst_minor >= 0),
  applied_minor bigint NOT NULL DEFAULT 0 CHECK (applied_minor >= 0),
  refunded_minor bigint NOT NULL DEFAULT 0 CHECK (refunded_minor >= 0),
  method text NOT NULL DEFAULT 'bank',
  reference text,
  received_at date NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (taxable_minor + cgst_minor + sgst_minor + igst_minor = amount_minor),
  CHECK (applied_minor + refunded_minor <= amount_minor),
  CHECK ((kind = 'refund') = (original_receipt_id IS NOT NULL)),
  UNIQUE (workspace_id, note_id),
  UNIQUE (workspace_id, number),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, contact_id) REFERENCES crm.contacts (workspace_id, note_id),
  FOREIGN KEY (workspace_id, quotation_id) REFERENCES billing.quotations (workspace_id, note_id) ON DELETE SET NULL (quotation_id),
  FOREIGN KEY (workspace_id, original_receipt_id) REFERENCES billing.receipts (workspace_id, note_id)
);
CREATE INDEX receipts_contact ON billing.receipts(workspace_id, contact_id, kind, status);
CREATE INDEX receipts_received ON billing.receipts(workspace_id, received_at);

CREATE TABLE billing.receipt_applications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  receipt_id uuid NOT NULL,
  invoice_id uuid NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  taxable_minor bigint NOT NULL CHECK (taxable_minor >= 0),   -- the part of the amount that was taxable value
  cgst_minor bigint NOT NULL DEFAULT 0,
  sgst_minor bigint NOT NULL DEFAULT 0,
  igst_minor bigint NOT NULL DEFAULT 0,
  applied_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, receipt_id) REFERENCES billing.receipts (workspace_id, note_id),
  FOREIGN KEY (workspace_id, invoice_id) REFERENCES billing.invoices (workspace_id, note_id)
);
CREATE INDEX receipt_applications_receipt ON billing.receipt_applications(workspace_id, receipt_id);
CREATE INDEX receipt_applications_invoice ON billing.receipt_applications(workspace_id, invoice_id);

CREATE TRIGGER receipt_applications_append_only BEFORE UPDATE OR DELETE ON billing.receipt_applications
  FOR EACH ROW EXECUTE FUNCTION core.forbid_mutation();

CREATE OR REPLACE FUNCTION billing.guard_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE mutable text[] := ARRAY['status','applied_minor','refunded_minor'];
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'voucher % cannot be deleted; issue a refund voucher', OLD.number;
  END IF;
  IF (to_jsonb(NEW) - mutable) IS DISTINCT FROM (to_jsonb(OLD) - mutable) THEN
    RAISE EXCEPTION 'voucher % is immutable', OLD.number;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER receipts_immutable BEFORE UPDATE OR DELETE ON billing.receipts
  FOR EACH ROW EXECUTE FUNCTION billing.guard_receipt();

SELECT core.enable_tenant_rls('billing.receipts');
SELECT core.enable_tenant_rls('billing.receipt_applications');
GRANT SELECT, INSERT, UPDATE, DELETE ON billing.receipts, billing.receipt_applications TO cnote_app;
