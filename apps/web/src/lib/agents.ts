// AI employees: shapes of the agent.* commands (docs/design/agent-runtime.md section 2.3, as revised by section 16).

export type AgentTemplateKey = 'ask' | 'briefing' | 'lead_responder' | 'support'
export type ModelTier = 'reasoning' | 'routine'
export type RunTrigger = 'ask' | 'chat' | 'event' | 'schedule' | 'manual'
export type RunStatus = 'queued' | 'running' | 'held' | 'completed' | 'failed' | 'cancelled' | 'skipped'
export type StepOutcome = 'ok' | 'error' | 'denied' | 'held'

export type Agent = {
  id: string
  name: string
  description: string
  kind: 'internal' | 'external'
  template: AgentTemplateKey | null
  owner_user_id: string
  owner_name: string
  enabled: boolean
  runtime: boolean
  instructions: string | null
  instructions_default: string | null
  tools: string[]
  scopes: string[]
  model_tier: ModelTier
  max_steps: number
  token_budget: number
  daily_runs: number
  events: string[]
  every_minutes: number | null
  next_run_at: string | null
  deliver_channel_id: string | null
  created_at: string
}

export type Run = {
  id: string
  agent_id: string
  agent_name: string
  trigger: RunTrigger
  status: RunStatus
  on_behalf_of: string
  on_behalf_name: string
  conversation_id: string | null
  note_id: string | null
  input: string
  output: string | null
  error: string | null
  steps: number
  held: number
  pending: number
  tokens_in: number
  tokens_out: number
  provider: string | null
  model: string | null
  created_at: string
  started_at: string | null
  finished_at: string | null
}

export type ModelStep = {
  seq: number
  kind: 'model'
  text: string | null
  stop: string | null
  tokens_in: number | null
  tokens_out: number | null
  created_at: string
}

export type ToolStep = {
  seq: number
  kind: 'tool'
  command: string
  input: unknown
  result: string | null
  outcome: StepOutcome
  confirmation: { id: string; kind: string; status: string } | null
  ms: number | null
  created_at: string
}

export type Step = ModelStep | ToolStep

export type Template = {
  key: AgentTemplateKey
  name: string
  description: string
  tier: ModelTier
  scopes: string[]
  tools: string[]
  events: string[]
  every_minutes: number | null
  needs: { groups_off: string[]; chat_agents: boolean }
  agent_id: string | null
}

export type RunDetail = { run: Run; steps: Step[] }

export const AGENT_CMD = {
  templates: 'agent.templates',
  install: 'agent.install',
  get: 'agent.get',
  update: 'agent.update',
  ask: 'agent.ask',
  run: 'agent.run',
  runGet: 'agent.run_get',
  runList: 'agent.run_list',
  runCancel: 'agent.run_cancel',
  list: 'agent.list',
} as const
