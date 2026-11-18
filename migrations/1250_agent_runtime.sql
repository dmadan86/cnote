-- AI employees: agent runtime (docs/design/agent-runtime.md, section 2.1 as revised by section 16).
-- No auto-approval columns: every outbound or money action is a held confirmation a person decides.
ALTER TABLE core.agents
  ADD COLUMN description text NOT NULL DEFAULT '',
  ADD COLUMN template text,              -- ask | briefing | lead_responder | support
  ADD COLUMN instructions text,          -- null on a template agent = the built-in text
  ADD COLUMN tools text[],               -- may only narrow the template allowlist; null = template default
  ADD COLUMN model_tier text NOT NULL DEFAULT 'reasoning' CHECK (model_tier IN ('reasoning','routine')),
  ADD COLUMN max_steps int NOT NULL DEFAULT 12 CHECK (max_steps BETWEEN 1 AND 40),
  ADD COLUMN token_budget int NOT NULL DEFAULT 150000 CHECK (token_budget BETWEEN 5000 AND 2000000),
  ADD COLUMN daily_runs int NOT NULL DEFAULT 200 CHECK (daily_runs BETWEEN 1 AND 5000),
  ADD COLUMN enabled boolean NOT NULL DEFAULT true,
  ADD COLUMN events text[] NOT NULL DEFAULT '{}',   -- event kind prefixes, as core.hooks.events
  ADD COLUMN every_minutes int CHECK (every_minutes >= 5),
  ADD COLUMN next_run_at timestamptz,
  ADD COLUMN deliver_channel_id uuid,
  ADD FOREIGN KEY (workspace_id, deliver_channel_id) REFERENCES chat.channels (workspace_id, id) ON DELETE SET NULL (deliver_channel_id);
CREATE UNIQUE INDEX agents_template_once ON core.agents (workspace_id, template) WHERE template IS NOT NULL;
CREATE INDEX agents_due ON core.agents (next_run_at) WHERE enabled AND every_minutes IS NOT NULL;

CREATE TABLE core.agent_runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  agent_id uuid NOT NULL,
  on_behalf_of uuid NOT NULL REFERENCES core.users(id),
  trigger text NOT NULL CHECK (trigger IN ('ask','chat','event','schedule','manual')),
  dedupe_key text, conversation_id uuid, note_id uuid,
  input text NOT NULL DEFAULT '', context jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','held','completed','failed','cancelled','skipped')),
  attempt uuid,                          -- execution fence: set when a worker starts the run; every write says "where attempt = mine"
  output text, error text,
  steps int NOT NULL DEFAULT 0, held int NOT NULL DEFAULT 0,
  tokens_in bigint NOT NULL DEFAULT 0, tokens_out bigint NOT NULL DEFAULT 0,
  provider text, model text,
  created_at timestamptz NOT NULL DEFAULT now(), started_at timestamptz, finished_at timestamptz,
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, agent_id) REFERENCES core.agents (workspace_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX agent_runs_dedupe ON core.agent_runs (agent_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
CREATE INDEX agent_runs_agent ON core.agent_runs (workspace_id, agent_id, created_at DESC);
CREATE INDEX agent_runs_note ON core.agent_runs (note_id) WHERE note_id IS NOT NULL;
CREATE INDEX agent_runs_conv ON core.agent_runs (conversation_id, created_at) WHERE conversation_id IS NOT NULL;

CREATE TABLE core.agent_run_steps (
  workspace_id uuid NOT NULL, run_id uuid NOT NULL, seq int NOT NULL,
  kind text NOT NULL CHECK (kind IN ('model','tool')),
  text text, raw json, stop text, tokens_in int, tokens_out int, cache_read int,       -- model step; raw is json, not jsonb: provider content replays byte for byte
  call_id text, command text, input jsonb, result text,                                -- tool step
  outcome text CHECK (outcome IN ('ok','error','denied','held')),
  confirmation_id uuid,
  ms int, created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (run_id, seq),
  FOREIGN KEY (workspace_id, run_id) REFERENCES core.agent_runs (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, confirmation_id) REFERENCES core.confirmations (workspace_id, id) ON DELETE SET NULL (confirmation_id)
);
CREATE INDEX agent_run_steps_conf ON core.agent_run_steps (confirmation_id) WHERE confirmation_id IS NOT NULL;

-- Daily token budget per workspace: reserve before a model call, settle to actual usage after.
CREATE TABLE core.agent_budget (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  day date NOT NULL,
  reserved bigint NOT NULL DEFAULT 0 CHECK (reserved >= 0),
  used bigint NOT NULL DEFAULT 0 CHECK (used >= 0),
  PRIMARY KEY (workspace_id, day)
);

SELECT core.enable_tenant_rls('core.agent_runs');
SELECT core.enable_tenant_rls('core.agent_run_steps');
SELECT core.enable_tenant_rls('core.agent_budget');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.agent_runs, core.agent_run_steps, core.agent_budget TO cnote_app;

-- Section 9b: the automation "run command" action ran any command as System; leftover rows are switched off.
UPDATE projects.automations SET enabled = false WHERE actions @> '[{"type":"command"}]';

-- An approval opened by an agent or API key remembers it: the replay re-derives that machine's rights (a paused agent or
-- a removed scope refuses) instead of running as the person.
ALTER TABLE core.confirmations
  ADD COLUMN requested_by_agent uuid,
  ADD COLUMN requested_by_kind text CHECK (requested_by_kind IN ('Agent','ApiKey')),
  ADD FOREIGN KEY (workspace_id, requested_by_agent) REFERENCES core.agents (workspace_id, id) ON DELETE SET NULL (requested_by_agent);
