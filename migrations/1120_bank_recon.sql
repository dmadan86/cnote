-- WP14: bank reconciliation, smallest version. A bank account holds the CSV column mapping that worked for its bank.
-- A statement line is imported once (unique content hash per account) and is either unmatched or linked to ONE recorded
-- money movement: an invoice payment, a receipt voucher, a bill payment or an expense. The link is a reference only:
-- matching never posts to the ledger. match_id has no foreign key (it points into four tables); the partial unique index
-- keeps a payment from matching two lines.
CREATE SCHEMA IF NOT EXISTS bank;
GRANT USAGE ON SCHEMA bank TO cnote_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA bank GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO cnote_app;

CREATE TABLE bank.accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
  account_hint text CHECK (length(account_hint) <= 20),     -- last digits only, never the full number
  csv_mapping jsonb,                                         -- header names saved by the first successful import
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, name)
);

CREATE TABLE bank.lines (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  account_id uuid NOT NULL,
  seq bigint GENERATED ALWAYS AS IDENTITY,                   -- file order
  line_date date NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor <> 0),    -- credit positive, debit negative
  narration text NOT NULL DEFAULT '' CHECK (length(narration) <= 1000),
  reference text CHECK (length(reference) <= 200),
  balance_minor bigint,
  hash text NOT NULL,
  status text NOT NULL DEFAULT 'unmatched' CHECK (status IN ('unmatched','matched')),
  match_kind text CHECK (match_kind IN ('payment','receipt','bill_payment','expense')),
  match_id uuid,
  matched_by uuid,
  matched_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((status = 'matched') = (match_id IS NOT NULL AND match_kind IS NOT NULL)),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, account_id, hash),
  FOREIGN KEY (workspace_id, account_id) REFERENCES bank.accounts (workspace_id, id)
);
CREATE INDEX bank_lines_account_date ON bank.lines (workspace_id, account_id, line_date, seq);
CREATE UNIQUE INDEX bank_lines_match ON bank.lines (workspace_id, match_kind, match_id) WHERE match_id IS NOT NULL;

SELECT core.enable_tenant_rls('bank.accounts');
SELECT core.enable_tenant_rls('bank.lines');
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA bank TO cnote_app;
