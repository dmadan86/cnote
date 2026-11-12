'use client'
import type { Node as PMNode } from '@tiptap/pm/model'
import { TextSelection } from '@tiptap/pm/state'
import { TableMap } from '@tiptap/pm/tables'
import type { Editor } from '@tiptap/react'
import {
  ArrowDownWideNarrow,
  ArrowUpNarrowWide,
  BetweenHorizontalEnd,
  BetweenHorizontalStart,
  BetweenVerticalEnd,
  BetweenVerticalStart,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  GripHorizontal,
  GripVertical,
  Heading,
  TableCellsMerge,
  TableCellsSplit,
  Trash2,
  X,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'

const pop = 'rounded-lg border border-line bg-canvas p-1 text-fg shadow-xl'
const item = 'flex w-full items-center gap-2.5 rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover'

/** Table + cursor cell position, or null outside a table. With merged cells row/col are child indexes, not grid columns. */
function locate(editor: Editor) {
  const { $from } = editor.state.selection
  for (let d = $from.depth; d > 0; d--) {
    const node = $from.node(d)
    if (node.type.name === 'table')
      return {
        table: node,
        pos: $from.before(d),
        row: $from.index(d),
        col: $from.index(d + 1),
        cellPos: $from.depth > d + 1 ? $from.before(d + 2) : -1,
      }
  }
  return null
}

/** Replace the table with rebuilt rows, keeping the cursor in cell (row, col). */
function rebuild(editor: Editor, rows: PMNode[], row: number, col: number) {
  const at = locate(editor)
  if (!at) return
  const table = at.table.type.create(at.table.attrs, rows)
  const tr = editor.state.tr.replaceWith(at.pos, at.pos + at.table.nodeSize, table)
  const map = TableMap.get(table)
  const cell = at.pos + 1 + map.positionAt(Math.min(row, map.height - 1), Math.min(col, map.width - 1), table)
  editor.view.dispatch(tr.setSelection(TextSelection.near(tr.doc.resolve(cell + 1))).scrollIntoView())
  editor.view.focus()
}

const rowsOf = (t: PMNode) => Array.from({ length: t.childCount }, (_, i) => t.child(i))
const cellsOf = (r: PMNode) => Array.from({ length: r.childCount }, (_, i) => r.child(i))
/** Sort/move/clear index cells by child position, which is wrong once a cell spans rows or columns. */
const hasMerge = (t: PMNode) =>
  rowsOf(t).some((r) => cellsOf(r).some((c) => c.attrs.colspan > 1 || c.attrs.rowspan > 1))
const isHeader = (r: PMNode) => r.firstChild?.type.name === 'tableHeader'

export function sortRows(editor: Editor, dir: 1 | -1) {
  const at = locate(editor)
  if (!at || hasMerge(at.table)) return
  const rows = rowsOf(at.table)
  const head = rows.filter(isHeader)
  const body = rows.filter((r) => !isHeader(r))
  const key = (r: PMNode) => r.child(at.col).textContent.trim()
  body.sort((a, b) => {
    const [x, y] = [key(a), key(b)]
    if (!x || !y) return x ? -1 : y ? 1 : 0 // empty cells last either way
    return dir * x.localeCompare(y, undefined, { numeric: true, sensitivity: 'base' })
  })
  rebuild(editor, [...head, ...body], at.row, at.col)
}

export function moveRow(editor: Editor, by: 1 | -1) {
  const at = locate(editor)
  if (!at || hasMerge(at.table)) return
  const rows = rowsOf(at.table)
  const to = at.row + by
  if (to < 0 || to >= rows.length || isHeader(rows[at.row]) !== isHeader(rows[to])) return
  ;[rows[at.row], rows[to]] = [rows[to], rows[at.row]]
  rebuild(editor, rows, to, at.col)
}

export function moveColumn(editor: Editor, by: 1 | -1) {
  const at = locate(editor)
  if (!at || hasMerge(at.table)) return
  const to = at.col + by
  if (to < 0 || to >= at.table.child(0).childCount) return
  const rows = rowsOf(at.table).map((r) => {
    const c = cellsOf(r)
    ;[c[at.col], c[to]] = [c[to], c[at.col]]
    return r.type.create(r.attrs, c)
  })
  rebuild(editor, rows, at.row, to)
}

function clearCells(editor: Editor, what: 'row' | 'col') {
  const at = locate(editor)
  if (!at || hasMerge(at.table)) return
  const empty = (c: PMNode) => c.type.createAndFill(c.attrs) ?? c
  const rows = rowsOf(at.table).map((r, i) =>
    r.type.create(
      r.attrs,
      cellsOf(r).map((c, j) => ((what === 'row' ? i === at.row : j === at.col) ? empty(c) : c)),
    ),
  )
  rebuild(editor, rows, at.row, at.col)
}

/** Fixed-position rects of the table and the cursor's cell, kept in sync with edits, scroll and resize. */
function useTableRects(editor: Editor, keep: React.RefObject<boolean>) {
  const [r, setR] = useState<{ table: DOMRect; cell: DOMRect; header: boolean } | null>(null)
  const update = useCallback(() => {
    const at = locate(editor)
    // An open menu holds focus, so the editor is blurred while it is up.
    if (!at || !(editor.isFocused || keep.current)) return setR(null)
    const dom = editor.view.nodeDOM(at.pos) as HTMLElement | null
    const table = dom?.querySelector('table') ?? dom
    const cell = editor.view.nodeDOM(at.cellPos) as HTMLElement | null
    if (!table || !cell) return setR(null)
    setR({
      table: table.getBoundingClientRect(),
      cell: cell.getBoundingClientRect(),
      header: isHeader(at.table.child(0)),
    })
  }, [editor, keep])
  useEffect(() => {
    editor.on('transaction', update)
    editor.on('focus', update)
    window.addEventListener('scroll', update, true)
    window.addEventListener('resize', update)
    return () => {
      editor.off('transaction', update)
      editor.off('focus', update)
      window.removeEventListener('scroll', update, true)
      window.removeEventListener('resize', update)
    }
  }, [editor, update])
  return [r, update] as const
}

type Item = { label: string; icon: React.ElementType; run: () => void; danger?: boolean; disabled?: boolean }

function Menu({
  label,
  items,
  style,
  onClose,
}: {
  label: string
  items: (Item | '-')[]
  style: React.CSSProperties
  onClose: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    ref.current?.querySelector('button')?.focus()
    const down = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && onClose()
    document.addEventListener('mousedown', down)
    return () => document.removeEventListener('mousedown', down)
  }, [onClose])
  return (
    <div
      ref={ref}
      role="menu"
      aria-label={label}
      style={style}
      className={`fixed z-50 w-56 ${pop}`}
      onKeyDown={(e) => {
        if (e.key === 'Escape') return onClose()
        if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
        e.preventDefault()
        const all = [...(ref.current?.querySelectorAll('button') ?? [])]
        const i = all.indexOf(document.activeElement as HTMLButtonElement)
        all[(i + (e.key === 'ArrowDown' ? 1 : all.length - 1)) % all.length]?.focus()
      }}
    >
      {items.map((it, i) =>
        it === '-' ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: static separators
          <hr key={i} className="my-1 border-line" />
        ) : (
          <button
            key={it.label}
            type="button"
            role="menuitem"
            disabled={it.disabled}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => {
              onClose()
              it.run()
            }}
            className={`${item} disabled:opacity-40 disabled:hover:bg-transparent ${it.danger ? 'text-red-600' : ''}`}
          >
            <it.icon size={15} /> {it.label}
          </button>
        ),
      )}
    </div>
  )
}

