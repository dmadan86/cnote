-- Docs parity A2 (7.10.7): the consistency constraint, added NOT VALID so it checks every new and changed row and does not scan the table.
-- Every writer of core.notes.deleted_at now sets deletion_id (note.delete is the only one; the importers and the other modules never
-- delete a page). A live page never carries deletion data. The other direction (a deleted page always has a deletion_id) waits for the
-- worker sweep docs.a2_backfill to finish the pages deleted before A2: until then such rows may still be updated by other writers
-- (freshness sweep, data erasure), so it is not part of this constraint.
-- Follow-up release (docs/deploy.md): VALIDATE this constraint and notes_deleted_by_fk, then add
--   CHECK ((deleted_at IS NULL) = (deletion_id IS NULL)) NOT VALID, validate it, and drop this one.
SET LOCAL lock_timeout = '5s';
ALTER TABLE core.notes
  ADD CONSTRAINT notes_deletion_consistent CHECK (deleted_at IS NOT NULL OR (deletion_id IS NULL AND deleted_by IS NULL AND deleted_parent_id IS NULL AND deleted_restricted_by IS NULL)) NOT VALID;
