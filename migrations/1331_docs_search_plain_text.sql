-- Docs parity A1 (W2): page search indexes the plain-text form of the body, not the raw Markdown, so the HTML tags and
-- block markers of panels, expands, status and date chips never become search terms. This is the SQL twin of
-- notes.PlainText (apps/api/internal/modules/notes/plaintext.go): same removals, without the code-fence special case.
--
-- Online-safe: nothing here rewrites or locks core.notes for long. The old generated `search` column and its index stay
-- (a later migration drops them). `search_plain` is a plain nullable column, which Postgres adds without a rewrite; a
-- trigger fills it on insert and when the title or body change; the worker sweep docs.search_plain_backfill fills the
-- existing rows in small batches; the GIN index is built in 1333 outside a transaction. Search reads search_plain where
-- it is set and falls back to `search` where it is still null, so pages stay searchable throughout.
CREATE OR REPLACE FUNCTION core.note_plain_text(md text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT regexp_replace(regexp_replace(regexp_replace(regexp_replace(coalesce(md, ''),
    '!\[((?:\\.|[^\]\\])*)\]\([^)\s]*(?:\s+"(?:\\.|[^"\\])*")?\)', '\1', 'g'),  -- image: keep the alt text
    '\[!(info|note|success|warning|error)\]|\[\[toc\]\]', ' ', 'gi'),                      -- panel marker, table of contents
    '</?(details|summary|span|time|table|thead|tbody|tr|th|td|p|br)(\s[^>]*)?/?>', ' ', 'gi'),  -- editor tags
    '&(lt|gt|quot|amp|#39);', ' ', 'g')
$$;

CREATE OR REPLACE FUNCTION core.note_search_vector(title text, md text) RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
         setweight(to_tsvector('english', core.note_plain_text(md)), 'B')
$$;

ALTER TABLE core.notes ADD COLUMN search_plain tsvector;

CREATE OR REPLACE FUNCTION core.notes_fill_search_plain() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.search_plain := core.note_search_vector(NEW.title, NEW.content_md);
  RETURN NEW;
END $$;

CREATE TRIGGER notes_search_plain BEFORE INSERT OR UPDATE OF title, content_md ON core.notes
  FOR EACH ROW EXECUTE FUNCTION core.notes_fill_search_plain();
