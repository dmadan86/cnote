-- Recurring templates are typed notes; generated invoices use the ordinary billing commands.
CREATE TABLE billing.recurring_invoices (
 note_id uuid PRIMARY KEY REFERENCES core.notes(id) ON DELETE CASCADE,
 workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
 contact_id uuid NOT NULL REFERENCES crm.contacts(note_id),
 frequency text NOT NULL CHECK (frequency IN ('monthly','quarterly','yearly')),
 interval integer NOT NULL DEFAULT 1 CHECK (interval BETWEEN 1 AND 120),
 start_date date NOT NULL,
 next_run date NOT NULL,
 end_date date CHECK (end_date >= start_date),
 auto_issue boolean NOT NULL DEFAULT false,
 status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','ended')),
 last_invoice_id uuid REFERENCES billing.invoices(note_id) ON DELETE SET NULL,
 created_by uuid NOT NULL REFERENCES core.users(id),
 UNIQUE (workspace_id, note_id)
);
CREATE INDEX recurring_due ON billing.recurring_invoices(workspace_id,next_run) WHERE status = 'active';
CREATE TABLE billing.recurring_invoice_lines (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
 recurring_invoice_id uuid NOT NULL REFERENCES billing.recurring_invoices(note_id) ON DELETE CASCADE,
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
CREATE INDEX recurring_lines_schedule ON billing.recurring_invoice_lines(recurring_invoice_id,position);
SELECT core.enable_tenant_rls('billing.recurring_invoices');
SELECT core.enable_tenant_rls('billing.recurring_invoice_lines');
GRANT SELECT, INSERT, UPDATE, DELETE ON billing.recurring_invoices, billing.recurring_invoice_lines TO cnote_app;
