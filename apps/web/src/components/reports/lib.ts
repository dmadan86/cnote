import { dayIST, money } from '@/components/billing/fmt'
import { CmdFailure } from '@/lib/api'

export type FieldType = 'text' | 'enum' | 'date' | 'money' | 'number' | 'bool' | 'person' | 'contact'
export type Opt = { value: string; label: string }
export type FieldInfo = {
  key: string
  label: string
  type: FieldType
  aggs: string[]
  values?: Opt[]
  group: boolean
}
export type DatasetInfo = {
  key: string
  label: string
  description: string
  default_date: string
  default_columns: string[]
  fields: FieldInfo[]
}
export type Filter = { field: string; op: string; value?: unknown }
export type Period = { field: string; preset: string; from?: string; to?: string }
export type Measure = { field?: string; agg: string }
export type Spec = {
  dataset: string
  columns?: string[]
  filters?: Filter[]
  period?: Period | null
  group_by?: { field: string; bucket?: string }[]
  measures?: Measure[]
  sort?: { key: string; dir: 'asc' | 'desc' }[]
  limit?: number
  viz?: 'table' | 'bar' | 'number'
}
export type Col = { key: string; label: string; type: FieldType }
export type RunResult = {
  columns: Col[]
  rows: unknown[][]
  totals: unknown[] | null
  count: number
  truncated: boolean
  period: { from: string; to: string; label: string } | null
  error?: string
}
export type Report = {
  id: string
  name: string
  dataset: string
  spec: Spec
  visibility: 'private' | 'workspace'
  created_by: string
  owner_name: string
  created_at: string
  updated_at: string
  can_edit: boolean
}
export type Tile = { report_id: string; size: 'number' | 'half' | 'full'; available?: boolean; name?: string }
export type Dashboard = {
  id: string
  name: string
  tiles: Tile[]
  period: Period | null
  visibility: 'private' | 'workspace'
  created_by: string
  created_at: string
  updated_at: string
  can_edit: boolean
}

export const PRESETS: [string, string][] = [
  ['this_month', 'This month'],
  ['last_month', 'Last month'],
  ['this_quarter', 'This quarter'],
  ['last_quarter', 'Last quarter'],
  ['this_fy', 'This financial year'],
  ['last_fy', 'Last financial year'],
  ['fy_to_date', 'Financial year to date'],
  ['last_12_months', 'Last 12 months'],
  ['custom', 'Custom dates'],
]

/** cmd() without an abort signal; the live preview needs to cancel stale requests. */
export async function runCmd<T>(ws: string, name: string, input: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`/api/w/${ws}/commands/${name}`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(input),
    credentials: 'same-origin',
    signal,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const e = data?.error ?? {}
    throw new CmdFailure(e.code ?? 'internal', e.message ?? res.statusText, res.status)
  }
  return data as T
}

