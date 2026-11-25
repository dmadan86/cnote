// biome-ignore-all lint/a11y/useSemanticElements: a virtualised, editable grid cannot be a <table>
// biome-ignore-all lint/a11y/useFocusableInteractive: one focus stop on the grid, cells follow aria-activedescendant
// biome-ignore-all lint/a11y/noStaticElementInteractions: same pattern, keyboard is on the grid
// biome-ignore-all lint/correctness/useExhaustiveDependencies: effects run on cursor moves and data changes only
// biome-ignore-all lint/a11y/useKeyWithClickEvents: keyboard is handled on the grid (aria-activedescendant)
'use client'
import {
  type ColumnDef,
  columnPinningFeature,
  columnResizingFeature,
  columnSizingFeature,
  columnVisibilityFeature,
  tableFeatures,
  useTable,
} from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ChevronDown, ChevronRight, Lock, Maximize2, MoreHorizontal, Plus } from 'lucide-react'
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useShell } from '@/components/shell'
import { EmptyState, ErrorText } from '@/components/ui'
import { useScreenConfig } from '@/lib/screen-config'
import {
  bulkUpdateRecords,
  createRecord,
  type Field,
  pasteRecords,
  type Table,
  type TableRecord,
  type TableView,
  updateRecord,
} from '@/lib/tables'
import { CellEditor, CellView, canEditField, cellText, FIELD_ICONS, type Move } from '../cells'
import { parseTsv, toTsv } from './clipboard'
import { DEFAULT_VIEW_STATE, parseViewState, ROW_PX, type ViewState, viewScreen } from './state'
import { useRows } from './use-rows'

export type TableGridProps = {
  table: Table
  fields: Field[]
  view: TableView
  role: 'read' | 'write' | 'manage'
  onOpenRecord: (id: string) => void
  onFieldMenu: (fieldId: string, anchor: HTMLElement) => void
  onAddField?: () => void
  /** Optional: opens the linked-record picker for one cell. Without it a link cell opens the record. */
  onPickLinks?: (recordId: string, fieldId: string) => void
}

const features = tableFeatures({
  columnSizingFeature,
  columnResizingFeature,
  columnVisibilityFeature,
  columnPinningFeature,
})
const NO_DATA: TableRecord[] = []
const LEAD = 72
const HEAD = 36
const ADD_COL = 48
const DEFAULT_W = 180
const SAVE_MS = 800

type Item =
  | { k: 'group'; key: string; label: string; count: number; collapsed: boolean }
  | { k: 'row'; key: string; rec: TableRecord; ri: number }
  | { k: 'gadd'; key: string; raw: unknown }
  | { k: 'add'; key: string }
type Pos = { r: number; c: number }

export function TableGrid(p: TableGridProps) {
  return <Grid key={`${p.table.id}:${p.view.id}`} {...p} />
}

