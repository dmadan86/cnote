import type { ViewConfig } from '@/lib/tables'

/** Per-person grid state (design 17.7). Stored through useScreenConfig under `tables.view:<view id>`. */
export type RowHeight = NonNullable<ViewConfig['row_height']>
export type ViewState = {
  v: 1
  hidden?: string[]
  widths?: Record<string, number>
  row_height?: RowHeight
  collapsed?: string[]
}
export const viewScreen = (viewId: string) => `tables.view:${viewId}`
export const DEFAULT_VIEW_STATE: ViewState = { v: 1 }
export const ROW_PX: Record<RowHeight, number> = { short: 32, medium: 56, tall: 88 }

const strs = (x: unknown) =>
  Array.isArray(x) ? x.filter((s): s is string => typeof s === 'string') : undefined

export function parseViewState(raw: unknown, d: ViewState): ViewState {
  if (!raw || typeof raw !== 'object') return d
  const r = raw as Record<string, unknown>
  const widths: Record<string, number> = {}
  if (r.widths && typeof r.widths === 'object')
    for (const [k, w] of Object.entries(r.widths))
      if (typeof w === 'number' && w >= 40 && w <= 1000) widths[k] = w
  return {
    v: 1,
    hidden: strs(r.hidden),
    widths,
    row_height:
      r.row_height === 'medium' || r.row_height === 'tall' || r.row_height === 'short'
        ? r.row_height
        : undefined,
    collapsed: strs(r.collapsed),
  }
}
