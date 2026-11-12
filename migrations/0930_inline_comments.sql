-- Inline comments: anchored to a quote of the page text (not a mark in the content, so the stored Markdown
-- never changes), with replies and resolve.
ALTER TABLE core.comments
  ADD COLUMN anchor_text text CHECK (length(anchor_text) BETWEEN 1 AND 500),
  ADD COLUMN anchor_prefix text CHECK (length(anchor_prefix) <= 64),
  ADD COLUMN parent_id uuid REFERENCES core.comments(id) ON DELETE CASCADE,
  ADD COLUMN resolved_at timestamptz,
  ADD COLUMN resolved_by uuid REFERENCES core.users(id);
CREATE INDEX comments_note ON core.comments(note_id, created_at);
