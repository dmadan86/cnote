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
import { ArrowDown, ArrowUp, GripVertical } from 'lucide-react'
import { useState } from 'react'
import { btn, btnPrimary, Modal } from '@/components/ui'
import { type Column, DENSITIES, type Density, type TableConfig } from './config'

const iconBtn =
  'rounded p-1.5 hover:bg-hover disabled:opacity-30 focus-visible:outline-2 focus-visible:outline-accent'
const reduced = () =>
  typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches

type Props<T> = {
  columns: Column<T>[]
  cfg: TableConfig
  error?: Error
  /** Applies at once and saves; `say` is announced in this dialog, `focus` names the element to focus next. */
  onChange: (next: TableConfig) => void
  onReset: () => Promise<void>
  open: boolean
  onClose: () => void
  focusOn: (key: string) => void
}

function Item({
  col,
  index,
  count,
  onToggle,
  onMove,
}: {
  col: Column<never>
  index: number
  count: number
  onToggle: () => void
  onMove: (d: -1 | 1) => void
}) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } =
    useSortable({ id: col.key, transition: reduced() ? null : undefined })
  return (
    <li
      ref={setNodeRef}
      style={{
        transform: transform ? `translate3d(${transform.x}px, ${transform.y}px, 0)` : undefined,
        transition,
      }}
      className={`flex items-center gap-2 rounded-md border border-line bg-canvas p-1 ${isDragging ? 'opacity-60' : ''}`}
    >
      <button
        type="button"
        ref={setActivatorNodeRef}
        {...attributes}
        {...listeners}
        aria-label={`Drag ${col.label}`}
        className={`${iconBtn} touch-none cursor-grab`}
      >
        <GripVertical size={16} aria-hidden />
      </button>
      <label className="flex min-w-0 flex-1 items-center gap-2">
        <input type="checkbox" checked data-lt-focus={`chk:${col.key}`} onChange={onToggle} />
        <span className="truncate">{col.label}</span>
      </label>
      <button
        type="button"
        aria-label={`Move ${col.label} up`}
        data-lt-focus={`up:${col.key}`}
        disabled={index === 0}
        onClick={() => onMove(-1)}
        className={iconBtn}
      >
        <ArrowUp size={16} aria-hidden />
      </button>
      <button
        type="button"
        aria-label={`Move ${col.label} down`}
        data-lt-focus={`down:${col.key}`}
        disabled={index === count - 1}
        onClick={() => onMove(1)}
        className={iconBtn}
      >
        <ArrowDown size={16} aria-hidden />
      </button>
    </li>
  )
}

