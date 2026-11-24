'use client'
import {
  type Announcements,
  closestCenter,
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
} from '@dnd-kit/core'
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import { ArrowDown, ArrowUp, GripVertical, Plus, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, EmptyState, Modal } from '@/components/ui'
import { type Ctx, MAX_WIDGETS, type Size, type WidgetCfg, type WidgetDef } from './types'

const SIZE_NAME: Record<Size, string> = { s: 'Small', m: 'Medium', l: 'Large' }
const reduced = () =>
  typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
const iconBtn =
  'rounded p-1.5 hover:bg-hover disabled:opacity-30 focus-visible:outline-2 focus-visible:outline-accent'

type Row = { cfg: WidgetCfg; def?: WidgetDef; usable: boolean; index: number; count: number }

function SizeRadios({ row, onSize }: { row: Row; onSize: (s: Size) => void }) {
  const { cfg, def } = row
  if (!def || !row.usable) return null
  return (
    <div
      role="radiogroup"
      aria-label={`Size of ${def.title}`}
      className="flex rounded-md border border-line p-0.5"
    >
      {def.sizes.map((s) => (
        <label key={s} className="relative">
          <input
            type="radio"
            name={`size-${cfg.id}`}
            checked={cfg.size === s}
            onChange={() => onSize(s)}
            className="peer sr-only"
          />
          <span className="block min-w-8 cursor-pointer rounded px-2 py-1 text-center text-[12px] font-medium text-muted peer-checked:bg-accent peer-checked:text-accent-fg peer-focus-visible:outline-2 peer-focus-visible:outline-accent">
            <span aria-hidden>{s.toUpperCase()}</span>
            <span className="sr-only">{SIZE_NAME[s]}</span>
          </span>
        </label>
      ))}
    </div>
  )
}

function SortableRow({
  row,
  onSize,
  onMove,
  onRemove,
}: {
  row: Row
  onSize: (s: Size) => void
  onMove: (d: -1 | 1) => void
  onRemove: () => void
}) {
  const { cfg, def, index, count } = row
  const title = def?.title ?? cfg.type
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } =
    useSortable({ id: cfg.id, transition: reduced() ? null : undefined })
  return (
    <li
      ref={setNodeRef}
      style={{
        transform: transform ? `translate3d(${transform.x}px, ${transform.y}px, 0)` : undefined,
        transition,
      }}
      className={`flex flex-wrap items-center gap-2 rounded-xl border border-line bg-canvas p-2 shadow-sm ${isDragging ? 'opacity-60' : ''}`}
    >
      <button
        type="button"
        ref={setActivatorNodeRef}
        {...attributes}
        {...listeners}
        aria-label={`Move ${title}`}
        data-focus={`handle:${cfg.id}`}
        className={`${iconBtn} touch-none cursor-grab`}
      >
        <GripVertical size={16} aria-hidden />
      </button>
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium">{title}</span>
        {!row.usable && <span className="block text-[12px] text-muted">Not available to you right now</span>}
      </span>
      <SizeRadios row={row} onSize={onSize} />
      <button
        type="button"
        aria-label={`Move ${title} up`}
        data-focus={`up:${cfg.id}`}
        disabled={index === 0}
        onClick={() => onMove(-1)}
        className={iconBtn}
      >
        <ArrowUp size={16} aria-hidden />
      </button>
      <button
        type="button"
        aria-label={`Move ${title} down`}
        data-focus={`down:${cfg.id}`}
        disabled={index === count - 1}
        onClick={() => onMove(1)}
        className={iconBtn}
      >
        <ArrowDown size={16} aria-hidden />
      </button>
      <button type="button" aria-label={`Remove ${title}`} onClick={onRemove} className={iconBtn}>
        <X size={16} aria-hidden />
      </button>
    </li>
  )
}

