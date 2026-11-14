-- Durable collaboration and page versions (CN-DOCS-ENGINEERING-001, reconciled with ADR-005).
-- 1. Every live edit is committed to core.note_updates before it is relayed to anyone; the debounced snapshot
--    in core.notes.content_ydoc then absorbs and deletes exactly the updates it contains. A crash loses nothing
--    that another person has seen.
-- 2. core.notes.collab_epoch fences writers: a restore bumps it, and every log insert and snapshot checks it in
--    the same statement, so a room still open on the old content can no longer write.
-- 3. core.note_revisions keeps immutable page versions (checkpoints while editing, before and after a restore).

ALTER TABLE core.notes ADD COLUMN collab_epoch int NOT NULL DEFAULT 1;

CREATE TABLE core.note_updates (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id),
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  epoch int NOT NULL,
  update bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX note_updates_replay ON core.note_updates(note_id, id);
SELECT core.enable_tenant_rls('core.note_updates');

CREATE TABLE core.note_revisions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id),
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  number int NOT NULL,
  title text NOT NULL,
  content_md text NOT NULL,
  reason text NOT NULL CHECK (reason IN ('edit', 'before_restore', 'restore')),
  restored_from uuid REFERENCES core.note_revisions(id),
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (note_id, number)
);
SELECT core.enable_tenant_rls('core.note_revisions');
-- Versions are history: the app can add them, never change or remove them (note deletion cascades as owner).
REVOKE UPDATE, DELETE ON core.note_revisions FROM cnote_app;
