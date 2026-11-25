-- Docs parity, Phase A2 (docs/design/docs-parity.md 7.9 and 7.10): stable tree order and page deletions that can be restored.
-- Staged and online-safe (7.10.7): this file only adds nullable columns, one trigger and one function. No table rewrite, no scan,
-- no backfill and no constraint that checks old rows. Every lock is short or it fails fast (lock_timeout).
-- Order of the release: 1334 (this), the worker sweep docs.a2_backfill fills deletion_id of old deletions, 1337 recreates the access
-- functions, 1338 adds the consistency constraint NOT VALID. A later release VALIDATEs it (docs/deploy.md).
SET LOCAL lock_timeout = '5s';

-- position: sibling order inside (space_id, parent_id). bigint, gaps of 1024. Nullable with a constant default: existing rows read 0
-- ("never placed") without being rewritten, and the trigger below places every new row last. Order is (position, created_at, id).
-- deletion_id groups the pages one delete took with it; deleted_by is who did it; deleted_parent_id and deleted_restricted_by are the
-- parent and the restricted ancestors at that moment (restore evidence). NULL deleted_restricted_by means "unknown" (deleted before A2).
ALTER TABLE core.notes
  ADD COLUMN position bigint DEFAULT 0,
  ADD COLUMN deletion_id uuid,
  ADD COLUMN deleted_by uuid,
  ADD COLUMN deleted_parent_id uuid,
  ADD COLUMN deleted_restricted_by uuid[];

-- deleted_by names a person; when the person is removed the deletion stays and the name goes. NOT VALID: new rows are checked now, the
-- old rows (all NULL) are checked by the VALIDATE step of the follow-up release.
ALTER TABLE core.notes
  ADD CONSTRAINT notes_deleted_by_fk FOREIGN KEY (deleted_by) REFERENCES core.users(id) ON DELETE SET NULL NOT VALID;

-- Every insert path (note.create, duplicate, templates, importers) lands at the end of its siblings without knowing about order.
-- note.create and note.duplicate take the tree lock (advisory, per space) before inserting; other paths may race and share a
-- position, which only means the creation time decides between them.
CREATE FUNCTION core.notes_default_position() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.position IS NULL OR NEW.position = 0 THEN
    SELECT coalesce(max(position), 0) + 1024 INTO NEW.position FROM core.notes
      WHERE space_id = NEW.space_id AND parent_id IS NOT DISTINCT FROM NEW.parent_id AND deleted_at IS NULL;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER notes_default_position BEFORE INSERT ON core.notes FOR EACH ROW EXECUTE FUNCTION core.notes_default_position();

-- core.note_access for a page that may be in the trash: identical to note_access except that the starting page need not be
-- live. Trash and restore use it to ask "could this person edit it as it was". Ancestors are walked as before. Migration 1337
-- recreates both functions to fail closed on chains deeper than 50 and on cycles.
CREATE OR REPLACE FUNCTION core.note_access_any(note uuid, uid uuid, is_admin boolean, space_readable uuid[])
RETURNS text LANGUAGE sql STABLE SECURITY INVOKER AS $$
  WITH RECURSIVE chain AS (
    SELECT n.id, n.parent_id, n.space_id, n.restriction, 1 AS d
      FROM core.notes n WHERE n.id = note
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
