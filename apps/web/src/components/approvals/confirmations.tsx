'use client'
import { Bot, ChevronRight, ShieldCheck } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { btn, btnPrimary, EmptyState, ErrorText } from '@/components/ui'
import type { RunDetail } from '@/lib/agents'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

// agent_name and run_id are set when an AI employee asked (docs/design/agent-runtime.md 2.3); outside agents have neither.
export type Confirmation = {
  id: string
  command: string
  requested_by: string
  created_at: string
  input: unknown
  agent_name?: string | null
  run_id?: string | null
}
type Decision = { status: string; executed?: boolean; result?: unknown }

const str = (v: unknown) => (typeof v === 'string' && v.trim() ? v.trim() : '')

/** What the request will do, in words. Agent text is shown as plain text only, never as HTML. */
export function describe(c: Confirmation, ws: string) {
  const i = (c.input && typeof c.input === 'object' ? c.input : {}) as Record<string, unknown>
  const ref = str(i.ref) || str(i.number) || str(i.ticket_ref) || str(i.id)
  const body = str(i.body) || str(i.message) || str(i.text)
  const to = str(i.to) || str(i.email) || str(i.contact_name)
  const subject = str(i.subject)
  const rec = str(i.note_id) || str(i.deal_id) || str(i.ticket_id) || str(i.quote_id) || str(i.id)
  const link = /^[0-9a-f-]{36}$/.test(rec) ? `/w/${ws}/n/${rec}` : undefined
  if (c.command === 'ticket.reply') return { title: `Reply on ticket ${ref}`.trim(), preview: body, link }
  if (c.command === 'quote.send') return { title: `Send quote ${ref}`.trim(), preview: body, link }
  if (
    (c.command.endsWith('.send') || c.command === 'contact.email' || c.command === 'share.email') &&
    (to || subject)
  )
    return { title: `Email ${to || 'the contact'}${subject ? `: ${subject}` : ''}`, preview: body, link }
  return { title: c.command, preview: body, link }
}

function RunTrail({ ws, runId }: { ws: string; runId: string }) {
  const [open, setOpen] = useState(false)
  const run = useCmd<RunDetail>(ws, 'agent.run_get', { id: runId }, open)
  return (
    <details
      className="border-t border-line px-4 py-2 text-[12px]"
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary className="cursor-pointer text-muted hover:text-fg">What the agent did</summary>
      <ErrorText error={run.error} />
      {run.data && (
        <ol className="mt-2 space-y-1">
          {run.data.steps.map((s) =>
            s.kind === 'tool' ? (
              <li key={s.seq} className="flex gap-2">
                <ChevronRight size={12} className="mt-0.5 shrink-0 text-muted" aria-hidden />
                <code>{s.command}</code>
                <span className="text-muted">{s.outcome === 'held' ? 'waiting for you' : s.outcome}</span>
              </li>
            ) : null,
          )}
        </ol>
      )}
    </details>
  )
}

export function ConfirmationCard({ c, ws, done }: { c: Confirmation; ws: string; done: () => void }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  const [out, setOut] = useState<Decision & { approve: boolean }>()
  const d = describe(c, ws)
  const agent = c.agent_name
  // busy blocks a double click; a "not found" answer means another click or person already decided it.
  const decide = async (approve: boolean) => {
    if (busy) return
    setBusy(true)
    setErr(undefined)
    try {
      setOut({ ...(await cmd<Decision>(ws, 'confirmation.decide', { id: c.id, approve })), approve })
    } catch (e) {
      const m = (e as Error).message
      if (/not.?found/i.test(m)) done()
      else {
        setErr(m)
        setBusy(false)
      }
    }
  }
  return (
    <li className="rounded-lg border border-line">
      <div className="flex flex-wrap items-start gap-3 p-4">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-line bg-hover text-muted">
          <Bot size={16} aria-hidden />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-[14px] font-medium">{d.title}</p>
          <p className="mt-0.5 text-[12px] text-muted">
            {agent ? `${agent} for ${c.requested_by}` : `Requested by ${c.requested_by}`} ·{' '}
            {ago(c.created_at)}
            {d.link && (
              <>
                {' · '}
                <Link href={d.link} className="underline">
                  Open the record
                </Link>
              </>
            )}
          </p>
        </div>
        {!out && (
          <div className="flex shrink-0 gap-2">
            <button type="button" className={btn} disabled={busy} onClick={() => decide(false)}>
              Reject
            </button>
            <button type="button" className={btnPrimary} disabled={busy} onClick={() => decide(true)}>
              Approve
            </button>
          </div>
        )}
      </div>
      {d.preview && (
        <blockquote className="mx-4 mb-3 whitespace-pre-wrap break-words rounded-md border-l-2 border-line bg-side p-3 text-[13px]">
          {d.preview}
        </blockquote>
      )}
      {out && (
        <p role="status" className="mx-4 mb-3 text-[13px]">
          {out.approve
            ? out.executed === false
              ? 'Approved, but it did not run.'
              : 'Approved and sent.'
            : 'Rejected. Nothing was sent.'}
          {out.approve && typeof out.result === 'string' && ` ${out.result}`}{' '}
          <button type="button" className="underline" onClick={done}>
            Done
          </button>
        </p>
      )}
      <ErrorText error={err} />
      {c.run_id && <RunTrail ws={ws} runId={c.run_id} />}
      <details className="border-t border-line px-4 py-2 text-[12px]">
        <summary className="cursor-pointer text-muted hover:text-fg">What it will do</summary>
        <pre className="mt-2 overflow-x-auto rounded bg-side p-2">{JSON.stringify(c.input, null, 2)}</pre>
      </details>
    </li>
  )
}

export function ConfirmationList({ ws, agentsOnly }: { ws: string; agentsOnly?: boolean }) {
  const { data, error, reload } = useCmd<Confirmation[]>(ws, 'confirmation.list')
  if (error)
    return (
      <div className="space-y-2">
        <ErrorText error={error} />
        <button type="button" className={btn} onClick={reload}>
          Try again
        </button>
      </div>
    )
  if (!data) return <div aria-busy="true" className="h-24 animate-pulse rounded-lg bg-hover" />
  const rows = agentsOnly ? data.filter((c) => c.agent_name) : data
  if (rows.length === 0)
    return (
      <div className="rounded-lg border border-line">
        <EmptyState icon={ShieldCheck} title="No pending confirmations">
          When an agent asks to send, pay or delete something, the request waits here for you to approve or
          reject.
        </EmptyState>
      </div>
    )
  return (
    <ul className="space-y-3">
      {rows.map((c) => (
        <ConfirmationCard key={c.id} c={c} ws={ws} done={reload} />
      ))}
    </ul>
  )
}

export function ConfirmationsTab({ ws }: { ws: string }) {
  return <ConfirmationList ws={ws} />
}
