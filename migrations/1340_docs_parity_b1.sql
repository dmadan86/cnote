-- Docs parity, Phase B1 (docs/design/docs-parity-b.md sections 4 and 13): space settings and archive, public page links, email
-- notifications rebuilt at send time, the task index, comment edit/delete/reactions, and the stores that make mention and task
-- notifications once-per-version. Online-safe: nullable columns without defaults, new tables, CHECKs added NOT VALID (VALIDATE them
-- in the release after B1, see docs/deploy.md). The indexes on existing big tables are in 1341 and 1342 (CONCURRENTLY).
-- Every new tenant table has RLS, composite foreign keys that carry workspace_id and explicit grants.
SET LOCAL lock_timeout = '5s';

-- ---- spaces: settings and the archive state ----
-- public_links NULL means on: no default, no backfill (13.2.9).
ALTER TABLE core.spaces
  ADD COLUMN description text,
  ADD COLUMN archived_at timestamptz,
  ADD COLUMN public_links boolean;
ALTER TABLE core.spaces ADD CONSTRAINT spaces_description_len CHECK (char_length(description) <= 500) NOT VALID;

-- ---- public page links: purpose 'page' reuses core.public_links ----
ALTER TABLE core.public_links DROP CONSTRAINT IF EXISTS public_links_purpose_check;
ALTER TABLE core.public_links
  ADD CONSTRAINT public_links_purpose_check CHECK (purpose IN ('view','quote_accept','page')) NOT VALID;

-- The billing endpoint can never answer for a page token.
CREATE OR REPLACE FUNCTION core.resolve_public_link(p_hash text)
RETURNS TABLE(id uuid, workspace_id uuid, note_id uuid, purpose text, created_by uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT l.id, l.workspace_id, l.note_id, l.purpose, l.created_by FROM core.public_links l
  WHERE l.token_hash = p_hash AND l.purpose <> 'page' AND l.revoked_at IS NULL AND (l.expires_at IS NULL OR l.expires_at > now())
$$;

-- Tables 18.8 rule: definer, stable, pinned search_path, everything qualified, execute for cnote_app only. Zero rows for every
-- reason a link must not serve: not a page link, revoked, expired, page gone/typed/archived, space off or archived.
-- Restriction, creator and per-request checks run in the tenant transaction (design 4.2, 13.2.4).
CREATE OR REPLACE FUNCTION core.resolve_page_link(p_hash text)
RETURNS TABLE(id uuid, workspace_id uuid, note_id uuid, space_id uuid, created_by uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT l.id, l.workspace_id, l.note_id, n.space_id, l.created_by
  FROM core.public_links l
  JOIN core.notes n ON n.workspace_id = l.workspace_id AND n.id = l.note_id
  JOIN core.spaces s ON s.workspace_id = n.workspace_id AND s.id = n.space_id
  WHERE l.token_hash = p_hash AND l.purpose = 'page' AND l.revoked_at IS NULL
    AND (l.expires_at IS NULL OR l.expires_at > pg_catalog.now())
    AND n.deleted_at IS NULL AND n.types = '{}' AND n.freshness <> 'archived'
    AND coalesce(s.public_links, true) AND s.archived_at IS NULL
$$;
REVOKE ALL ON FUNCTION core.resolve_page_link(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.resolve_page_link(text) TO cnote_app;

-- ---- notifications: email state and two kinds ----
-- emailed_at: the sweep claimed the row for email. email_state: queued (a job holds a reference), mailed or suppressed
-- (the job dropped it at send time). NULL means never considered.
ALTER TABLE core.notifications ADD COLUMN emailed_at timestamptz, ADD COLUMN email_state text;
ALTER TABLE core.notifications
  ADD CONSTRAINT notifications_email_state_check CHECK (email_state IN ('queued','mailed','suppressed')) NOT VALID;
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_kind_check;
ALTER TABLE core.notifications
  ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('mention','approval_requested','approval_decided','reminder','assigned','quote_decided','chat_mention','lead','site_abuse','comment','page_updated','shared','access_request')) NOT VALID;

-- ---- notification preferences ----
-- No row means the defaults. direct_off and digest_off are what the one-click unsubscribe links set (per kind), kept apart
-- from email_mode so a person's chosen mode is not rewritten by a mail client.
CREATE TABLE core.notify_prefs (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  email_mode text NOT NULL DEFAULT 'smart' CHECK (email_mode IN ('smart','digest','off')),
  digest_hour smallint NOT NULL DEFAULT 8 CHECK (digest_hour BETWEEN 0 AND 23),
  direct_off boolean NOT NULL DEFAULT false,
  digest_off boolean NOT NULL DEFAULT false,
  last_digest_on date,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id)
);
SELECT core.enable_tenant_rls('core.notify_prefs');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.notify_prefs TO cnote_app;

-- ---- mentions from live editing (13.2.10) ----
-- One row per note: the last version whose mentions were handled and the people mentioned then. Written by the single shared
-- function that both the API path and the gateway path call, under a per-note advisory lock.
CREATE TABLE core.note_mention_state (
  workspace_id uuid NOT NULL,
  note_id uuid NOT NULL,
  processed_version integer NOT NULL,
  mentioned uuid[] NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes(workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('core.note_mention_state');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.note_mention_state TO cnote_app;

-- ---- tasks (4.4, 13.2.6) ----
-- note_tasks is the index "My tasks" and the digest read; note_task_state holds the version it was built from, also for a page
-- that has no tasks (a rebuild applies only when the event's version is newer); note_task_notified records each assignment once.
CREATE TABLE core.note_tasks (
  workspace_id uuid NOT NULL,
  note_id uuid NOT NULL,
  ord integer NOT NULL,
  text text NOT NULL CHECK (char_length(text) <= 300),
  assignee_id uuid,
  due date,
  done boolean NOT NULL,
  indexed_version integer NOT NULL,
  PRIMARY KEY (note_id, ord),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX note_tasks_by_assignee ON core.note_tasks(workspace_id, assignee_id, done, due);
CREATE TABLE core.note_task_state (
  workspace_id uuid NOT NULL,
  note_id uuid NOT NULL,
  indexed_version integer NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes(workspace_id, id) ON DELETE CASCADE
);
CREATE TABLE core.note_task_notified (
  workspace_id uuid NOT NULL,
  note_id uuid NOT NULL,
  assignee_id uuid NOT NULL,
  text_hash text NOT NULL, -- hex sha256 of the normalised task text
  notified_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, assignee_id, text_hash),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes(workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('core.note_tasks');
SELECT core.enable_tenant_rls('core.note_task_state');
SELECT core.enable_tenant_rls('core.note_task_notified');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.note_tasks, core.note_task_state, core.note_task_notified TO cnote_app;

-- ---- comments: edit, delete, reactions (4.5) ----
ALTER TABLE core.comments ADD COLUMN edited_at timestamptz, ADD COLUMN deleted_at timestamptz, ADD COLUMN deleted_by uuid;
CREATE TABLE core.comment_reactions (
  workspace_id uuid NOT NULL,
  comment_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  emoji text NOT NULL CHECK (emoji IN ('👍','❤️','😂','🎉','👀','✅')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (comment_id, user_id, emoji),
  FOREIGN KEY (workspace_id, comment_id) REFERENCES core.comments(workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('core.comment_reactions');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.comment_reactions TO cnote_app;
