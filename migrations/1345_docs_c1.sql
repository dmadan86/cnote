-- Docs parity C1 (design 13.3.2, 13.6). Online-safe: nullable columns without a rewrite, a NOT VALID check, one small UPDATE, no new table.
SET LOCAL lock_timeout = '5s';

-- Cover: page chrome. No foreign key to core.attachments (design 5.3): a cover whose file is gone does not render.
ALTER TABLE core.notes ADD COLUMN cover_file_id uuid, ADD COLUMN cover_y smallint;
ALTER TABLE core.notes ADD CONSTRAINT notes_cover_y_check CHECK (cover_y IS NULL OR cover_y BETWEEN 0 AND 100) NOT VALID;

-- Trash purge clock. NULL disables the sweep (and is what a workspace created by an old build keeps, which is safe).
-- Existing workspaces get a full notice period from the release; new ones are stamped by auth.CreateWorkspace.
-- workspaces is one small table, so the UPDATE is quick.
ALTER TABLE core.workspaces ADD COLUMN trash_purge_from timestamptz;
UPDATE core.workspaces SET trash_purge_from = now() WHERE trash_purge_from IS NULL;

-- Page files in object storage are removed only by the committed sweep, like record files (core.file_gc). The trigger now
-- also fires for page files (field_id IS NULL); it is the cascade of a page purge as well as file.delete.
-- The sweep already checks that no attachment row still has the id (files.sweepWorkspace), so a rolled-back delete keeps the object.
DROP TRIGGER attachments_gc ON core.attachments;
CREATE TRIGGER attachments_gc BEFORE DELETE ON core.attachments FOR EACH ROW
  WHEN (OLD.storage_key = 's3') EXECUTE FUNCTION core.attachment_gc();

-- A purge hard-deletes pages, and core.events.note_id is ON DELETE SET NULL: that is an UPDATE, which the append-only trigger
-- refused, so a page with any history could not be deleted at all. The only UPDATE now allowed is the foreign key's own
-- note_id -> NULL with every other column unchanged. DELETE and every other UPDATE stay forbidden.
CREATE FUNCTION core.events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND OLD.note_id IS NOT NULL AND NEW.note_id IS NULL
     AND to_jsonb(NEW) - 'note_id' = to_jsonb(OLD) - 'note_id' THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END $$;
DROP TRIGGER events_append_only ON core.events;
CREATE TRIGGER events_append_only BEFORE UPDATE OR DELETE ON core.events FOR EACH ROW EXECUTE FUNCTION core.events_append_only();
