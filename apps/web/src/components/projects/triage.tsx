'use client'
import Link from 'next/link'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btnPrimary, ErrorText } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { AssigneeSelect, assigneeInput, type Item, PRIORITIES, useAgents } from './shared'

function Row({ item, onDone }: { item: Item; onDone: () => void }) {
  const { ws } = useShell()
  const agents = useAgents(ws)
  const [prio, setPrio] = useState(item.priority)
  const [who, setWho] = useState('')
  const [err, setErr] = useState<string>()
  async function accept() {
    const a = assigneeInput(who)
    try {
      await cmd(ws, 'triage.accept', {
        id: item.id,
        priority: prio,
        assignee_id: a.user_id,
        assignee_agent_id: a.agent_id,
      })
      onDone()
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  return (
    <li className="flex flex-wrap items-center gap-3 border-b border-line px-6 py-2">
      <span className="w-16 text-[12px] text-muted">{item.key}</span>
      <Link href={`/w/${ws}/projects/${item.key}`} className="min-w-0 flex-1 truncate hover:underline">
        {item.title}
      </Link>
      <span className="text-[12px] text-muted">{ago(item.created_at)}</span>
      <select
        aria-label={`Priority for ${item.key}`}
        value={prio}
        onChange={(e) => setPrio(e.target.value)}
        className="rounded-md border border-line bg-canvas px-2 py-1 text-[13px]"
      >
        {PRIORITIES.map((p) => (
          <option key={p}>{p}</option>
        ))}
      </select>
      <div className="w-44">
        <AssigneeSelect value={who} agents={agents} onChange={setWho} label={`Assignee for ${item.key}`} />
      </div>
      <button type="button" onClick={accept} className={btnPrimary}>
        Accept
      </button>
      <ErrorText error={err} />
    </li>
  )
}

export function TriageView() {
  const { ws } = useShell()
  const list = useCmd<Item[]>(ws, 'triage.list', { limit: 200 })
  return (
    <div>
      <div className="border-b border-line px-6 py-3">
        <h1 className="text-[15px] font-semibold">Triage</h1>
        <p className="text-[13px] text-muted">
          Items created by agents and integrations wait here until accepted.
        </p>
      </div>
      {list.error && <ErrorText error={list.error} />}
      {list.data?.length === 0 && <p className="p-6 text-muted">Inbox zero. Nothing to triage.</p>}
      <ul>
        {list.data?.map((i) => (
          <Row key={i.id} item={i} onDone={list.reload} />
        ))}
      </ul>
    </div>
  )
}
