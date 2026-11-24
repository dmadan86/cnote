import type { ReactNode } from 'react'

export type Column<T> = {
  key: string
  label: string
  cell: (row: T) => ReactNode
  /** Present = sortable. null (or '') sorts last in both directions. */
  sort?: (row: T) => string | number | null
  align?: 'right'
  /** Always shown and always first (the name or number). */
  locked?: boolean
  /** Hidden until the person shows it. */
  hidden?: boolean
}
export type Filters = Record<string, string>
export type View = { id: string; name: string; filters: Filters; columns: string[] }
export type Density = 'compact' | 'standard' | 'relaxed'
export type TableConfig = {
  v: 1
  columns: string[] // visible keys in order, locked first
  sort: { key: string; dir: 'asc' | 'desc' } | null // per screen, not per view
  density: Density
  pageSize: 25 | 50 | 100
  freezeFirst: boolean
  views: View[]
}

export const MAX_VIEWS = 10
export const DENSITIES: Density[] = ['compact', 'standard', 'relaxed']
export const PAGE_SIZES = [25, 50, 100] as const

export const defaultConfig = <T>(cols: Column<T>[]): TableConfig => ({
  v: 1,
  columns: cols.filter((c) => !c.hidden || c.locked).map((c) => c.key),
  sort: null,
  density: 'standard',
  pageSize: 50,
  freezeFirst: true,
  views: [],
})

/** Stored JSON is untrusted: keep only what is well formed and known, clamp the rest to the defaults. */
export const parseConfig =
  <T>(cols: Column<T>[]) =>
  (raw: unknown, d: TableConfig): TableConfig => {
    const r = (raw ?? {}) as Record<string, unknown>
    const known = new Set(cols.map((c) => c.key))
    const locked = cols.filter((c) => c.locked).map((c) => c.key)
    const keys = (x: unknown) =>
      Array.isArray(x)
        ? [
            ...locked,
            ...new Set(
              x.filter((k): k is string => typeof k === 'string' && known.has(k) && !locked.includes(k)),
            ),
          ]
        : null
    const s = r.sort as { key?: unknown; dir?: unknown } | null
    const sortable = cols.find((c) => c.key === s?.key)?.sort
    const views: View[] = []
    for (const v of Array.isArray(r.views) ? r.views : []) {
      const { id, name, filters, columns } = (v ?? {}) as Record<string, unknown>
      if (views.length >= MAX_VIEWS || typeof id !== 'string' || id.length > 64) continue
      if (typeof name !== 'string' || !name || name.length > 40) continue
      const f: Filters = {}
      for (const [k, x] of Object.entries((filters ?? {}) as object).slice(0, 10))
        if (typeof x === 'string' && x.length <= 200) f[k] = x
      views.push({ id, name, filters: f, columns: keys(columns) ?? d.columns })
    }
    return {
      v: 1,
      columns: keys(r.columns) ?? d.columns,
      sort: sortable && (s?.dir === 'asc' || s?.dir === 'desc') ? { key: String(s.key), dir: s.dir } : null,
      density: DENSITIES.find((x) => x === r.density) ?? d.density,
      pageSize: PAGE_SIZES.find((x) => x === r.pageSize) ?? d.pageSize,
      freezeFirst: typeof r.freezeFirst === 'boolean' ? r.freezeFirst : d.freezeFirst,
      views,
    }
  }

/**
 * Sort semantics: null and empty values last in both directions; numbers numerically; text with numeric-aware,
 * case-insensitive compare; ISO dates compare as text; ties keep the screen's own (server) order in both directions.
 */
export function sortRows<T>(rows: T[], get: (r: T) => string | number | null, dir: 'asc' | 'desc'): T[] {
  const nil = (x: unknown) => x == null || x === ''
  return rows
    .map((r, i) => ({ r, i, k: get(r) }))
    .sort((a, b) => {
      if (nil(a.k) || nil(b.k)) return nil(a.k) && nil(b.k) ? a.i - b.i : nil(a.k) ? 1 : -1
      const c =
        typeof a.k === 'number' && typeof b.k === 'number'
          ? a.k - b.k
          : String(a.k).localeCompare(String(b.k), undefined, { numeric: true, sensitivity: 'base' })
      return c ? (dir === 'asc' ? c : -c) : a.i - b.i
    })
    .map((x) => x.r)
}

export const sameFilters = (a: Filters, b: Filters) => {
  const ks = new Set([...Object.keys(a), ...Object.keys(b)])
  return [...ks].every((k) => (a[k] ?? '') === (b[k] ?? ''))
}
