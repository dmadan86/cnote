'use client'
import { Check, Search } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { Secret } from '@/components/secret'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { moduleOn } from '@/lib/modules'
import { GROUPS } from '@/lib/tool-groups'

type Listed = {
  id: string
  slug: string
  name: string
  tagline: string
  category: string
  icon_url: string | null
  publisher_name: string
  scopes: string[]
  installed: boolean
  installs: number
}
type Detail = Listed & {
  description: string
  homepage_url: string | null
  status: string
  blocked_scopes: string[]
}
type DevApp = {
  id: string
  slug: string
  name: string
  tagline: string
  description: string
  category: string
  icon_url: string | null
  homepage_url: string | null
  redirect_url: string
  status: string
  review_note: string | null
  scopes: string[]
  installs: number
}
type Queued = DevApp & { publisher_name: string }

const OFFICIAL = [
  { key: 'docs', name: 'Docs', tagline: 'Living docs and wikis for your team' },
  { key: 'projects', name: 'Projects', tagline: 'Work items, boards and sprints' },
  { key: 'crm', name: 'CRM', tagline: 'Contacts, companies and deals' },
  { key: 'billing', name: 'Billing', tagline: 'Invoices and payments' },
  { key: 'whiteboard', name: 'Whiteboard', tagline: 'Canvases for visual thinking' },
]
type Official = (typeof OFFICIAL)[number]
const CATEGORIES = ['productivity', 'sales', 'finance', 'developer', 'communication', 'other']
const SCOPES = GROUPS.filter(([g]) => g !== 'admin' && g !== 'destructive')
const STATUS_TONE = { draft: 'neutral', review: 'warn', published: 'ok', rejected: 'bad' } as const

/** Third-party icons render only over https, with no referrer. */
function Icon({ url, name }: { url: string | null; name: string }) {
  return url && /^https:\/\//i.test(url) ? (
    // biome-ignore lint/performance/noImgElement: third-party icon, not optimizable
    <img src={url} alt="" referrerPolicy="no-referrer" className="h-9 w-9 shrink-0 rounded-md" />
  ) : (
    <span
      aria-hidden
      className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-hover font-semibold text-muted"
    >
      {name[0]?.toUpperCase()}
    </span>
  )
}

function Card({
  name,
  tagline,
  icon,
  installed,
  official,
  onClick,
}: {
  name: string
  tagline: string
  icon: React.ReactNode
  installed: boolean
  official?: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      aria-label={name}
      onClick={onClick}
      className="flex min-w-0 items-start gap-3 rounded-lg border border-line p-3 text-left hover:bg-hover"
    >
      {icon}
      <span className="min-w-0 flex-1">
        <span className="flex flex-wrap items-center gap-1.5">
          <span className="truncate font-medium">{name}</span>
          {official && <Pill>Official</Pill>}
          {installed && (
            <Pill tone="ok">
              <Check size={11} className="mr-0.5 inline" aria-hidden />
              Installed
            </Pill>
          )}
        </span>
        <span className="line-clamp-2 text-[13px] text-muted">{tagline}</span>
      </span>
    </button>
  )
}

function Perms({ scopes }: { scopes: string[] }) {
  return (
    <div>
      <p className="mb-1 text-[13px] font-medium">This app will be able to:</p>
      <ul className="list-disc space-y-0.5 pl-5 text-[13px]">
        {scopes.map((s) => (
          <li key={s}>{GROUPS.find(([g]) => g === s)?.[1] ?? s}</li>
        ))}
      </ul>
    </div>
  )
}