function Grid({
  table,
  fields: allFields,
  view,
  role,
  onOpenRecord,
  onFieldMenu,
  onAddField,
  onPickLinks,
}: TableGridProps) {
  const { ws, members } = useShell()
  const gid = useId()
  const cfg = useScreenConfig(viewScreen(view.id), DEFAULT_VIEW_STATE, parseViewState)
  const cfgRef = useRef(cfg)
  cfgRef.current = cfg
  const write = role !== 'read'

  // ---- per-person state (17.7): saved 800 ms after the last change
  const patchRef = useRef<Partial<ViewState>>({})
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const flush = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = undefined
    const patch = patchRef.current
    patchRef.current = {}
    if (Object.keys(patch).length)
      cfgRef.current.save({ ...cfgRef.current.config, ...patch, v: 1 }, true).catch(() => {})
  }, [])
  const persist = useCallback(
    (patch: Partial<ViewState>) => {
      patchRef.current = { ...patchRef.current, ...patch }
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(flush, SAVE_MS)
    },
    [flush],
  )
  useEffect(() => flush, [flush])

  const hidden = cfg.config.hidden ?? view.config.hidden ?? []
  const rowH = ROW_PX[cfg.config.row_height ?? view.config.row_height ?? 'short']
  const [sizing, setSizing] = useState<Record<string, number>>({})
  const [collapsedLocal, setCollapsed] = useState<string[] | undefined>()
  const collapsed = new Set(collapsedLocal ?? cfg.config.collapsed ?? [])

  // ---- columns: primary first, then the shared order
  const fields = useMemo(() => {
    const order = view.config.order ?? []
    const rank = (f: Field) =>
      f.id === table.primary_field_id ? -2 : order.includes(f.id) ? order.indexOf(f.id) : 1e6 + f.position
    return [...allFields].sort((a, b) => rank(a) - rank(b))
  }, [allFields, view.config.order, table.primary_field_id])
  const shown = useMemo(
    () => fields.filter((f) => f.id === table.primary_field_id || !hidden.includes(f.id)),
    [fields, hidden, table.primary_field_id],
  )
  const width = (id: string) => sizing[id] ?? cfg.config.widths?.[id] ?? view.config.widths?.[id] ?? DEFAULT_W
  const columnSizing = Object.fromEntries(fields.map((f) => [f.id, width(f.id)]))
  const columns = useMemo<ColumnDef<typeof features, TableRecord>[]>(
    () => fields.map((f) => ({ id: f.id, header: f.name, minSize: 80, maxSize: 640 })),
    [fields],
  )
  const tbl = useTable({
    features,
    columns,
    data: NO_DATA, // rows are paged and virtualised below; the table only owns column state
    columnResizeMode: 'onChange',
    state: {
      columnSizing,
      columnVisibility: Object.fromEntries(fields.map((f) => [f.id, shown.includes(f)])),
      columnPinning: { start: [table.primary_field_id], end: [] },
    },
    onColumnSizingChange: (u) => {
      const next = typeof u === 'function' ? u(columnSizing) : u
      setSizing(next)
      persist({ widths: Object.fromEntries(shown.map((f) => [f.id, next[f.id] ?? DEFAULT_W])) })
    },
  })
  const headers =
    tbl
      .getHeaderGroups()[0]
      ?.headers.filter((h) => tbl.getVisibleLeafColumns().some((c) => c.id === h.column.id)) ?? []
  const totalW = LEAD + shown.reduce((s, f) => s + width(f.id), 0) + (onAddField ? ADD_COL : 0)

  // ---- rows
  const groupField = view.config.group ? fields.find((f) => f.id === view.config.group?.field) : undefined
  const restart = JSON.stringify([view.config.filters, view.config.sorts, view.config.group])
  const rows = useRows(
    ws,
    table.id,
    { filters: view.config.filters, sorts: view.config.sorts, group: view.config.group ?? undefined },
    restart,
  )
  const { items, recs } = useMemo(() => {
    const items: Item[] = []
    const recs: TableRecord[] = []
    const counts = new Map((rows.groups ?? []).map((g) => [String(g.value ?? ''), g.count]))
    const loaded = new Map<string, number>()
    const gkey = (r: TableRecord) =>
      groupField
        ? groupField.type === 'link'
          ? (rows.links[r.id]?.[groupField.id] ?? []).map((c) => c.id).join(',')
          : String(r.data[groupField.id] ?? '')
        : ''
    if (groupField) for (const r of rows.records) loaded.set(gkey(r), (loaded.get(gkey(r)) ?? 0) + 1)
    let cur: string | undefined
    let curRaw: unknown
    const closeGroup = () =>
      cur !== undefined && !collapsed.has(cur) && items.push({ k: 'gadd', key: `ga:${cur}`, raw: curRaw })
    for (const r of rows.records) {
      const g = gkey(r)
      if (groupField && g !== cur) {
        if (write) closeGroup()
        cur = g
        curRaw = r.data[groupField.id]
        items.push({
          k: 'group',
          key: `g:${g}`,
          label: cellText(groupField, r, rows.links[r.id]?.[groupField.id], members) || 'Empty',
          count: counts.get(g) ?? loaded.get(g) ?? 0,
          collapsed: collapsed.has(g),
        })
      }
      if (groupField && collapsed.has(g)) continue
      items.push({ k: 'row', key: r.id, rec: r, ri: recs.length })
      recs.push(r)
    }
    if (groupField && write && !rows.cursor) closeGroup()
    if (write) items.push({ k: 'add', key: 'add' })
    return { items, recs }
  }, [
    rows.records,
    rows.links,
    rows.groups,
    rows.cursor,
    groupField,
    collapsedLocal,
    cfg.config.collapsed,
    members,
    write,
  ])

  const scroller = useRef<HTMLDivElement>(null)
  const itemH = (i: number) => (items[i]?.k === 'row' ? rowH : items[i]?.k === 'add' ? 40 : 32)
  const virt = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroller.current,
    estimateSize: itemH,
    getItemKey: (i) => items[i]?.key ?? i,
    overscan: 10,
  })
  useEffect(() => virt.measure(), [rowH, virt])
  const vItems = virt.getVirtualItems()
  const lastIdx = vItems.at(-1)?.index ?? 0
  useEffect(() => {
    if (lastIdx >= items.length - 30) void rows.loadMore()
  }, [lastIdx, items.length, rows.loadMore])

  // ---- selection, editing, messages
  const [act, setAct] = useState<Pos>({ r: 0, c: 0 })
  const [anchor, setAnchor] = useState<Pos>({ r: 0, c: 0 })
  const [edit, setEdit] = useState<{ rid: string; fid: string; initial?: string } | null>(null)
  const [notice, setNotice] = useState<{ error: boolean; text: string } | null>(null)
  const [bad, setBad] = useState<Record<string, string>>({}) // `${record}:${field}` -> reason
  const [live, setLive] = useState('')
  const undo = useRef<{ rid: string; fid: string; before: unknown; after: unknown; at: string } | null>(null)
  const announce = (s: string) => setLive(s)
  const focusGrid = () => scroller.current?.focus({ preventScroll: true })

  const cellId = (rid: string, fid: string) => `${gid}-${rid}-${fid}`
  const activeRec = recs[act.r]
  const activeField = shown[act.c]
  const inRange = (r: number, c: number) =>
    r >= Math.min(act.r, anchor.r) &&
    r <= Math.max(act.r, anchor.r) &&
    c >= Math.min(act.c, anchor.c) &&
    c <= Math.max(act.c, anchor.c)

  const go = (r: number, c: number, extend = false) => {
    const next = {
      r: Math.max(0, Math.min(recs.length - 1, r)),
      c: Math.max(0, Math.min(shown.length - 1, c)),
    }
    setAct(next)
    if (!extend) setAnchor(next)
    const rec = recs[next.r]
    const f = shown[next.c]
    if (rec && f)
      announce(
        `${f.name}, ${cellText(f, rec, rows.links[rec.id]?.[f.id], members) || 'empty'}, row ${next.r + 1} of ${rows.total}`,
      )
  }
  // keep the active cell on screen
  useEffect(() => {
    const idx = items.findIndex((i) => i.k === 'row' && i.ri === act.r)
    if (idx >= 0) virt.scrollToIndex(idx, { align: 'auto' })
    if (activeRec && activeField)
      document
        .getElementById(cellId(activeRec.id, activeField.id))
        ?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [act.r, act.c])

  const after = (move?: Move) => {
    setEdit(null)
    focusGrid()
    if (move === 'down') go(act.r + 1, act.c)
    else if (move === 'right') go(act.r, act.c + 1)
    else if (move === 'left') go(act.r, act.c - 1)
  }

  /** Writes one cell. Resolves to the reason when the server refused it. */
  const commit = async (
    rec: TableRecord,
    f: Field,
    value: unknown | undefined,
  ): Promise<string | undefined> => {
    const key = `${rec.id}:${f.id}`
    const before = rec.data[f.id]
    if (JSON.stringify(before) === JSON.stringify(value) || (before === undefined && value === undefined))
      return undefined
    try {
      const { links, ...saved } = await updateRecord(
        ws,
        rec.id,
        value === undefined ? { clear: [f.id] } : { values: { [f.id]: value } },
      )
      rows.put(saved, links)
      setBad(({ [key]: _, ...rest }) => rest)
      undo.current = { rid: rec.id, fid: f.id, before, after: value, at: saved.updated_at }
      announce(`${f.name} saved`)
    } catch (e) {
      const text = (e as Error).message
      setBad((b) => ({ ...b, [key]: text }))
      setNotice({ error: true, text: `${f.name} was not saved: ${text}` })
      return text
    }
  }

  const startEdit = (initial?: string) => {
    if (!activeRec || !activeField) return
    if (activeField.type === 'link')
      return onPickLinks ? onPickLinks(activeRec.id, activeField.id) : onOpenRecord(activeRec.id)
    if (!canEditField(activeField, role)) return
    if (activeField.type === 'checkbox')
      return void commit(activeRec, activeField, activeRec.data[activeField.id] === true ? undefined : true)
    setEdit({ rid: activeRec.id, fid: activeField.id, initial })
  }

  const clearRange = async () => {
    const [r0, r1, c0, c1] = [
      Math.min(act.r, anchor.r),
      Math.max(act.r, anchor.r),
      Math.min(act.c, anchor.c),
      Math.max(act.c, anchor.c),
    ]
    const ids = shown.slice(c0, c1 + 1).filter((f) => canEditField(f, role))
    const updates = recs
      .slice(r0, r1 + 1)
      .map((r) => ({ id: r.id, clear: ids.filter((f) => r.data[f.id] !== undefined).map((f) => f.id) }))
      .filter((u) => u.clear.length)
    if (!updates.length) return
    try {
      await bulkUpdateRecords(ws, table.id, updates.slice(0, 1000))
      await rows.refresh()
      announce(`Cleared ${updates.reduce((n, u) => n + u.clear.length, 0)} cells`)
    } catch (e) {
      setNotice({ error: true, text: `Not cleared: ${(e as Error).message}` })
    }
  }

  const undoLast = async () => {
    const u = undo.current
    const rec = recs.find((r) => r.id === u?.rid)
    const f = fields.find((x) => x.id === u?.fid)
    if (!u || !rec || !f) return
    // only while the cell still holds the value this person wrote (17.4)
    if (rec.updated_at !== u.at || JSON.stringify(rec.data[f.id]) !== JSON.stringify(u.after))
      return setNotice({ error: true, text: 'Not undone: someone changed this cell since.' })
    undo.current = null
    await commit(rec, f, u.before)
    undo.current = null
    announce(`Undid the change to ${f.name}`)
  }

  const addRecord = async (values?: Record<string, unknown>) => {
    try {
      const { links, ...rec } = await createRecord(ws, table.id, values ?? {})
      rows.put(rec, links, true)
      setAct({ r: recs.length, c: 0 })
      setAnchor({ r: recs.length, c: 0 })
      announce('Record added')
      focusGrid()
    } catch (e) {
      setNotice({ error: true, text: `Record not added: ${(e as Error).message}` })
    }
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (edit) return
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z' && !e.shiftKey) {
      e.preventDefault()
      return void undoLast()
    }
    if (e.ctrlKey || e.metaKey || e.altKey) return // copy and paste arrive as clipboard events
    const move = (r: number, c: number) => {
      e.preventDefault()
      go(r, c, e.shiftKey)
    }
    switch (e.key) {
      case 'ArrowDown':
        return move(act.r + 1, act.c)
      case 'ArrowUp':
        return move(act.r - 1, act.c)
      case 'ArrowRight':
        return move(act.r, act.c + 1)
      case 'ArrowLeft':
        return move(act.r, act.c - 1)
      case 'Home':
        return move(act.r, 0)
      case 'End':
        return move(act.r, shown.length - 1)
      case 'PageDown':
        return move(act.r + 10, act.c)
      case 'PageUp':
        return move(act.r - 10, act.c)
      case 'Tab': {
        const c = act.c + (e.shiftKey ? -1 : 1)
        if (c < 0 || c >= shown.length) {
          const r = act.r + (e.shiftKey ? -1 : 1)
          if (r < 0 || r >= recs.length) return // let focus leave the grid
          e.preventDefault()
          return go(r, e.shiftKey ? shown.length - 1 : 0)
        }
        e.preventDefault()
        return go(act.r, c)
      }
      case 'Enter':
      case 'F2':
        e.preventDefault()
        return startEdit()
      case 'Escape':
        return setAnchor(act)
      case 'Delete':
      case 'Backspace':
        if (write) {
          e.preventDefault()
          void clearRange()
        }
        return
      case ' ':
        e.preventDefault()
        if (e.shiftKey && activeRec) onOpenRecord(activeRec.id)
        else if (activeField?.type === 'checkbox') startEdit()
        return
      default:
        if (
          write &&
          e.key.length === 1 &&
          activeField &&
          !['checkbox', 'link'].includes(activeField.type) &&
          canEditField(activeField, role)
        ) {
          e.preventDefault()
          startEdit(e.key)
        }
    }
  }

  // ---- clipboard
  const onCopy = (e: React.ClipboardEvent) => {
    if (edit || !recs.length) return
    const [r0, r1, c0, c1] = [
      Math.min(act.r, anchor.r),
      Math.max(act.r, anchor.r),
      Math.min(act.c, anchor.c),
      Math.max(act.c, anchor.c),
    ]
    const grid = recs
      .slice(r0, r1 + 1)
      .map((r) => shown.slice(c0, c1 + 1).map((f) => cellText(f, r, rows.links[r.id]?.[f.id], members)))
    e.clipboardData.setData('text/plain', toTsv(grid))
    e.preventDefault()
    announce(`Copied ${grid.length * (c1 - c0 + 1)} cells`)
  }
  const onPaste = async (e: React.ClipboardEvent) => {
    if (edit || !write) return
    e.preventDefault()
    const data = parseTsv(e.clipboardData.getData('text/plain'))
    if (!data.length) return
    if (data.length > 1000)
      return setNotice({ error: true, text: 'Pasting is limited to 1,000 rows. Use Import for more.' })
    const width = Math.min(50, shown.length - act.c, Math.max(...data.map((r) => r.length)))
    const ids = shown.slice(act.c, act.c + width).map((f) => f.id)
    const start = recs.length ? act.r : 0
    try {
      const res = await pasteRecords(
        ws,
        table.id,
        ids,
        data.map((cells, i) => ({ id: recs[start + i]?.id, cells: cells.slice(0, width) })),
      )
      const marks: Record<string, string> = {}
      for (const x of res.errors) {
        const rid = recs[start + x.row]?.id
        if (rid && ids[x.col]) marks[`${rid}:${ids[x.col]}`] = x.reason
      }
      setBad(marks)
      const first = res.errors[0]
      const names = (x: { row: number; col: number }) =>
        `row ${start + x.row + 1}, ${shown[act.c + x.col]?.name ?? 'column'}`
      const text = `Pasted: ${res.updated} updated, ${res.created} added.${first ? ` ${res.errors.length} cells were not pasted, for example ${names(first)}: ${first.reason}` : ''}`
      setNotice({ error: res.errors.length > 0, text })
      announce(text)
      await rows.refresh()
    } catch (err) {
      setNotice({ error: true, text: `Paste failed: ${(err as Error).message}` })
    }
  }

  // ---- render
  const filtered = (view.config.filters?.rules.length ?? 0) > 0
  const empty = rows.status === 'ready' && rows.records.length === 0
  const cellCls =
    'relative flex shrink-0 items-center gap-1 overflow-hidden border-b border-r border-line px-2 text-[13px]'
  const clamp = rowH > 32 ? 'whitespace-normal [overflow-wrap:anywhere]' : 'whitespace-nowrap'

  return (
    <div className="flex h-full min-h-80 flex-col">
      <div
        ref={scroller}
        role="grid"
        tabIndex={0}
        aria-label={`${table.name} records`}
        aria-rowcount={rows.total + 1}
        aria-colcount={shown.length + 1}
        aria-readonly={!write}
        aria-multiselectable="true"
        aria-activedescendant={
          activeRec && activeField && !edit ? cellId(activeRec.id, activeField.id) : undefined
        }
        onKeyDown={onKeyDown}
        onCopy={onCopy}
        onPaste={onPaste}
        style={{ scrollPaddingLeft: LEAD + width(table.primary_field_id), scrollPaddingTop: HEAD }}
        className="relative min-h-0 flex-1 overflow-auto bg-canvas outline-none focus-visible:ring-2 focus-visible:ring-accent"
      >
        <div style={{ width: totalW }}>
          <div
            role="row"
            aria-rowindex={1}
            style={{ height: HEAD }}
            className="sticky top-0 z-20 flex border-b border-line bg-side"
          >
            <div
              role="columnheader"
              style={{ width: LEAD }}
              className="sticky left-0 z-30 flex shrink-0 items-center border-r border-line bg-side px-2 text-[12px] text-muted"
            >
              #
            </div>
            {headers.map((h, i) => {
              const f = fields.find((x) => x.id === h.column.id)
              if (!f) return null
              const Icon = FIELD_ICONS[f.type]
              return (
                <div
                  key={f.id}
                  role="columnheader"
                  aria-colindex={i + 2}
                  style={{
                    width: h.getSize(),
                    ...(i === 0 ? { position: 'sticky', left: LEAD, zIndex: 25 } : {}),
                  }}
                  className="relative flex shrink-0 items-center gap-1.5 border-r border-line bg-side px-2 text-[12px] font-medium"
                >
                  <Icon size={14} aria-hidden="true" className="shrink-0 text-muted" />
                  <span className="truncate">{f.name}</span>
                  {f.locked ? <Lock size={12} aria-label="Locked" className="shrink-0 text-muted" /> : null}
                  <button
                    type="button"
                    aria-label={`Field menu: ${f.name}`}
                    onClick={(e) => onFieldMenu(f.id, e.currentTarget)}
                    className="ml-auto rounded p-0.5 text-muted hover:bg-hover"
                  >
                    <MoreHorizontal size={14} aria-hidden="true" />
                  </button>
                  <div
                    role="separator"
                    aria-orientation="vertical"
                    aria-label={`Resize ${f.name}`}
                    onMouseDown={h.getResizeHandler()}
                    onTouchStart={h.getResizeHandler()}
                    className="absolute right-0 top-0 h-full w-1.5 cursor-col-resize hover:bg-accent/40"
                  />
                </div>
              )
            })}
            {onAddField ? (
              <div
                role="columnheader"
                style={{ width: ADD_COL }}
                className="flex shrink-0 items-center justify-center bg-side"
              >
                <button
                  type="button"
                  aria-label="Add a field"
                  onClick={onAddField}
                  className="rounded p-1 text-muted hover:bg-hover"
                >
                  <Plus size={14} aria-hidden="true" />
                </button>
              </div>
            ) : null}
          </div>

          <div style={{ height: virt.getTotalSize(), position: 'relative' }}>
            {vItems.map((v) => {
              const it = items[v.index]
              if (!it) return null
              const pos = {
                position: 'absolute' as const,
                top: 0,
                left: 0,
                width: totalW,
                height: v.size,
                transform: `translateY(${v.start}px)`,
              }
              if (it.k === 'group')
                return (
                  <div
                    key={it.key}
                    role="row"
                    style={pos}
                    className="flex items-center border-b border-line bg-side"
                  >
                    <div
                      role="gridcell"
                      aria-colspan={shown.length + 1}
                      className="sticky left-0 flex items-center"
                    >
                      <button
                        type="button"
                        aria-expanded={!it.collapsed}
                        onClick={() => {
                          const key = it.key.slice(2)
                          const next = it.collapsed
                            ? [...collapsed].filter((x) => x !== key)
                            : [...collapsed, key]
                          setCollapsed(next)
                          persist({ collapsed: next })
                        }}
                        className="flex items-center gap-1.5 px-2 py-1 text-[13px] font-medium hover:bg-hover"
                      >
                        {it.collapsed ? (
                          <ChevronRight size={14} aria-hidden="true" />
                        ) : (
                          <ChevronDown size={14} aria-hidden="true" />
                        )}
                        {it.label}
                        <span className="font-normal text-muted">{it.count}</span>
                      </button>
                    </div>
                  </div>
                )
              if (it.k === 'gadd')
                return (
                  <div key={it.key} role="row" style={pos} className="flex items-center border-b border-line">
                    <div role="gridcell" aria-colspan={shown.length + 1} className="sticky left-0">
                      <button
                        type="button"
                        onClick={() =>
                          void addRecord(
                            groupField && it.raw !== undefined ? { [groupField.id]: it.raw } : undefined,
                          )
                        }
                        className="px-3 py-1 text-[13px] text-muted hover:text-fg"
                      >
                        + Add a record
                      </button>
                    </div>
                  </div>
                )
              if (it.k === 'add')
                return (
                  <div key={it.key} role="row" style={pos} className="flex items-center border-b border-line">
                    <div role="gridcell" aria-colspan={shown.length + 1} className="sticky left-0">
                      <button
                        type="button"
                        onClick={() => void addRecord()}
                        className="flex items-center gap-1.5 px-3 py-2 text-[13px] text-muted hover:text-fg"
                      >
                        <Plus size={14} aria-hidden="true" /> Add a record
                      </button>
                    </div>
                  </div>
                )
              const rec = it.rec
              return (
                <div key={it.key} role="row" aria-rowindex={it.ri + 2} style={pos} className="group flex">
                  <div
                    role="rowheader"
                    style={{ width: LEAD }}
                    className={`${cellCls} sticky left-0 z-10 justify-between bg-canvas text-[12px] text-muted`}
                  >
                    <span>{rec.seq}</span>
                    <button
                      type="button"
                      aria-label={`Open record ${rec.seq}`}
                      onClick={() => onOpenRecord(rec.id)}
                      className="rounded p-0.5 hover:bg-hover hover:text-fg"
                    >
                      <Maximize2 size={13} aria-hidden="true" />
                    </button>
                  </div>
                  {shown.map((f, c) => {
                    const chips = rows.links[rec.id]?.[f.id]
                    const isAct = act.r === it.ri && act.c === c
                    const sel = inRange(it.ri, c)
                    const editing = edit?.rid === rec.id && edit.fid === f.id
                    const reason = bad[`${rec.id}:${f.id}`]
                    const editable = canEditField(f, role)
                    return (
                      <div
                        key={f.id}
                        id={cellId(rec.id, f.id)}
                        role="gridcell"
                        aria-colindex={c + 2}
                        aria-selected={sel}
                        aria-readonly={!editable}
                        aria-invalid={!!reason}
                        title={reason}
                        style={{
                          width: width(f.id),
                          ...(c === 0 ? { position: 'sticky', left: LEAD, zIndex: 9 } : {}),
                        }}
                        onClick={(e) => {
                          setAct({ r: it.ri, c })
                          if (!e.shiftKey) setAnchor({ r: it.ri, c })
                          if (activeField?.type !== 'checkbox' || f.type !== 'checkbox') focusGrid()
                        }}
                        onDoubleClick={() => {
                          setAct({ r: it.ri, c })
                          if (f.type === 'link')
                            return onPickLinks ? onPickLinks(rec.id, f.id) : onOpenRecord(rec.id)
                          if (canEditField(f, role) && f.type !== 'checkbox')
                            setEdit({ rid: rec.id, fid: f.id })
                        }}
                        className={`${cellCls} ${clamp} ${reason ? 'border-bad bg-bad-bg' : sel ? 'bg-accent/10' : 'bg-canvas'} ${isAct ? 'outline outline-2 -outline-offset-2 outline-accent' : ''}`}
                      >
                        {editing ? (
                          <CellEditor
                            field={f}
                            value={rec.data[f.id]}
                            initial={edit.initial}
                            onCancel={() => after()}
                            onCommit={async (value, move) => {
                              const err = await commit(rec, f, value)
                              if (!err) after(move)
                              return err
                            }}
                          />
                        ) : (
                          <CellView
                            field={f}
                            record={rec}
                            chips={chips}
                            members={members}
                            active={isAct}
                            readOnly={!editable}
                            onToggle={(on) => void commit(rec, f, on ? true : undefined)}
                            onPickLinks={() =>
                              onPickLinks ? onPickLinks(rec.id, f.id) : onOpenRecord(rec.id)
                            }
                          />
                        )}
                      </div>
                    )
                  })}
                </div>
              )
            })}
          </div>

          {rows.status === 'loading' ? (
            <p role="status" className="px-3 py-6 text-[13px] text-muted">
              Loading records
            </p>
          ) : null}
          {empty ? (
            <EmptyState
              icon={Plus}
              title={filtered ? 'No records match these filters.' : 'No records yet'}
              compact
            >
              {filtered
                ? null
                : write
                  ? 'Add a record, or paste from Excel (Ctrl+V) after selecting a cell.'
                  : null}
            </EmptyState>
          ) : null}
        </div>
      </div>

      <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-t border-line px-3 py-1.5 text-[12px] text-muted">
        <span>
          {rows.total} {rows.total === 1 ? 'record' : 'records'}
          {rows.records.length < rows.total ? ` (${rows.records.length} loaded)` : ''}
        </span>
        {rows.status === 'error' ? (
          <span className="flex items-center gap-2">
            <ErrorText error={rows.error} />
            <button type="button" onClick={() => void rows.refresh()} className="text-accent">
              Retry
            </button>
          </span>
        ) : null}
        {cfg.error ? (
          <span role="alert" className="text-bad">
            Your layout couldn't be saved: {cfg.error.message}
          </span>
        ) : null}
        {notice ? (
          <span role={notice.error ? 'alert' : 'status'} className={notice.error ? 'text-bad' : ''}>
            {notice.text}{' '}
            <button type="button" onClick={() => setNotice(null)} className="text-accent">
              Dismiss
            </button>
          </span>
        ) : null}
      </div>
      <div role="status" aria-live="polite" className="sr-only">
        {live}
      </div>
    </div>
  )
}
