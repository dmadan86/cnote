'use client'
import { Phone } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { money } from '@/components/billing/fmt'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

export type FollowUp = {
  id: string
  title: string
  stage: string
  amount_minor: number
  contact_id: string | null
  contact_name: string
  contact_phone: string | null
  owner_id: string | null
  source: string | null
  next_follow_up: string | null
  next_step: string | null
  first_response_at: string | null
  created_at: string
  last_activity: string | null
}
export type Activity = {
  id: string
  note_id: string
  note_title: string
  kind: string
  summary: string
  happened_at: string
  created_by_name: string
}

export const KINDS: [string, string][] = [
  ['call', 'Call'],
  ['visit', 'Visit'],
  ['whatsapp', 'WhatsApp'],
  ['email', 'Email'],
  ['note', 'Note'],
]
export const kindLabel = (k: string) => KINDS.find(([v]) => v === k)?.[1] ?? k

/** Local calendar date n days from today as YYYY-MM-DD (the shop owner's own day). */
export function dayPlus(n: number) {
  const d = new Date()
  d.setDate(d.getDate() + n)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
export const niceDate = (iso: string) =>
  new Date(`${iso}T00:00:00`).toLocaleDateString('en-IN', { day: 'numeric', month: 'short' })

const big = 'min-h-11 px-4 text-[14px]'
const tel = (p: string) => p.replace(/[^\d+]/g, '')

/** Log what happened and, optionally, what comes next. The summary may stay empty for quick outcomes. */
export function LogActivity({
  noteId,
  title,
  kind = 'call',
  onClose,
  onSaved,
}: {
  noteId: string
  title: string
  kind?: string
  onClose: () => void
  onSaved: () => void
}) {
  const { ws } = useShell()
  const [k, setK] = useState(kind)
  const [date, setDate] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const chips: [string, number][] = [
    ['Tomorrow', 1],
    ['In 3 days', 3],
    ['Next week', 7],
  ]
  return (
    <Modal open title={`Log activity: ${title}`} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>
          setBusy(true)
          try {
            await cmd(ws, 'activity.log', {
              note_id: noteId,
              kind: k,
              summary: f.summary?.trim() || kindLabel(k),
              ...(date || f.next_step?.trim()
                ? { next_follow_up: date || undefined, next_step: f.next_step?.trim() || undefined }
                : {}),
            })
            onSaved()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <fieldset>
          <legend className="mb-1 text-[12px] font-medium text-muted">What happened</legend>
          <div className="flex flex-wrap gap-2">
            {KINDS.map(([v, l]) => (
              <label
                key={v}
                className="flex min-h-11 cursor-pointer items-center rounded-md border border-line px-3 text-[14px] has-[:checked]:border-accent has-[:checked]:bg-hover has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-accent"
              >
                <input
                  type="radio"
                  name="kind"
                  value={v}
                  checked={k === v}
                  onChange={() => setK(v)}
                  className="sr-only"
                />
                {l}
              </label>
            ))}
          </div>
        </fieldset>
        <Field label="Summary (optional)">
          {(id) => (
            <textarea
              id={id}
              name="summary"
              rows={2}
              maxLength={2000}
              placeholder="Wants a site visit on Friday"
              className={`${inputCls} text-[16px]`}
            />
          )}
        </Field>
        <Field label="Next step (optional)">
          {(id) => <input id={id} name="next_step" maxLength={300} className={`${inputCls} text-[16px]`} />}
        </Field>
        <Field label="Follow up on">
          {(id) => (
            <input
              id={id}
              type="date"
              value={date}
              min={dayPlus(0)}
              onChange={(e) => setDate(e.target.value)}
              className={`${inputCls} text-[16px]`}
            />
          )}
        </Field>
        <div className="flex flex-wrap gap-2">
          {chips.map(([l, n]) => (
            <button type="button" key={l} className={btn} onClick={() => setDate(dayPlus(n))}>
              {l} · {niceDate(dayPlus(n))}
            </button>
          ))}
        </div>
        {!date && <p className="text-[12px] text-muted">No date set: this deal keeps no reminder.</p>}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={`${btn} ${big}`} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={busy} className={`${btnPrimary} ${big}`}>
            Save
          </button>
        </div>
      </form>
    </Modal>
  )
}

export const LOST_REASONS = [
  'Price too high',
  'Chose a competitor',
  'No response',
  'Not a fit',
  'Just browsing',
  'Other',
]

/** Moves a deal to Lost with a reason. Cancelling changes nothing. */
export function LostSheet({
  dealId,
  title,
  onClose,
  onSaved,
}: {
  dealId: string
  title: string
  onClose: () => void
  onSaved: () => void
}) {
  const { ws } = useShell()
  const [reason, setReason] = useState('')
  const [other, setOther] = useState('')
  const [err, setErr] = useState<string>()
  const final = reason === 'Other' ? other.trim() : reason
  return (
    <Modal open title={`Why was "${title}" lost?`} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          try {
            await cmd(ws, 'deal.lose', { id: dealId, lost_reason: final })
            onSaved()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <fieldset className="space-y-1.5">
          <legend className="sr-only">Reason</legend>
          {LOST_REASONS.map((r) => (
            <label key={r} className="flex min-h-11 items-center gap-2 text-[14px]">
              <input
                type="radio"
                name="reason"
                value={r}
                checked={reason === r}
                onChange={() => setReason(r)}
              />
              {r}
            </label>
          ))}
        </fieldset>
        {reason === 'Other' && (
          <Field label="Reason">
            {(id) => (
              <input
                id={id}
                value={other}
                maxLength={200}
                onChange={(e) => setOther(e.target.value)}
                className={`${inputCls} text-[16px]`}
              />
            )}
          </Field>
        )}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={`${btn} ${big}`} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={!final} className={`${btnPrimary} ${big}`}>
            Mark as lost
          </button>
        </div>
      </form>
    </Modal>
  )
}

/** Newest first. `note_title` is shown when the list mixes a contact with its deals. */
export function ActivityList({ rows, showTitle }: { rows: Activity[]; showTitle?: boolean }) {
  if (!rows.length) return <p className="text-[13px] text-muted">No calls, visits or messages logged yet.</p>
  return (
    <ol className="space-y-2">
      {rows.map((a) => (
        <li key={a.id} className="text-[14px]">
          <div className="flex flex-wrap items-baseline justify-between gap-x-3">
            <span>
              <span className="mr-2 text-[12px] font-medium text-muted">{kindLabel(a.kind)}</span>
              {showTitle && <span className="mr-2 text-[12px] text-muted">{a.note_title}</span>}
            </span>
            <time dateTime={a.happened_at} className="text-[12px] text-muted">
              {ago(a.happened_at)}
            </time>
          </div>
          <p className="whitespace-pre-wrap">{a.summary}</p>
        </li>
      ))}
    </ol>
  )
}

function Contactable({ phone }: { phone: string | null }) {
  if (!phone) return null
  return (
    <span className="flex flex-wrap gap-x-3 text-[13px]">
      <a href={`tel:${tel(phone)}`} className="inline-flex min-h-8 items-center gap-1 underline">
        <Phone size={12} aria-hidden /> {phone}
      </a>
      <a
        href={`https://wa.me/${tel(phone).replace('+', '')}`}
        target="_blank"
        rel="noreferrer"
        className="inline-flex min-h-8 items-center underline"
      >
        WhatsApp
      </a>
    </span>
  )
}

export function Inbox() {
  const { ws } = useShell()
  const list = useCmd<FollowUp[]>(ws, 'followup.list', { due: 'inbox' })
  const [logging, setLogging] = useState<FollowUp>()
  return (
    <section aria-label="New inquiries">
      <p className="mb-3 text-[13px] text-muted">
        Inquiries nobody has answered yet. Logging a call, visit or message takes one off this list.
      </p>
      <ErrorText error={list.error} />
      {list.data?.length === 0 && <p className="text-muted">No unanswered inquiries.</p>}
      <ul className="space-y-2">
        {list.data?.map((d) => (
          <li key={d.id} className="rounded-lg border border-line p-3">
            <div className="flex flex-wrap items-baseline justify-between gap-x-3">
              <Link href={`/w/${ws}/crm/deals/${d.id}`} className="font-medium hover:underline">
                {d.contact_name || d.title}
              </Link>
              <span className="text-[12px] text-muted">
                {d.source && <Pill>{d.source}</Pill>} {ago(d.created_at)}
              </span>
            </div>
            {d.last_activity && <p className="mt-1 line-clamp-2 text-[14px]">{d.last_activity}</p>}
            <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
              <Contactable phone={d.contact_phone} />
              <button type="button" className={`${btnPrimary} ${big}`} onClick={() => setLogging(d)}>
                Log outcome
              </button>
            </div>
          </li>
        ))}
      </ul>
      {logging && (
        <LogActivity
          noteId={logging.id}
          title={logging.contact_name || logging.title}
          onClose={() => setLogging(undefined)}
          onSaved={() => {
            setLogging(undefined)
            list.reload()
          }}
        />
      )}
    </section>
  )
}

const TABS = ['today', 'overdue', 'week', 'nodate'] as const
const TAB_LABEL = { today: 'Today', overdue: 'Overdue', week: 'This week', nodate: 'No date' }

export function FollowUps() {
  const { ws, me } = useShell()
  const [mine, setMine] = useState(true)
  const [tab, setTab] = useState<(typeof TABS)[number]>('today')
  const [logging, setLogging] = useState<FollowUp>()
  const [msg, setMsg] = useState<{ text: string; undo?: () => void }>()
  const list = useCmd<FollowUp[]>(ws, 'followup.list', mine ? { owner_id: me.user.id } : {})
  const today = dayPlus(0)
  const bucket = (d: FollowUp) =>
    !d.next_follow_up
      ? 'nodate'
      : d.next_follow_up < today
        ? 'overdue'
        : d.next_follow_up === today
          ? 'today'
          : d.next_follow_up <= dayPlus(7)
            ? 'week'
            : 'later'
  const rows = list.data ?? []
  const shown = rows.filter((d) => bucket(d) === tab)
  const set = async (d: FollowUp, date: string | null) => {
    try {
      await cmd(ws, 'followup.set', { id: d.id, next_follow_up: date })
      setMsg({
        text: date ? `${d.title} moved to ${niceDate(date)}.` : 'Follow-up cleared.',
        undo: async () => {
          await cmd(ws, 'followup.set', { id: d.id, next_follow_up: d.next_follow_up })
          setMsg(undefined)
          list.reload()
        },
      })
    } catch (x) {
      setMsg({ text: (x as Error).message })
    }
    list.reload()
  }
  return (
    <section aria-label="Follow-ups">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div role="tablist" aria-label="When" className="flex gap-1">
          {TABS.map((t) => (
            <button
              key={t}
              type="button"
              role="tab"
              id={`tab-${t}`}
              aria-selected={tab === t}
              aria-controls="followup-panel"
              onClick={() => setTab(t)}
              className={`min-h-11 rounded-md px-3 text-[13px] ${tab === t ? 'bg-hover font-medium' : 'text-muted hover:text-fg'}`}
            >
              {TAB_LABEL[t]}{' '}
              <span className="tabular-nums" data-testid={`count-${t}`}>
                {rows.filter((d) => bucket(d) === t).length}
              </span>
            </button>
          ))}
        </div>
        <label className="ml-auto flex min-h-11 items-center gap-2 text-[13px]">
          <input type="checkbox" checked={mine} onChange={(e) => setMine(e.target.checked)} /> Only mine
        </label>
      </div>
      <p role="status" className="min-h-5 text-[13px] text-muted">
        {msg?.text}{' '}
        {msg?.undo && (
          <button type="button" className="underline" onClick={msg.undo}>
            Undo
          </button>
        )}
      </p>
      <ErrorText error={list.error} />
      <div id="followup-panel" role="tabpanel" aria-labelledby={`tab-${tab}`}>
        {shown.length === 0 && <p className="text-muted">Nothing here.</p>}
        <ul className="space-y-2">
          {shown.map((d) => (
            <li key={d.id} className="rounded-lg border border-line p-3">
              <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                <Link href={`/w/${ws}/crm/deals/${d.id}`} className="font-medium hover:underline">
                  {d.title}
                </Link>
                <span className="text-[13px] tabular-nums">
                  {d.next_follow_up ? niceDate(d.next_follow_up) : 'No date'}
                  {d.amount_minor > 0 && <span className="ml-2 text-muted">{money(d.amount_minor)}</span>}
                </span>
              </div>
              <p className="text-[13px] text-muted">
                {[d.contact_name, d.next_step].filter(Boolean).join(' · ') || 'No next step'}
              </p>
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <Contactable phone={d.contact_phone} />
                <span className="ml-auto flex gap-2">
                  <button type="button" className={`${btn} ${big}`} onClick={() => setLogging(d)}>
                    Log activity
                  </button>
                  <button type="button" className={`${btn} ${big}`} onClick={() => set(d, dayPlus(1))}>
                    Snooze to tomorrow
                    <span className="sr-only"> {d.title}</span>
                  </button>
                </span>
              </div>
            </li>
          ))}
        </ul>
      </div>
      {logging && (
        <LogActivity
          noteId={logging.id}
          title={logging.title}
          onClose={() => setLogging(undefined)}
          onSaved={() => {
            setLogging(undefined)
            list.reload()
          }}
        />
      )}
    </section>
  )
}

type FormCfg = { configured: boolean; enabled: boolean; owner_id?: string | null; path?: string }

export function InquiryFormSettings() {
  const { ws, me, spaces, members } = useShell()
  const cfg = useCmd<FormCfg>(ws, 'inquiry_form.get')
  const [err, setErr] = useState<string>()
  const [copied, setCopied] = useState(false)
  const admin = ['owner', 'admin'].includes(me.workspaces.find((w) => w.id === ws)?.role ?? '')
  if (!admin) return <p className="text-muted">Only admins manage the inquiry form.</p>
  const save = async (input: Record<string, unknown>) => {
    try {
      await cmd(ws, 'inquiry_form.set', input)
      setErr(undefined)
      cfg.reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const c = cfg.data
  const url = c?.path ? `${window.location.origin}${c.path}` : ''
  return (
    <section aria-label="Inquiry form" className="max-w-xl space-y-3">
      <p className="text-[13px] text-muted">
        Share this link on your website, Instagram or WhatsApp. Each submission becomes a lead in the Inbox.
      </p>
      <ErrorText error={err ?? cfg.error} />
      {c && !c.configured && (
        <button
          type="button"
          className={`${btnPrimary} ${big}`}
          onClick={() => save({ space_id: spaces[0].id, owner_id: me.user.id })}
        >
          Create inquiry form
        </button>
      )}
      {c?.configured && (
        <div className="space-y-3 rounded-lg border border-line p-4">
          <Field label="Public link">
            {(id) => (
              <div className="flex gap-2">
                <input id={id} readOnly value={url} className={`${inputCls} text-[16px]`} />
                <button
                  type="button"
                  className={btn}
                  onClick={() => {
                    navigator.clipboard?.writeText(url)
                    setCopied(true)
                  }}
                >
                  Copy
                </button>
              </div>
            )}
          </Field>
          <p role="status" className="text-[12px] text-muted">
            {copied ? 'Link copied.' : ''}
          </p>
          <Field label="New inquiries are assigned to">
            {(id) => (
              <select
                id={id}
                value={c.owner_id ?? ''}
                onChange={(e) => save({ owner_id: e.target.value || undefined })}
                className={`${inputCls} text-[16px]`}
              >
                {members
                  .filter((m) => m.role !== 'guest')
                  .map((m) => (
                    <option key={m.user_id} value={m.user_id}>
                      {m.name}
                    </option>
                  ))}
              </select>
            )}
          </Field>
          <label className="flex min-h-11 items-center gap-2 text-[14px]">
            <input
              type="checkbox"
              checked={c.enabled}
              onChange={(e) => save({ enabled: e.target.checked })}
            />
            Accepting inquiries
          </label>
          <button type="button" className={btn} onClick={() => save({ rotate: true })}>
            Make a new link (the old one stops working)
          </button>
        </div>
      )}
    </section>
  )
}
