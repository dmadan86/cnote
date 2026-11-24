-- The page a chat turn was asked on (design 11): the model sees only that page and the shared blocks.
ALTER TABLE sites.site_turns ADD COLUMN page text CHECK (page IS NULL OR page ~ '^[a-z0-9-]{1,40}$');
