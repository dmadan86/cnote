-- Outbound hooks: on matching events and/or on a schedule, POST a signed JSON body to a URL ('http')
-- or call a tool on a remote MCP server ('mcp'). secret_enc: AES-GCM (CNOTE_MASTER_KEY) of the HMAC
-- signing secret ('http') or the optional bearer token ('mcp').
CREATE TABLE core.hooks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  target text NOT NULL CHECK (target IN ('http', 'mcp')),
  url text NOT NULL,
  tool text,
  arguments jsonb NOT NULL DEFAULT '{}',
  secret_enc bytea,
  events text[] NOT NULL DEFAULT '{}',          -- event kind prefixes ('note.', 'item.created'); '*' = all
  every_minutes int CHECK (every_minutes >= 5),
  next_run_at timestamptz,
  enabled boolean NOT NULL DEFAULT true,
  created_by uuid REFERENCES core.users(id),
  last_run_at timestamptz,
  last_status text,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (target <> 'mcp' OR tool IS NOT NULL),
  CHECK (cardinality(events) > 0 OR every_minutes IS NOT NULL)
);
CREATE INDEX hooks_due ON core.hooks(next_run_at) WHERE enabled AND every_minutes IS NOT NULL;
SELECT core.enable_tenant_rls('core.hooks');
