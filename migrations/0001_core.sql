-- C Note core schema. ADR-001 (schema per module), ADR-002 (universal Note), ADR-003 (RLS).
-- Migration number ranges: 0001-0099 core/db, 0100 finance+crm, 0200 projects+workflow, 0300 docs, 0400 ai+mcp, 0500 importers.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS vector;

CREATE SCHEMA IF NOT EXISTS core;
CREATE SCHEMA IF NOT EXISTS projects;
CREATE SCHEMA IF NOT EXISTS docs;
CREATE SCHEMA IF NOT EXISTS crm;
CREATE SCHEMA IF NOT EXISTS billing;
CREATE SCHEMA IF NOT EXISTS ai;
CREATE SCHEMA IF NOT EXISTS importers;

-- The tenant id for the current transaction; NULL when unset so policies match nothing.
CREATE OR REPLACE FUNCTION core.current_workspace() RETURNS uuid
LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('app.workspace_id', true), '')::uuid $$;

-- ---------- Global (not tenant scoped) ----------
CREATE TABLE core.users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL UNIQUE,
  name text NOT NULL,
  password_hash text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.sessions (
  id text PRIMARY KEY, -- sha256 of the cookie token
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.workspaces (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  slug text NOT NULL UNIQUE,
  plan text NOT NULL DEFAULT 'free' CHECK (plan IN ('free','team','business','enterprise')),
  region text NOT NULL DEFAULT 'ap-south-1',
  settings jsonb NOT NULL DEFAULT '{}',          -- enabled MCP tool groups, fair-use caps, GSTIN, brand kit
  created_at timestamptz NOT NULL DEFAULT now()
);

-- ---------- Tenant scoped ----------
CREATE TABLE core.memberships (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  role text NOT NULL CHECK (role IN ('owner','admin','member','guest')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id)
);

-- Agent principals (ADR-014): act on behalf of an owner, never more than the owner.
CREATE TABLE core.agents (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  name text NOT NULL,
  kind text NOT NULL DEFAULT 'internal' CHECK (kind IN ('internal','external')),
  owner_user_id uuid NOT NULL REFERENCES core.users(id),
  scopes text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.api_keys (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id),
  agent_id uuid REFERENCES core.agents(id) ON DELETE CASCADE,
  name text NOT NULL,
  prefix text NOT NULL,
  key_hash text NOT NULL UNIQUE,
  scopes text[] NOT NULL DEFAULT '{}',
  last_used_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.spaces (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  key text NOT NULL,                       -- e.g. ENG, used for item keys ENG-42
  name text NOT NULL,
  template text NOT NULL DEFAULT 'blank',  -- software, kanban, docs, crm, ...
  config jsonb NOT NULL DEFAULT '{}',      -- types, workflow overrides, roles, owner defaults
  visibility text NOT NULL DEFAULT 'open' CHECK (visibility IN ('open','private')),
  item_seq integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, key)
);

CREATE TABLE core.space_members (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid NOT NULL REFERENCES core.spaces(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  role text NOT NULL DEFAULT 'editor' CHECK (role IN ('admin','editor','viewer')),
  PRIMARY KEY (space_id, user_id)
);

-- The universal Note (ADR-002). Pages, items, deals, contacts, invoices, designs are all Notes.
CREATE TABLE core.notes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid NOT NULL REFERENCES core.spaces(id) ON DELETE CASCADE,
  parent_id uuid REFERENCES core.notes(id) ON DELETE SET NULL,
  title text NOT NULL DEFAULT '',
  types text[] NOT NULL DEFAULT '{}',
  content_md text NOT NULL DEFAULT '',       -- snapshot of the CRDT doc, used by search and AI (ADR-005)
  content_ydoc bytea,                        -- latest Yjs state (collab gateway)
  -- ADR-013 living page fields
  owner_id uuid REFERENCES core.users(id),
  verify_by date,
  verified_at timestamptz,
  freshness text NOT NULL DEFAULT 'fresh' CHECK (freshness IN ('fresh','stale','unverified','archived')),
  lapse_count integer NOT NULL DEFAULT 0,
  archived_at timestamptz,
  -- ADR-012 identity preservation
  source_system text,
  source_id text,
  source_url text,
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  version integer NOT NULL DEFAULT 1,
  deleted_at timestamptz,
  search tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(title,'')), 'A') ||
    setweight(to_tsvector('english', coalesce(content_md,'')), 'B')
  ) STORED
);
CREATE INDEX notes_ws_space ON core.notes(workspace_id, space_id);
CREATE INDEX notes_parent ON core.notes(parent_id);
CREATE INDEX notes_types ON core.notes USING gin(types);
CREATE INDEX notes_search ON core.notes USING gin(search);
CREATE INDEX notes_title_trgm ON core.notes USING gin(title gin_trgm_ops);
CREATE UNIQUE INDEX notes_source ON core.notes(workspace_id, source_system, source_id) WHERE source_system IS NOT NULL;