/** Edit mode: a draft list with drag, move buttons, size radios, remove, a gallery to add from, and reset. */
export function WidgetEditor({
  title,
  catalog,
  ctx,
  initial,
  onDone,
  onCancel,
  onReset,
}: {
  title: string
  catalog: WidgetDef[]
  ctx: Ctx
  initial: WidgetCfg[]
  onDone: (widgets: WidgetCfg[]) => Promise<void>
  onCancel: () => void
  onReset: () => Promise<void>
}) {
  const [draft, setDraft] = useState(initial)
  const [msg, setMsg] = useState('')
  const [gallery, setGallery] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [focus, setFocus] = useState<{ key: string; n: number }>()
  const [added, setAdded] = useState<string>()
  const defOf = (type: string) => catalog.find((d) => d.type === type)
  const nameOf = (id: string) => {
    const c = draft.find((w) => w.id === id)
    return c ? (defOf(c.type)?.title ?? c.type) : id
  }
  const say = (s: string) => setMsg((m) => (m === s ? `${s} ` : s))
  const focusOn = (key: string) => setFocus((f) => ({ key, n: (f?.n ?? 0) + 1 }))

  // focus rules: heading on entry; after each change a sensible target
  useEffect(() => document.getElementById('customise-title')?.focus(), [])
  useEffect(() => {
    if (focus) document.querySelector<HTMLElement>(`[data-focus="${focus.key}"]`)?.focus()
  }, [focus])
  const opened = useRef(false)
  // biome-ignore lint/correctness/useExhaustiveDependencies: only when the gallery closes
  useEffect(() => {
    if (gallery) opened.current = true
    if (gallery || !opened.current) return
    focusOn(added ? `handle:${added}` : 'add')
    setAdded(undefined)
  }, [gallery])

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )
  const pos = (id: unknown) => draft.findIndex((w) => w.id === id) + 1
  const announcements: Announcements = {
    onDragStart: ({ active }) =>
      `Picked up ${nameOf(String(active.id))}, position ${pos(active.id)} of ${draft.length}.`,
    onDragOver: ({ active, over }) =>
      over ? `${nameOf(String(active.id))} is over position ${pos(over.id)} of ${draft.length}.` : undefined,
    onDragEnd: ({ active, over }) =>
      `Dropped ${nameOf(String(active.id))} at position ${pos(over?.id ?? active.id)} of ${draft.length}.`,
    onDragCancel: ({ active }) =>
      `Move cancelled. ${nameOf(String(active.id))} stays at position ${pos(active.id)}.`,
  }
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return
    setDraft((d) =>
      arrayMove(
        d,
        d.findIndex((w) => w.id === active.id),
        d.findIndex((w) => w.id === over.id),
      ),
    )
  }
  const move = (i: number, dir: -1 | 1) => {
    const w = draft[i]
    const to = i + dir
    setDraft(arrayMove(draft, i, to))
    say(`Moved ${nameOf(w.id)} ${dir < 0 ? 'up' : 'down'}, position ${to + 1} of ${draft.length}.`)
    // a button disabled at the edge cannot keep focus: hand it to the other arrow
    focusOn(`${dir < 0 ? (to === 0 ? 'down' : 'up') : to === draft.length - 1 ? 'up' : 'down'}:${w.id}`)
  }
  const remove = (i: number) => {
    const w = draft[i]
    const rest = draft.filter((_, j) => j !== i)
    setDraft(rest)
    say(`Removed ${nameOf(w.id)}. ${rest.length} widgets left.`)
    const next = rest[i] ?? rest[i - 1]
    focusOn(next ? `handle:${next.id}` : 'add')
  }
  const add = (d: WidgetDef, from: HTMLElement) => {
    if (draft.length >= MAX_WIDGETS) return
    // the clicked button turns into "Added": move focus to the next Add button, else the gallery heading
    const rest = [...(from.closest('dialog')?.querySelectorAll<HTMLElement>('[data-gallery-add]') ?? [])]
    const i = rest.indexOf(from)
    rest.splice(i, 1)
    const next = rest[i] ?? rest[rest.length - 1] ?? from.closest('dialog')?.querySelector<HTMLElement>('h2')
    next?.focus()
    setDraft([...draft, { id: d.type, type: d.type, size: d.sizes[0] }])
    setAdded(d.type)
    say(`Added ${d.title}.`)
  }
  const done = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await onDone(draft)
    } catch (e) {
      setError((e as Error).message || 'Could not save your changes.')
      say('Could not save your changes.')
      focusOn('error')
      setBusy(false)
    }
  }
  const reset = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await onReset()
    } catch (e) {
      setConfirm(false)
      setError((e as Error).message || 'Could not reset.')
      focusOn('error')
      setBusy(false)
    }
  }

  const rows: Row[] = draft.map((cfg, index) => {
    const def = defOf(cfg.type)
    return { cfg, def, usable: !!def?.available(ctx), index, count: draft.length }
  })
  const groups = [...new Set(catalog.filter((d) => d.available(ctx)).map((d) => d.group))]

  return (
    <section aria-labelledby="customise-title" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 id="customise-title" tabIndex={-1} className="text-[15px] font-semibold focus:outline-none">
            Customise {title}
          </h2>
          <p className="text-[13px] text-muted">
            Drag the handle, or use the arrows. Press Done to keep your changes.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button type="button" onClick={() => setConfirm(true)} disabled={busy} className={btn}>
            Reset to default
          </button>
          <button type="button" onClick={onCancel} disabled={busy} className={btn}>
            Cancel
          </button>
          <button type="button" onClick={done} disabled={busy} className={btnPrimary}>
            Done
          </button>
        </div>
      </div>
      {error && (
        <p role="alert" tabIndex={-1} data-focus="error" className="text-[13px] text-bad focus:outline-none">
          {error}
        </p>
      )}
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragEnd={onDragEnd}
        accessibility={{ announcements }}
      >
        <SortableContext items={draft.map((w) => w.id)} strategy={verticalListSortingStrategy}>
          <ul aria-label={`${title} widgets`} className="space-y-2">
            {rows.map((r) => (
              <SortableRow
                key={r.cfg.id}
                row={r}
                onSize={(s) => {
                  setDraft(draft.map((w) => (w.id === r.cfg.id ? { ...w, size: s } : w)))
                  say(`${nameOf(r.cfg.id)} size set to ${SIZE_NAME[s]}.`)
                }}
                onMove={(d) => move(r.index, d)}
                onRemove={() => remove(r.index)}
              />
            ))}
          </ul>
        </SortableContext>
      </DndContext>
      {draft.length === 0 && (
        <EmptyState compact icon={Plus} title={`Your ${title.toLowerCase()} is empty`}>
          Add a widget to see it here.
        </EmptyState>
      )}
      <button
        type="button"
        data-focus="add"
        onClick={() => setGallery(true)}
        disabled={draft.length >= MAX_WIDGETS}
        className={btn}
      >
        <Plus size={14} aria-hidden /> Add a widget
      </button>
      {draft.length >= MAX_WIDGETS && <p className="text-[12px] text-muted">Up to {MAX_WIDGETS} widgets.</p>}
      <div role="status" aria-live="polite" className="sr-only">
        {msg}
      </div>

      <Modal open={gallery} title="Add a widget" onClose={() => setGallery(false)} className="max-w-lg">
        <div className="max-h-[65vh] space-y-4 overflow-auto">
          {groups.map((g) => (
            <section key={g} aria-label={g}>
              <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wide text-muted">{g}</h3>
              <ul className="space-y-1">
                {catalog
                  .filter((d) => d.group === g && d.available(ctx))
                  .map((d) => {
                    const has = draft.some((w) => w.type === d.type)
                    return (
                      <li
                        key={d.type}
                        className="flex items-center justify-between gap-2 rounded-lg border border-line px-3 py-2"
                      >
                        <span>
                          <span className="block font-medium">{d.title}</span>
                          <span className="block text-[12px] text-muted">Uses {d.command}</span>
                        </span>
                        {has ? (
                          <span className="text-[13px] text-muted">Added</span>
                        ) : (
                          <button
                            type="button"
                            aria-label={`Add ${d.title}`}
                            data-gallery-add
                            onClick={(e) => add(d, e.currentTarget)}
                            className={btn}
                          >
                            <Plus size={14} aria-hidden /> Add
                          </button>
                        )}
                      </li>
                    )
                  })}
              </ul>
            </section>
          ))}
        </div>
        <div role="status" aria-live="polite" className="sr-only">
          {msg}
        </div>
        <div className="mt-3 flex justify-end">
          <button type="button" onClick={() => setGallery(false)} className={btnPrimary}>
            Close
          </button>
        </div>
      </Modal>
      <Modal open={confirm} title="Reset to default" onClose={() => setConfirm(false)}>
        <p className="mb-3 text-[13px]">
          Put {title} back to the standard layout? Your own arrangement is discarded.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" onClick={() => setConfirm(false)} className={btn}>
            Keep editing
          </button>
          <button type="button" onClick={reset} disabled={busy} className={btnPrimary}>
            Reset
          </button>
        </div>
      </Modal>
    </section>
  )
}
