'use client'
import { Bot, Wrench } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { Modal, Pill } from '@/components/ui'
import type { Run, RunDetail, RunStatus, StepOutcome } from '@/lib/agents'
import { ago, useCmd } from '@/lib/hooks'

const STATUS: Record<RunStatus, { label: string; tone: 'ok' | 'warn' | 'bad' | 'neutral' }> = {
  queued: { label: 'Queued', tone: 'neutral' },
  running: { label: 'Running', tone: 'warn' },
  held: { label: 'Waiting for approval', tone: 'warn' },
  completed: { label: 'Done', tone: 'ok' },
  failed: { label: 'Failed', tone: 'bad' },
  cancelled: { label: 'Cancelled', tone: 'neutral' },
  skipped: { label: 'Skipped', tone: 'neutral' },
}
const OUTCOME: Record<StepOutcome, { label: string; tone: 'ok' | 'warn' | 'bad' | 'neutral' }> = {
  ok: { label: 'Done', tone: 'ok' },
  error: { label: 'Error', tone: 'bad' },
  denied: { label: 'Not allowed', tone: 'bad' },
  held: { label: 'Waiting for approval', tone: 'warn' },
}
const TRIGGER = {
  ask: 'Asked',
  chat: 'Chat mention',
  event: 'Event',
  schedule: 'Schedule',
  manual: 'Run now',
}

export const StatusPill = ({ status }: { status: RunStatus }) => (
  <Pill tone={STATUS[status].tone}>{STATUS[status].label}</Pill>
)

// Friendly name for a tool command; unknown ones fall back to the command id spelled out.
const LABELS: Record<string, string> = {
  'home.summary': 'Looked at what needs attention',
  'report.run': 'Ran a report',
  'report.datasets': 'Checked which reports exist',
  'report.receivables_ageing': 'Checked money to collect',
  'report.payables_ageing': 'Checked money to pay',
  'search.hybrid': 'Searched your pages',
  'note.get': 'Opened a page',
  'item.list': 'Looked up items',
  'followup.list': 'Listed follow-ups',
  'followup.set': 'Set a follow-up',
  'invoice.list': 'Listed invoices',
  'deal.list': 'Listed deals',
  'ticket.list': 'Listed tickets',
  'ticket.get': 'Opened a ticket',
  'ticket.messages': 'Read the ticket conversation',
  'ticket.reply': 'Drafted a ticket reply',
  'contact.get': 'Opened a contact',
  'contact.list': 'Listed contacts',
  'contact.lifecycle': 'Read the lead history',
  'contact.email': 'Drafted an email',
  'activity.list': 'Read the activity log',
  'activity.log': 'Logged a note',
  'quote.create_draft': 'Drafted a quote',
  'quote.list': 'Listed quotes',
  'stock_item.list': 'Checked stock',
  'comment.add': 'Added an internal comment',
  'approval.list': 'Checked approvals',
}
export const toolLabel = (c: string) => LABELS[c] ?? c.replace(/[._]/g, ' ')

