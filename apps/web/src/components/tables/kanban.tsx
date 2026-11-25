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
import { GripVertical, Kanban as KanbanIcon, Plus } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { btn, EmptyState, ErrorText, inputCls } from '@/components/ui'
import {
  createRecord,
  type Field,
  type LinkChip,
  listRecords,
  type Role,
  type Rule,
  type Table,
  type TableRecord,
  type TableView,
  updateRecord,
} from '@/lib/tables'
import { chipCls } from './field-dialog'
import { useDisplay } from './record-panel'

const PAGE = 50
type Col = {
  records: TableRecord[]
  links: Record<string, Record<string, LinkChip[]>>
  next: string | null
  total: number
}
const NONE = ''

export function Kanban({
  ws,
  table,
  fields,
  view,
  role,
  refresh,
  onOpenRecord,
}: {
  ws: string
  table: Table
  fields: Field[]
  view: TableView
  role: Role
  refresh: number
  onOpenRecord: (id: string) => void
}) {
  const stack = fields.find((f) => f.id === view.config.kanban?.stack && f.type === 'single_select')
  const cardFields = (view.config.kanban?.card_fields ?? [])
    .map((id) => fields.find((f) => f.id === id))
    .filter(Boolean) as Field[]
  const [cols, setCols] = useState<Record<string, Col>>({})
  const [err, setErr] = useState<string>()
  const [note, setNote] = useState('')
  const display = useDisplay()
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  )
  const canMove = role !== 'read' && !!stack && (!stack.locked || role === 'manage')
  const cfgKey = JSON.stringify([view.config.filters, view.config.sorts, stack?.id, stack?.config.options])
  const seen = useRef(cfgKey)

  const keys = useMemo(() => (stack ? [NONE, ...(stack.config.options ?? []).map((o) => o.id)] : []), [stack])
  const f = view.config.filters
  const orMixed = !!f && f.match === 'or' && f.rules.length > 1

  const fetchCol = useCallback(
    async (key: string, cursor?: string) => {
      if (!stack) return
      const rule: Rule =
        key === NONE ? { field: stack.id, op: 'empty' } : { field: stack.id, op: 'any_of', value: [key] }
      const r = await listRecords(ws, table.id, {
        filters: { match: 'and', rules: [...(f?.rules ?? []), rule] },
        sorts: view.config.sorts,
        limit: PAGE,
        cursor,
      })
      setCols((c) => {
        const old = cursor ? c[key] : undefined
        return {
          ...c,
          [key]: {
            records: [...(old?.records ?? []), ...r.records],
            links: { ...(old?.links ?? {}), ...r.links },
            next: r.next_cursor,
            total: r.total,
          },
        }
      })
    },
    [ws, table.id, stack, f, view.config.sorts],
  )
  const loadAll = useCallback(() => {
    if (!stack || orMixed) return
    setErr(undefined)
    Promise.all(keys.map((k) => fetchCol(k))).catch((e) => setErr((e as Error).message))
  }, [stack, orMixed, fetchCol, keys])
  // biome-ignore lint/correctness/useExhaustiveDependencies: refresh and cfgKey are the triggers
  useEffect(() => {
    if (seen.current !== cfgKey) {
      seen.current = cfgKey
      setCols({})
    }
    loadAll()
  }, [loadAll, refresh, cfgKey])

  async function move(id: string, from: string, to: string) {
    if (!stack || from === to) return
    const rec = cols[from]?.records.find((r) => r.id === id)
    if (!rec) return
    setCols((c) => ({
      ...c,
      [from]: { ...c[from], records: c[from].records.filter((r) => r.id !== id), total: c[from].total - 1 },
      [to]: {
        ...c[to],
        records: [{ ...rec, data: { ...rec.data, [stack.id]: to || undefined } }, ...c[to].records],
        total: c[to].total + 1,
      },
    }))
    const name = stack.config.options?.find((o) => o.id === to)?.name ?? `No ${stack.name.toLowerCase()}`
    try {
      await updateRecord(ws, id, to ? { values: { [stack.id]: to } } : { clear: [stack.id] })
      setNote(`Moved to ${name}`)
    } catch (x) {
      setErr((x as Error).message)
      loadAll()
    }
  }
  function onDragEnd(e: DragEndEvent) {
    const from = e.active.data.current?.col as string | undefined
    if (e.over && from !== undefined) move(String(e.active.id), from, String(e.over.id).replace(/^col:/, ''))
  }

  if (!stack)
    return (
      <EmptyState icon={KanbanIcon} title="Choose how to stack this board">
        {fields.some((x) => x.type === 'single_select')
          ? 'Pick a single select field under "Stack by". Each of its options becomes a column.'
          : 'Add a single select field to the table first. Each of its options becomes a column.'}
      </EmptyState>
    )
  if (orMixed)
    return (
      <EmptyState icon={KanbanIcon} title="This board cannot use an any-of filter">
        Change the filter to match all rules, or remove all but one rule.
      </EmptyState>
    )

  const title = (r: TableRecord) => String(r.data[table.primary_field_id] ?? '') || `Record ${r.seq}`
  return (
    <div className="space-y-2">
      <ErrorText error={err} />
      <p role="status" className="sr-only">
        {note}
      </p>
      <DndContext sensors={sensors} onDragEnd={onDragEnd}>
        <div className="flex items-start gap-3 overflow-x-auto pb-3">
          {keys.map((k) => {
            const opt = stack.config.options?.find((o) => o.id === k)
            const col = cols[k]
            if (k === NONE && col && col.total === 0) return null // an empty "no value" column is just noise
            return (
              <Column
                key={k}
                colKey={k}
                label={opt?.name ?? `No ${stack.name.toLowerCase()}`}
                color={opt?.color ?? 'gray'}
                count={col?.total}
                droppable={canMove}
              >
                {col?.records.map((r) => (
                  <Card
                    key={r.id}
                    r={r}
                    col={k}
                    title={title(r)}
                    draggable={canMove}
                    onOpen={() => onOpenRecord(r.id)}
                    lines={cardFields.flatMap((cf) => {
                      const chips = col.links[r.id]?.[cf.id]
                      const v =
                        cf.type === 'link'
                          ? chips?.map((c) => c.title).join(', ')
                          : r.data[cf.id] == null
                            ? ''
                            : display(cf, r.data[cf.id])
                      return v ? [[cf.name, v] as [string, string]] : []
                    })}
                    moveTo={
                      canMove
                        ? [
                            [NONE, `No ${stack.name.toLowerCase()}`],
                            ...(stack.config.options ?? []).map((o) => [o.id, o.name] as [string, string]),
                          ]
                        : undefined
                    }
                    onMove={(to) => move(r.id, k, to)}
                  />
                ))}
                {col?.next && (
                  <button
                    type="button"
                    className={`${btn} w-full`}
                    onClick={() => fetchCol(k, col.next as string).catch((e) => setErr((e as Error).message))}
                  >
                    Show more
                  </button>
                )}
                {role !== 'read' && (
                  <AddCard
                    table={table}
                    stackId={stack.id}
                    optId={k}
                    ws={ws}
                    onAdded={() => fetchCol(k).catch((e) => setErr((e as Error).message))}
                  />
                )}
              </Column>
            )
          })}
        </div>
      </DndContext>
    </div>
  )
}

