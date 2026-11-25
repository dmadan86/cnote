-- Docs parity, Phase A1 (docs/design/docs-parity.md sections 5.3 and 7): page chrome, labels, stars, recently viewed,
-- watches and the comment thread on a notification. Every table is tenant RLS, every FK to a tenant table carries
-- workspace_id (TestQAEveryTenantForeignKeyIncludesWorkspace), grants are explicit.

-- ---- page status, title emoji, wide page ----
-- page_status is NULL for "no status". Verified is not a status: it stays in the freshness columns.
ALTER TABLE core.notes
  ADD COLUMN page_status text CHECK (page_status IN ('draft', 'in_review', 'final')),
  ADD COLUMN icon text CHECK (char_length(icon) <= 16),
  ADD COLUMN wide boolean NOT NULL DEFAULT false;

-- ---- labels ----
CREATE TABLE core.labels (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (name = lower(name) AND name ~ '^[^\s,]{1,40}$'),
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, name)
);
CREATE TABLE core.note_labels (
  workspace_id uuid NOT NULL,
  note_id uuid NOT NULL,
  label_id uuid NOT NULL,
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, label_id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, label_id) REFERENCES core.labels(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX note_labels_by_label ON core.note_labels(workspace_id, label_id);

-- ---- stars, recently viewed, watches: one row per person per page, each its own table ----
CREATE TABLE core.note_stars (
  workspace_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  note_id uuid NOT NULL,
  starred_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, note_id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX note_stars_recent ON core.note_stars(workspace_id, user_id, starred_at DESC);

CREATE TABLE core.note_views (
  workspace_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  note_id uuid NOT NULL,
  last_viewed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, note_id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX note_views_recent ON core.note_views(workspace_id, user_id, last_viewed_at DESC);

-- reason: why the person watches (the creator, the owner and commenters start watching on their own).
CREATE TABLE core.note_watches (
  workspace_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  note_id uuid NOT NULL,
  reason text NOT NULL DEFAULT 'manual' CHECK (reason IN ('manual', 'creator', 'owner', 'commenter')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, note_id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX note_watches_by_note ON core.note_watches(workspace_id, note_id);

SELECT core.enable_tenant_rls('core.labels');
SELECT core.enable_tenant_rls('core.note_labels');
SELECT core.enable_tenant_rls('core.note_stars');
SELECT core.enable_tenant_rls('core.note_views');
SELECT core.enable_tenant_rls('core.note_watches');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.labels, core.note_labels, core.note_stars, core.note_views, core.note_watches TO cnote_app;

-- ---- notifications: comment kinds and the thread a notification points at ----
-- thread_id is the top-level comment of the thread; deleting the comment clears only that column (not workspace_id).
ALTER TABLE core.notifications ADD COLUMN thread_id uuid;
ALTER TABLE core.notifications
  ADD FOREIGN KEY (workspace_id, thread_id) REFERENCES core.comments(workspace_id, id) ON DELETE SET NULL (thread_id);
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_kind_check;
ALTER TABLE core.notifications
  ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('mention','approval_requested','approval_decided','reminder','assigned','quote_decided','chat_mention','lead','site_abuse','comment','page_updated'));
