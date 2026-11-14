-- WP11: (a) staff are notified when a customer decides a quote through the share link; (b) a minimal file store.

-- (a) one in-app notification per recipient per quote decision (a quote is decided once, so (user, note) is enough).
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_kind_check;
ALTER TABLE core.notifications
  ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('mention','approval_requested','approval_decided','reminder','assigned','quote_decided'));
CREATE UNIQUE INDEX notifications_quote_decided_once ON core.notifications(user_id, note_id) WHERE kind = 'quote_decided';

-- (b) core.attachments gains the bytes. Rows made by importers keep their storage_key and have no data.
-- shortcut: bytes live in Postgres (bytea, 10 MB cap); move to object storage when hosting is decided.
ALTER TABLE core.attachments
  ADD COLUMN data bytea,
  ADD COLUMN created_by uuid REFERENCES core.users(id),
  ADD CONSTRAINT attachments_data_size CHECK (data IS NULL OR octet_length(data) <= 10485760);
CREATE INDEX attachments_note ON core.attachments(workspace_id, note_id);
