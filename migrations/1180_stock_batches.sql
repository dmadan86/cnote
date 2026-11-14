-- WP21: batch and serial tracking on top of the WP13 stock ledger. An item is tracked 'none' (as before), by 'batch'
-- (batch number, optional manufacturing and expiry date) or by 'serial' (one unique serial per unit). Movements carry
-- the batch or serial; stock per batch or serial is the sum of the ledger like everything else. Valuation stays
-- weighted average per item. Switching an item's mode is allowed only at zero stock (enforced by the command).
ALTER TABLE inventory.items ADD COLUMN tracking text NOT NULL DEFAULT 'none' CHECK (tracking IN ('none','batch','serial'));

CREATE TABLE inventory.batches (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  item_id uuid NOT NULL,
  number text NOT NULL CHECK (length(btrim(number)) BETWEEN 1 AND 40),
  mfg_date date,
  expiry_date date,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  CHECK (mfg_date IS NULL OR expiry_date IS NULL OR expiry_date >= mfg_date),
  FOREIGN KEY (workspace_id, item_id) REFERENCES inventory.items (workspace_id, id)
);
CREATE UNIQUE INDEX batches_number ON inventory.batches (workspace_id, item_id, lower(number));
CREATE INDEX batches_expiry ON inventory.batches (workspace_id, expiry_date) WHERE expiry_date IS NOT NULL;

ALTER TABLE inventory.movements ADD COLUMN batch_id uuid, ADD COLUMN serial text;
ALTER TABLE inventory.movements ADD FOREIGN KEY (workspace_id, batch_id) REFERENCES inventory.batches (workspace_id, id);
ALTER TABLE inventory.movements ADD CHECK (serial IS NULL OR (length(btrim(serial)) BETWEEN 1 AND 60 AND qty IN (1, -1) AND batch_id IS NULL));
CREATE INDEX movements_batch ON inventory.movements (workspace_id, batch_id) WHERE batch_id IS NOT NULL;
CREATE INDEX movements_serial ON inventory.movements (workspace_id, item_id, serial) WHERE serial IS NOT NULL;
-- One posting per document, item and location for untracked stock; tracked stock may post several batches of an
-- item, and a serial posts once per document.
DROP INDEX inventory.movements_once;
CREATE UNIQUE INDEX movements_once ON inventory.movements (workspace_id, source_type, source_id, item_id, location_id)
  WHERE kind IN ('receipt','issue') AND source_id IS NOT NULL AND batch_id IS NULL AND serial IS NULL;
CREATE UNIQUE INDEX movements_serial_once ON inventory.movements (workspace_id, source_type, source_id, item_id, serial)
  WHERE kind IN ('receipt','issue') AND source_id IS NOT NULL AND serial IS NOT NULL;

-- Batch or serial entered on a document line, applied when the bill is recorded or the invoice issued.
ALTER TABLE billing.invoice_lines ADD COLUMN tracking jsonb;
ALTER TABLE billing.purchase_bill_lines ADD COLUMN tracking jsonb;
ALTER TABLE billing.quotation_lines ADD COLUMN tracking jsonb;
ALTER TABLE billing.recurring_invoice_lines ADD COLUMN tracking jsonb;

SELECT core.enable_tenant_rls('inventory.batches');
GRANT SELECT, INSERT ON inventory.batches TO cnote_app;
