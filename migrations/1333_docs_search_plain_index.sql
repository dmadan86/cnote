-- no-transaction
-- Docs parity A1: the GIN index for core.notes.search_plain (added in 1331). Built CONCURRENTLY so writes to pages are not
-- blocked. The first line makes the migrate runner run this file outside a transaction; such a file must hold one statement.
-- If the build fails it leaves an invalid index named notes_search_plain_idx: DROP INDEX CONCURRENTLY core.notes_search_plain_idx and rerun.
CREATE INDEX CONCURRENTLY IF NOT EXISTS notes_search_plain_idx ON core.notes USING gin(search_plain);
