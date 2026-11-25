-- no-transaction
-- Docs parity B1: the mail sweeps look for notifications not yet considered for email (design 4.3). CONCURRENTLY, one statement; an
-- invalid leftover index is dropped with DROP INDEX CONCURRENTLY core.notifications_unmailed and the file rerun (docs/deploy.md).
CREATE INDEX CONCURRENTLY IF NOT EXISTS notifications_unmailed ON core.notifications (workspace_id, created_at) WHERE emailed_at IS NULL;