function Browse({ ws, isAdmin }: { ws: string; isAdmin: boolean }) {
  const shell = useShell()
  const { data: apps, reload } = useCmd<Listed[]>(ws, 'market.list')
  const [q, setQ] = useState('')
  const [only, setOnly] = useState(false)
  const [official, setOfficial] = useState<Official>()
  const [slug, setSlug] = useState<{ slug: string; name: string }>()
  const [err, setErr] = useState<string>()
  const { data: det, reload: reloadDet } = useCmd<Detail>(ws, 'market.get', { slug: slug?.slug }, !!slug)

  const needle = q.trim().toLowerCase()
  const hit = (n: string, t: string) => !needle || `${n} ${t}`.toLowerCase().includes(needle)
  const offs = OFFICIAL.filter((o) => hit(o.name, o.tagline) && (!only || moduleOn(shell.modules, o.key)))
  const list = (apps ?? []).filter((a) => hit(a.name, a.tagline) && (!only || a.installed))

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn()
      setErr(undefined)
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const toggleOfficial = (o: Official) =>
    run(async () => {
      const w = await cmd<{ settings: { modules?: string[] } }>(ws, 'workspace.get')
      const cur = w.settings.modules ?? OFFICIAL.map((x) => x.key)
      const modules = cur.includes(o.key) ? cur.filter((k) => k !== o.key) : [...cur, o.key]
      await cmd(ws, 'workspace.update', { settings: { ...w.settings, modules } })
      shell.reload()
    })
  const close = () => {
    setOfficial(undefined)
    setSlug(undefined)
    setErr(undefined)
  }
  const d = det && det.slug === slug?.slug ? det : undefined

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 flex-1 basis-56">
          <Search size={14} className="absolute top-2.5 left-2.5 text-muted" aria-hidden />
          <input
            type="search"
            aria-label="Search apps"
            placeholder="Search apps"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            className={`${inputCls} pl-8`}
          />
        </div>
        {[false, true].map((v) => (
          <button
            key={String(v)}
            type="button"
            aria-pressed={only === v}
            onClick={() => setOnly(v)}
            className={`${btn} aria-pressed:bg-hover aria-pressed:font-semibold`}
          >
            {v ? 'Installed' : 'All'}
          </button>
        ))}
      </div>

      <section aria-labelledby="mk-official">
        <h2 id="mk-official" className="mb-2 text-[13px] font-semibold text-muted">
          C Note apps
        </h2>
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {offs.map((o) => (
            <Card
              key={o.key}
              name={o.name}
              tagline={o.tagline}
              icon={<Icon url={null} name={o.name} />}
              installed={moduleOn(shell.modules, o.key)}
              official
              onClick={() => setOfficial(o)}
            />
          ))}
        </div>
      </section>

      <section aria-labelledby="mk-apps">
        <h2 id="mk-apps" className="mb-2 text-[13px] font-semibold text-muted">
          Apps
        </h2>
        {list.length === 0 && <p className="text-[13px] text-muted">No apps found.</p>}
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((a) => (
            <Card
              key={a.id}
              name={a.name}
              tagline={a.tagline}
              icon={<Icon url={a.icon_url} name={a.name} />}
              installed={a.installed}
              onClick={() => setSlug({ slug: a.slug, name: a.name })}
            />
          ))}
        </div>
      </section>

      <Modal open={!!official} onClose={close} title={official?.name ?? ''}>
        {official && (
          <div className="space-y-3">
            <p className="text-[13px]">{official.tagline}</p>
            <p className="text-[13px] text-muted">Publisher: C Note</p>
            {isAdmin ? (
              <button
                type="button"
                className={moduleOn(shell.modules, official.key) ? btn : btnPrimary}
                onClick={() => toggleOfficial(official)}
              >
                {moduleOn(shell.modules, official.key) ? 'Uninstall' : 'Install'}
              </button>
            ) : (
              <p className="text-[13px] text-muted">Ask an admin to install</p>
            )}
            <ErrorText error={err} />
          </div>
        )}
      </Modal>

      <Modal open={!!slug} onClose={close} title={slug?.name ?? ''}>
        {!d ? (
          <p className="text-[13px] text-muted">Loading...</p>
        ) : (
          <div className="space-y-3">
            <div className="flex items-center gap-3">
              <Icon url={d.icon_url} name={d.name} />
              <p className="text-[13px] text-muted">
                {d.tagline}
                <br />
                Publisher: {d.publisher_name}
              </p>
            </div>
            <p className="whitespace-pre-wrap text-[13px]">{d.description}</p>
            {d.homepage_url && /^https:\/\//i.test(d.homepage_url) && (
              <a
                href={d.homepage_url}
                target="_blank"
                rel="noopener noreferrer"
                className="block text-[13px] underline"
              >
                Homepage
              </a>
            )}
            <Perms scopes={d.scopes} />
            {d.blocked_scopes.length > 0 && (
              <p role="alert" className="rounded-md bg-warn-bg p-2 text-[13px] text-warn">
                Disabled in this workspace: {d.blocked_scopes.join(', ')}. Enable them in{' '}
                <Link href={`/w/${ws}/settings`} className="underline">
                  Settings → Tool groups
                </Link>{' '}
                or the app will not work fully.
              </p>
            )}
            {!isAdmin ? (
              <p className="text-[13px] text-muted">Ask an admin to install</p>
            ) : d.installed ? (
              <button
                type="button"
                className={btn}
                onClick={() =>
                  run(async () => {
                    await cmd(ws, 'market.uninstall', { app_id: d.id })
                    reload()
                    reloadDet()
                  })
                }
              >
                Uninstall
              </button>
            ) : (
              <button
                type="button"
                className={btnPrimary}
                onClick={() =>
                  run(async () => {
                    const r = await cmd<{ redirect_to: string }>(ws, 'market.install', { app_id: d.id })
                    window.location.assign(r.redirect_to)
                  })
                }
              >
                Install
              </button>
            )}
            <ErrorText error={err} />
          </div>
        )}
      </Modal>
    </div>
  )
}

