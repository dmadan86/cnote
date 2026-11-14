'use client'
import { AlertTriangle, Clock } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { SubNav } from '@/components/billing/bits'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

export type Ticket = {
  id: string
  title: string
  status: 'open' | 'pending' | 'resolved' | 'closed'
  priority: 'low' | 'normal' | 'high' | 'urgent'
  assignee_id: string | null
  assignee_name: string | null
  contact_id: string
  contact_name: string
  contact_phone: string | null
  invoice_id: string | null
  project_id: string | null
  description: string
  source: string
  sla_due_at: string
  resolved_at: string | null
  created_at: string
  breached: boolean
}

const PRIORITIES = ['low', 'normal', 'high', 'urgent'] as const
const STATUSES = ['open', 'pending', 'resolved', 'closed'] as const
const label = (s: string) => s[0].toUpperCase() + s.slice(1)
const big = 'min-h-11 px-4 text-[14px]'
const tones = { low: 'neutral', normal: 'neutral', high: 'warn', urgent: 'bad' } as const

/** SLA state in words and an icon, never colour alone. Resolved and closed tickets show no ticking clock. */
export function Sla({ t }: { t: Ticket }) {
  if (t.status === 'resolved' || t.status === 'closed')
    return <span className="text-[12px] text-muted">Done</span>
  const ms = new Date(t.sla_due_at).getTime() - Date.now()
  const h = Math.floor(Math.abs(ms) / 3600000)
  const m = Math.floor((Math.abs(ms) % 3600000) / 60000)
  const span = h >= 48 ? `${Math.floor(h / 24)}d` : h ? `${h}h ${m}m` : `${m}m`
  const Icon = t.breached ? AlertTriangle : Clock
  return (
    <span
      className={`inline-flex items-center gap-1 text-[12px] font-medium ${t.breached ? 'text-bad' : 'text-muted'}`}
      title={new Date(t.sla_due_at).toLocaleString('en-IN')}
    >
      <Icon size={12} aria-hidden /> {t.breached ? `Overdue by ${span}` : `Due in ${span}`}
    </span>
  )
}

export function TicketsNav() {
  const { ws } = useShell()
  return (
    <SubNav
      label="Ticket sections"
      items={[
        [`/w/${ws}/crm/tickets`, 'Tickets'],
        [`/w/${ws}/crm/tickets/settings`, 'SLA and form'],
      ]}
    />
  )
}

const chips: [string, string][] = [
  ['active', 'Active'],
  ['resolved', 'Resolved'],
  ['closed', 'Closed'],
  ['all', 'All'],
]

