'use client'
import {
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
} from '@dnd-kit/core'
import { Inbox, Plus, Save, Zap } from 'lucide-react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd, type Space } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import {
  type Agent,
  Assignee,
  type Item,
  PRIORITIES,
  PriorityPill,
  StatusPill,
  typing,
  useAgents,
  useToast,
  type Workflow,
} from './shared'
import { SprintsView } from './sprints'

type Layout = 'list' | 'board' | 'sprints'
type View = { id: string; name: string; layout: string; query: string }

function QueryBar({
  space,
  statuses,
  applied,
  onApply,
  error,
  views,
  onSaved,
  layout,
}: {
  space: Space
  statuses: string[]
  applied: string
  onApply: (q: string) => void
  error?: string
  views: View[]
  onSaved: () => void
  layout: Layout
}) {
  const { ws } = useShell()
  const [mode, setMode] = useState<'basic' | 'cnql'>('basic')
  const [f, setF] = useState({ status: '', priority: '', mine: false, text: '' })
  const [draft, setDraft] = useState(applied)
  const [saving, setSaving] = useState(false)
  const [saveErr, setSaveErr] = useState<string>()
  const q = (s: string) => `"${s.replace(/"/g, '')}"`

  function runBasic(n: typeof f) {
    setF(n)
    const parts = [`project = ${space.key}`]
    if (n.status) parts.push(`status = ${q(n.status)}`)
    if (n.priority) parts.push(`priority = ${n.priority}`)
    if (n.mine) parts.push('assignee = currentUser()')
    if (n.text.trim()) parts.push(`text ~ ${q(n.text.trim())}`)
    const query = parts.length > 1 ? parts.join(' AND ') : ''
    setDraft(query)
    onApply(query)
  }

  async function save(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const name = String(new FormData(e.currentTarget).get('name') ?? '')
    try {
      await cmd(ws, 'view.create', {
        name,
        layout: layout === 'board' ? 'board' : 'list',
        query: draft || `project = ${space.key}`,
        space_id: space.id,
      })
      setSaving(false)
      setSaveErr(undefined)
      onSaved()
    } catch (x) {
      setSaveErr((x as Error).message)
    }
  }

  // The reported column points into the text the user typed (mode "cnql"), so a caret line lines it up.
  const col = Number(/at column (\d+)/.exec(error ?? '')?.[1])
  const sel = 'rounded-md border border-line bg-canvas px-2 py-1 text-[13px]'
  return (
    <div className="space-y-2 border-b border-line px-6 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <div role="tablist" aria-label="Filter mode" className="flex rounded-md border border-line p-0.5">
          {(['basic', 'cnql'] as const).map((m) => (
            <button
              key={m}
              type="button"
              role="tab"
              aria-selected={mode === m}
              onClick={() => setMode(m)}
              className="rounded px-2 py-0.5 text-[12px] text-muted aria-selected:bg-hover aria-selected:font-medium aria-selected:text-fg"
            >
              {m === 'basic' ? 'Basic filters' : 'CNQL'}
            </button>
          ))}
        </div>
        {mode === 'basic' ? (
          <>
            <select
              aria-label="Filter by status"
              value={f.status}
              onChange={(e) => runBasic({ ...f, status: e.target.value })}
              className={sel}
            >
              <option value="">Any status</option>
              {statuses.map((s) => (
                <option key={s}>{s}</option>
              ))}
            </select>
            <select
              aria-label="Filter by priority"
              value={f.priority}
              onChange={(e) => runBasic({ ...f, priority: e.target.value })}
              className={sel}
            >
              <option value="">Any priority</option>
              {PRIORITIES.map((p) => (
                <option key={p}>{p}</option>
              ))}
            </select>
            <label className="flex items-center gap-1.5 text-[13px]">
              <input
                type="checkbox"
                checked={f.mine}
                onChange={(e) => runBasic({ ...f, mine: e.target.checked })}
              />
              Assigned to me
            </label>
            <input
              aria-label="Search text"
              placeholder="Search..."
              value={f.text}
              onChange={(e) => setF({ ...f, text: e.target.value })}
              onKeyDown={(e) => e.key === 'Enter' && runBasic(f)}
              className={`${sel} w-44`}
            />
          </>
        ) : (
          <form
            className="flex min-w-0 flex-1 items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              onApply(draft)
            }}
          >
            <input
              aria-label="CNQL query"
              spellCheck={false}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder={`project = ${space.key} AND priority = high ORDER BY created DESC`}
              className={`${inputCls} font-mono text-[13px]`}
            />
            <button type="submit" className={btn}>
              Run
            </button>
          </form>
        )}
        <button type="button" onClick={() => setSaving(!saving)} className={`${btn} ml-auto`}>
          <Save size={13} /> Save view
        </button>
      </div>
      {mode === 'cnql' && error && (
        <div role="alert" className="font-mono text-[12px] text-bad">
          {Number.isFinite(col) && (
            <pre className="overflow-x-auto">
              {draft}
              {'\n'}
              {' '.repeat(Math.max(col - 1, 0))}^
            </pre>
          )}
          {error}
        </div>
      )}
      {mode === 'basic' && error && <ErrorText error={error} />}
      {saving && (
        <form onSubmit={save} className="flex items-center gap-2">
          <input
            name="name"
            required
            aria-label="View name"
            placeholder="View name"
            className={`${inputCls} max-w-xs`}
          />
          <button type="submit" className={btnPrimary}>
            Save
          </button>
          <ErrorText error={saveErr} />
        </form>
      )}
      {views.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 text-[12px]">
          <span className="text-muted">Saved views</span>
          {views.map((v) => (
            <button
              key={v.id}
              type="button"
              onClick={() => {
                setMode('cnql')
                setDraft(v.query)
                onApply(v.query)
              }}
              className="rounded-full border border-line px-2 py-0.5 hover:bg-hover"
            >
              {v.name}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function InlineNew({ onCreate, label }: { onCreate: (title: string) => Promise<void>; label: string }) {
  const [open, setOpen] = useState(false)
  if (!open)
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-[13px] text-muted hover:bg-hover"
      >
        <Plus size={13} /> {label}
      </button>
    )
  return (
    <input
      // biome-ignore lint/a11y/noAutofocus: the user just asked for this field
      autoFocus
      aria-label="New item title"
      placeholder="Title, then Enter"
      onBlur={() => setOpen(false)}
      onKeyDown={async (e) => {
        if (e.key === 'Escape') setOpen(false)
        if (e.key === 'Enter' && e.currentTarget.value.trim()) {
          const t = e.currentTarget.value.trim()
          e.currentTarget.value = ''
          await onCreate(t)
        }
      }}
      className={`${inputCls} m-1.5 w-[calc(100%-12px)]`}
    />
  )
}

function ListView({
  groups,
  agents,
  ws,
  selected,
  onCreate,
}: {
  groups: { status: string; items: Item[] }[]
  agents: Agent[]
  ws: string
  selected: string | undefined
  onCreate: (title: string, status: string) => Promise<void>
}) {
  return (
    <div>
      {groups.map((g) => (
        <section key={g.status} aria-label={g.status}>
          <h2 className="flex items-center gap-2 border-b border-line bg-side px-6 py-1.5 text-[12px] font-semibold">
            {g.status} <span className="font-normal text-muted">{g.items.length}</span>
          </h2>
          <ul>
            {g.items.map((i) => (
              <li
                key={i.id}
                aria-current={selected === i.id ? 'true' : undefined}
                className="flex items-center gap-3 border-b border-line px-6 py-1.5 hover:bg-hover aria-[current=true]:bg-hover aria-[current=true]:shadow-[inset_2px_0_0_var(--accent)]"
              >
                <span className="w-16 shrink-0 text-[12px] text-muted">{i.key}</span>
                <Link href={`/w/${ws}/projects/${i.key}`} className="min-w-0 flex-1 truncate hover:underline">
                  {i.title}
                </Link>
                <StatusPill status={i.status} cat={i.status_category} />
                <PriorityPill p={i.priority} />
                <Assignee item={i} agents={agents} />
              </li>
            ))}
          </ul>
          <InlineNew label="New item" onCreate={(t) => onCreate(t, g.status)} />
        </section>
      ))}
    </div>
  )
}

function Card({ item, ws, agents }: { item: Item; ws: string; agents: Agent[] }) {
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({
    id: item.id,
    data: { status: item.status },
  })
  return (
    <li
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      aria-label={`${item.key} ${item.title}`}
      style={{ transform: transform ? `translate(${transform.x}px, ${transform.y}px)` : undefined }}
      className={`cursor-grab space-y-2 rounded-md border border-line bg-canvas p-2.5 shadow-sm ${isDragging ? 'relative z-20 shadow-lg' : ''}`}
    >
      <p className="text-[13px]">{item.title}</p>
      <div className="flex items-center gap-2">
        <Link
          href={`/w/${ws}/projects/${item.key}`}
          onKeyDown={(e) => e.stopPropagation()}
          className="text-[11px] text-muted hover:underline"
        >
          {item.key}
        </Link>
        <PriorityPill p={item.priority} />
        <span className="ml-auto">
          <Assignee item={item} agents={agents} />
        </span>
      </div>
    </li>
  )
}

function Column({
  g,
  ws,
  agents,
  onCreate,
}: {
  g: { status: string; cat: Item['status_category']; items: Item[] }
  ws: string
  agents: Agent[]
  onCreate: (title: string, status: string) => Promise<void>
}) {
  const { setNodeRef, isOver } = useDroppable({ id: g.status })
  return (
    <section
      ref={setNodeRef}
      aria-label={`${g.status} column`}
      className={`flex w-64 shrink-0 flex-col rounded-md bg-side ${isOver ? 'ring-2 ring-accent' : ''}`}
    >
      <h2 className="flex items-center gap-2 px-3 py-2 text-[12px] font-semibold">
        <span
          className={`h-2 w-2 rounded-full ${g.cat === 'done' ? 'bg-ok' : g.cat === 'in_progress' ? 'bg-warn' : 'bg-muted'}`}
        />
        {g.status} <span className="font-normal text-muted">{g.items.length}</span>
      </h2>
      <ul className="min-h-12 flex-1 space-y-2 px-2 pb-2">
        {g.items.map((i) => (
          <Card key={i.id} item={i} ws={ws} agents={agents} />
        ))}
      </ul>
      <InlineNew label="New" onCreate={(t) => onCreate(t, g.status)} />
    </section>
  )
}

export function SpaceView({ space }: { space: Space }) {
  const { ws } = useShell()
  const router = useRouter()
  const agents = useAgents(ws)
  const { toast, push } = useToast()
  const [layout, setLayout] = useState<Layout>('list')
  const [applied, setApplied] = useState('')
  const [newOpen, setNewOpen] = useState(false)
  const [newErr, setNewErr] = useState<string>()
  const [sel, setSel] = useState(0)

  const wf = useCmd<Workflow>(ws, 'workflow.get', { space_id: space.id })
  const views = useCmd<View[]>(ws, 'view.list', { space_id: space.id })
  const items = useCmd<Item[]>(ws, 'item.list', { cnql: applied || `project = ${space.key}`, limit: 200 })
  const all = items.data ?? [] // stays on the last good result while a bad query shows its error
  const statuses = wf.data?.definition.statuses ?? []

  const groups = useMemo(
    () =>
      statuses.map((s) => ({
        status: s.name,
        cat: s.category,
        items: all.filter((i) => i.space_id === space.id && !i.triage && i.status === s.name),
      })),
    [statuses, all, space.id],
  )
  const flat = useMemo(() => groups.flatMap((g) => g.items), [groups])

  const create = useCallback(
    async (title: string, status?: string, priority?: string) => {
      const it = await cmd<Item>(ws, 'item.create', { space_id: space.id, title, priority, triage: false })
      if (status && status !== it.status) {
        try {
          await cmd(ws, 'item.transition', { id: it.id, to: status })
        } catch (e) {
          push((e as Error).message)
        }
      }
      items.reload()
    },
    [ws, space.id, items.reload, push],
  )

  const transition = useCallback(
    async (e: DragEndEvent) => {
      const from = e.active.data.current?.status
      const to = e.over?.id as string | undefined
      if (!to || to === from) return
      try {
        await cmd(ws, 'item.transition', { id: e.active.id, to })
      } catch (x) {
        push((x as Error).message)
      }
      items.reload()
    },
    [ws, push, items.reload],
  )

  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if (typing(e)) return
      if (e.key === 'c') {
        e.preventDefault()
        setNewOpen(true)
      }
      if (layout !== 'list') return
      if (e.key === 'j') setSel((s) => Math.min(s + 1, flat.length - 1))
      if (e.key === 'k') setSel((s) => Math.max(s - 1, 0))
      if (e.key === 'Enter' && flat[sel]) router.push(`/w/${ws}/projects/${flat[sel].key}`)
    }
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [layout, flat, sel, router, ws])

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  )

  async function submitNew(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    try {
      await create(String(f.get('title')), undefined, String(f.get('priority')))
      setNewOpen(false)
      setNewErr(undefined)
    } catch (x) {
      setNewErr((x as Error).message)
    }
  }

  const tab = (l: Layout, label: string) => (
    <button
      type="button"
      role="tab"
      aria-selected={layout === l}
      onClick={() => setLayout(l)}
      className="rounded px-2.5 py-1 text-[13px] text-muted aria-selected:bg-hover aria-selected:font-medium aria-selected:text-fg"
    >
      {label}
    </button>
  )

  return (
    <div className="flex min-h-full flex-col">
      <div className="flex items-center gap-3 border-b border-line px-6 py-2">
        <h1 className="text-[15px] font-semibold">{space.name}</h1>
        <Pill>{space.key}</Pill>
        <div role="tablist" aria-label="Layout" className="ml-2 flex gap-0.5">
          {tab('list', 'List')}
          {tab('board', 'Board')}
          {tab('sprints', 'Sprints')}
        </div>
        <Link href={`/w/${ws}/projects/triage`} className={`${btn} ml-auto`}>
          <Inbox size={14} /> Triage
        </Link>
        <Link href={`/w/${ws}/projects/automations`} className={btn}>
          <Zap size={14} /> Automations
        </Link>
        <button type="button" onClick={() => setNewOpen(true)} className={btnPrimary}>
          <Plus size={14} /> New item <kbd className="rounded border border-white/40 px-1 text-[10px]">c</kbd>
        </button>
      </div>

      {layout === 'sprints' ? (
        <SprintsView space={space} agents={agents} />
      ) : (
        <>
          <QueryBar
            space={space}
            statuses={statuses.map((s) => s.name)}
            applied={applied}
            onApply={setApplied}
            error={items.error?.message}
            views={views.data ?? []}
            onSaved={views.reload}
            layout={layout}
          />
          {!wf.data || (items.loading && !all.length) ? (
            <p className="p-6 text-muted">Loading...</p>
          ) : flat.length === 0 && !items.error ? (
            <p className="p-6 text-muted">
              {applied ? 'No items match this query.' : 'No items yet. Press c to create one.'}
            </p>
          ) : null}
          {wf.data &&
            (layout === 'list' ? (
              <ListView
                groups={groups}
                agents={agents}
                ws={ws}
                selected={flat[sel]?.id}
                onCreate={(t, s) => create(t, s)}
              />
            ) : (
              <DndContext sensors={sensors} onDragEnd={transition}>
                <div className="flex flex-1 items-start gap-3 overflow-x-auto p-4">
                  {groups.map((g) => (
                    <Column key={g.status} g={g} ws={ws} agents={agents} onCreate={(t, s) => create(t, s)} />
                  ))}
                </div>
              </DndContext>
            ))}
        </>
      )}

      <Modal open={newOpen} onClose={() => setNewOpen(false)} title="New item">
        <form onSubmit={submitNew} className="space-y-3">
          <Field label="Title">{(id) => <input id={id} name="title" required className={inputCls} />}</Field>
          <Field label="Priority">
            {(id) => (
              <select id={id} name="priority" defaultValue="medium" className={inputCls}>
                {PRIORITIES.map((p) => (
                  <option key={p}>{p}</option>
                ))}
              </select>
            )}
          </Field>
          <ErrorText error={newErr} />
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setNewOpen(false)} className={btn}>
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Create item
            </button>
          </div>
        </form>
      </Modal>
      {toast}
    </div>
  )
}