export function RunDrawer({ ws, id, onClose }: { ws: string; id: string; onClose: () => void }) {
  const { data, error } = useCmd<RunDetail>(ws, 'agent.run_get', { id })
  return (
    <Modal open onClose={onClose} title="Run" className="max-w-2xl">
      {error && (
        <p role="alert" className="text-[13px] text-bad">
          {error.message}
        </p>
      )}
      {!data && !error && <p className="text-[13px] text-muted">Loading…</p>}
      {data && (
        <div className="space-y-4 text-[13px]">
          <div className="flex flex-wrap items-center gap-2">
            <StatusPill status={data.run.status} />
            <span className="text-muted">
              {TRIGGER[data.run.trigger]} · for {data.run.on_behalf_name} · {ago(data.run.created_at)}
            </span>
          </div>
          {data.run.status === 'held' && (
            <p className="rounded-md border border-line bg-hover p-2">
              Something this run drafted is waiting for a person to approve it.{' '}
              <Link href="approvals" className="font-medium underline">
                Open the approvals inbox
              </Link>
            </p>
          )}
          {data.run.input && <p className="whitespace-pre-wrap text-muted">{data.run.input}</p>}
          <ol className="space-y-2">
            {data.steps.map((s) => (
              <li key={s.seq} className="flex gap-2 rounded-md border border-line p-2">
                {s.kind === 'model' ? (
                  <Bot size={15} className="mt-0.5 shrink-0 text-muted" />
                ) : (
                  <Wrench size={15} className="mt-0.5 shrink-0 text-muted" />
                )}
                <div className="min-w-0 flex-1">
                  {s.kind === 'model' ? (
                    <p className="whitespace-pre-wrap">
                      {s.text || <span className="text-muted">Thinking</span>}
                    </p>
                  ) : (
                    <>
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{toolLabel(s.command)}</span>
                        <Pill tone={OUTCOME[s.outcome].tone}>{OUTCOME[s.outcome].label}</Pill>
                        {s.ms != null && <span className="text-muted">{s.ms} ms</span>}
                      </div>
                      <details className="mt-1 text-muted">
                        <summary className="cursor-pointer">Details</summary>
                        <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-words text-[12px]">
                          {s.command}
                          {'\n'}
                          {JSON.stringify(s.input, null, 2)}
                          {s.result ? `\n\n${s.result}` : ''}
                        </pre>
                      </details>
                      {s.outcome === 'held' && (
                        <Link
                          href="approvals"
                          className="mt-1 inline-block text-[12px] font-medium underline"
                        >
                          Review in approvals
                        </Link>
                      )}
                    </>
                  )}
                </div>
              </li>
            ))}
          </ol>
          {data.run.output && (
            <div>
              <div className="mb-1 text-[12px] font-medium text-muted">Result</div>
              <p className="whitespace-pre-wrap">{data.run.output}</p>
            </div>
          )}
          {data.run.error && (
            <p role="alert" className="text-bad">
              {data.run.error}
            </p>
          )}
          <p className="text-[12px] text-muted">
            {data.run.steps} steps · {data.run.tokens_in + data.run.tokens_out} tokens
          </p>
        </div>
      )}
    </Modal>
  )
}

export function RunsTable({ ws, agentId }: { ws: string; agentId: string }) {
  const { data, error } = useCmd<Run[]>(ws, 'agent.run_list', { agent_id: agentId, limit: 30 })
  const [open, setOpen] = useState<string>()
  if (error)
    return (
      <p role="alert" className="text-[13px] text-bad">
        {error.message}
      </p>
    )
  if (!data) return <p className="text-[13px] text-muted">Loading…</p>
  if (!data.length) return <p className="text-[13px] text-muted">No runs yet.</p>
  return (
    <>
      <div className="overflow-x-auto rounded-md border border-line">
        <table className="w-full text-left text-[13px]">
          <thead className="text-[12px] text-muted">
            <tr>
              {['Trigger', 'Status', 'Started', 'Steps', 'Tokens'].map((h) => (
                <th key={h} className="px-3 py-2 font-medium">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {data.map((r) => (
              <tr key={r.id} className="hover:bg-hover">
                <td className="px-3 py-2">
                  <button
                    type="button"
                    className="font-medium underline-offset-2 hover:underline"
                    onClick={() => setOpen(r.id)}
                  >
                    {TRIGGER[r.trigger]}
                  </button>
                </td>
                <td className="px-3 py-2">
                  <StatusPill status={r.status} />
                </td>
                <td className="px-3 py-2 text-muted">{ago(r.started_at ?? r.created_at)}</td>
                <td className="px-3 py-2">{r.steps}</td>
                <td className="px-3 py-2">{r.tokens_in + r.tokens_out}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {open && <RunDrawer ws={ws} id={open} onClose={() => setOpen(undefined)} />}
    </>
  )
}
