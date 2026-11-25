-- Docs parity A2 (7.10.4): the delete fence is a database fact every collab gateway instance enforces. A fenced fold sets it, the
-- live-edit log refuses writes while it is in the future, and note.delete clears it when the deletion commits. Online-safe:
-- nullable, no default, no backfill.
SET LOCAL lock_timeout = '5s';
ALTER TABLE core.notes ADD COLUMN fenced_until timestamptz;
