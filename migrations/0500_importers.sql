-- ADR-012 importers: the job keeps its (token-free) input so a batch can resume, and comments keep their source id
-- so a re-run updates instead of duplicating.
ALTER TABLE importers.jobs ADD COLUMN input jsonb NOT NULL DEFAULT '{}';
ALTER TABLE core.comments ADD COLUMN source_id text;
CREATE UNIQUE INDEX comments_source ON core.comments(note_id, source_id) WHERE source_id IS NOT NULL;