export const download = (name: string, mime: string, body: string) => {
  const url = URL.createObjectURL(new Blob([body], { type: mime }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

const compact = new Intl.NumberFormat('en-IN', {
  style: 'currency',
  currency: 'INR',
  notation: 'compact',
  maximumFractionDigits: 1,
})
const num = new Intl.NumberFormat('en-IN', { maximumFractionDigits: 2 })
const compactNum = new Intl.NumberFormat('en-IN', { notation: 'compact', maximumFractionDigits: 1 })
const dmy = new Intl.DateTimeFormat('en-IN', {
  day: 'numeric',
  month: 'short',
  year: 'numeric',
  timeZone: 'UTC',
})
const my = new Intl.DateTimeFormat('en-IN', { month: 'short', year: 'numeric', timeZone: 'UTC' })

export const fmtDate = (s: string) => dmy.format(new Date(`${s.slice(0, 10)}T00:00:00Z`))

/** Short form for tiles and bar labels. */
export const compactVal = (type: string, v: unknown) =>
  typeof v !== 'number' ? '' : type === 'money' ? compact.format(v / 100) : compactNum.format(v)

type Label = { id: string; label: string }
const isLabel = (v: unknown): v is Label => !!v && typeof v === 'object' && 'label' in v

/** One cell as text. field/bucket let the group column show enum labels and date buckets. */
export function cellText(
  col: Col,
  v: unknown,
  field?: FieldInfo,
  bucket?: string,
  compactMoney = false,
): string {
  if (v === null || v === undefined || v === '') return col.key === 'g0' ? '(none)' : ''
  if (isLabel(v)) return v.label
  if (col.type === 'money' && typeof v === 'number') return compactMoney ? compact.format(v / 100) : money(v)
  if (typeof v === 'number') return num.format(v)
  if (typeof v === 'boolean') return v ? 'Yes' : 'No'
  if (col.type === 'enum') return field?.values?.find((o) => o.value === v)?.label ?? String(v)
  if (col.type === 'date' && typeof v === 'string') {
    const d = new Date(`${v.slice(0, 10)}T00:00:00Z`)
    if (bucket === 'month') return my.format(d)
    if (bucket === 'quarter') return `${my.format(d)} quarter`
    if (bucket === 'fy') return `FY from ${my.format(d)}`
    if (bucket === 'week') return `Week of ${dmy.format(d)}`
    return dmy.format(d)
  }
  return String(v)
}

export const isNum = (t: string) => t === 'money' || t === 'number'

const addDays = (s: string, n: number) =>
  new Date(Date.parse(`${s}T00:00:00Z`) + n * 86_400_000).toISOString().slice(0, 10)
const addMonths = (s: string, n: number) => {
  const [y, m, d] = s.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1 + n, d)).toISOString().slice(0, 10)
}
/** Inclusive end date of a bucket that starts on `from`. */
export const bucketEnd = (from: string, bucket?: string) =>
  bucket === 'week'
    ? addDays(from, 6)
    : bucket === 'month'
      ? addDays(addMonths(from, 1), -1)
      : bucket === 'quarter'
        ? addDays(addMonths(from, 3), -1)
        : bucket === 'fy'
          ? addDays(addMonths(from, 12), -1)
          : from

export const todayIST = () => dayIST()

const lower = (s: string) => s.charAt(0).toLowerCase() + s.slice(1)
export const measureLabel = (m: Measure, ds: DatasetInfo) => {
  if (m.agg === 'count' || !m.field) return `Number of ${lower(ds.label)}`
  const f = ds.fields.find((x) => x.key === m.field)?.label ?? m.field
  const w = { sum: 'Total', avg: 'Average', min: 'Lowest', max: 'Highest' }[m.agg] ?? m.agg
  return `${w} ${lower(f)}`
}
export const measureKey = (m: Measure) => `${m.agg}:${m.field ?? ''}`

/** "Total amount by Customer" / "Invoices". */
export function summarySentence(spec: Spec, ds: DatasetInfo) {
  const g = spec.group_by?.[0]
  if (!g) return ds.label
  const gl = ds.fields.find((f) => f.key === g.field)?.label ?? g.field
  const ms = spec.measures?.length ? spec.measures : [{ agg: 'count' }]
  return `${ms.map((m) => measureLabel(m, ds)).join(' and ')} by ${lower(gl)}${g.bucket ? ` (${g.bucket})` : ''}`
}

export const defaultSpec = (ds: DatasetInfo): Spec => ({
  dataset: ds.key,
  columns: ds.default_columns,
  filters: [],
  period: null, // a default period would hide rows without that date (tasks with no due date)
  group_by: [],
  measures: [],
  sort: [],
  viz: 'table',
})

export const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))
export const isDenied = (e: unknown) =>
  e instanceof CmdFailure &&
  (e.code === 'forbidden' || e.code === 'not_found' || e.status === 403 || e.status === 404)

const ist = new Intl.DateTimeFormat('en-IN', { day: 'numeric', month: 'short', timeZone: 'Asia/Kolkata' })
export const fmtTs = (iso: string) => ist.format(new Date(iso))