const grip =
  'fixed z-40 grid place-items-center rounded border border-line bg-canvas text-muted shadow-sm hover:bg-hover hover:text-fg aria-expanded:bg-accent aria-expanded:text-white'

/** Notion-style table chrome: a grip above the cursor's column and left of its row, each with a menu, and a toolbar under the table. */
export function TableControls({ editor }: { editor: Editor }) {
  const keepOpen = useRef(false)
  const [r, refresh] = useTableRects(editor, keepOpen)
  const [open, setOpenState] = useState<'col' | 'row' | null>(null)
  const setOpen = (o: 'col' | 'row' | null) => {
    keepOpen.current = o !== null
    setOpenState(o)
  }
  // biome-ignore lint/correctness/useExhaustiveDependencies: stable refs; re-check visibility once a menu closes
  const close = useCallback(() => {
    keepOpen.current = false
    setOpenState(null)
    refresh()
  }, [])
  if (!r) return null
  const c = () => editor.chain().focus()
  const at = locate(editor)
  const merged = !!at && hasMerge(at.table)
  const colItems: (Item | '-')[] = [
    { disabled: merged, label: 'Sort increasing', icon: ArrowUpNarrowWide, run: () => sortRows(editor, 1) },
    {
      disabled: merged,
      label: 'Sort decreasing',
      icon: ArrowDownWideNarrow,
      run: () => sortRows(editor, -1),
    },
    '-',
    { label: 'Add column left', icon: BetweenVerticalStart, run: () => c().addColumnBefore().run() },
    { label: 'Add column right', icon: BetweenVerticalEnd, run: () => c().addColumnAfter().run() },
    { disabled: merged, label: 'Move column left', icon: ChevronLeft, run: () => moveColumn(editor, -1) },
    { disabled: merged, label: 'Move column right', icon: ChevronRight, run: () => moveColumn(editor, 1) },
    '-',
    { disabled: merged, label: 'Clear cells', icon: X, run: () => clearCells(editor, 'col') },
    { label: 'Delete column', icon: Trash2, run: () => c().deleteColumn().run(), danger: true },
  ]
  const rowItems: (Item | '-')[] = [
    { label: 'Add row above', icon: BetweenHorizontalStart, run: () => c().addRowBefore().run() },
    { label: 'Add row below', icon: BetweenHorizontalEnd, run: () => c().addRowAfter().run() },
    { disabled: merged, label: 'Move row up', icon: ChevronUp, run: () => moveRow(editor, -1) },
    { disabled: merged, label: 'Move row down', icon: ChevronDown, run: () => moveRow(editor, 1) },
    '-',
    { disabled: merged, label: 'Clear cells', icon: X, run: () => clearCells(editor, 'row') },
    { label: 'Delete row', icon: Trash2, run: () => c().deleteRow().run(), danger: true },
  ]
  const keep = (e: React.MouseEvent) => e.preventDefault() // keep the cursor in the cell
  return (
    <>
      <button
        type="button"
        aria-label="Column options"
        aria-haspopup="menu"
        aria-expanded={open === 'col'}
        onMouseDown={keep}
        onClick={() => setOpen(open === 'col' ? null : 'col')}
        style={{ left: r.cell.left + r.cell.width / 2 - 12, top: r.table.top - 9 }}
        className={`${grip} h-[18px] w-6`}
      >
        <GripHorizontal size={13} />
      </button>
      <button
        type="button"
        aria-label="Row options"
        aria-haspopup="menu"
        aria-expanded={open === 'row'}
        onMouseDown={keep}
        onClick={() => setOpen(open === 'row' ? null : 'row')}
        style={{ left: r.table.left - 9, top: r.cell.top + r.cell.height / 2 - 12 }}
        className={`${grip} h-6 w-[18px]`}
      >
        <GripVertical size={13} />
      </button>
      {open === 'col' && (
        <Menu
          label="Column options"
          items={colItems}
          onClose={close}
          style={{ left: r.cell.left, top: r.table.top + 12 }}
        />
      )}
      {open === 'row' && (
        <Menu
          label="Row options"
          items={rowItems}
          onClose={close}
          style={{ left: r.table.left + 12, top: r.cell.top }}
        />
      )}
      <div
        role="toolbar"
        aria-label="Table"
        style={{ left: r.table.left + r.table.width / 2, top: r.table.bottom + 8 }}
        className={`fixed z-40 flex -translate-x-1/2 items-center gap-0.5 ${pop}`}
      >
        <button
          type="button"
          aria-pressed={r.header}
          onMouseDown={keep}
          onClick={() => c().toggleHeaderRow().run()}
          className={`${item} w-auto aria-pressed:text-accent`}
        >
          <Heading size={15} /> Header row
        </button>
        <span className="mx-0.5 h-5 w-px bg-line" />
        <button
          type="button"
          disabled={!editor.can().mergeCells()}
          onMouseDown={keep}
          onClick={() => c().mergeCells().run()}
          className={`${item} w-auto disabled:opacity-40 disabled:hover:bg-transparent`}
        >
          <TableCellsMerge size={15} /> Merge cells
        </button>
        <button
          type="button"
          disabled={!editor.can().splitCell()}
          onMouseDown={keep}
          onClick={() => c().splitCell().run()}
          className={`${item} w-auto disabled:opacity-40 disabled:hover:bg-transparent`}
        >
          <TableCellsSplit size={15} /> Split cell
        </button>
        <span className="mx-0.5 h-5 w-px bg-line" />
        <button
          type="button"
          onMouseDown={keep}
          onClick={() => c().deleteTable().run()}
          className={`${item} w-auto text-red-600`}
        >
          <Trash2 size={15} /> Delete table
        </button>
      </div>
    </>
  )
}