export function Settings<T>({ columns, cfg, error, onChange, onReset, open, onClose, focusOn }: Props<T>) {
  const [msg, setMsg] = useState('')
  const [confirm, setConfirm] = useState(false)
  const say = (s: string) => setMsg((m) => (m === s ? `${s} ` : s))
  const by = (k: string) => columns.find((c) => c.key === k)
  const label = (k: string) => by(k)?.label ?? k
  const locked = columns.filter((c) => c.locked)
  const shown = cfg.columns.filter((k) => !by(k)?.locked)
  const hidden = columns.filter((c) => !c.locked && !cfg.columns.includes(c.key))
  const setShown = (list: string[]) => onChange({ ...cfg, columns: [...locked.map((c) => c.key), ...list] })

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )
  const pos = (id: unknown) => shown.indexOf(String(id)) + 1
  const announcements: Announcements = {
    onDragStart: ({ active }) =>
      `Picked up ${label(String(active.id))}, position ${pos(active.id)} of ${shown.length}.`,
    onDragOver: ({ active, over }) =>
      over ? `${label(String(active.id))} is over position ${pos(over.id)} of ${shown.length}.` : undefined,
    onDragEnd: ({ active, over }) =>
      `Dropped ${label(String(active.id))} at position ${pos(over?.id ?? active.id)} of ${shown.length}.`,
    onDragCancel: ({ active }) =>
      `Move cancelled. ${label(String(active.id))} stays at position ${pos(active.id)}.`,
  }
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (over && active.id !== over.id)
      setShown(arrayMove(shown, shown.indexOf(String(active.id)), shown.indexOf(String(over.id))))
  }
  const move = (i: number, dir: -1 | 1) => {
    const k = shown[i]
    const to = i + dir
    setShown(arrayMove(shown, i, to))
    say(`Moved ${label(k)} ${dir < 0 ? 'up' : 'down'}, position ${to + 1} of ${shown.length}.`)
    // a button disabled at the edge cannot keep focus: hand it to the other arrow
    focusOn(`${dir < 0 ? (to === 0 ? 'down' : 'up') : to === shown.length - 1 ? 'up' : 'down'}:${k}`)
  }
  const hide = (k: string) => {
    setShown(shown.filter((x) => x !== k))
    say(`${label(k)} hidden.`)
    focusOn(`chk:${k}`)
  }
  const show = (k: string) => {
    setShown([...shown, k])
    say(`${label(k)} shown.`)
    focusOn(`chk:${k}`)
  }
  const reset = async () => {
    try {
      await onReset()
      setConfirm(false)
      say('Reset to default. Columns, display options and saved views are back to the defaults.')
      focusOn('reset')
    } catch {
      setConfirm(false) // the error text below says why
      focusOn('reset')
    }
  }

  return (
    <Modal open={open} title="Columns and display" onClose={onClose} className="max-w-lg">
      <div className="space-y-4 text-[14px]">
        <p role="status" aria-live="polite" className="sr-only">
          {msg}
        </p>
        <fieldset>
          <legend className="mb-1 text-[12px] font-medium text-muted">Shown columns, in order</legend>
          <ul className="space-y-1">
            {locked.map((c) => (
              <li key={c.key} className="flex items-center gap-2 rounded-md border border-line p-1 pl-3">
                <span className="flex-1 py-1.5">{c.label}</span>
                <span className="pr-2 text-[12px] text-muted">Always shown, first</span>
              </li>
            ))}
          </ul>
          <DndContext
            sensors={sensors}
            collisionDetection={closestCenter}
            onDragEnd={onDragEnd}
            accessibility={{ announcements }}
          >
            <SortableContext items={shown} strategy={verticalListSortingStrategy}>
              <ul className="mt-1 space-y-1" aria-label="Shown columns">
                {shown.map((k, i) => (
                  <Item
                    key={k}
                    col={by(k) as Column<never>}
                    index={i}
                    count={shown.length}
                    onToggle={() => hide(k)}
                    onMove={(d) => move(i, d)}
                  />
                ))}
              </ul>
            </SortableContext>
          </DndContext>
        </fieldset>
        {hidden.length > 0 && (
          <fieldset>
            <legend className="mb-1 text-[12px] font-medium text-muted">Hidden columns</legend>
            <ul className="space-y-1" aria-label="Hidden columns">
              {hidden.map((c) => (
                <li key={c.key} className="rounded-md border border-line p-2">
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={false}
                      data-lt-focus={`chk:${c.key}`}
                      onChange={() => show(c.key)}
                    />
                    {c.label}
                  </label>
                </li>
              ))}
            </ul>
          </fieldset>
        )}
        <fieldset>
          <legend className="mb-1 text-[12px] font-medium text-muted">Row density</legend>
          <div className="flex gap-4">
            {DENSITIES.map((d: Density) => (
              <label key={d} className="flex items-center gap-1.5 capitalize">
                <input
                  type="radio"
                  name="density"
                  checked={cfg.density === d}
                  onChange={() => {
                    onChange({ ...cfg, density: d })
                    say(`Row density ${d}.`)
                  }}
                />
                {d}
              </label>
            ))}
          </div>
        </fieldset>
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={cfg.freezeFirst}
            onChange={(e) => {
              onChange({ ...cfg, freezeFirst: e.target.checked })
              say(e.target.checked ? 'First column kept in place.' : 'First column scrolls with the table.')
            }}
          />
          Keep the first column in place when scrolling sideways
        </label>
        {error && (
          <p role="alert" className="text-[13px] text-bad">
            Could not save: {error.message}
          </p>
        )}
        <div className="flex flex-wrap items-center justify-between gap-2">
          {confirm ? (
            <span className="flex flex-wrap items-center gap-2">
              <span className="text-[13px]">Also removes your saved views.</span>
              <button type="button" className={btnPrimary} onClick={reset}>
                Confirm reset
              </button>
              <button type="button" className={btn} onClick={() => setConfirm(false)}>
                Keep my settings
              </button>
            </span>
          ) : (
            <button type="button" data-lt-focus="reset" className={btn} onClick={() => setConfirm(true)}>
              Reset to default
            </button>
          )}
          <button type="button" className={btnPrimary} onClick={onClose}>
            Done
          </button>
        </div>
      </div>
    </Modal>
  )
}
