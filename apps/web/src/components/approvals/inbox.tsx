'use client'
import Link from 'next/link'
import { useState } from 'react'
import { money } from '@/components/billing/fmt'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, inputCls, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { ACTION, type Approval, docPath } from './labels'

const TABS = ['Waiting for me', 'My requests', 'History'] as const
type Tab = (typeof TABS)[number]
const tone = {
  pending: 'warn',
  approved: 'ok',
  rejected: 'bad',
  expired: 'neutral',
  cancelled: 'neutral',
} as const
const word = {
  pending: 'Waiting',
  approved: 'Approved',
  rejected: 'Rejected',
  expired: 'Out of date',
  cancelled: 'Cancelled',
}

function Row({ a, ws, done }: { a: Approval; ws: string; done: () => void }) {
  const [rejecting, setRejecting] = useState(false)
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  const act = async (name: string, input: Record<string, unknown>) => {
    setBusy(true)
    try {
      await cmd(ws, name, input)
      done()
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }
  const title = a.note_title || 'Untitled'
  return (
    <li className="space-y-2 rounded-lg border border-line p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <Link href={docPath(ws, a)} className="block truncate text-[15px] font-semibold hover:underline">
            {title}
          </Link>
          <p className="text-[13px] text-muted">
            {ACTION[a.command] ?? a.command} · requested by {a.requester_name ?? 'someone'} ·{' '}
            <time dateTime={a.created_at} title={new Date(a.created_at).toLocaleString()}>
              {ago(a.created_at)}
            </time>
            {a.approver_name ? ` · approver ${a.approver_name}` : ''}
            {a.escalated ? ' · escalated' : ''}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {a.amount_minor !== null && (
            <span className="text-[15px] font-semibold">{money(a.amount_minor)}</span>
          )}
          <Pill tone={tone[a.status]}>{word[a.status]}</Pill>
        </div>
      </div>
      {a.reason && <p className="text-[13px]">Reason: {a.reason}</p>}
      {(a.can_decide || a.can_cancel) && !rejecting && (
        <div className="flex flex-wrap gap-2">
          {a.can_decide && (
            <>
              <button
                type="button"
                className={`${btnPrimary} min-h-9`}
                disabled={busy}
                onClick={() => act('approval.decide', { id: a.id, decision: 'approve' })}
              >
                Approve {ACTION[a.command]?.toLowerCase()}
              </button>
              <button
                type="button"
                className={`${btn} min-h-9`}
                disabled={busy}
                onClick={() => setRejecting(true)}
              >
                Reject
              </button>
            </>
          )}
          {a.can_cancel && (
            <button
              type="button"
              className={`${btn} min-h-9`}
              disabled={busy}
              onClick={() => act('approval.cancel', { id: a.id })}
            >
              Cancel request
            </button>
          )}
        </div>
      )}
      {rejecting && (
        <form
          className="space-y-2"
          onSubmit={(e) => {
            e.preventDefault()
            act('approval.decide', { id: a.id, decision: 'reject', reason })
          }}
        >
          <label className="block space-y-1">
            <span className="text-[12px] font-medium text-muted">
              Reason for rejecting (the requester sees it)
            </span>
            <input
              className={inputCls}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              // biome-ignore lint/a11y/noAutofocus: the field appears because the user asked to reject
              autoFocus
              required
            />
          </label>
          <div className="flex gap-2">
            <button type="submit" className={btnPrimary} disabled={busy || !reason.trim()}>
              Submit rejection
            </button>
            <button type="button" className={btn} onClick={() => setRejecting(false)}>
              Cancel
            </button>
          </div>
        </form>
      )}
      <ErrorText error={err} />
    </li>
  )
}

export function ApprovalInbox() {
  const { ws } = useShell()
  const [tab, setTab] = useState<Tab>('Waiting for me')
  const all = useCmd<Approval[]>(ws, 'approval.list', {})
  const mine = useCmd<Approval[]>(ws, 'approval.list', { mine: true })
  const reload = () => {
    all.reload()
    mine.reload()
  }
  const waiting = (all.data ?? []).filter((a) => a.status === 'pending' && a.can_decide)
  const rows: Approval[] =
    tab === 'Waiting for me'
      ? waiting
      : tab === 'My requests'
        ? (mine.data ?? [])
        : (all.data ?? []).filter((a) => a.status !== 'pending')
  const empty = {
    'Waiting for me': 'Nothing needs your approval.',
    'My requests': 'You have not asked for any approvals.',
    History: 'No decisions yet.',
  }[tab]
  const error = all.error ?? mine.error
  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <h1 className="text-lg font-semibold">Approvals</h1>
      <div role="tablist" aria-label="Approvals" className="flex gap-1 border-b border-line">
        {TABS.map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            onClick={() => setTab(t)}
            className="border-b-2 border-transparent px-3 py-2 text-[13px] font-medium text-muted hover:text-fg aria-selected:border-accent aria-selected:text-fg"
          >
            {t}
            {t === 'Waiting for me' && waiting.length > 0 ? ` (${waiting.length})` : ''}
          </button>
        ))}
      </div>
      <ErrorText error={error} />
      {!all.data && !error ? (
        <div aria-busy="true" className="h-24 animate-pulse rounded-lg bg-hover" />
      ) : rows.length === 0 ? (
        <p className="py-6 text-center text-[14px] text-muted">{empty}</p>
      ) : (
        <ul aria-label={tab} className="space-y-2">
          {rows.map((a) => (
            <Row key={a.id} a={a} ws={ws} done={reload} />
          ))}
        </ul>
      )}
    </div>
  )
}
