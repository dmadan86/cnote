-- Docs parity A1 (W2): the page search column indexes the plain-text form of the body, not the raw Markdown, so the
-- HTML tags and block markers of panels, expands, status and date chips never become search terms. This is the SQL
-- twin of notes.PlainText (apps/api/internal/modules/notes/plaintext.go): same removals, without the code-fence
-- special case. Adding the generated column rewrites every row, so existing pages stay searchable.
CREATE OR REPLACE FUNCTION core.note_plain_text(md text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT regexp_replace(regexp_replace(regexp_replace(regexp_replace(coalesce(md, ''),
    '!\[([^\]]*)\]\([^)\s]*\)', '\1', 'g'),                                              -- image: keep the alt text
    '\[!(info|note|success|warning|error)\]|\[\[toc\]\]', ' ', 'gi'),                      -- panel marker, table of contents
    '</?(details|summary|span|time|table|thead|tbody|tr|th|td|p|br)(\s[^>]*)?/?>', ' ', 'gi'),  -- editor tags
    '&(lt|gt|quot|amp|#39);', ' ', 'g')
$$;

ALTER TABLE core.notes DROP COLUMN search;
ALTER TABLE core.notes ADD COLUMN search tsvector GENERATED ALWAYS AS (
  setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
  setweight(to_tsvector('english', core.note_plain_text(content_md)), 'B')
) STORED;
CREATE INDEX notes_search ON core.notes USING gin(search);
