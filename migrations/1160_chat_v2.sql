-- WP19: chat v2. Threads (one level: parent_id), reactions, agents as chat participants, and a NOTIFY on every change
-- so the Go servers can push live updates (server-sent events) to browsers connected to any instance.
-- An agent message has agent_id and no user_id: it never carries its owner's identity.
ALTER TABLE core.agents ADD CONSTRAINT agents_ws_id UNIQUE (workspace_id, id);

ALTER TABLE chat.messages
  ALTER COLUMN user_id DROP NOT NULL,
  ADD COLUMN agent_id uuid,
  ADD COLUMN parent_id uuid,
  ADD CONSTRAINT messages_author CHECK ((user_id IS NULL) <> (agent_id IS NULL)),
  ADD CONSTRAINT messages_agent_fk FOREIGN KEY (workspace_id, agent_id) REFERENCES core.agents (workspace_id, id) ON DELETE CASCADE,
  ADD CONSTRAINT messages_parent_fk FOREIGN KEY (workspace_id, parent_id) REFERENCES chat.messages (workspace_id, id) ON DELETE CASCADE;
CREATE INDEX messages_parent ON chat.messages (parent_id, created_at) WHERE parent_id IS NOT NULL;

-- An agent reads and posts only in channels it was added to (never a direct message; the command refuses those).
CREATE TABLE chat.agent_members (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL,
  agent_id uuid NOT NULL,
  added_by uuid REFERENCES core.users(id),
  added_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, agent_id),
  FOREIGN KEY (workspace_id, channel_id) REFERENCES chat.channels (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, agent_id) REFERENCES core.agents (workspace_id, id) ON DELETE CASCADE
);

-- channel_id is kept here so a reaction event needs no lookup.
CREATE TABLE chat.reactions (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL,
  message_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  emoji text NOT NULL CHECK (emoji IN ('👍','❤️','😂','🎉','🙏','👀')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, user_id, emoji),
  FOREIGN KEY (workspace_id, channel_id) REFERENCES chat.channels (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, message_id) REFERENCES chat.messages (workspace_id, id) ON DELETE CASCADE
);

SELECT core.enable_tenant_rls('chat.agent_members');
SELECT core.enable_tenant_rls('chat.reactions');

-- Payload is ids only (well under the 8000 byte limit); readers fetch the data through the commands, which authorise.
-- k: message | reaction | read. For 'read' u is the reader: only that person is told.
CREATE FUNCTION chat.notify_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r record := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END; p jsonb;
BEGIN
  IF TG_TABLE_NAME = 'messages' THEN
    p := jsonb_build_object('k','message','m',r.id,'p',r.parent_id,'c',r.channel_id);
  ELSIF TG_TABLE_NAME = 'reactions' THEN
    p := jsonb_build_object('k','reaction','m',r.message_id,'c',r.channel_id);
  ELSE
    p := jsonb_build_object('k','read','u',r.user_id,'c',r.channel_id);
  END IF;
  PERFORM pg_notify('chat_events', (p || jsonb_build_object('w',r.workspace_id,'t',(extract(epoch FROM clock_timestamp())*1000000)::bigint))::text);
  RETURN NULL;
END $$;
CREATE TRIGGER messages_notify AFTER INSERT OR UPDATE ON chat.messages FOR EACH ROW EXECUTE FUNCTION chat.notify_change();
CREATE TRIGGER reactions_notify AFTER INSERT OR DELETE ON chat.reactions FOR EACH ROW EXECUTE FUNCTION chat.notify_change();
CREATE TRIGGER members_notify AFTER INSERT OR UPDATE OF last_read_at ON chat.members FOR EACH ROW EXECUTE FUNCTION chat.notify_change();
