'use client'
import {
  Bot,
  CalendarClock,
  Headset,
  Inbox,
  IndianRupee,
  type LucideIcon,
  MessageCircleQuestion,
} from 'lucide-react'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, EmptyState, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import type { Agent, Run, Template } from '@/lib/agents'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { AgentDetail, triggerText } from './detail'
import { StatusPill } from './runs'

type Meta = { icon: LucideIcon; job: string; can: string }
const META: Record<string, Meta> = {
  ask: {
    icon: MessageCircleQuestion,
    job: 'Answers questions about your business.',
    can: 'Reads reports, never sends.',
  },
  briefing: {
    icon: CalendarClock,
    job: 'Posts a short daily briefing to a channel.',
    can: 'Reads reports, posts one message to your team channel.',
  },
  lead_responder: {
    icon: Inbox,
    job: 'Drafts a reply and a follow-up for every new inquiry.',
    can: 'Logs notes and sets follow-ups. Replies wait for your approval.',
  },
  support: {
    icon: Headset,
    job: 'Drafts replies to support tickets.',
    can: 'Reads tickets and pages. Replies wait for your approval.',
  },
  collections: {
    icon: IndianRupee,
    job: 'Sends you a weekly collections brief and drafts follow-ups for long-overdue invoices.',
    can: 'Reads receivables. Follow-up emails wait for your approval.',
  },
}
// a template the web does not know yet still renders, using the server's own name and description
const metaOf = (t: Pick<Template, 'key' | 'description'>): Meta =>
  META[t.key] ?? { icon: Bot, job: t.description ?? '', can: '' }

type Listed = Agent & { last_run?: Pick<Run, 'id' | 'status' | 'created_at'> | null; pending?: number }
type Chan = { id: string; kind: string; name: string }

