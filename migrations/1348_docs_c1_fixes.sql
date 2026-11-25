SET LOCAL lock_timeout = '5s';

ALTER TABLE core.notes ADD COLUMN purge_skipped_at timestamptz;
