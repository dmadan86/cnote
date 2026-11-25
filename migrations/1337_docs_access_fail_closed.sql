-- Docs parity A2 (7.10.8): a page chain deeper than 50 levels, or one that loops back on itself, is treated as restricted: both access
-- functions answer 'none' for everyone but admins (fail closed). Before this, the walk simply stopped at 50 and treated the rest of
-- the chain as unrestricted, so a deep or cyclic tree could hide a restriction. Structural writes (note.create, note.move, note.update,
-- note.duplicate, note.undelete) refuse a result deeper than 50 from this release on.
-- Only the function bodies change; callers and signatures are the same. Check existing data first (docs/deploy.md): no tree should be
-- deeper than 50 and none should contain a cycle.
SET LOCAL lock_timeout = '5s';

CREATE OR REPLACE FUNCTION core.note_access(note uuid, uid uuid, is_admin boolean, space_readable uuid[])
RETURNS text LANGUAGE sql STABLE SECURITY INVOKER AS $$
  WITH RECURSIVE chain AS (
    SELECT n.id, n.parent_id, n.space_id, n.restriction, 1 AS d
      FROM core.notes n WHERE n.id = note AND n.deleted_at IS NULL
    UNION ALL
    SELECT p.id, p.parent_id, p.space_id, p.restriction, c.d + 1
      FROM core.notes p JOIN chain c ON p.id = c.parent_id WHERE c.d < 51
  ), per AS (
    SELECT c.*, pm.level FROM chain c
      LEFT JOIN core.note_permissions pm ON pm.note_id = c.id AND pm.user_id = uid
  ), agg AS (
    SELECT (SELECT space_id FROM chain WHERE d = 1) AS sp,
           (SELECT count(*) FROM chain) AS n,
           coalesce(bool_or(d > 50), false) AS deep,
           coalesce(bool_and(restriction <> 'view' OR level IS NOT NULL), true) AS view_ok,
           coalesce(bool_and(restriction = 'none' OR coalesce(level = 'edit', false)), true) AS edit_ok,
           coalesce(max(CASE level WHEN 'edit' THEN 2 WHEN 'view' THEN 1 ELSE 0 END), 0) AS best
      FROM per
  )
  SELECT CASE
    WHEN n = 0 THEN 'none'
    WHEN is_admin THEN 'edit'
    WHEN deep THEN 'none'
    WHEN NOT view_ok THEN 'none'
    WHEN sp = ANY(space_readable) THEN CASE WHEN edit_ok THEN 'edit' ELSE 'view' END
    WHEN best = 2 THEN CASE WHEN edit_ok THEN 'edit' ELSE 'view' END
    WHEN best = 1 THEN 'view'
    ELSE 'none' END
  FROM agg
$$;

CREATE OR REPLACE FUNCTION core.note_access_any(note uuid, uid uuid, is_admin boolean, space_readable uuid[])
RETURNS text LANGUAGE sql STABLE SECURITY INVOKER AS $$
  WITH RECURSIVE chain AS (
    SELECT n.id, n.parent_id, n.space_id, n.restriction, 1 AS d
      FROM core.notes n WHERE n.id = note
    UNION ALL
    SELECT p.id, p.parent_id, p.space_id, p.restriction, c.d + 1
      FROM core.notes p JOIN chain c ON p.id = c.parent_id WHERE c.d < 51
  ), per AS (
    SELECT c.*, pm.level FROM chain c
      LEFT JOIN core.note_permissions pm ON pm.note_id = c.id AND pm.user_id = uid
  ), agg AS (
    SELECT (SELECT space_id FROM chain WHERE d = 1) AS sp,
           (SELECT count(*) FROM chain) AS n,
           coalesce(bool_or(d > 50), false) AS deep,
           coalesce(bool_and(restriction <> 'view' OR level IS NOT NULL), true) AS view_ok,
           coalesce(bool_and(restriction = 'none' OR coalesce(level = 'edit', false)), true) AS edit_ok,
           coalesce(max(CASE level WHEN 'edit' THEN 2 WHEN 'view' THEN 1 ELSE 0 END), 0) AS best
      FROM per
  )
  SELECT CASE
    WHEN n = 0 THEN 'none'
    WHEN is_admin THEN 'edit'
    WHEN deep THEN 'none'
    WHEN NOT view_ok THEN 'none'
    WHEN sp = ANY(space_readable) THEN CASE WHEN edit_ok THEN 'edit' ELSE 'view' END
    WHEN best = 2 THEN CASE WHEN edit_ok THEN 'edit' ELSE 'view' END
    WHEN best = 1 THEN 'view'
    ELSE 'none' END
  FROM agg
$$;
