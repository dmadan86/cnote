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
import { GripVertical } from 'lucide-react'
import Link from 'next/link'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, inputCls, Pill } from '@/components/ui'
import type { Space } from '@/lib/api'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { type Agent, Assignee, type Item, PriorityPill, type Sprint, useToast } from './shared'

type Burn = { total_points: number; points: { date: string; ideal: number; remaining: number }[] }
type Velocity = {
  average_points: number
  sprints: { sprint_id: string; name: string; completed_points: number; committed_points: number }[]
}

const W = 360
const H = 140
const PAD = 24

function Burndown({ ws, sprint }: { ws: string; sprint: Sprint }) {
  const { data } = useCmd<Burn>(ws, 'report.burndown', { sprint_id: sprint.id })
  if (!data) return null
  const pts = data.points
  const max = Math.max(data.total_points, ...pts.map((p) => p.remaining), 1)
  const x = (i: number) => PAD + (pts.length < 2 ? 0 : (i * (W - PAD * 2)) / (pts.length - 1))
  const y = (v: number) => H - PAD - (v / max) * (H - PAD * 2)
  const line = (k: 'ideal' | 'remaining') => pts.map((p, i) => `${x(i)},${y(p[k])}`).join(' ')
  return (
    <figure className="rounded-md border border-line p-3">
      <figcaption className="mb-1 text-[12px] font-medium">Burndown: {sprint.name}</figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`Burndown for ${sprint.name}`} className="w-full">
        <line x1={PAD} y1={H - PAD} x2={W - PAD} y2={H - PAD} stroke="var(--line)" />
        <line x1={PAD} y1={PAD} x2={PAD} y2={H - PAD} stroke="var(--line)" />
        <text x={4} y={PAD + 4} fontSize="10" fill="var(--muted)">
          {max}
        </text>
        <text x={PAD} y={H - 6} fontSize="10" fill="var(--muted)">
          {pts[0]?.date}
        </text>
        <text x={W - PAD} y={H - 6} fontSize="10" textAnchor="end" fill="var(--muted)">
          {pts.at(-1)?.date}
        </text>
        <polyline points={line('ideal')} fill="none" stroke="var(--muted)" strokeDasharray="4 3" />
        <polyline points={line('remaining')} fill="none" stroke="var(--accent)" strokeWidth="2" />
      </svg>
      <p className="text-[11px] text-muted">Solid: remaining points. Dashed: ideal.</p>
    </figure>
  )
}

function VelocityChart({ ws, space }: { ws: string; space: Space }) {
  const { data } = useCmd<Velocity>(ws, 'report.velocity', { space_id: space.id })
  if (!data) return null
  const bars = [...data.sprints].reverse()
  const max = Math.max(...bars.map((b) => Math.max(b.committed_points, b.completed_points)), 1)
  const bw = bars.length ? (W - PAD * 2) / bars.length : 0
  return (
    <figure className="rounded-md border border-line p-3">
      <figcaption className="mb-1 text-[12px] font-medium">
        Velocity (average {data.average_points.toFixed(1)} pts)
      </figcaption>
      {bars.length === 0 ? (
        <p className="text-[13px] text-muted">Complete a sprint to see velocity.</p>
      ) : (
        <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Velocity by sprint" className="w-full">
          <line x1={PAD} y1={H - PAD} x2={W - PAD} y2={H - PAD} stroke="var(--line)" />
          {bars.map((b, i) => {
            const h = (v: number) => (v / max) * (H - PAD * 2)
            const bx = PAD + i * bw
            return (
              <g key={b.sprint_id}>
                <title>{`${b.name}: ${b.completed_points} of ${b.committed_points} pts`}</title>
                <rect
                  x={bx + 4}
                  y={H - PAD - h(b.committed_points)}
                  width={bw - 8}
                  height={h(b.committed_points)}
                  fill="var(--hover)"
                />
                <rect
                  x={bx + 4}
                  y={H - PAD - h(b.completed_points)}
                  width={bw - 8}
                  height={h(b.completed_points)}
                  fill="var(--accent)"
                />
                <text x={bx + bw / 2} y={H - 8} fontSize="9" textAnchor="middle" fill="var(--muted)">
                  {b.name.slice(0, 8)}
                </text>
              </g>
            )
          })}
        </svg>
      )}
      <p className="text-[11px] text-muted">Filled: completed. Grey: committed.</p>
    </figure>
  )
}