function InstallDialog({
  ws,
  t,
  onClose,
  done,
}: {
  ws: string
  t: Template
  onClose: () => void
  done: () => void
}) {
  const { data: chans } = useCmd<Chan[]>(ws, 'chat.channel_list', {}, t.key === 'briefing')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  return (
    <Modal open onClose={onClose} title={`Add: ${t.name}`}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          setBusy(true)
          try {
            await cmd(ws, 'agent.install', {
              template: t.key,
              ...(t.key === 'briefing' && { at: f.get('at'), channel_id: f.get('channel') || undefined }),
              ...(t.key === 'lead_responder' && { quotes: f.get('quotes') === 'on' }),
            })
            done()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <p className="text-[13px] text-muted">
          {metaOf(t).job} {metaOf(t).can}
        </p>
        <p className="text-[12px] text-muted">
          It sees only what you can see, and what it reads is sent to your AI provider to write the answer.
          Nothing leaves C Note without someone approving it.
        </p>
        {t.needs.groups_off.length > 0 && (
          <p className="text-[12px] text-muted">This turns on: {t.needs.groups_off.join(', ')}.</p>
        )}
        {t.key === 'briefing' && (
          <>
            <Field label="Send at">
              {(id) => (
                <input id={id} name="at" type="time" defaultValue="08:30" required className={inputCls} />
              )}
            </Field>
            <Field label="Channel">
              {(id) => (
                <select id={id} name="channel" className={inputCls}>
                  <option value="">A new channel</option>
                  {chans
                    ?.filter((c) => c.kind !== 'dm')
                    .map((c) => (
                      <option key={c.id} value={c.id}>
                        #{c.name}
                      </option>
                    ))}
                </select>
              )}
            </Field>
          </>
        )}
        {t.key === 'collections' && (
          <p className="text-[13px]">
            Sent to you as a direct message. Receivables are never posted in a channel.
          </p>
        )}
        {t.key === 'lead_responder' && (
          <label className="flex items-center gap-2 text-[13px]">
            <input type="checkbox" name="quotes" /> Also draft quotes
          </label>
        )}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy}>
            Add employee
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function AgentsTab({ ws, outside }: { ws: string; outside: React.ReactNode }) {
  const { me } = useShell()
  const sp = useSearchParams()
  const role = me.workspaces.find((w) => w.id === ws)?.role
  const admin = role === 'owner' || role === 'admin'
  const tpl = useCmd<Template[]>(ws, 'agent.templates')
  const list = useCmd<Listed[]>(ws, 'agent.list')
  const [install, setInstall] = useState<Template>()
  const [err, setErr] = useState<string>()
  const open = sp.get('agent')
  const base = `/w/${ws}/settings?tab=Agents`
  if (open) return <AgentDetail ws={ws} id={open} admin={admin} back={base} />
  const byId = new Map(list.data?.map((a) => [a.id, a]))
  const reload = () => {
    tpl.reload()
    list.reload()
  }
  return (
    <div className="space-y-8">
      <ErrorText error={tpl.error ?? list.error ?? err} />
      <section className="space-y-3">
        <h3 className="text-[14px] font-semibold">AI employees</h3>
        {!admin && <p className="text-[12px] text-muted">Only admins can add or change AI employees.</p>}
        {tpl.loading && <p className="text-[13px] text-muted">Loading…</p>}
        {tpl.data?.length === 0 && <EmptyState icon={Bot} title="No AI employees available" />}
        <ul className="grid gap-3 sm:grid-cols-2">
          {tpl.data?.map((t) => {
            const m = metaOf(t)
            const a = t.agent_id ? byId.get(t.agent_id) : undefined
            return (
              <li key={t.key} className="flex flex-col gap-2 rounded-md border border-line p-4">
                <div className="flex items-center gap-2">
                  <span className="flex h-8 w-8 items-center justify-center rounded-md border border-line text-muted">
                    <m.icon size={16} aria-hidden />
                  </span>
                  <span className="font-medium">{t.name}</span>
                </div>
                <p className="text-[13px]">{m.job}</p>
                <p className="text-[12px] text-muted">{m.can}</p>
                <div className="mt-auto flex flex-wrap items-center gap-2 pt-1">
                  {t.agent_id ? (
                    <>
                      <label className="flex items-center gap-2 text-[12px] text-muted">
                        <input
                          type="checkbox"
                          role="switch"
                          aria-checked={a?.enabled ?? false}
                          aria-label={`${t.name} enabled`}
                          checked={a?.enabled ?? false}
                          disabled={!admin || !a}
                          className="relative h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full bg-line transition-colors checked:bg-accent disabled:opacity-50 after:absolute after:top-0.5 after:left-0.5 after:h-4 after:w-4 after:rounded-full after:bg-canvas after:shadow-sm after:transition-transform checked:after:translate-x-4"
                          onChange={async (e) => {
                            try {
                              await cmd(ws, 'agent.update', { id: t.agent_id, enabled: e.target.checked })
                              setErr(undefined)
                            } catch (x) {
                              setErr((x as Error).message)
                            }
                            reload()
                          }}
                        />
                        {a?.enabled ? 'On' : 'Off'}
                      </label>
                      {a?.last_run && (
                        <span className="flex items-center gap-1 text-[12px] text-muted">
                          <StatusPill status={a.last_run.status} /> {ago(a.last_run.created_at)}
                        </span>
                      )}
                      {!!a?.pending && <Pill tone="warn">{a.pending} waiting</Pill>}
                      <Link href={`${base}&agent=${t.agent_id}`} className={`${btn} ml-auto`}>
                        Open
                      </Link>
                    </>
                  ) : (
                    admin && (
                      <button type="button" className={btnPrimary} onClick={() => setInstall(t)}>
                        Install
                      </button>
                    )
                  )}
                </div>
                {a && <p className="text-[12px] text-muted">{triggerText(a)}</p>}
              </li>
            )
          })}
        </ul>
      </section>
      <section className="space-y-3">
        <h3 className="text-[14px] font-semibold">Connect an outside agent (API key)</h3>
        <p className="text-[12px] text-muted">
          For other software that uses C Note with its own key. These do not run inside C Note.
        </p>
        {outside}
      </section>
      {install && (
        <InstallDialog
          ws={ws}
          t={install}
          onClose={() => setInstall(undefined)}
          done={() => {
            setInstall(undefined)
            reload()
          }}
        />
      )}
    </div>
  )
}
