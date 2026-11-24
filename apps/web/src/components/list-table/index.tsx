'use client'
import { ArrowDown, ArrowUp, ChevronsUpDown, SlidersHorizontal, X } from 'lucide-react'
import { type ReactNode, useEffect, useMemo, useState } from 'react'
import { btn, btnPrimary, inputCls } from '@/components/ui'
import { useScreenConfig } from '@/lib/screen-config'
import {
  type Column,
  defaultConfig,
  type Filters,
  MAX_VIEWS,
  PAGE_SIZES,
  parseConfig,
  sameFilters,
  sortRows,
  type TableConfig,
} from './config'
import { Settings } from './settings'

export type { Column, Filters } from './config'

const ROW_H = { compact: 'h-8', standard: 'h-10', relaxed: 'h-12' } as const
const pill = (on: boolean) =>
  `rounded-full border px-3 py-1 text-[13px] ${on ? 'border-accent bg-accent text-accent-fg' : 'border-line hover:bg-hover'}`

function useFocus() {
  const [f, set] = useState<{ key: string; n: number }>()
  useEffect(() => {
    if (f) document.querySelector<HTMLElement>(`[data-lt-focus="${f.key}"]`)?.focus()
  }, [f])
  return (key: string) => set((p) => ({ key, n: (p?.n ?? 0) + 1 }))
}

/**
 * Shared list table (docs/design/home-dashboard-widgets.md section 8 and "Release 2 screen contracts").
 * Native <table>. Per-user settings (columns, order, sort, density, page size, frozen first column, saved views)
 * live in useScreenConfig. The screen owns data loading and filters; this component never refetches.
 * Sorting and paging cover only the rows passed in; `partial` says so whenever those rows are a capped window.
 */
