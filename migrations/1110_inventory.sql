-- WP13: inventory for a trading SMB. Items, locations and an append-only stock ledger. Stock on hand is always the sum
-- of the ledger, never a stored number. Purchase bills post receipts and invoices post issues in the same transaction;
-- cancelling either appends exact reversals. Valuation is weighted average cost (value_minor is the signed cost of
-- each movement; an issue is valued at the average cost at that moment). The general ledger is untouched: bills stay
-- expensed, and the valuation report is an operational view. Not covered: batches, serials, expiry, BOM.
CREATE SCHEMA IF NOT EXISTS inventory;
GRANT USAGE ON SCHEMA inventory TO cnote_app;

CREATE TABLE inventory.locations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 80),
  is_default boolean NOT NULL DEFAULT false,   -- where bills and invoices post
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, name)
);
CREATE UNIQUE INDEX locations_one_default ON inventory.locations (workspace_id) WHERE is_default;

CREATE TABLE inventory.items (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  sku text NOT NULL CHECK (length(btrim(sku)) BETWEEN 1 AND 40),
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
  hsn_sac text CHECK (hsn_sac IS NULL OR hsn_sac ~ '^[0-9]{4,8}$'),
  unit text NOT NULL DEFAULT 'pcs' CHECK (length(btrim(unit)) BETWEEN 1 AND 12),
  gst_rate_bp integer NOT NULL DEFAULT 1800 CHECK (gst_rate_bp BETWEEN 0 AND 2800),
  price_minor bigint NOT NULL DEFAULT 0 CHECK (price_minor >= 0),       -- default selling price per unit
  reorder_level numeric NOT NULL DEFAULT 0 CHECK (reorder_level >= 0),  -- 0 = no alert
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id)
);
CREATE UNIQUE INDEX items_sku ON inventory.items (workspace_id, lower(sku));

-- Append-only: the app role can only SELECT and INSERT. A mistake is fixed by a new adjustment or a reversal.
CREATE TABLE inventory.movements (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  item_id uuid NOT NULL,
  location_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('receipt','issue','adjustment','transfer_out','transfer_in','reversal')),
  qty numeric NOT NULL CHECK (qty <> 0),                 -- signed change of stock on hand
  value_minor bigint NOT NULL,                           -- signed change of stock value at cost, paise
  reason text CHECK (length(reason) <= 300),
  source_type text CHECK (source_type IN ('invoice','purchase_bill')),
  source_id uuid,                                        -- the invoice or bill note
  transfer_id uuid,                                      -- ties the two legs of a transfer together
  reverses uuid,
  created_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  CHECK ((qty > 0 AND value_minor >= 0) OR (qty < 0 AND value_minor <= 0)),
  CHECK (kind NOT IN ('receipt','transfer_in') OR qty > 0),
  CHECK (kind NOT IN ('issue','transfer_out') OR qty < 0),
  CHECK (kind <> 'adjustment' OR btrim(coalesce(reason, '')) <> ''),
  CHECK ((kind = 'reversal') = (reverses IS NOT NULL)),
  FOREIGN KEY (workspace_id, item_id) REFERENCES inventory.items (workspace_id, id),
  FOREIGN KEY (workspace_id, location_id) REFERENCES inventory.locations (workspace_id, id),
  FOREIGN KEY (workspace_id, reverses) REFERENCES inventory.movements (workspace_id, id)
);
CREATE INDEX movements_item ON inventory.movements (workspace_id, item_id, location_id);
CREATE INDEX movements_source ON inventory.movements (workspace_id, source_type, source_id);
-- One posting per document, item and location, and one reversal per movement: a retry cannot double-post.
CREATE UNIQUE INDEX movements_once ON inventory.movements (workspace_id, source_type, source_id, item_id, location_id)
  WHERE kind IN ('receipt','issue') AND source_id IS NOT NULL;
CREATE UNIQUE INDEX movements_reversed_once ON inventory.movements (workspace_id, reverses) WHERE reverses IS NOT NULL;

-- Document lines point at an item; lines without one behave as before.
ALTER TABLE billing.invoice_lines ADD COLUMN stock_item_id uuid;
ALTER TABLE billing.purchase_bill_lines ADD COLUMN stock_item_id uuid;
ALTER TABLE billing.quotation_lines ADD COLUMN stock_item_id uuid;
ALTER TABLE billing.recurring_invoice_lines ADD COLUMN stock_item_id uuid;
ALTER TABLE billing.invoice_lines ADD FOREIGN KEY (workspace_id, stock_item_id) REFERENCES inventory.items (workspace_id, id);
ALTER TABLE billing.purchase_bill_lines ADD FOREIGN KEY (workspace_id, stock_item_id) REFERENCES inventory.items (workspace_id, id);
ALTER TABLE billing.quotation_lines ADD FOREIGN KEY (workspace_id, stock_item_id) REFERENCES inventory.items (workspace_id, id);
ALTER TABLE billing.recurring_invoice_lines ADD FOREIGN KEY (workspace_id, stock_item_id) REFERENCES inventory.items (workspace_id, id);

SELECT core.enable_tenant_rls('inventory.locations');
SELECT core.enable_tenant_rls('inventory.items');
SELECT core.enable_tenant_rls('inventory.movements');
GRANT SELECT, INSERT, UPDATE ON inventory.locations, inventory.items TO cnote_app;
GRANT SELECT, INSERT ON inventory.movements TO cnote_app;
