-- no-transaction
-- Docs parity A2: Trash lists and restores by deletion_id. Built CONCURRENTLY; one statement per file (see 1335).
-- If the build fails: DROP INDEX CONCURRENTLY core.notes_deletion and rerun.
CREATE INDEX CONCURRENTLY IF NOT EXISTS notes_deletion ON core.notes(workspace_id, deletion_id, deleted_at DESC) WHERE deletion_id IS NOT NULL;
