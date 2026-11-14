-- WP20: support v2. Customer-visible ticket numbers (TKT-000123 per workspace), the public conversation
-- (crm.ticket_messages; internal notes stay core.comments and never reach a customer), the email-in secret and
-- portal switch (crm.ticket_channels) and the portal's single-use sign-in links and sessions (crm.portal_tokens).

-- A trigger numbers new tickets so every writer, old binaries included, gets one.
CREATE TABLE crm.ticket_counters (
  workspace_id uuid PRIMARY KEY REFERENCES core.workspaces(id) ON DELETE CASCADE,
  last integer NOT NULL
);
SELECT core.enable_tenant_rls('crm.ticket_counters');
GRANT SELECT, INSERT, UPDATE ON crm.ticket_counters TO cnote_app;

ALTER TABLE crm.tickets ADD COLUMN number integer;
UPDATE crm.tickets t SET number = r.n
  FROM (SELECT note_id, row_number() OVER (PARTITION BY workspace_id ORDER BY created_at, note_id) AS n FROM crm.tickets) r
  WHERE r.note_id = t.note_id;
INSERT INTO crm.ticket_counters(workspace_id, last) SELECT workspace_id, max(number) FROM crm.tickets GROUP BY workspace_id;

CREATE FUNCTION crm.ticket_number() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.number IS NULL THEN
    INSERT INTO crm.ticket_counters(workspace_id, last) VALUES (NEW.workspace_id, 1)
      ON CONFLICT (workspace_id) DO UPDATE SET last = crm.ticket_counters.last + 1 RETURNING last INTO NEW.number;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER tickets_number BEFORE INSERT ON crm.tickets FOR EACH ROW EXECUTE FUNCTION crm.ticket_number();
ALTER TABLE crm.tickets ALTER COLUMN number SET NOT NULL;
ALTER TABLE crm.tickets ADD CONSTRAINT tickets_number_key UNIQUE (workspace_id, number);
ALTER TABLE crm.tickets ADD CONSTRAINT tickets_ws_note_key UNIQUE (workspace_id, note_id);
ALTER TABLE crm.tickets DROP CONSTRAINT tickets_source_check;
ALTER TABLE crm.tickets ADD CONSTRAINT tickets_source_check CHECK (source IN ('internal','form','email'));

-- Public messages only: what the customer wrote (email or portal) and what an agent chose to send them.
CREATE TABLE crm.ticket_messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  ticket_id uuid NOT NULL,
  origin text NOT NULL CHECK (origin IN ('customer_email','customer_portal','agent')),
  body text NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
  author_id uuid REFERENCES core.users(id),
  message_id text CHECK (length(message_id) <= 998),     -- RFC 5322 Message-ID of an inbound email: dedupe and threading
  emailed boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, ticket_id) REFERENCES crm.tickets (workspace_id, note_id) ON DELETE CASCADE
);
CREATE INDEX ticket_messages_ticket ON crm.ticket_messages(workspace_id, ticket_id, created_at);
CREATE UNIQUE INDEX ticket_messages_message_id ON crm.ticket_messages(workspace_id, message_id) WHERE message_id IS NOT NULL;
SELECT core.enable_tenant_rls('crm.ticket_messages');
GRANT SELECT, INSERT, UPDATE, DELETE ON crm.ticket_messages TO cnote_app;

CREATE TABLE crm.ticket_channels (
  workspace_id uuid PRIMARY KEY REFERENCES core.workspaces(id) ON DELETE CASCADE,
  portal_enabled boolean NOT NULL DEFAULT false,
  inbound_secret bytea,                       -- secretbox(CNOTE_MASTER_KEY); null = email-in off
  inbound_space_id uuid,
  inbound_owner_id uuid REFERENCES core.users(id),
  inbound_rotated_at timestamptz,
  FOREIGN KEY (workspace_id, inbound_space_id) REFERENCES core.spaces (workspace_id, id) ON DELETE SET NULL (inbound_space_id)
);
SELECT core.enable_tenant_rls('crm.ticket_channels');
GRANT SELECT, INSERT, UPDATE, DELETE ON crm.ticket_channels TO cnote_app;

-- Portal sign-in: kind 'login' is the emailed 15-minute single-use link, 'session' is what it is exchanged for.
-- Only the sha256 is stored.
CREATE TABLE crm.portal_tokens (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  contact_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('login','session')),
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, contact_id) REFERENCES crm.contacts (workspace_id, note_id) ON DELETE CASCADE
);
CREATE INDEX portal_tokens_contact ON crm.portal_tokens(workspace_id, contact_id, kind, created_at);
SELECT core.enable_tenant_rls('crm.portal_tokens');
GRANT SELECT, INSERT, UPDATE, DELETE ON crm.portal_tokens TO cnote_app;
