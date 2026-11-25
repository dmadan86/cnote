-- Docs parity A1: a notification can say which event it came from, so a page notification is suppressed only for the
-- mention made by the same edit or comment, not for any mention in the last minute. Nullable, no default: no rewrite.
ALTER TABLE core.notifications ADD COLUMN event_key text;