function Row({
  item,
  ws,
  agents,
  sprints,
  onMove,
}: {
  item: Item
  ws: string
  agents: Agent[]
  sprints: Sprint[]
  onMove: (id: string, sprint: string | null) => void
}) {
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({ id: item.id })
  return (
    <li
      ref={setNodeRef}
      style={{ transform: transform ? `translate(${transform.x}px, ${transform.y}px)` : undefined }}
      className={`flex items-center gap-2 border-b border-line bg-canvas px-3 py-1.5 ${isDragging ? 'relative z-20 shadow-lg' : ''}`}
    >
      <button
        type="button"
        {...attributes}
        {...listeners}
        aria-label={`Drag ${item.key}`}
        className="cursor-grab rounded p-0.5 text-muted hover:bg-hover"
      >
        <GripVertical size={14} />
      </button>
      <span className="w-16 shrink-0 text-[12px] text-muted">{item.key}</span>
      <Link href={`/w/${ws}/projects/${item.key}`} className="min-w-0 flex-1 truncate hover:underline">
        {item.title}
      </Link>
      <PriorityPill p={item.priority} />
      <Assignee item={item} agents={agents} />
      <select
        aria-label={`Sprint for ${item.key}`}
        value={item.sprint_id ?? ''}
        onChange={(e) => onMove(item.id, e.target.value || null)}
        className="rounded border border-line bg-canvas px-1 py-0.5 text-[12px]"
      >
        <option value="">Backlog</option>
        {sprints.map((s) => (
          <option key={s.id} value={s.id}>
            {s.name}
          </option>
        ))}
      </select>
    </li>
  )
}

function Zone({
  id,
  title,
  extra,
  children,
  count,
}: {
  id: string
  title: string
  extra?: React.ReactNode
  children: React.ReactNode
  count: number
}) {
  const { setNodeRef, isOver } = useDroppable({ id })
  return (
    <section
      aria-label={title}
      ref={setNodeRef}
      className={`rounded-md border border-line ${isOver ? 'ring-2 ring-accent' : ''}`}
    >
      <h2 className="flex items-center gap-2 border-b border-line bg-side px-3 py-1.5 text-[13px] font-semibold">
        {title} <span className="font-normal text-muted">{count}</span>
        <span className="ml-auto flex items-center gap-2">{extra}</span>
      </h2>
      <ul className="min-h-9">{children}</ul>
    </section>
  )
}

export function SprintsView({ space, agents }: { space: Space; agents: Agent[] }) {
  const { ws } = useShell()
  const { toast, push } = useToast()
  const sprints = useCmd<Sprint[]>(ws, 'sprint.list', { space_id: space.id })
  const items = useCmd<Item[]>(ws, 'item.list', { cnql: `project = ${space.key}`, limit: 200 })
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  )
  const open = (sprints.data ?? []).filter((s) => s.state !== 'closed')
  const live = (items.data ?? []).filter((i) => i.space_id === space.id && !i.triage)
  const active = open.find((s) => s.state === 'active')

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn()
    } catch (e) {
      push((e as Error).message)
    }
    sprints.reload()
    items.reload()
  }
  const move = (id: string, sprint: string | null) =>
    run(() => cmd(ws, 'item.update', sprint ? { id, sprint_id: sprint } : { id, clear: ['sprint'] }))
  const onDragEnd = (e: DragEndEvent) => {
    const to = e.over?.id as string | undefined
    const item = live.find((i) => i.id === e.active.id)
    if (item && to && (item.sprint_id ?? 'backlog') !== to) move(item.id, to === 'backlog' ? null : to)
  }

  async function create(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const name = String(new FormData(form).get('name'))
    await run(() => cmd(ws, 'sprint.create', { space_id: space.id, name }))
    form.reset()
  }

  const rows = (list: Item[]) =>
    list.map((i) => <Row key={i.id} item={i} ws={ws} agents={agents} sprints={open} onMove={move} />)

  return (
    <div className="grid gap-4 p-6 lg:grid-cols-[1fr_380px]">
      <DndContext sensors={sensors} onDragEnd={onDragEnd}>
        <div className="space-y-4">
          <form onSubmit={create} className="flex gap-2">
            <input
              name="name"
              required
              aria-label="Sprint name"
              placeholder="Sprint name"
              className={`${inputCls} max-w-xs`}
            />
            <button type="submit" className={btn}>
              Create sprint
            </button>
          </form>
          {open.map((s) => (
            <Zone
              key={s.id}
              id={s.id}
              title={s.name}
              count={live.filter((i) => i.sprint_id === s.id).length}
              extra={
                <>
                  <Pill tone={s.state === 'active' ? 'ok' : 'neutral'}>{s.state}</Pill>
                  {s.state === 'future' && (
                    <button
                      type="button"
                      className={btnPrimary}
                      onClick={() => run(() => cmd(ws, 'sprint.start', { id: s.id }))}
                    >
                      Start sprint
                    </button>
                  )}
                  {s.state === 'active' && (
                    <button
                      type="button"
                      className={btn}
                      onClick={() => run(() => cmd(ws, 'sprint.complete', { id: s.id }))}
                    >
                      Complete sprint
                    </button>
                  )}
                </>
              }
            >
              {rows(live.filter((i) => i.sprint_id === s.id))}
            </Zone>
          ))}
          <Zone
            id="backlog"
            title="Backlog"
            count={live.filter((i) => !i.sprint_id && i.status_category !== 'done').length}
          >
            {rows(live.filter((i) => !i.sprint_id && i.status_category !== 'done'))}
          </Zone>
        </div>
      </DndContext>
      <div className="space-y-4">
        {active ? (
          <Burndown ws={ws} sprint={active} />
        ) : (
          <p className="text-[13px] text-muted">Start a sprint to see its burndown.</p>
        )}
        <VelocityChart ws={ws} space={space} />
      </div>
      {toast}
    </div>
  )
}
