'use client'
import {
  DndContext,
  type DragEndEvent,
  type DragMoveEvent,
  DragOverlay,
  MouseSensor,
  useDraggable,
  useSensor,
  useSensors,
} from '@dnd-kit/core'
import { createContext, useContext, useRef, useState } from 'react'
import { useShell } from '../shell'
import { useTreeActions } from './page-actions'
import { kidKey, liveParent, parentOfKey, subtreeIds } from './tree-model'

export type Zone = 'before' | 'into' | 'after'
type Drop = { id: string; zone: Zone }
const DropCtx = createContext<Drop | null>(null)

/** Space header rows are drop targets for "top level of the space". */
export const spaceRowId = (spaceId: string) => `space:${spaceId}`

/**
 * Drag to move and reorder pages. Mouse only: touch keeps scrolling, and phones use "Move…" (the keyboard and touch
 * alternative). The pointer is hit-tested against rows: top third drops before, middle drops inside (last child),
 * bottom third drops after.
 */
export function TreeDnd({
  onOpen,
  children,
}: {
  /** Called with the page a drag dropped into, so the tree can unfold it. */
  onOpen: (id: string) => void
  children: React.ReactNode
}) {
  const { kids, byId, place, announce } = useTreeActions()
  const { tree } = useShell()
  const sensors = useSensors(useSensor(MouseSensor, { activationConstraint: { distance: 6 } }))
  const [drop, setDrop] = useState<Drop | null>(null)
  const [dragging, setDragging] = useState<string | null>(null)
  const last = useRef<Drop | null>(null)
  const set = (d: Drop | null) => {
    last.current = d
    setDrop(d)
  }

  function target(e: DragMoveEvent): Drop | null {
    const me = e.active.id as string
    const src = byId.get(me)
    const ev = e.activatorEvent as MouseEvent
    if (!src) return null
    const x = ev.clientX + e.delta.x
    const y = ev.clientY + e.delta.y
    const row = document
      .elementsFromPoint(x, y)
      .map((el) => el.closest<HTMLElement>('[data-row-id]'))
      .find(Boolean)
    if (!row) return null
    const id = row.dataset.rowId as string
    if (row.dataset.rowSpace !== src.space_id || row.dataset.rowShared) return null // the Shared with you level is not a place to drop
    if (id.startsWith('space:')) return { id, zone: 'into' }
    if (subtreeIds(kids, me).has(id)) return null // not onto itself or its own sub-pages
    const r = row.getBoundingClientRect()
    const f = (y - r.top) / r.height
    return { id, zone: f < 0.3 ? 'before' : f > 0.7 ? 'after' : 'into' }
  }

  async function end(e: DragEndEvent) {
    const d = last.current
    set(null)
    setDragging(null)
    const n = byId.get(e.active.id as string)
    if (!d || !n) return
    let parent: string | null
    let before: string | null = null
    if (d.id.startsWith('space:')) parent = null
    else {
      const t = byId.get(d.id)
      if (!t) return
      if (d.zone === 'into') parent = t.id
      else {
        const key = tree.keyOf.get(t.id) ?? kidKey(t.space_id, liveParent(t, byId))
        parent = parentOfKey(key)
        const sibs = (kids.get(key) ?? []).filter((s) => s.id !== n.id)
        const i = sibs.findIndex((s) => s.id === t.id)
        before = d.zone === 'before' ? t.id : (sibs[i + 1]?.id ?? null)
        // the target is the last loaded row of a longer level: the page after it is not loaded yet
        if (d.zone === 'after' && !before && tree.levels.get(key)?.next)
          before = (await tree.peek(t.space_id, parent, 'next', 2)).find((r) => r.id !== n.id)?.id ?? null
      }
    }
    try {
      await place(n, parent, before)
      if (parent) onOpen(parent)
    } catch (err) {
      announce((err as Error).message)
    }
  }
  const title = dragging ? (byId.get(dragging)?.title ?? '') || 'Untitled' : ''
  return (
    <DndContext
      sensors={sensors}
      onDragStart={(e) => setDragging(e.active.id as string)}
      onDragMove={(e) => set(target(e))}
      onDragEnd={end}
      onDragCancel={() => {
        set(null)
        setDragging(null)
      }}
    >
      <DropCtx.Provider value={drop}>{children}</DropCtx.Provider>
      <DragOverlay dropAnimation={null}>
        {dragging && (
          <div className="w-48 truncate rounded border border-accent bg-canvas px-2 py-1 text-[13px] shadow-lg">
            {title}
          </div>
        )}
      </DragOverlay>
    </DndContext>
  )
}

/** Drag handle props for a row, plus the drop marker for it. */
export function useRowDnd(id: string) {
  const { setNodeRef, listeners, isDragging } = useDraggable({ id })
  const drop = useContext(DropCtx)
  return { setNodeRef, listeners, isDragging, zone: drop?.id === id ? drop.zone : null }
}
export const useDropZone = (id: string) => {
  const drop = useContext(DropCtx)
  return drop?.id === id ? drop.zone : null
}

/** The line or outline that shows where the page will land. */
export function DropMarker({ zone }: { zone: Zone | null }) {
  if (!zone) return null
  return (
    <span
      aria-hidden
      className={
        zone === 'into'
          ? 'pointer-events-none absolute inset-0 rounded ring-2 ring-accent'
          : `pointer-events-none absolute inset-x-0 h-0.5 bg-accent ${zone === 'before' ? 'top-0' : 'bottom-0'}`
      }
    />
  )
}
