'use client'
import { Plus, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { dayIST } from '@/components/billing/fmt'
import { useShell } from '@/components/shell'
import { btn, inputCls } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { type FieldInfo, type Filter, fmtDate } from './lib'

const OPS: Record<string, string> = {
  eq: 'is',
  neq: 'is not',
  contains: 'contains',
  empty: 'is empty',
  not_empty: 'is not empty',
  in: 'is any of',
  not_in: 'is none of',
  between: 'is between',
  before: 'is before',
  after: 'is after',
  gt: 'is more than',
  gte: 'is at least',
  lt: 'is less than',
  lte: 'is at most',
  is: 'is',
  me: 'is me',
}
const OPS_BY_TYPE: Record<string, string[]> = {
  text: ['contains', 'eq', 'neq', 'empty', 'not_empty'],
  enum: ['in', 'not_in'],
  date: ['between', 'before', 'after', 'empty', 'not_empty'],
  money: ['gte', 'gt', 'lte', 'lt', 'eq', 'between'],
  number: ['gte', 'gt', 'lte', 'lt', 'eq', 'between'],
  bool: ['is'],
  person: ['me', 'in', 'not_in', 'empty', 'not_empty'],
  contact: ['in', 'not_in', 'empty', 'not_empty'],
}
const NO_VALUE = ['empty', 'not_empty', 'me']

/** Money filters are typed in rupees and sent in paise. */
const toWire = (t: string, s: string) => (t === 'money' ? Math.round(Number(s) * 100) : Number(s))
const fromWire = (t: string, v: unknown) => (typeof v === 'number' ? String(t === 'money' ? v / 100 : v) : '')

export const newFilter = (f: FieldInfo): Filter => {
  const t = f.type
  if (t === 'text') return { field: f.key, op: 'contains', value: '' }
  if (t === 'enum') return { field: f.key, op: 'in', value: [] }
  if (t === 'date') return { field: f.key, op: 'between', value: [dayIST(-30), dayIST()] }
  if (t === 'money' || t === 'number') return { field: f.key, op: 'gte', value: 0 }
  if (t === 'bool') return { field: f.key, op: 'is', value: true }
  if (t === 'person') return { field: f.key, op: 'me' }
  return { field: f.key, op: 'in', value: [] }
}

/** A filter the server can run: a half-typed one stays in the panel but is not sent. */
export const filterReady = (f: Filter) => {
  if (NO_VALUE.includes(f.op)) return true
  if (Array.isArray(f.value) && f.op !== 'between') return f.value.length > 0
  if (Array.isArray(f.value)) return f.value.length === 2 && f.value.every((x) => x !== '' && x !== null)
  return f.value !== '' && f.value !== undefined && !Number.isNaN(f.value)
}

function Pick({ field, filter, set }: { field: FieldInfo; filter: Filter; set: (v: string[]) => void }) {
  const { ws, members } = useShell()
  const contacts = useCmd<{ id: string; name: string }[]>(
    ws,
    'contact.list',
    { limit: 500 },
    field.type === 'contact',
  )
  const [q, setQ] = useState('')
  const opts =
    field.type === 'enum'
      ? (field.values ?? [])
      : field.type === 'person'
        ? members.map((m) => ({ value: m.user_id, label: m.name }))
        : (contacts.data ?? []).map((c) => ({ value: c.id, label: c.name }))
  const sel = (filter.value as string[]) ?? []
  const shown = opts.filter((o) => o.label.toLowerCase().includes(q.toLowerCase()))
  return (
    <fieldset className="space-y-1">
      <legend className="sr-only">Values</legend>
      {opts.length > 8 && (
        <input
          aria-label="Search values"
          className={inputCls}
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
      )}
      <div className="max-h-48 space-y-0.5 overflow-auto">
        {shown.map((o) => (
          <label
            key={o.value}
            className="flex items-center gap-2 rounded px-1 py-0.5 text-[13px] hover:bg-hover"
          >
            <input
              type="checkbox"
              checked={sel.includes(o.value)}
              onChange={(e) => set(e.target.checked ? [...sel, o.value] : sel.filter((x) => x !== o.value))}
            />
            {o.label}
          </label>
        ))}
      </div>
    </fieldset>
  )
}

function Editor({
  field,
  filter,
  onChange,
}: {
  field: FieldInfo
  filter: Filter
  onChange: (f: Filter) => void
}) {
  const t = field.type
  const ops = OPS_BY_TYPE[t] ?? []
  const v = filter.value
  const pair = Array.isArray(v) && filter.op === 'between' ? (v as unknown[]) : ['', '']
  const set = (value: unknown) => onChange({ ...filter, value })
  return (
    <div className="space-y-2">
      <select
        aria-label="Condition"
        className={inputCls}
        value={filter.op}
        onChange={(e) => {
          const op = e.target.value
          const keep = (op === 'between') === (filter.op === 'between') && !NO_VALUE.includes(op)
          onChange({
            field: filter.field,
            op,
            ...(NO_VALUE.includes(op) ? {} : { value: keep ? v : op === 'between' ? ['', ''] : '' }),
          })
        }}
      >
        {ops.map((o) => (
          <option key={o} value={o}>
            {OPS[o]}
          </option>
        ))}
      </select>
      {!NO_VALUE.includes(filter.op) && (t === 'enum' || t === 'person' || t === 'contact') && (
        <Pick field={field} filter={filter} set={set} />
      )}
      {t === 'text' && !NO_VALUE.includes(filter.op) && (
        <input
          aria-label="Value"
          className={inputCls}
          value={String(v ?? '')}
          onChange={(e) => set(e.target.value)}
        />
      )}
      {t === 'bool' && (
        <select
          aria-label="Value"
          className={inputCls}
          value={String(v)}
          onChange={(e) => set(e.target.value === 'true')}
        >
          <option value="true">Yes</option>
          <option value="false">No</option>
        </select>
      )}
      {t === 'date' && filter.op === 'between' && (
        <div className="flex items-center gap-2 text-[13px]">
          <input
            type="date"
            aria-label="From"
            className={inputCls}
            value={String(pair[0])}
            onChange={(e) => set([e.target.value, pair[1]])}
          />
          to
          <input
            type="date"
            aria-label="To"
            className={inputCls}
            value={String(pair[1])}
            onChange={(e) => set([pair[0], e.target.value])}
          />
        </div>
      )}
      {t === 'date' && (filter.op === 'before' || filter.op === 'after') && (
        <input
          type="date"
          aria-label="Date"
          className={inputCls}
          value={String(v ?? '')}
          onChange={(e) => set(e.target.value)}
        />
      )}
      {(t === 'money' || t === 'number') &&
        (filter.op === 'between' ? (
          <div className="flex items-center gap-2 text-[13px]">
            <input
              aria-label="Minimum"
              inputMode="decimal"
              className={inputCls}
              value={fromWire(t, pair[0])}
              onChange={(e) => set([toWire(t, e.target.value), pair[1]])}
            />
            and
            <input
              aria-label="Maximum"
              inputMode="decimal"
              className={inputCls}
              value={fromWire(t, pair[1])}
              onChange={(e) => set([pair[0], toWire(t, e.target.value)])}
            />
          </div>
        ) : (
          <input
            aria-label={t === 'money' ? 'Amount in rupees' : 'Value'}
            inputMode="decimal"
            className={inputCls}
            value={fromWire(t, v)}
            onChange={(e) => set(e.target.value === '' ? '' : toWire(t, e.target.value))}
          />
        ))}
    </div>
  )
}

export function filterText(f: Filter, field?: FieldInfo, members: { user_id: string; name: string }[] = []) {
  const label = field?.label ?? f.field
  const op = OPS[f.op] ?? f.op
  if (NO_VALUE.includes(f.op)) return `${label} ${op}`
  const name = (x: unknown) =>
    field?.values?.find((o) => o.value === x)?.label ??
    members.find((m) => m.user_id === x)?.name ??
    (field?.type === 'contact' ? 'selected' : String(x))
  let val: string
  if (Array.isArray(f.value))
    val =
      f.op === 'between'
        ? field?.type === 'date'
          ? `${fmtDate(String(f.value[0]))} and ${fmtDate(String(f.value[1]))}`
          : `${fromWire(field?.type ?? '', f.value[0])} and ${fromWire(field?.type ?? '', f.value[1])}`
        : f.value.map(name).join(', ')
  else if (field?.type === 'date' && f.value) val = fmtDate(String(f.value))
  else if (field?.type === 'bool') val = f.value ? 'Yes' : 'No'
  else if (field?.type === 'money' || field?.type === 'number') val = fromWire(field.type, f.value)
  else val = String(f.value ?? '')
  return `${label} ${op} ${val}`
}

/** Filter chips: click a chip for its editor popover; "+ Filter" picks a field first. */
export function FilterBar({
  fields,
  filters,
  onChange,
}: {
  fields: FieldInfo[]
  filters: Filter[]
  onChange: (f: Filter[]) => void
}) {
  const { members } = useShell()
  const [open, setOpen] = useState<number | 'new' | null>(null)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (open === null) return
    const down = (e: MouseEvent) => !box.current?.contains(e.target as Node) && setOpen(null)
    const key = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(null)
    document.addEventListener('mousedown', down)
    document.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('mousedown', down)
      document.removeEventListener('keydown', key)
    }
  }, [open])
  const upd = (i: number, f: Filter) => onChange(filters.map((x, j) => (j === i ? f : x)))
  return (
    <div ref={box} className="flex flex-wrap items-center gap-1.5">
      {filters.map((f, i) => {
        const field = fields.find((x) => x.key === f.field)
        const text = filterText(f, field, members)
        return (
          // biome-ignore lint/suspicious/noArrayIndexKey: several filters may share a field
          <div key={`${f.field}-${i}`} className="relative">
            <span className="inline-flex items-center rounded-full border border-line bg-side text-[12px]">
              <button
                type="button"
                aria-label={`Edit filter: ${text}`}
                aria-expanded={open === i}
                onClick={() => setOpen(open === i ? null : i)}
                className="max-w-64 truncate rounded-l-full py-1 pl-2.5 pr-1.5 hover:bg-hover"
              >
                {text}
              </button>
              <button
                type="button"
                aria-label={`Remove filter: ${text}`}
                onClick={() => {
                  setOpen(null)
                  onChange(filters.filter((_, j) => j !== i))
                }}
                className="rounded-r-full py-1 pl-0.5 pr-1.5 hover:bg-hover"
              >
                <X size={12} aria-hidden />
              </button>
            </span>
            {open === i && field && (
              <div
                role="dialog"
                aria-label={`Filter on ${field.label}`}
                className="absolute left-0 z-20 mt-1 w-72 rounded-lg border border-line bg-canvas p-3 shadow-lg"
              >
                <p className="mb-2 text-[12px] font-medium text-muted">{field.label}</p>
                <Editor field={field} filter={f} onChange={(x) => upd(i, x)} />
                <div className="mt-2 flex justify-end">
                  <button type="button" className={btn} onClick={() => setOpen(null)}>
                    Done
                  </button>
                </div>
              </div>
            )}
          </div>
        )
      })}
      <div className="relative">
        <button
          type="button"
          className={btn}
          aria-expanded={open === 'new'}
          onClick={() => setOpen(open === 'new' ? null : 'new')}
        >
          <Plus size={13} aria-hidden /> Filter
        </button>
        {open === 'new' && (
          <div
            role="dialog"
            aria-label="Add filter"
            className="absolute left-0 z-20 mt-1 w-64 rounded-lg border border-line bg-canvas p-3 shadow-lg"
          >
            <label className="block text-[12px] font-medium text-muted" htmlFor="filter-field">
              Filter on
            </label>
            <select
              id="filter-field"
              className={`${inputCls} mt-1`}
              value=""
              onChange={(e) => {
                const f = fields.find((x) => x.key === e.target.value)
                if (!f) return
                onChange([...filters, newFilter(f)])
                setOpen(filters.length)
              }}
            >
              <option value="">Choose a field</option>
              {fields.map((f) => (
                <option key={f.key} value={f.key}>
                  {f.label}
                </option>
              ))}
            </select>
          </div>
        )}
      </div>
    </div>
  )
}
