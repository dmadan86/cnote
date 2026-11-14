-- WP22: bank reconciliation v2. A statement line can be matched to SEVERAL recorded movements whose amounts sum exactly to the line
-- (bank.line_matches), or ignored with a reason (bank charges not worth recording, internal transfers). A single match keeps its
-- bank.lines.match_* columns and also gets a row here, so the one unique index below stops any payment matching two lines
-- whether alone or in a split. A split line has status 'matched', split = true and null match_kind/match_id.
-- Links only: nothing here posts to the ledger.
CREATE TABLE bank.line_matches (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  line_id uuid NOT NULL,
  match_kind text NOT NULL CHECK (match_kind IN ('payment','receipt','bill_payment','expense')),
  match_id uuid NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, match_kind, match_id),
  FOREIGN KEY (workspace_id, line_id) REFERENCES bank.lines (workspace_id, id)
);
CREATE INDEX bank_line_matches_line ON bank.line_matches (workspace_id, line_id);
SELECT core.enable_tenant_rls('bank.line_matches');
GRANT SELECT, INSERT, UPDATE, DELETE ON bank.line_matches TO cnote_app;

INSERT INTO bank.line_matches (workspace_id, line_id, match_kind, match_id, amount_minor)
  SELECT workspace_id, id, match_kind, match_id, abs(amount_minor) FROM bank.lines WHERE status = 'matched';

ALTER TABLE bank.lines
  ADD COLUMN split boolean NOT NULL DEFAULT false,
  ADD COLUMN ignore_reason text CHECK (length(btrim(ignore_reason)) BETWEEN 1 AND 200),
  DROP CONSTRAINT lines_check,
  DROP CONSTRAINT lines_status_check,
  ADD CONSTRAINT lines_status_check CHECK (status IN ('unmatched','matched','ignored')),
  ADD CONSTRAINT lines_match_shape CHECK (
    (status = 'matched' AND ((match_id IS NOT NULL AND match_kind IS NOT NULL AND NOT split) OR (split AND match_id IS NULL AND match_kind IS NULL)))
    OR (status <> 'matched' AND match_id IS NULL AND match_kind IS NULL AND NOT split)),
  ADD CONSTRAINT lines_ignore_shape CHECK ((status = 'ignored') = (ignore_reason IS NOT NULL));
