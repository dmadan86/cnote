-- WP16: team chat. Public channels are open to every non-guest member; private channels and 1:1 direct messages
-- are visible only to their chat.members rows. A direct message is a channel of kind 'dm' keyed by the two user ids
-- (smaller first), so opening it twice finds the same one. Deleting a message blanks it (deleted_at) so order and
-- unread counts stay stable.
CREATE SCHEMA IF NOT EXISTS chat;
GRANT USAGE ON SCHEMA chat TO cnote_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA chat GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO cnote_app;

CREATE TABLE chat.channels (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('public','private','dm')),
  name text CHECK (length(name) BETWEEN 1 AND 60),
  dm_key text,
  created_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'dm') = (dm_key IS NOT NULL) AND (kind = 'dm') = (name IS NULL)),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, dm_key)
);
CREATE UNIQUE INDEX channels_name ON chat.channels (workspace_id, lower(name)) WHERE kind <> 'dm';

CREATE TABLE chat.members (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL,
  user_id uuid NOT NULL,
  last_read_at timestamptz NOT NULL DEFAULT now(),
  joined_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, user_id),
  FOREIGN KEY (workspace_id, channel_id) REFERENCES chat.channels (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, user_id) REFERENCES core.memberships (workspace_id, user_id) ON DELETE CASCADE
);

CREATE TABLE chat.messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id),  -- history outlives a removed member
  body text NOT NULL CHECK (length(body) <= 4000),
  created_at timestamptz NOT NULL DEFAULT now(),
  edited_at timestamptz,
  deleted_at timestamptz,
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, channel_id) REFERENCES chat.channels (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX messages_channel ON chat.messages (channel_id, created_at DESC);

SELECT core.enable_tenant_rls('chat.channels');
SELECT core.enable_tenant_rls('chat.members');
SELECT core.enable_tenant_rls('chat.messages');

-- A chat mention is a notification with a channel instead of a page.
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_kind_check;
ALTER TABLE core.notifications
  ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('mention','approval_requested','approval_decided','reminder','assigned','quote_decided','chat_mention')),
  ADD COLUMN channel_id uuid,
  ADD FOREIGN KEY (workspace_id, channel_id) REFERENCES chat.channels (workspace_id, id) ON DELETE CASCADE;
