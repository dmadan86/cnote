// biome-ignore-all lint/a11y/noStaticElementInteractions: the popover only watches focus leaving it
// biome-ignore-all lint/a11y/noAutofocus: the editor opens on the person's own keypress
'use client'
import {
  AlignLeft,
  Calendar,
  CircleChevronDown,
  Clock,
  Hash,
  IndianRupee,
  Link2,
  List,
  ListOrdered,
  Mail,
  Paperclip,
  Percent,
  Phone,
  SquareCheck,
  Type,
  User,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import type { Member } from '@/lib/api'
import type { Field, FieldType, LinkChip, SelectOption, TableRecord } from '@/lib/tables'

/** Display and editor for every 1a field type. Shared by the grid, record panel, kanban cards and forms. */

export const FIELD_ICONS: Record<FieldType, typeof Type> = {
  text: Type,
  long_text: AlignLeft,
  number: Hash,
  percent: Percent,
  currency: IndianRupee,
  date: Calendar,
  checkbox: SquareCheck,
  single_select: CircleChevronDown,
  multi_select: List,
  phone: Phone,
  email: Mail,
  url: Link2,
  user: User,
  attachment: Paperclip,
  link: Link2,
  autonumber: ListOrdered,
  created_time: Clock,
  modified_time: Clock,
  created_by: User,
  modified_by: User,
}

const COMPUTED: FieldType[] = [
  'autonumber',
  'created_time',
  'modified_time',
  'created_by',
  'modified_by',
  'attachment', // 1b
  'user', // member picker is not in 1a's grid
]
/** Whether a person with this role can type into the field's cells. */
export const canEditField = (f: Field, role: 'read' | 'write' | 'manage') =>
  role !== 'read' && !COMPUTED.includes(f.type) && (!f.locked || role === 'manage')

const inr = (n: number, d: number) =>
  new Intl.NumberFormat('en-IN', { minimumFractionDigits: d, maximumFractionDigits: d }).format(n)
const dmy = (s: string) => s.split('-').reverse().join('-')
const stamp = (s: string) => new Date(s).toLocaleString('en-IN', { dateStyle: 'medium', timeStyle: 'short' })
const name = (members: Member[], id: string | null) => members.find((m) => m.user_id === id)?.name ?? ''
const optName = (f: Field, id: unknown) => f.config.options?.find((o) => o.id === id)?.name ?? ''
const decimals = (f: Field) => f.config.decimals ?? (f.type === 'currency' ? 2 : undefined)

/** Plain text of a cell: what is shown, copied as TSV and read out. */
export function cellText(f: Field, r: TableRecord, chips: LinkChip[] | undefined, members: Member[]): string {
  const v = r.data[f.id]
  switch (f.type) {
    case 'autonumber':
      return `${f.config.prefix ?? ''}${r.seq}`
    case 'created_time':
      return stamp(r.created_at)
    case 'modified_time':
      return stamp(r.updated_at)
    case 'created_by':
      return name(members, r.created_by)
    case 'modified_by':
      return name(members, r.updated_by)
    case 'link':
      return (chips ?? []).map((c) => c.title).join(', ')
    case 'checkbox':
      return v === true ? 'Yes' : ''
    case 'single_select':
      return optName(f, v)
    case 'multi_select':
      return Array.isArray(v) ? v.map((x) => optName(f, x)).join(', ') : ''
    case 'user':
      return Array.isArray(v) ? v.map((x) => name(members, String(x))).join(', ') : ''
    case 'attachment':
      return Array.isArray(v) && v.length ? `${v.length} file${v.length > 1 ? 's' : ''}` : ''
    case 'date':
      return typeof v === 'string' ? dmy(v) : ''
    case 'number':
      return typeof v === 'number' ? (decimals(f) === undefined ? String(v) : v.toFixed(decimals(f))) : ''
    case 'percent':
      return typeof v === 'number' ? String(v) : ''
    case 'currency':
      return typeof v === 'number' ? (v / 100).toFixed(decimals(f)) : ''
    default:
      return typeof v === 'string' ? v : ''
  }
}

const TINTS: Record<string, string> = {
  green: 'bg-ok-bg text-ok',
  yellow: 'bg-warn-bg text-warn',
  orange: 'bg-warn-bg text-warn',
  red: 'bg-bad-bg text-bad',
  blue: 'bg-accent/15 text-accent',
  purple: 'bg-accent/15 text-accent',
}
const chipCls = 'inline-flex max-w-full shrink-0 items-center rounded-md px-1.5 py-0.5 text-[12px]'
export function OptionChip({ o }: { o: SelectOption }) {
  return <span className={`${chipCls} ${TINTS[o.color] ?? 'bg-hover text-fg'}`}>{o.name}</span>
}

export type CellViewProps = {
  field: Field
  record: TableRecord
  chips?: LinkChip[]
  members: Member[]
  /** The selected cell: phone cells then offer Call and WhatsApp. */
  active?: boolean
  readOnly?: boolean
  onToggle?: (checked: boolean) => void
  onPickLinks?: () => void
}

export function CellView({
  field: f,
  record: r,
  chips,
  members,
  active,
  readOnly,
  onToggle,
  onPickLinks,
}: CellViewProps) {
  const v = r.data[f.id]
  const right = f.type === 'number' || f.type === 'currency' || f.type === 'percent'
  const flex = 'flex flex-wrap items-center gap-1'
  switch (f.type) {
    case 'checkbox':
      return (
        <input
          type="checkbox"
          tabIndex={-1}
          aria-label={f.name}
          checked={v === true}
          disabled={readOnly}
          onChange={(e) => onToggle?.(e.target.checked)}
          className="h-4 w-4 accent-accent"
        />
      )
    case 'single_select': {
      const o = f.config.options?.find((x) => x.id === v)
      return o ? <OptionChip o={o} /> : null
    }
    case 'multi_select':
      return (
        <span className={flex}>
          {(Array.isArray(v) ? v : []).map((id) => {
            const o = f.config.options?.find((x) => x.id === id)
            return o ? <OptionChip key={o.id} o={o} /> : null
          })}
        </span>
      )
    case 'link':
      return (
        <span className={flex}>
          {(chips ?? []).map((c) => (
            <span key={c.id} className={`${chipCls} border border-line bg-side`}>
              {c.title || 'Untitled'}
            </span>
          ))}
          {!readOnly && active && onPickLinks ? (
            <button type="button" tabIndex={-1} onClick={onPickLinks} className="text-[12px] text-accent">
              {chips?.length ? 'Edit' : 'Link'}
            </button>
          ) : null}
        </span>
      )
    case 'phone': {
      const t = cellText(f, r, chips, members)
      const digits = t.replace(/\D/g, '')
      return (
        <span className="flex items-center gap-2">
          <span className="truncate">{t}</span>
          {active && t ? (
            <>
              <a tabIndex={-1} href={`tel:${t}`} className="text-[12px] text-accent">
                Call
              </a>
              <a
                tabIndex={-1}
                href={`https://wa.me/${digits}`}
                target="_blank"
                rel="noreferrer"
                className="text-[12px] text-accent"
              >
                WhatsApp
              </a>
            </>
          ) : null}
        </span>
      )
    }
    case 'number':
    case 'currency': {
      if (typeof v !== 'number') return null
      const n = f.type === 'currency' ? v / 100 : v
      return <span className={right ? 'ml-auto' : ''}>{inr(n, decimals(f) ?? 0)}</span>
    }
    case 'percent':
      return typeof v === 'number' ? <span className="ml-auto">{inr(v, f.config.decimals ?? 0)}%</span> : null
    default:
      return <span>{cellText(f, r, chips, members)}</span>
  }
}

export type Move = 'down' | 'right' | 'left'
export type CellEditorProps = {
  field: Field
  value: unknown
  /** First typed character: replaces the value (or starts the filter in a select). */
  initial?: string
  /** Resolves to a reason when the save failed: the editor stays open and shows it. */
  onCommit: (value: unknown | undefined, move?: Move) => Promise<string | undefined> | undefined
  onCancel: () => void
}

/** Typed text to a stored value; undefined clears the cell, a string is the reason it was refused. */
function parse(f: Field, s: string): { value?: unknown; error?: string } {
  const t = s.trim()
  if (!t) return {}
  if (f.type === 'number' || f.type === 'percent' || f.type === 'currency') {
    const n = Number(t.replace(/[,₹%\s]/g, ''))
    if (!Number.isFinite(n)) return { error: 'Enter a number' }
    return { value: f.type === 'currency' ? Math.round(n * 100) : n }
  }
  return { value: f.type === 'long_text' ? s : t }
}
function editString(f: Field, v: unknown) {
  if (typeof v === 'number') return String(f.type === 'currency' ? v / 100 : v)
  return typeof v === 'string' ? v : ''
}

const errCls =
  'absolute left-0 top-full z-30 mt-0.5 rounded-md border border-bad bg-canvas px-2 py-1 text-[12px] text-bad'

export function CellEditor(p: CellEditorProps) {
  if (p.field.type === 'single_select' || p.field.type === 'multi_select') return <SelectEditor {...p} />
  return <InputEditor {...p} />
}

function InputEditor({ field: f, value, initial, onCommit, onCancel }: CellEditorProps) {
  const [text, setText] = useState(initial ?? editString(f, value))
  const [error, setError] = useState<string>()
  const done = useRef(false)
  const ref = useRef<HTMLInputElement & HTMLTextAreaElement>(null)
  useEffect(() => {
    ref.current?.focus()
    if (initial === undefined) ref.current?.select?.()
  }, [initial])
  const commit = async (move?: Move) => {
    if (done.current) return
    const r = parse(f, text)
    if (r.error) return setError(r.error)
    done.current = true
    const reason = await onCommit(r.value, move)
    if (reason) {
      done.current = false
      setError(reason)
    }
  }
  const common = {
    ref,
    value: text,
    'aria-label': f.name,
    'aria-invalid': !!error,
    onChange: (e: { target: { value: string } }) => {
      setText(e.target.value)
      setError(undefined)
    },
    onBlur: () => void commit(),
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        done.current = true
        return onCancel()
      }
      if (e.key === 'Enter' && !(f.type === 'long_text' && e.shiftKey)) {
        e.preventDefault()
        void commit('down')
      } else if (e.key === 'Tab') {
        e.preventDefault()
        void commit(e.shiftKey ? 'left' : 'right')
      }
    },
    className: `h-full w-full bg-canvas px-2 text-[13px] outline-none ${error ? 'text-bad' : ''}`,
  }
  return (
    <>
      {f.type === 'long_text' ? (
        <textarea
          {...common}
          className={`${common.className} absolute left-0 top-0 z-20 min-h-24 w-full resize-none border border-accent py-1 shadow-md`}
        />
      ) : (
        <input
          {...common}
          type={f.type === 'date' ? 'date' : 'text'}
          inputMode={
            f.type === 'number' || f.type === 'currency' || f.type === 'percent' ? 'decimal' : undefined
          }
        />
      )}
      {error ? (
        <p role="alert" className={errCls}>
          {error}
        </p>
      ) : null}
    </>
  )
}

