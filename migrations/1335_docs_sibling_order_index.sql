-- no-transaction
-- Docs parity A2: sibling order for the tree, the default-position trigger and reorder. Built CONCURRENTLY so page writes are not blocked.
-- The first line makes the migrate runner run this file outside a transaction; such a file must hold one statement.
-- If the build fails it leaves an invalid index named notes_siblings: DROP INDEX CONCURRENTLY core.notes_siblings and rerun.
CREATE INDEX CONCURRENTLY IF NOT EXISTS notes_siblings ON core.notes(space_id, parent_id, position) WHERE deleted_at IS NULL;
