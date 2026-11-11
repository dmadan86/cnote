-- Confluence-style page restrictions and sharing on top of space access (see crates/cnote-core/src/access.rs).
-- restriction: none = space rules; edit = everyone with access reads, only listed 'edit' users write;
-- view = only listed users read (and only 'edit' ones write). Restrictions are inherited through parent_id.
ALTER TABLE core.notes
  ADD COLUMN restriction text NOT NULL DEFAULT 'none' CHECK (restriction IN ('none','edit','view'));

CREATE TABLE core.note_permissions (
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id),
  level text NOT NULL CHECK (level IN ('view','edit')),
  granted_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, user_id)
);
CREATE INDEX note_permissions_user ON core.note_permissions(user_id);
SELECT core.enable_tenant_rls('core.note_permissions');

-- The one place page access is decided. Returns 'none' | 'view' | 'edit' for the page itself (space-level
-- write roles are still checked by the caller). Walks ancestor-or-self (depth-capped, soft-deleted ancestors
-- still restrict): reading needs every view-restricted node to list the user; writing needs every restricted
-- node to list the user as 'edit'. A user with no space access reads only through an explicit share on the
-- page or an ancestor (a share also covers descendants). Admins bypass.
CREATE OR REPLACE FUNCTION core.note_access(note uuid, uid uuid, is_admin boolean, space_readable uuid[])
RETURNS text LANGUAGE sql STABLE SECURITY INVOKER AS $$
  WITH RECURSIVE chain AS (
    SELECT n.id, n.parent_id, n.space_id, n.restriction, 1 AS d
      FROM core.notes n WHERE n.id = note AND n.deleted_at IS NULL
    UNION ALL
    SELECT p.id, p.parent_id, p.space_id, p.restriction, c.d + 1
      FROM core.notes p JOIN chain c ON p.id = c.parent_id WHERE c.d < 50
  ), per AS (
    SELECT c.*, pm.level FROM chain c
      LEFT JOIN core.note_permissions pm ON pm.note_id = c.id AND pm.user_id = uid
  ), agg AS (
    SELECT (SELECT space_id FROM chain WHERE d = 1) AS sp,
           (SELECT count(*) FROM chain) AS n,
           coalesce(bool_and(restriction <> 'view' OR level IS NOT NULL), true) AS view_ok,
           coalesce(bool_and(restriction = 'none' OR coalesce(level = 'edit', false)), true) AS edit_ok,
           coalesce(max(CASE level WHEN 'edit' THEN 2 WHEN 'view' THEN 1 ELSE 0 END), 0) AS best
      FROM per
  )
  SELECT CASE
    WHEN n = 0 THEN 'none'
    WHEN is_admin THEN 'edit'
    WHEN NOT view_ok THEN 'none'
    WHEN sp = ANY(space_readable) THEN CASE WHEN edit_ok THEN 'edit' ELSE 'view' END
    WHEN best = 2 THEN CASE WHEN edit_ok THEN 'edit' ELSE 'view' END
    WHEN best = 1 THEN 'view'
    ELSE 'none' END
  FROM agg
$$;