function SelectEditor({ field: f, value, initial, onCommit, onCancel }: CellEditorProps) {
  const multi = f.type === 'multi_select'
  const [q, setQ] = useState(initial ?? '')
  const [hi, setHi] = useState(0)
  const [picked, setPicked] = useState<string[]>(
    multi ? (Array.isArray(value) ? (value as string[]) : []) : [],
  )
  const [error, setError] = useState<string>()
  const done = useRef(false)
  const box = useRef<HTMLDivElement>(null)
  const opts = (f.config.options ?? []).filter((o) => o.name.toLowerCase().includes(q.toLowerCase()))
  const commit = async (v: unknown | undefined, move?: Move) => {
    if (done.current) return
    done.current = true
    const reason = await onCommit(v, move)
    if (reason) {
      done.current = false
      setError(reason)
    }
  }
  const finish = (move?: Move) => commit(multi ? (picked.length ? picked : undefined) : value, move)
  const choose = (o?: SelectOption) =>
    multi && o
      ? setPicked((s) => (s.includes(o.id) ? s.filter((x) => x !== o.id) : [...s, o.id]))
      : void commit(o?.id, 'down')
  return (
    <div
      ref={box}
      className="absolute left-0 top-0 z-30 w-64 rounded-md border border-accent bg-canvas shadow-md"
      onBlur={(e) => {
        // focus left the popover: a multi select keeps what was ticked, a single select keeps its value
        if (!box.current?.contains(e.relatedTarget as Node | null) && !done.current) {
          if (multi) void finish()
          else onCancel()
        }
      }}
    >
      <input
        autoFocus
        value={q}
        aria-label={`Find an option for ${f.name}`}
        placeholder="Find an option"
        onChange={(e) => {
          setQ(e.target.value)
          setHi(0)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            done.current = true
            onCancel()
          } else if (e.key === 'ArrowDown') {
            e.preventDefault()
            setHi((h) => Math.min(h + 1, opts.length - 1))
          } else if (e.key === 'ArrowUp') {
            e.preventDefault()
            setHi((h) => Math.max(h - 1, 0))
          } else if (e.key === 'Enter') {
            e.preventDefault()
            if (multi && !q && e.ctrlKey) void finish('down')
            else if (opts[hi]) choose(opts[hi])
            else if (multi) void finish('down')
          } else if (e.key === 'Tab') {
            e.preventDefault()
            if (multi) void finish(e.shiftKey ? 'left' : 'right')
            else onCancel()
          }
        }}
        className="w-full border-b border-line bg-canvas px-2 py-1.5 text-[13px] outline-none"
      />
      <div
        role="listbox"
        aria-label={f.name}
        aria-multiselectable={multi}
        className="max-h-48 overflow-auto p-1"
      >
        {!multi && value !== undefined ? (
          <button
            type="button"
            tabIndex={-1}
            onClick={() => choose()}
            className="w-full rounded px-2 py-1 text-left text-[12px] text-muted hover:bg-hover"
          >
            Clear
          </button>
        ) : null}
        {opts.map((o, i) => {
          const on = multi ? picked.includes(o.id) : value === o.id
          return (
            <button
              type="button"
              role="option"
              aria-selected={on}
              tabIndex={-1}
              key={o.id}
              onClick={() => choose(o)}
              className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left ${i === hi ? 'bg-hover' : ''}`}
            >
              {multi ? <span aria-hidden="true">{on ? '☑' : '☐'}</span> : null}
              <OptionChip o={o} />
            </button>
          )
        })}
        {opts.length === 0 ? <p className="px-2 py-1 text-[12px] text-muted">No matching option</p> : null}
      </div>
      {multi ? (
        <div className="flex justify-end border-t border-line p-1">
          <button
            type="button"
            onClick={() => void finish('down')}
            className="rounded px-2 py-1 text-[12px] font-medium text-accent hover:bg-hover"
          >
            Done
          </button>
        </div>
      ) : null}
      {error ? (
        <p role="alert" className="border-t border-bad px-2 py-1 text-[12px] text-bad">
          {error}
        </p>
      ) : null}
    </div>
  )
}
