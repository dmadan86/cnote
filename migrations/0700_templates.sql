-- ADR-006: templates are page/board content with {{placeholders}} bound to a CRM, billing or projects record.
-- Content is copied at save time, so deleting or editing the source note never changes a template.
CREATE TABLE docs.templates (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('page','board')),
  source_kind text NOT NULL DEFAULT 'none' CHECK (source_kind IN ('none','contact','deal','invoice','item')),
  types text[] NOT NULL DEFAULT '{}',        -- note types of rendered notes: {} for pages, {whiteboard} or {design} for boards
  title text NOT NULL DEFAULT '',            -- title pattern, may contain placeholders
  content_md text NOT NULL DEFAULT '',
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);
SELECT core.enable_tenant_rls('docs.templates');

-- Which template and record a rendered note came from, so template.refresh can re-render it.
CREATE TABLE docs.template_bindings (
  note_id uuid PRIMARY KEY REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  template_id uuid REFERENCES docs.templates(id) ON DELETE SET NULL,
  source_kind text NOT NULL,
  source_id uuid REFERENCES core.notes(id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('docs.template_bindings');
