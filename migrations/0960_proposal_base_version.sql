-- An AI edit proposal records the page version it was drafted against, so approving it after the page changed is refused.
ALTER TABLE docs.edit_proposals ADD COLUMN IF NOT EXISTS base_version integer;