export function ListTable<T>({
  screen,
  label,
  columns,
  rows,
  rowKey,
  filters,
  onFilters,
  empty,
  partial,
}: {
  screen: string
  label: string
  columns: Column<T>[]
  rows: T[] | undefined
  rowKey: (r: T) => string
  filters: Filters
  onFilters: (f: Filters) => void
  empty?: ReactNode
  /** Set when `rows` is a capped window of a bigger set, e.g. "newest 500 invoices". */
  partial?: string
}) {
  const defaults = useMemo(() => defaultConfig(columns), [columns])
  const cfg = useScreenConfig<TableConfig>(screen, defaults, parseConfig(columns))
  const c = cfg.config
  const [open, setOpen] = useState(false)
  const [naming, setNaming] = useState(false)
  const [name, setName] = useState('')
  const [nameErr, setNameErr] = useState('')
  const [msg, setMsg] = useState('')
  const focusOn = useFocus()
  const say = (s: string) => setMsg((m) => (m === s ? `${s} ` : s))
  const commit = (next: TableConfig) => void cfg.save(next, true).catch(() => {}) // failure shows via cfg.error

  const shown = c.columns.flatMap((k) => columns.find((x) => x.key === k) ?? [])
  // a sort on a hidden column is ignored: it would order rows with nothing on screen saying so
  const sort = c.sort && c.columns.includes(c.sort.key) ? c.sort : null
  const sortCol = sort ? columns.find((x) => x.key === sort.key) : undefined
  const sorted = useMemo(
    () => (rows && sortCol?.sort && sort ? sortRows(rows, sortCol.sort, sort.dir) : rows),
    [rows, sortCol, sort],
  )

  // Page resets whenever the sort, page size, filters or the number of rows change.
  const key = `${JSON.stringify(filters)}|${sort?.key}${sort?.dir}|${c.pageSize}|${rows?.length}`
  const [pg, setPg] = useState({ n: 0, key })
  if (pg.key !== key) setPg({ n: 0, key }) // reset for good, so returning to old inputs does not bring back the old page
  const total = sorted?.length ?? 0
  const pages = Math.max(1, Math.ceil(total / c.pageSize))
  const page = Math.min(pg.n, pages - 1)
  const go = (n: number) => {
    setPg({ n, key })
    say(`Page ${n + 1} of ${pages}.`)
  }
  const slice = sorted?.slice(page * c.pageSize, (page + 1) * c.pageSize)

  const cycle = (col: Column<T>) => {
    const cur = sort?.key === col.key ? sort.dir : null
    const next = cur === null ? 'asc' : cur === 'asc' ? 'desc' : null
    commit({ ...c, sort: next ? { key: col.key, dir: next } : null })
    say(
      next
        ? `Sorted by ${col.label}, ${next === 'asc' ? 'ascending' : 'descending'}.`
        : `Sort cleared. Showing the original order.`,
    )
  }
  const hasFilters = Object.values(filters).some(Boolean)
  const activeView = c.views.find(
    (v) => sameFilters(v.filters, filters) && (hasFilters || v.columns.join() === c.columns.join()),
  )
  const apply = (f: Filters, cols?: string[]) => {
    onFilters(f)
    if (cols) commit({ ...c, columns: cols })
  }
  const saveView = () => {
    const n = name.trim()
    const others = c.views.filter((v) => v.name.toLowerCase() !== n.toLowerCase())
    if (!n) return setNameErr('Give the view a name.')
    if (others.length >= MAX_VIEWS) return setNameErr(`You can keep ${MAX_VIEWS} views. Delete one first.`)
    const id = Math.random().toString(36).slice(2, 10)
    commit({ ...c, views: [...others, { id, name: n, filters: { ...filters }, columns: c.columns }] })
    setNaming(false)
    setName('')
    setNameErr('')
    say(`View ${n} saved.`)
    focusOn(`view:${id}`)
  }

  if (!cfg.ready || !rows)
    return rows === undefined && cfg.ready ? null : (
      <div aria-busy="true" className="h-40 animate-pulse rounded-xl bg-hover" />
    )

  return (
    <div>
      <p role="status" aria-live="polite" className="sr-only">
        {msg}
      </p>
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <fieldset
          aria-label="Saved views"
          className="m-0 flex min-w-0 flex-wrap items-center gap-1.5 border-0 p-0"
        >
          <button
            type="button"
            data-lt-focus="all"
            aria-pressed={!hasFilters && !activeView}
            className={pill(!hasFilters && !activeView)}
            onClick={() => apply({})}
          >
            All
          </button>
          {c.views.map((v) => {
            const on = v.id === activeView?.id
            return (
              <span key={v.id} className="inline-flex items-center">
                <button
                  type="button"
                  data-lt-focus={`view:${v.id}`}
                  aria-pressed={on}
                  className={pill(on)}
                  onClick={() => {
                    apply(v.filters, v.columns)
                    say(`View ${v.name} applied.`)
                  }}
                >
                  {v.name}
                </button>
                <button
                  type="button"
                  aria-label={`Delete view ${v.name}`}
                  className="rounded p-1 text-muted hover:bg-hover"
                  disabled={cfg.locked}
                  onClick={() => {
                    commit({ ...c, views: c.views.filter((x) => x.id !== v.id) })
                    say(`View ${v.name} deleted.`)
                    focusOn('all')
                  }}
                >
                  <X size={12} aria-hidden />
                </button>
              </span>
            )
          })}
          {!cfg.locked &&
            (naming ? (
              <form
                className="inline-flex items-center gap-1"
                onSubmit={(e) => {
                  e.preventDefault()
                  saveView()
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Escape') {
                    e.stopPropagation()
                    setNaming(false)
                    setNameErr('')
                    focusOn('save-view')
                  }
                }}
              >
                <label>
                  <span className="sr-only">View name</span>
                  <input
                    // biome-ignore lint/a11y/noAutofocus: the field appears because the person asked for it
                    autoFocus
                    value={name}
                    maxLength={40}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="View name"
                    aria-invalid={!!nameErr || undefined}
                    className={`${inputCls} w-36`}
                  />
                </label>
                <button type="submit" className={btnPrimary}>
                  Save
                </button>
                <button
                  type="button"
                  className={btn}
                  onClick={() => {
                    setNaming(false)
                    setNameErr('')
                    focusOn('save-view')
                  }}
                >
                  Cancel
                </button>
              </form>
            ) : (
              <button
                type="button"
                data-lt-focus="save-view"
                className="rounded-full border border-dashed border-line px-3 py-1 text-[13px] hover:bg-hover"
                onClick={() => setNaming(true)}
              >
                Save view
              </button>
            ))}
        </fieldset>
        {!cfg.locked && (
          <button
            type="button"
            aria-haspopup="dialog"
            className={`${btn} ml-auto`}
            onClick={() => setOpen(true)}
          >
            <SlidersHorizontal size={14} aria-hidden /> Columns
          </button>
        )}
      </div>
      {nameErr && (
        <p role="alert" className="mb-2 text-[13px] text-bad">
          {nameErr}
        </p>
      )}
      {cfg.error && !open && (
        <p role="alert" className="mb-2 text-[13px] text-bad">
          Could not save your table settings: {cfg.error.message}
        </p>
      )}
      {partial && (
        <p role="status" className="mb-2 text-[13px] text-muted">
          Showing only the {partial}. Sorting and paging cover these rows, not every record.
        </p>
      )}
      {rows.length === 0 ? (
        empty
      ) : (
        <>
          {/* Phones: the region scrolls sideways with the first column kept in place. */}
          {/* biome-ignore lint/a11y/useSemanticElements: a scroll container needs a keyboard stop and a name */}
          <div
            role="region"
            aria-label={`${label} table`}
            // biome-ignore lint/a11y/noNoninteractiveTabindex: a sideways-scrolling region must be reachable by keyboard
            tabIndex={0}
            className="overflow-x-auto focus-visible:outline-2 focus-visible:outline-accent"
          >
            <table data-density={c.density} className="w-full whitespace-nowrap text-left text-[14px]">
              <caption className="sr-only">
                {label}, {total} rows
              </caption>
              <thead className="text-[12px] text-muted">
                <tr className="border-b border-line">
                  {shown.map((col, i) => {
                    const dir = sort?.key === col.key ? sort.dir : undefined
                    return (
                      <th
                        key={col.key}
                        scope="col"
                        aria-sort={dir ? (dir === 'asc' ? 'ascending' : 'descending') : undefined}
                        className={`py-2 pr-3 font-medium ${col.align === 'right' ? 'text-right' : ''} ${
                          i === 0 && c.freezeFirst ? 'sticky left-0 z-10 bg-canvas' : ''
                        }`}
                      >
                        {col.sort ? (
                          <button
                            type="button"
                            onClick={() => cycle(col)}
                            disabled={cfg.locked}
                            className="inline-flex items-center gap-1 font-medium hover:text-fg focus-visible:outline-2 focus-visible:outline-accent"
                          >
                            {col.label}
                            {dir === 'asc' ? (
                              <ArrowUp size={12} aria-hidden />
                            ) : dir === 'desc' ? (
                              <ArrowDown size={12} aria-hidden />
                            ) : (
                              <ChevronsUpDown size={12} aria-hidden className="opacity-40" />
                            )}
                          </button>
                        ) : (
                          col.label
                        )}
                      </th>
                    )
                  })}
                </tr>
              </thead>
              <tbody>
                {slice?.map((r) => (
                  <tr
                    key={rowKey(r)}
                    className={`group border-b border-line hover:bg-hover ${ROW_H[c.density]}`}
                  >
                    {shown.map((col, i) => (
                      <td
                        key={col.key}
                        className={`pr-3 ${col.align === 'right' ? 'text-right tabular-nums' : ''} ${
                          i === 0 ? 'font-medium' : ''
                        } ${i === 0 && c.freezeFirst ? 'sticky left-0 z-10 bg-canvas group-hover:bg-hover' : ''}`}
                      >
                        {col.cell(r)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-[13px] text-muted">
            <label className="flex items-center gap-1.5">
              Rows per page
              <select
                value={c.pageSize}
                disabled={cfg.locked}
                onChange={(e) => {
                  commit({ ...c, pageSize: Number(e.target.value) as TableConfig['pageSize'] })
                  say(`${e.target.value} rows per page.`)
                }}
                className={`${inputCls} w-auto py-1`}
              >
                {PAGE_SIZES.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </label>
            <span className="flex items-center gap-2">
              {page * c.pageSize + 1}-{Math.min(total, (page + 1) * c.pageSize)} of {total}
              <button type="button" className={btn} disabled={page === 0} onClick={() => go(page - 1)}>
                Previous page
              </button>
              <button type="button" className={btn} disabled={page >= pages - 1} onClick={() => go(page + 1)}>
                Next page
              </button>
            </span>
          </div>
        </>
      )}
      <Settings
        open={open}
        columns={columns}
        cfg={c}
        error={cfg.error}
        onChange={commit}
        onReset={async () => {
          await cfg.reset()
          say('Table settings reset to default.')
        }}
        onClose={() => setOpen(false)}
        focusOn={focusOn}
      />
    </div>
  )
}
