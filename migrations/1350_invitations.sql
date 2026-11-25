-- Email invites (docs/design/onboarding.md 6.6). One new table and one lookup function; nothing existing is rewritten, so
-- it is online-safe: no table scan, no lock on a table that serves traffic (migration 1327 is retired; Phase C uses 1345-1349).
SET LOCAL lock_timeout = '5s';

-- An invitation never creates a membership or a user. Only the SHA-256 of the 32-byte token is the credential; token_enc is
-- the same token sealed with the master key (secretbox, bound to the workspace) and kept only until the mail job has sent it,
-- so the outbox can hold references and still rebuild the mail. It is cleared on send, revoke, accept and resend.
CREATE TABLE core.invitations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  email text NOT NULL CHECK (email = lower(btrim(email)) AND position('@' in email) > 1 AND length(email) <= 254),
  role text NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'guest')),
  invited_by uuid,
  token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  token_enc bytea,
  expires_at timestamptz NOT NULL,
  accepted_at timestamptz,
  accepted_by uuid,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  CHECK (accepted_at IS NULL OR revoked_at IS NULL),
  -- the inviter and the person who accepted are members of this same workspace; leaving it keeps the row, minus the name
  FOREIGN KEY (workspace_id, invited_by) REFERENCES core.memberships (workspace_id, user_id) ON DELETE SET NULL (invited_by),
  FOREIGN KEY (workspace_id, accepted_by) REFERENCES core.memberships (workspace_id, user_id) ON DELETE SET NULL (accepted_by)
);
-- one open invitation per address; a resend rotates its token instead of adding a row
CREATE UNIQUE INDEX invitations_open ON core.invitations (workspace_id, email) WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invitations_list ON core.invitations (workspace_id, created_at DESC);
SELECT core.enable_tenant_rls('core.invitations');
-- no DELETE: an invitation is history (accepted, revoked or expired), not something to remove
GRANT SELECT, INSERT, UPDATE ON core.invitations TO cnote_app;

-- The link opens before any tenant is known. This answers only "which workspace?" for a token hash; everything else is then
-- read under that workspace's row security.
CREATE OR REPLACE FUNCTION core.invitation_workspace(p_hash text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT workspace_id FROM core.invitations WHERE token_hash = p_hash
$$;
REVOKE ALL ON FUNCTION core.invitation_workspace(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.invitation_workspace(text) TO cnote_app;