-- Typed edges between notes: blocks, depends_on, bills, designed_for, belongs_to, child, mentions, binds, relates
CREATE TABLE core.relations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  from_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  to_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  kind text NOT NULL,
  external_ref text,                        -- for bindings to repo paths, services, URLs
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (from_id, to_id, kind)
);
CREATE INDEX relations_to ON core.relations(to_id, kind);

-- Append-only event log per note: history, reports, automations, audit (ADR-002).
CREATE TABLE core.events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid REFERENCES core.notes(id) ON DELETE SET NULL,
  kind text NOT NULL,                        -- note.created, note.updated, item.transitioned, invoice.issued, ...
  actor_type text NOT NULL CHECK (actor_type IN ('user','agent','system','import')),
  actor_id text,
  on_behalf_of uuid,                         -- human owner when actor is an agent
  payload jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_note ON core.events(note_id, id);
CREATE INDEX events_ws_kind ON core.events(workspace_id, kind, id);
CREATE OR REPLACE FUNCTION core.forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION '% is append-only', TG_TABLE_NAME; END $$;
CREATE TRIGGER events_append_only BEFORE UPDATE OR DELETE ON core.events
  FOR EACH ROW EXECUTE FUNCTION core.forbid_mutation();

CREATE TABLE core.comments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  author_id uuid REFERENCES core.users(id),
  author_name text,                          -- preserved for imported comments
  body text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.attachments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  note_id uuid NOT NULL REFERENCES core.notes(id) ON DELETE CASCADE,
  filename text NOT NULL,
  content_type text NOT NULL,
  size_bytes bigint NOT NULL,
  storage_key text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Saved views are CNQL queries (ADR-002).
CREATE TABLE core.views (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  space_id uuid REFERENCES core.spaces(id) ON DELETE CASCADE,
  name text NOT NULL,
  layout text NOT NULL DEFAULT 'table' CHECK (layout IN ('table','board','timeline','pipeline','list','calendar')),
  query text NOT NULL DEFAULT '',
  options jsonb NOT NULL DEFAULT '{}',
  created_by uuid REFERENCES core.users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Idempotency keys for write commands (ADR-004).
CREATE TABLE core.idempotency (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  key text NOT NULL,
  command text NOT NULL,
  result jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, key)
);

-- Human confirmations for money-moving/outbound commands (ADR-004 elicitation).
CREATE TABLE core.confirmations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  command text NOT NULL,
  input jsonb NOT NULL,
  requested_by text NOT NULL,
  confirmed_by uuid REFERENCES core.users(id),
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed','rejected','expired')),
  created_at timestamptz NOT NULL DEFAULT now(),
  decided_at timestamptz
);

-- Durable job queue (workers run the same image, different entrypoint; ADR-001).
CREATE TABLE core.jobs (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id uuid REFERENCES core.workspaces(id) ON DELETE CASCADE,
  kind text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed')),
  attempts integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 5,
  run_at timestamptz NOT NULL DEFAULT now(),
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_ready ON core.jobs(status, run_at) WHERE status = 'queued';

-- Internal usage ledger (ADR-011): every meterable event, billed or not.
CREATE TABLE core.usage (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  kind text NOT NULL,           -- mcp.call, automation.run, ai.tokens_in, ai.tokens_out, storage.bytes
  quantity bigint NOT NULL,
  meta jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX usage_ws_kind ON core.usage(workspace_id, kind, created_at);