function Column({
  colKey,
  label,
  color,
  count,
  droppable,
  children,
}: {
  colKey: string
  label: string
  color: string
  count?: number
  droppable: boolean
  children: React.ReactNode
}) {
  const { setNodeRef, isOver } = useDroppable({ id: `col:${colKey}`, disabled: !droppable })
  return (
    <section
      ref={setNodeRef}
      aria-label={`${label} column`}
      className={`flex w-64 shrink-0 flex-col rounded-md bg-side ${isOver ? 'ring-2 ring-accent' : ''}`}
    >
      <h3 className="flex items-center gap-2 px-3 py-2 text-[12px] font-semibold">
        <span className={`rounded-full px-2 py-0.5 ${chipCls(color)}`}>{label}</span>
        <span className="font-normal text-muted">{count ?? ''}</span>
      </h3>
      <ul className="min-h-12 space-y-2 px-2 pb-2">{children}</ul>
    </section>
  )
}

function Card({
  r,
  col,
  title,
  lines,
  draggable,
  moveTo,
  onMove,
  onOpen,
}: {
  r: TableRecord
  col: string
  title: string
  lines: [string, string][]
  draggable: boolean
  moveTo?: [string, string][]
  onMove: (to: string) => void
  onOpen: () => void
}) {
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({
    id: r.id,
    data: { col },
    disabled: !draggable,
  })
  return (
    <li
      ref={setNodeRef}
      style={{ transform: transform ? `translate(${transform.x}px, ${transform.y}px)` : undefined }}
      className={`space-y-1.5 rounded-md border border-line bg-canvas p-2.5 shadow-sm ${isDragging ? 'relative z-20 shadow-lg' : ''}`}
    >
      <div className="flex items-start gap-1">
        {draggable && (
          <button
            type="button"
            aria-label={`Drag ${title}`}
            {...attributes}
            {...listeners}
            className="cursor-grab rounded p-0.5 text-muted hover:bg-hover"
          >
            <GripVertical size={14} />
          </button>
        )}
        <button
          type="button"
          onClick={onOpen}
          className="min-w-0 flex-1 text-left text-[13px] font-medium hover:underline"
        >
          {title}
        </button>
      </div>
      {lines.map(([k, v]) => (
        <p key={k} className="truncate text-[12px]">
          <span className="text-muted">{k} </span>
          {v}
        </p>
      ))}
      {moveTo && (
        <select
          aria-label={`Move ${title} to`}
          value={col}
          onChange={(e) => onMove(e.target.value)}
          className="w-full rounded border border-line bg-canvas px-1 py-0.5 text-[12px] text-muted"
        >
          {moveTo.map(([id, name]) => (
            <option key={id} value={id}>
              {id === col ? `In ${name}` : `Move to ${name}`}
            </option>
          ))}
        </select>
      )}
    </li>
  )
}

function AddCard({
  ws,
  table,
  stackId,
  optId,
  onAdded,
}: {
  ws: string
  table: Table
  stackId: string
  optId: string
  onAdded: () => void
}) {
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [err, setErr] = useState<string>()
  if (!open)
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex w-full items-center gap-1 rounded px-1 py-1 text-[13px] text-muted hover:bg-hover"
      >
        <Plus size={14} /> Add
      </button>
    )
  return (
    <form
      className="space-y-1"
      onSubmit={async (e) => {
        e.preventDefault()
        try {
          await createRecord(ws, table.id, {
            ...(text.trim() ? { [table.primary_field_id]: text.trim() } : {}),
            ...(optId ? { [stackId]: optId } : {}),
          })
          setText('')
          setOpen(false)
          onAdded()
        } catch (x) {
          setErr((x as Error).message)
        }
      }}
    >
      <input
        aria-label="New record name"
        // biome-ignore lint/a11y/noAutofocus: opened by the Add button
        autoFocus
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}
        className={`${inputCls} py-1`}
      />
      <ErrorText error={err} />
    </form>
  )
}
