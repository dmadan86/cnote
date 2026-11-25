-- no-transaction
-- Docs parity C1 (design 13.3.9): keyset order of one tree level (position, created_at, id) without a sort. CONCURRENTLY, one statement; an invalid leftover index is dropped with DROP INDEX CONCURRENTLY core.notes_tree_level and the file rerun (docs/deploy.md).
CREATE INDEX CONCURRENTLY IF NOT EXISTS notes_tree_level ON core.notes (space_id, parent_id, "position", created_at, id) WHERE deleted_at IS NULL;
