-- ADR-013: bindings to non-note targets (repo paths, URLs). core.relations needs a note on both ends,
-- so external targets live here; note-to-note bindings stay as `binds` relations.
CREATE TABLE docs.external_bindings (
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('github','repo','url')),
  ref text NOT NULL,
  PRIMARY KEY (note_id, kind, ref)
);
SELECT core.enable_tenant_rls('docs.external_bindings');

CREATE INDEX freshness_flags_open ON docs.freshness_flags(note_id) WHERE resolved_at IS NULL;
CREATE INDEX edit_proposals_note ON docs.edit_proposals(note_id, status);
