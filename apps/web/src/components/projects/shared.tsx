'use client'
import { Bot } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useShell } from '@/components/shell'
import { Avatar, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'

export type Item = {
  id: string
  key: string
  title: string
  status: string
  status_category: 'todo' | 'in_progress' | 'done'
  priority: string
  item_type: string
  space_id: string
  assignee_id: string | null
  assignee_agent_id: string | null
  sprint_id: string | null
  fix_version_id: string | null
  component_id: string | null
  labels: string[]
  estimate: number | null
  due_date: string | null
  triage: boolean
  created_at: string
}
export type Transition = { name: string; to: string; guards: { type: string }[] }
export type ItemDetail = Item & { transitions: Transition[] }
export type Workflow = { definition: { statuses: { name: string; category: Item['status_category'] }[] } }
export type Sprint = {
  id: string
  name: string
  state: 'future' | 'active' | 'closed'
  goal: string | null
  start_date: string | null
  end_date: string | null
}
export type Agent = { id: string; name: string }

export const PRIORITIES = ['urgent', 'high', 'medium', 'low', 'none']
export const PROJECT_TEMPLATES = ['software', 'kanban']

const prioTone = { urgent: 'bad', high: 'warn', medium: 'neutral', low: 'neutral', none: 'neutral' } as const
export const PriorityPill = ({ p }: { p: string }) => (
  <Pill tone={prioTone[p as keyof typeof prioTone] ?? 'neutral'}>{p}</Pill>
)
const catTone = { todo: 'neutral', in_progress: 'warn', done: 'ok' } as const
export const StatusPill = ({ status, cat }: { status: string; cat: Item['status_category'] }) => (
  <Pill tone={catTone[cat]}>{status}</Pill>
)

export const useAgents = (ws: string) => useCmd<Agent[]>(ws, 'agent.list').data ?? []

/** Person avatar, or a badge for an agent assignee. */
export function Assignee({ item, agents }: { item: Item; agents: Agent[] }) {
  const { members } = useShell()
  if (item.assignee_agent_id) {
    const a = agents.find((x) => x.id === item.assignee_agent_id)
    return (
      <span
        title={`Agent: ${a?.name ?? 'unknown'}`}
        className="inline-flex items-center gap-1 rounded-full bg-hover px-1.5 py-0.5 text-[11px] text-muted"
      >
        <Bot size={12} /> {a?.name ?? 'Agent'}
      </span>
    )
  }
  return <Avatar name={members.find((m) => m.user_id === item.assignee_id)?.name} size={22} />
}

/** Value is 'u:<id>', 'a:<id>' or '' (unassigned). */
export const assigneeValue = (i: Pick<Item, 'assignee_id' | 'assignee_agent_id'>) =>
  i.assignee_agent_id ? `a:${i.assignee_agent_id}` : i.assignee_id ? `u:${i.assignee_id}` : ''
export const assigneeInput = (v: string) => ({
  user_id: v.startsWith('u:') ? v.slice(2) : null,
  agent_id: v.startsWith('a:') ? v.slice(2) : null,
})

export function AssigneeSelect({
  value,
  agents,
  onChange,
  label = 'Assignee',
  id,
}: {
  value: string
  agents: Agent[]
  onChange: (v: string) => void
  label?: string
  id?: string
}) {
  const { members } = useShell()
  return (
    <select
      id={id}
      aria-label={label}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="w-full rounded-md border border-line bg-canvas px-2 py-1 text-[13px]"
    >
      <option value="">Unassigned</option>
      <optgroup label="People">
        {members.map((m) => (
          <option key={m.user_id} value={`u:${m.user_id}`}>
            {m.name}
          </option>
        ))}
      </optgroup>
      {agents.length > 0 && (
        <optgroup label="Agents">
          {agents.map((a) => (
            <option key={a.id} value={`a:${a.id}`}>
              {a.name} (agent)
            </option>
          ))}
        </optgroup>
      )}
    </select>
  )
}

/** Minimal toast: errors from guarded transitions land here. */
export function useToast() {
  const [msg, setMsg] = useState<string>()
  useEffect(() => {
    if (!msg) return
    const t = setTimeout(() => setMsg(undefined), 6000)
    return () => clearTimeout(t)
  }, [msg])
  const toast = msg ? (
    <div
      role="alert"
      className="fixed right-4 bottom-4 z-50 max-w-sm rounded-md border border-line bg-bad-bg px-3 py-2 text-[13px] text-bad shadow-lg"
    >
      {msg}
    </div>
  ) : null
  return { toast, push: useCallback((m: string) => setMsg(m), []) }
}

/** True when a keystroke belongs to a form control, so single-key shortcuts must stay out of the way. */
export const typing = (e: KeyboardEvent) => {
  const t = e.target as HTMLElement
  return (
    e.metaKey ||
    e.ctrlKey ||
    e.altKey ||
    ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName) ||
    t.isContentEditable ||
    !!t.closest('dialog[open]')
  )
}
