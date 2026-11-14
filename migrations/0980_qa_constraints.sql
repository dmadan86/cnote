-- CN-QA-001 DB-003/004/005: a row may only reference rows of its own workspace.
-- Single-column foreign keys are checked by Postgres as the table owner, so row-level security does not apply to them:
-- a tenant could point parent_id, a relation, a view or a deal contact at another tenant's UUID. Each FK below becomes
-- (workspace_id, col) -> parent(workspace_id, key), keeping its name and delete action.
-- Not covered: users (global), billing.recurring_* and billing.purchase_* (added concurrently by 0960/0970).
DO $$
DECLARE
  r record;
  fk text;
  col_attnum smallint;
  act text;
  uq text;
BEGIN
  FOR r IN SELECT * FROM (VALUES
    ('core.space_members', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('core.notes', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('core.notes', 'parent_id', 'core.notes', 'id', 'SET NULL'),
    ('core.relations', 'from_id', 'core.notes', 'id', 'CASCADE'),
    ('core.relations', 'to_id', 'core.notes', 'id', 'CASCADE'),
    ('core.events', 'note_id', 'core.notes', 'id', 'SET NULL'),
    ('core.comments', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('core.comments', 'parent_id', 'core.comments', 'id', 'CASCADE'),
    ('core.attachments', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('core.views', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('core.note_permissions', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('core.notifications', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('core.note_updates', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('core.note_revisions', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('core.note_revisions', 'restored_from', 'core.note_revisions', 'id', 'NO ACTION'),
    ('ai.chunks', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('billing.journal_lines', 'entry_id', 'billing.journal_entries', 'id', 'NO ACTION'),
    ('billing.quotations', 'converted_invoice_id', 'billing.invoices', 'note_id', 'SET NULL'),
    ('billing.quotations', 'converted_item_id', 'core.notes', 'id', 'SET NULL'),
    ('docs.edit_proposals', 'flag_id', 'docs.freshness_flags', 'id', 'SET NULL'),
    ('importers.jobs', 'space_id', 'core.spaces', 'id', 'SET NULL'),
    ('projects.automation_runs', 'automation_id', 'projects.automations', 'id', 'CASCADE'),
    ('core.api_keys', 'agent_id', 'core.agents', 'id', 'CASCADE'),
    ('core.app_installs', 'agent_id', 'core.agents', 'id', 'CASCADE'),
    ('crm.contacts', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('crm.deals', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('crm.deals', 'contact_id', 'crm.contacts', 'note_id', 'SET NULL'),
    ('billing.invoices', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('billing.invoices', 'contact_id', 'crm.contacts', 'note_id', 'NO ACTION'),
    ('billing.invoices', 'original_invoice_id', 'billing.invoices', 'note_id', 'NO ACTION'),
    ('billing.invoice_lines', 'invoice_id', 'billing.invoices', 'note_id', 'CASCADE'),
    ('billing.invoice_lines', 'item_note_id', 'core.notes', 'id', 'SET NULL'),
    ('billing.payments', 'invoice_id', 'billing.invoices', 'note_id', 'NO ACTION'),
    ('billing.quotations', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('billing.quotations', 'contact_id', 'crm.contacts', 'note_id', 'NO ACTION'),
    ('billing.quotation_lines', 'quotation_id', 'billing.quotations', 'note_id', 'CASCADE'),
    ('billing.quotation_lines', 'item_note_id', 'core.notes', 'id', 'SET NULL'),
    ('projects.sprints', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('projects.versions', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('projects.components', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('projects.workflows', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('projects.automations', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('projects.items', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('projects.items', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('projects.items', 'component_id', 'projects.components', 'id', 'SET NULL'),
    ('projects.items', 'fix_version_id', 'projects.versions', 'id', 'SET NULL'),
    ('projects.items', 'sprint_id', 'projects.sprints', 'id', 'SET NULL'),
    ('projects.items', 'assignee_agent_id', 'core.agents', 'id', 'NO ACTION'),
    ('projects.evidence', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('docs.freshness_flags', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('docs.freshness_flags', 'source_note_id', 'core.notes', 'id', 'SET NULL'),
    ('docs.edit_proposals', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('docs.capture_requests', 'answer_note_id', 'core.notes', 'id', 'SET NULL'),
    ('docs.templates', 'space_id', 'core.spaces', 'id', 'CASCADE'),
    ('docs.template_bindings', 'note_id', 'core.notes', 'id', 'CASCADE'),
    ('docs.template_bindings', 'source_id', 'core.notes', 'id', 'CASCADE'),
    ('docs.template_bindings', 'template_id', 'docs.templates', 'id', 'SET NULL'),
    ('docs.external_bindings', 'note_id', 'core.notes', 'id', 'CASCADE')
  ) AS t(tbl, col, reftbl, refcol, onDelete)
  LOOP
    uq := format('%s_ws_%s_key', split_part(r.reftbl, '.', 2), r.refcol);
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = r.reftbl::regclass AND conname = uq) THEN
      EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I UNIQUE (workspace_id, %I)', r.reftbl, uq, r.refcol);
    END IF;
    SELECT attnum INTO col_attnum FROM pg_attribute WHERE attrelid = r.tbl::regclass AND attname = r.col;
    SELECT conname INTO fk FROM pg_constraint
      WHERE conrelid = r.tbl::regclass AND contype = 'f' AND confrelid = r.reftbl::regclass AND conkey = ARRAY[col_attnum];
    IF fk IS NULL THEN
      CONTINUE WHEN EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = r.tbl::regclass AND contype = 'f' AND confrelid = r.reftbl::regclass
        AND array_length(conkey, 1) = 2 AND col_attnum = ANY (conkey)); -- already composite (re-run)
      RAISE EXCEPTION 'no single-column FK on %.%', r.tbl, r.col;
    END IF;
    act := CASE WHEN r.onDelete = 'SET NULL' THEN format('SET NULL (%I)', r.col) ELSE r.onDelete END;
    EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', r.tbl, fk);
    EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I FOREIGN KEY (workspace_id, %I) REFERENCES %s (workspace_id, %I) ON DELETE %s',
                   r.tbl, fk, r.col, r.reftbl, r.refcol, act);
  END LOOP;
END $$;
