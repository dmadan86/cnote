-- Docs B2: template answers persist across refresh. Existing note_views, notes.freshness/archived_at,
-- files and notification source/day uniqueness already cover views, archive, attachments and request dedupe.
-- Request limits use durable core.rate_buckets via db.Bump (including suppressed deliveries).
SET LOCAL lock_timeout = '5s';
ALTER TABLE docs.template_bindings ADD COLUMN answers jsonb;