export function TicketList({ initialBreached = false }: { initialBreached?: boolean }) {
  const { ws } = useShell()
  const [status, setStatus] = useState('active')
  const [priority, setPriority] = useState('')
  const [mine, setMine] = useState(false)
  const [breached, setBreached] = useState(initialBreached)
  const [adding, setAdding] = useState(false)
  const input = {
    status,
    ...(priority ? { priority } : {}),
    ...(mine ? { assignee_id: 'me' } : {}),
    ...(breached ? { breached: true } : {}),
  }
  const list = useCmd<Ticket[]>(ws, 'ticket.list', input)
  const filtered = status !== 'active' || priority || mine || breached
  const clear = () => {
    setStatus('active')
    setPriority('')
    setMine(false)
    setBreached(false)
  }
  return (
    <section aria-label="Tickets" className="space-y-3">
      <TicketsNav />
      <div className="flex flex-wrap items-center gap-2">
        <fieldset aria-label="Status" className="flex flex-wrap gap-1 border-0 p-0">
          {chips.map(([v, l]) => (
            <button
              key={l}
              type="button"
              aria-pressed={status === v}
              onClick={() => setStatus(v)}
              className={`${btn} min-h-11 aria-pressed:border-accent aria-pressed:bg-hover`}
            >
              {l}
            </button>
          ))}
        </fieldset>
        <details className="relative">
          <summary className={`${btn} min-h-11 cursor-pointer list-none`}>
            Filters
            {priority || mine || breached ? ` (${[priority, mine, breached].filter(Boolean).length})` : ''}
          </summary>
          <div className="absolute z-10 mt-1 w-64 space-y-2 rounded-lg border border-line bg-canvas p-3 shadow">
            <Field label="Priority">
              {(id) => (
                <select
                  id={id}
                  value={priority}
                  onChange={(e) => setPriority(e.target.value)}
                  className={`${inputCls} min-h-11 text-[16px]`}
                >
                  <option value="">Any</option>
                  {PRIORITIES.map((p) => (
                    <option key={p} value={p}>
                      {label(p)}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <label className="flex min-h-11 items-center gap-2 text-[14px]">
              <input type="checkbox" checked={mine} onChange={(e) => setMine(e.target.checked)} /> Assigned to
              me
            </label>
            <label className="flex min-h-11 items-center gap-2 text-[14px]">
              <input type="checkbox" checked={breached} onChange={(e) => setBreached(e.target.checked)} />{' '}
              Breaching SLA
            </label>
            <button type="button" className={btn} onClick={clear}>
              Clear filters
            </button>
          </div>
        </details>
        <button type="button" className={`${btnPrimary} ${big} ml-auto`} onClick={() => setAdding(true)}>
          New ticket
        </button>
      </div>
      <p className="text-[12px] text-muted">
        Active means open or pending. Resolved tickets can be closed later; a customer reply is a comment and
        does not reopen a ticket by itself. The SLA clock is the time to resolve, in calendar hours.
      </p>
      <ErrorText error={list.error} />
      {list.data?.length === 0 &&
        (filtered ? (
          <p className="text-muted">
            No matching tickets.{' '}
            <button type="button" className="underline" onClick={clear}>
              Clear filters
            </button>
          </p>
        ) : (
          <p className="text-muted">
            No open tickets. Nice. New ones from your public form or the New ticket button show up here.
          </p>
        ))}
      <ul className="space-y-2">
        {list.data?.map((t) => (
          <li key={t.id} className="rounded-lg border border-line p-3">
            <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <Link href={`/w/${ws}/crm/tickets/${t.id}`} className="font-medium hover:underline">
                {t.title}
              </Link>
              <Sla t={t} />
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-muted">
              <span>{t.contact_name}</span>
              <Pill tone={tones[t.priority]}>{label(t.priority)}</Pill>
              <Pill>{label(t.status)}</Pill>
              <span>{t.assignee_name ? `Assigned to ${t.assignee_name}` : 'Unassigned'}</span>
              <span>{ago(t.created_at)}</span>
            </div>
          </li>
        ))}
      </ul>
      {adding && (
        <NewTicket
          onClose={() => setAdding(false)}
          onSaved={() => {
            setAdding(false)
            list.reload()
          }}
        />
      )}
    </section>
  )
}

function NewTicket({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const { ws, members } = useShell()
  const contacts = useCmd<{ id: string; name: string }[]>(ws, 'contact.list', { limit: 500 })
  const [err, setErr] = useState<string>()
  return (
    <Modal open title="New ticket" onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>
          try {
            await cmd(ws, 'ticket.create', {
              title: f.title,
              contact_id: f.contact_id,
              description: f.description,
              priority: f.priority,
              ...(f.assignee_id ? { assignee_id: f.assignee_id } : {}),
            })
            onSaved()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <Field label="Subject">
          {(id) => (
            <input
              id={id}
              name="title"
              required
              maxLength={200}
              className={`${inputCls} min-h-11 text-[16px]`}
            />
          )}
        </Field>
        <Field label="Customer">
          {(id) => (
            <select id={id} name="contact_id" required className={`${inputCls} min-h-11 text-[16px]`}>
              <option value="">Choose a customer</option>
              {contacts.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Details">
          {(id) => (
            <textarea
              id={id}
              name="description"
              rows={3}
              maxLength={4000}
              className={`${inputCls} text-[16px]`}
            />
          )}
        </Field>
        <Field label="Priority">
          {(id) => (
            <select
              id={id}
              name="priority"
              defaultValue="normal"
              className={`${inputCls} min-h-11 text-[16px]`}
            >
              {PRIORITIES.map((p) => (
                <option key={p} value={p}>
                  {label(p)}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Assignee">
          {(id) => (
            <select id={id} name="assignee_id" className={`${inputCls} min-h-11 text-[16px]`}>
              <option value="">Unassigned</option>
              {members.map((m) => (
                <option key={m.user_id} value={m.user_id}>
                  {m.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={`${btn} ${big}`} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={`${btnPrimary} ${big}`}>
            Create ticket
          </button>
        </div>
      </form>
    </Modal>
  )
}

type Comment = { id: string; author_name: string; body: string; created_at: string }

export function TicketDetail({ id }: { id: string }) {
  const { ws, members, spaces } = useShell()
  const tk = useCmd<Ticket>(ws, 'ticket.get', { id })
  const thread = useCmd<Comment[]>(ws, 'comment.list', { note_id: id })
  const [err, setErr] = useState<string>()
  const [reply, setReply] = useState('')
  const [saved, setSaved] = useState('')
  const t = tk.data
  const patch = async (p: Record<string, unknown>) => {
    try {
      await cmd(ws, 'ticket.update', { id, ...p })
      setErr(undefined)
      setSaved('Saved')
      tk.reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const sel = `${inputCls} min-h-11 text-[16px]`
  return (
    <section aria-label="Ticket" className="space-y-4">
      <Link href={`/w/${ws}/crm/tickets`} className="text-[13px] text-muted underline">
        All tickets
      </Link>
      <ErrorText error={err ?? tk.error} />
      {t && (
        <div className="grid gap-6 md:grid-cols-[1fr_16rem]">
          <div className="space-y-4">
            <div>
              <h2 className="text-lg font-semibold">{t.title}</h2>
              <div className="mt-1 flex flex-wrap items-center gap-2 text-[12px] text-muted">
                <Pill>{label(t.status)}</Pill>
                <Pill tone={tones[t.priority]}>{label(t.priority)}</Pill>
                <Sla t={t} />
                <span>
                  Raised {ago(t.created_at)}
                  {t.source === 'form' ? ' through the public form' : ''}
                </span>
              </div>
            </div>
            {t.description ? (
              <p className="whitespace-pre-wrap text-[14px]">{t.description}</p>
            ) : (
              <p className="text-muted">No details given.</p>
            )}
            <div>
              <h3 className="mb-2 text-[13px] font-medium">Replies</h3>
              {thread.data?.length === 0 && <p className="text-[13px] text-muted">No replies yet.</p>}
              <ol className="space-y-3">
                {thread.data?.map((c) => (
                  <li key={c.id} className="rounded-lg border border-line p-3 text-[14px]">
                    <div className="flex justify-between text-[12px] text-muted">
                      <span className="font-medium text-fg">{c.author_name}</span>
                      <time dateTime={c.created_at}>{ago(c.created_at)}</time>
                    </div>
                    <p className="whitespace-pre-wrap">{c.body}</p>
                  </li>
                ))}
              </ol>
              <form
                className="mt-3 space-y-2"
                onSubmit={async (e) => {
                  e.preventDefault()
                  try {
                    await cmd(ws, 'comment.add', { note_id: id, body: reply })
                    setReply('') // the draft stays when the call fails
                    setErr(undefined)
                    thread.reload()
                  } catch (x) {
                    setErr((x as Error).message)
                  }
                }}
              >
                <Field label="Reply">
                  {(fid) => (
                    <textarea
                      id={fid}
                      rows={3}
                      value={reply}
                      onChange={(e) => setReply(e.target.value)}
                      className={`${inputCls} text-[16px]`}
                    />
                  )}
                </Field>
                <button type="submit" disabled={!reply.trim()} className={`${btnPrimary} ${big}`}>
                  Send reply
                </button>
              </form>
            </div>
          </div>
          <aside
            aria-label="Ticket details"
            className="space-y-3 self-start rounded-lg border border-line p-3"
          >
            <Field label="Status">
              {(fid) => (
                <select
                  id={fid}
                  value={t.status}
                  onChange={(e) => patch({ status: e.target.value })}
                  className={sel}
                >
                  {STATUSES.map((s) => (
                    <option key={s} value={s}>
                      {label(s)}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Priority">
              {(fid) => (
                <select
                  id={fid}
                  value={t.priority}
                  onChange={(e) => patch({ priority: e.target.value })}
                  className={sel}
                >
                  {PRIORITIES.map((s) => (
                    <option key={s} value={s}>
                      {label(s)}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Assignee">
              {(fid) => (
                <select
                  id={fid}
                  value={t.assignee_id ?? ''}
                  onChange={(e) => patch({ assignee_id: e.target.value || null })}
                  className={sel}
                >
                  <option value="">Unassigned</option>
                  {members.map((m) => (
                    <option key={m.user_id} value={m.user_id}>
                      {m.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Project">
              {(fid) => (
                <select
                  id={fid}
                  value={t.project_id ?? ''}
                  onChange={(e) => patch({ project_id: e.target.value || null })}
                  className={sel}
                >
                  <option value="">None</option>
                  {spaces.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <dl className="space-y-1 text-[13px]">
              <dt className="text-[12px] text-muted">Customer</dt>
              <dd>
                <Link href={`/w/${ws}/crm/contacts/${t.contact_id}`} className="underline">
                  {t.contact_name}
                </Link>
                {t.contact_phone && <span className="block text-muted">{t.contact_phone}</span>}
              </dd>
              {t.invoice_id && (
                <>
                  <dt className="text-[12px] text-muted">Invoice</dt>
                  <dd>
                    <Link href={`/w/${ws}/billing/${t.invoice_id}`} className="underline">
                      Open invoice
                    </Link>
                  </dd>
                </>
              )}
              <dt className="text-[12px] text-muted">SLA due</dt>
              <dd>{new Date(t.sla_due_at).toLocaleString('en-IN')}</dd>
            </dl>
            <p role="status" className="sr-only">
              {saved}
            </p>
          </aside>
        </div>
      )}
    </section>
  )
}

type FormCfg = { configured: boolean; enabled?: boolean; path?: string }

export function TicketSettings() {
  const { ws, me } = useShell()
  const admin = ['owner', 'admin'].includes(me.workspaces.find((w) => w.id === ws)?.role ?? '')
  const sla = useCmd<{ hours: Record<string, number> }>(ws, 'ticket.sla_get')
  const cfg = useCmd<FormCfg>(ws, 'inquiry_form.get', {}, admin)
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState('')
  return (
    <section aria-label="Ticket settings" className="max-w-xl space-y-4">
      <TicketsNav />
      <ErrorText error={err ?? sla.error} />
      {!admin && <p className="text-muted">Only admins change SLA hours and the public form.</p>}
      {admin && sla.data && (
        <form
          className="space-y-3 rounded-lg border border-line p-4"
          onSubmit={async (e) => {
            e.preventDefault()
            const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>
            try {
              await cmd(ws, 'ticket.sla_set', {
                hours: Object.fromEntries(Object.entries(f).map(([k, v]) => [k, Number(v)])),
              })
              setErr(undefined)
              setMsg(
                'Saved. New tickets and priority changes use these hours; existing tickets keep their due time.',
              )
            } catch (x) {
              setErr((x as Error).message)
            }
          }}
        >
          <p className="text-[13px] text-muted">
            Hours allowed to resolve a ticket (calendar hours, not business hours).
          </p>
          {PRIORITIES.map((p) => (
            <Field key={p} label={`${label(p)} priority (hours)`}>
              {(id) => (
                <input
                  id={id}
                  name={p}
                  type="number"
                  min={1}
                  max={8760}
                  defaultValue={sla.data?.hours[p]}
                  className={`${inputCls} min-h-11 text-[16px]`}
                />
              )}
            </Field>
          ))}
          <button type="submit" className={`${btnPrimary} ${big}`}>
            Save hours
          </button>
          <p role="status" className="text-[13px]">
            {msg}
          </p>
        </form>
      )}
      {admin && cfg.data && (
        <div className="space-y-2 rounded-lg border border-line p-4">
          {cfg.data.configured && cfg.data.enabled ? (
            <Field label="Public ticket link">
              {(id) => (
                <input
                  id={id}
                  readOnly
                  value={`${window.location.origin}${cfg.data?.path?.replace('/f/', '/t/')}`}
                  className={`${inputCls} text-[16px]`}
                />
              )}
            </Field>
          ) : (
            <p className="text-[13px] text-muted">
              The public ticket form shares the inquiry form's link and on/off switch. Turn the inquiry form
              on under{' '}
              <Link href={`/w/${ws}/crm/form`} className="underline">
                Inquiry form
              </Link>{' '}
              first.
            </p>
          )}
        </div>
      )}
    </section>
  )
}
