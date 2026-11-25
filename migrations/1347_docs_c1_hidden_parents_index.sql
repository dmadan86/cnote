-- no-transaction
-- Docs parity C1 (design 13.3.9): the trashed or archived parents of a space, so the root level finds live pages whose parent is hidden without scanning the space. CONCURRENTLY, one statement; rerun as for 1346.
CREATE INDEX CONCURRENTLY IF NOT EXISTS notes_hidden_parents ON core.notes (space_id) INCLUDE (id) WHERE deleted_at IS NOT NULL OR freshness = 'archived';
