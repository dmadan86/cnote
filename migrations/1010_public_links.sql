-- WP2: public share links for quotations and invoices. The token (32 random bytes, base64url) is shown once in the URL
-- and only its sha256 is stored. The unauthenticated route knows no tenant, so one narrow SECURITY DEFINER lookup
-- by token hash (same pattern as core.resolve_api_key) returns the link row; everything after it runs under RLS.
CREATE TABLE core.public_links (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL,
  token_hash text NOT NULL UNIQUE,
  purpose text NOT NULL CHECK (purpose IN ('view','quote_accept')),
  expires_at timestamptz,
  revoked_at timestamptz,
  view_count integer NOT NULL DEFAULT 0,
  last_viewed_at timestamptz,
  created_by uuid NOT NULL REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX public_links_note ON core.public_links(workspace_id, note_id, created_at DESC);
SELECT core.enable_tenant_rls('core.public_links');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.public_links TO cnote_app;

-- Unknown, revoked and expired tokens are indistinguishable: zero rows.
CREATE OR REPLACE FUNCTION core.resolve_public_link(p_hash text)
RETURNS TABLE(id uuid, workspace_id uuid, note_id uuid, purpose text, created_by uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = core, pg_temp AS $$
  SELECT l.id, l.workspace_id, l.note_id, l.purpose, l.created_by FROM core.public_links l
  WHERE l.token_hash = p_hash AND l.revoked_at IS NULL AND (l.expires_at IS NULL OR l.expires_at > now())
$$;
GRANT EXECUTE ON FUNCTION core.resolve_public_link(text) TO cnote_app;

-- Who decided a quotation and how: 'link' = the customer on the public page, 'staff' = a member.
ALTER TABLE billing.quotations
  ADD COLUMN accepted_by_name text,
  ADD COLUMN accepted_note text,
  ADD COLUMN accepted_via text CHECK (accepted_via IN ('staff','link'));