/** Hover a size, click to insert: the grid grows from 5×10 to 10×10 as you approach the edge. */
export function TablePicker({ editor }: { editor: Editor }) {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null)
  const [size, setSize] = useState({ rows: 3, cols: 3 })
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const dom = editor.view.dom
    const show = () => {
      const p = editor.view.coordsAtPos(editor.state.selection.from)
      setSize({ rows: 3, cols: 3 })
      setAt({ x: p.left, y: p.bottom + 6 })
    }
    dom.addEventListener('cn:table-picker', show)
    return () => dom.removeEventListener('cn:table-picker', show)
  }, [editor])
  useEffect(() => {
    if (!at) return
    ref.current?.focus()
    const down = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setAt(null)
    document.addEventListener('mousedown', down)
    return () => document.removeEventListener('mousedown', down)
  }, [at])
  if (!at) return null
  const insert = (rows: number, cols: number) => {
    setAt(null)
    editor.view.focus() // synchronous: chain().focus() waits a frame and keystrokes typed meanwhile are lost
    editor.chain().focus().insertTable({ rows, cols, withHeaderRow: true }).run()
  }
  const shownRows = size.rows >= 5 ? 10 : 5
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label="Table size"
      tabIndex={-1}
      style={{ left: at.x, top: at.y }}
      className={`fixed z-50 p-3 outline-none ${pop}`}
      onKeyDown={(e) => {
        const k = e.key
        if (k === 'Escape') {
          setAt(null)
          editor.commands.focus()
          return
        }
        if (k === 'Enter') {
          e.preventDefault()
          insert(size.rows, size.cols)
          return
        }
        const d = { ArrowRight: [0, 1], ArrowLeft: [0, -1], ArrowDown: [1, 0], ArrowUp: [-1, 0] }[k]
        if (!d) return
        e.preventDefault()
        setSize((s) => ({
          rows: Math.min(10, Math.max(1, s.rows + d[0])),
          cols: Math.min(10, Math.max(1, s.cols + d[1])),
        }))
      }}
    >
      <div className="grid grid-cols-10 gap-1">
        {Array.from({ length: shownRows * 10 }, (_, i) => {
          const [row, col] = [Math.floor(i / 10) + 1, (i % 10) + 1]
          const on = row <= size.rows && col <= size.cols
          return (
            <button
              key={`${row}x${col}`}
              type="button"
              tabIndex={-1}
              aria-label={`${row} × ${col} table`}
              onMouseEnter={() => setSize({ rows: row, cols: col })}
              onClick={() => insert(row, col)}
              className={`size-[18px] rounded border ${on ? 'border-accent bg-accent/15' : 'border-line'}`}
            />
          )
        })}
      </div>
      <p aria-live="polite" className="mt-2 text-center text-[13px]">
        {size.rows} × {size.cols}
      </p>
    </div>
  )
}
