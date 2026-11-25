-- no-transaction
-- Docs parity B1: at most one active page link per page (design 4.2). CONCURRENTLY, one statement; an invalid leftover index is
-- dropped with DROP INDEX CONCURRENTLY core.public_links_one_active_page and the file rerun (docs/deploy.md).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS public_links_one_active_page ON core.public_links (note_id) WHERE purpose = 'page' AND revoked_at IS NULL;