function AppForm({
  ws,
  app,
  onClose,
  onSaved,
}: {
  ws: string
  app?: DevApp
  onClose: () => void
  onSaved: (r: { app_id: string; client_secret?: string }) => void
}) {
  const [err, setErr] = useState<string>()
  const t = (name: string, label: string, v?: string | null, extra: object = {}) => (
    <Field label={label}>
      {(id) => <input id={id} name={name} defaultValue={v ?? ''} className={inputCls} {...extra} />}
    </Field>
  )
  return (
    <form
      className="space-y-3"
      onSubmit={async (e) => {
        e.preventDefault()
        const f = new FormData(e.currentTarget)
        const s = (k: string) => String(f.get(k) ?? '').trim()
        try {
          onSaved(
            await cmd(ws, 'dev.app_save', {
              id: app?.id,
              slug: s('slug'),
              name: s('name'),
              tagline: s('tagline'),
              description: s('description'),
              category: s('category'),
              icon_url: s('icon_url') || null,
              homepage_url: s('homepage_url') || null,
              redirect_url: s('redirect_url'),
              scopes: ['read', ...f.getAll('scope').map(String)],
            }),
          )
        } catch (x) {
          setErr((x as Error).message)
        }
      }}
    >
      {t('name', 'App name', app?.name, { required: true, maxLength: 60 })}
      {t('slug', 'Slug', app?.slug, { required: true, pattern: '[a-z0-9][a-z0-9\\-]{2,39}' })}
      {t('tagline', 'Tagline', app?.tagline, { maxLength: 120 })}
      <Field label="Description">
        {(id) => (
          <textarea
            id={id}
            name="description"
            defaultValue={app?.description}
            rows={3}
            maxLength={4000}
            className={inputCls}
          />
        )}
      </Field>
      <Field label="Category">
        {(id) => (
          <select id={id} name="category" defaultValue={app?.category ?? 'other'} className={inputCls}>
            {CATEGORIES.map((c) => (
              <option key={c}>{c}</option>
            ))}
          </select>
        )}
      </Field>
      {t('icon_url', 'Icon URL', app?.icon_url, { type: 'url', placeholder: 'https://...' })}
      {t('homepage_url', 'Homepage URL', app?.homepage_url, { type: 'url', placeholder: 'https://...' })}
      {t('redirect_url', 'Redirect URL', app?.redirect_url, { type: 'url', required: true })}
      <fieldset className="space-y-1">
        <legend className="text-[12px] font-medium text-muted">Scopes</legend>
        {SCOPES.map(([g, desc]) => (
          <label key={g} className="flex items-start gap-2 text-[13px]">
            <input
              type="checkbox"
              name="scope"
              value={g}
              className="mt-1"
              defaultChecked={g === 'read' || app?.scopes.includes(g)}
              disabled={g === 'read'}
            />
            <span>
              {g}
              <span className="block text-[12px] text-muted">{desc}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <ErrorText error={err} />
      <div className="flex justify-end gap-2">
        <button type="button" className={btn} onClick={onClose}>
          Cancel
        </button>
        <button type="submit" className={btnPrimary}>
          {app ? 'Save app' : 'Create app'}
        </button>
      </div>
    </form>
  )
}

function Build({ ws }: { ws: string }) {
  const { data, reload } = useCmd<DevApp[]>(ws, 'dev.apps')
  const [form, setForm] = useState<{ app?: DevApp }>()
  const [shown, setShown] = useState<{ id: string; secret: string }>()
  const [err, setErr] = useState<string>()
  const act = async (fn: () => Promise<unknown>) => {
    try {
      await fn()
      setErr(undefined)
      reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <p className="text-[13px] text-muted">Apps your workspace publishes to the marketplace.</p>
        <button type="button" className={btnPrimary} onClick={() => setForm({})}>
          New app
        </button>
      </div>
      {shown && (
        <div className="space-y-2">
          <p className="text-[13px]">
            Client ID: <code data-testid="client-id">{shown.id}</code>
          </p>
          <Secret value={shown.secret} onDone={() => setShown(undefined)} />
        </div>
      )}
      <ErrorText error={err} />
      {data?.length === 0 && <p className="text-muted">No apps yet.</p>}
      <ul className="divide-y divide-line rounded-md border border-line">
        {data?.map((a) => (
          <li key={a.id} className="space-y-1 px-3 py-2">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{a.name}</span>
              <Pill tone={STATUS_TONE[a.status as keyof typeof STATUS_TONE]}>{a.status}</Pill>
              <span className="text-[12px] text-muted">{a.installs} installs</span>
              <div className="ml-auto flex flex-wrap gap-2">
                {(a.status === 'draft' || a.status === 'rejected') && (
                  <button
                    type="button"
                    className={btnPrimary}
                    onClick={() => act(() => cmd(ws, 'dev.app_submit', { id: a.id }))}
                  >
                    Submit for review
                  </button>
                )}
                <button
                  type="button"
                  className={btn}
                  aria-label={`Edit ${a.name}`}
                  onClick={() => setForm({ app: a })}
                >
                  Edit
                </button>
                <button
                  type="button"
                  className={btn}
                  aria-label={`Rotate secret for ${a.name}`}
                  onClick={() =>
                    act(async () => {
                      const r = await cmd<{ client_secret: string }>(ws, 'dev.app_rotate_secret', {
                        id: a.id,
                      })
                      setShown({ id: a.id, secret: r.client_secret })
                    })
                  }
                >
                  Rotate secret
                </button>
                {a.installs === 0 && (
                  <button
                    type="button"
                    className={btn}
                    aria-label={`Delete ${a.name}`}
                    onClick={() => act(() => cmd(ws, 'dev.app_delete', { id: a.id }))}
                  >
                    Delete
                  </button>
                )}
              </div>
            </div>
            {a.status === 'rejected' && a.review_note && (
              <p className="text-[13px] text-bad">Rejected: {a.review_note}</p>
            )}
          </li>
        ))}
      </ul>
      <Modal open={!!form} onClose={() => setForm(undefined)} title={form?.app ? 'Edit app' : 'New app'}>
        {form && (
          <AppForm
            ws={ws}
            app={form.app}
            onClose={() => setForm(undefined)}
            onSaved={(r) => {
              setForm(undefined)
              if (r.client_secret) setShown({ id: r.app_id, secret: r.client_secret })
              reload()
            }}
          />
        )}
      </Modal>
    </div>
  )
}

function ReviewQueue({ ws, queue, reload }: { ws: string; queue: Queued[]; reload: () => void }) {
  const [err, setErr] = useState<string>()
  const [notes, setNotes] = useState<Record<string, string>>({})
  if (queue.length === 0) return <p className="text-muted">Nothing waiting for review.</p>
  return (
    <div className="space-y-3">
      <ErrorText error={err} />
      <ul className="space-y-2">
        {queue.map((a) => (
          <li key={a.id} className="space-y-2 rounded-md border border-line p-3">
            <div>
              <span className="font-medium">{a.name}</span>{' '}
              <span className="text-[12px] text-muted">by {a.publisher_name}</span>
              <p className="text-[13px]">{a.tagline}</p>
              <p className="text-[12px] text-muted">Scopes: {a.scopes.join(', ')}</p>
              <p className="break-all text-[12px] text-muted">Redirect: {a.redirect_url}</p>
            </div>
            <input
              aria-label={`Review note for ${a.name}`}
              placeholder="Note (optional)"
              value={notes[a.id] ?? ''}
              onChange={(e) => setNotes({ ...notes, [a.id]: e.target.value })}
              className={inputCls}
            />
            <div className="flex gap-2">
              {[true, false].map((approve) => (
                <button
                  key={String(approve)}
                  type="button"
                  className={approve ? btnPrimary : btn}
                  aria-label={`${approve ? 'Approve' : 'Reject'} ${a.name}`}
                  onClick={async () => {
                    try {
                      await cmd(ws, 'market.review', {
                        id: a.id,
                        approve,
                        note: notes[a.id]?.trim() || undefined,
                      })
                      setErr(undefined)
                      reload()
                    } catch (x) {
                      setErr((x as Error).message)
                    }
                  }}
                >
                  {approve ? 'Approve' : 'Reject'}
                </button>
              ))}
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}

export default function Marketplace() {
  const { ws, me } = useShell()
  const isAdmin = ['owner', 'admin'].includes(me.workspaces.find((w) => w.id === ws)?.role ?? '')
  const { data: queue, reload } = useCmd<Queued[]>(ws, 'market.review_queue')
  const tabs = ['Browse', ...(isAdmin ? ['Build apps'] : []), ...(queue ? ['Review'] : [])]
  const [tab, setTab] = useState('Browse')
  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6">
      <h1 className="mb-4 text-xl font-semibold">Marketplace</h1>
      <div
        role="tablist"
        aria-label="Marketplace sections"
        className="mb-6 flex flex-wrap gap-1 border-b border-line"
      >
        {tabs.map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            onClick={() => {
              setTab(t)
              if (t === 'Review') reload()
            }}
            className="px-3 py-2 text-[13px] text-muted hover:text-fg aria-selected:border-b-2 aria-selected:border-accent aria-selected:font-medium aria-selected:text-fg"
          >
            {t}
          </button>
        ))}
      </div>
      <div role="tabpanel">
        {tab === 'Browse' && <Browse ws={ws} isAdmin={isAdmin} />}
        {tab === 'Build apps' && isAdmin && <Build ws={ws} />}
        {tab === 'Review' && queue && <ReviewQueue ws={ws} queue={queue} reload={reload} />}
      </div>
    </div>
  )
}
