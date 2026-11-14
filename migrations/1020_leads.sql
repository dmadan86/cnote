-- Leads and follow-ups: a lead is a deal in stage 'lead'. Deals gain a source, a next step with a date and a lost
-- reason; calls/visits/messages are rows in crm.activities; one public inquiry form per workspace.
ALTER TABLE crm.deals
  ADD COLUMN source text,
  ADD COLUMN next_follow_up date,
  ADD COLUMN next_step text,
  ADD COLUMN lost_reason text,
  ADD COLUMN first_response_at timestamptz;
CREATE INDEX deals_follow_up ON crm.deals(workspace_id, next_follow_up) WHERE closed_at IS NULL;

-- DPDP: when the person consented to be contacted (set by the public form).
ALTER TABLE crm.contacts ADD COLUMN consent_at timestamptz;

CREATE TABLE crm.activities (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('call','visit','whatsapp','email','note')),
  summary text NOT NULL CHECK (length(summary) BETWEEN 1 AND 2000),
  happened_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, note_id) REFERENCES core.notes (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX activities_note ON crm.activities(workspace_id, note_id, happened_at DESC);
SELECT core.enable_tenant_rls('crm.activities');

CREATE TABLE crm.inquiry_forms (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL UNIQUE REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid NOT NULL,
  owner_id uuid REFERENCES core.users(id),
  token text NOT NULL UNIQUE,        -- public by design (it sits in a link on a website), not a credential
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, space_id) REFERENCES core.spaces (workspace_id, id) ON DELETE CASCADE
);
SELECT core.enable_tenant_rls('crm.inquiry_forms');

-- The public endpoint knows only the token, so it cannot set a tenant first.
CREATE OR REPLACE FUNCTION crm.inquiry_form_lookup(p_token text)
RETURNS TABLE(workspace_id uuid, workspace_name text, space_id uuid, owner_id uuid, enabled boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = crm, core, pg_temp AS $$
  SELECT f.workspace_id, w.name, f.space_id, f.owner_id, f.enabled
  FROM crm.inquiry_forms f JOIN core.workspaces w ON w.id = f.workspace_id WHERE f.token = p_token $$;
