'use client'
import { Filter as FilterIcon, Plus, X } from 'lucide-react'
import { useShell } from '@/components/shell'
import { btn, inputCls } from '@/components/ui'
import type { Field, Filter, Rule, ViewConfig } from '@/lib/tables'
import { Popover } from './popover'

const TEXT = ['text', 'long_text', 'phone', 'email', 'url']
const NUM = ['number', 'currency', 'percent', 'autonumber']
const DATE = ['date', 'created_time', 'modified_time']
const USER = ['user', 'created_by', 'modified_by']

export const OP_LABEL: Record<string, string> = {
  contains: 'contains',
  is: 'is',
  is_not: 'is not',
  empty: 'is empty',
  not_empty: 'is not empty',
  '=': '=',
  '!=': '!=',
  '<': '<',
  '<=': '<=',
  '>': '>',
  '>=': '>=',
  today: 'is today',
  this_week: 'is this week',
  this_month: 'is this month',
  past_days: 'is in the past N days',
  next_days: 'is in the next N days',
  any_of: 'is any of',
  none_of: 'is none of',
}
const NUMOPS = ['=', '!=', '<', '<=', '>', '>=', 'empty']
export function opsFor(t: Field['type']): string[] {
  if (TEXT.includes(t)) return ['contains', 'is', 'is_not', 'empty', 'not_empty']
  if (NUM.includes(t)) return NUMOPS
  if (DATE.includes(t)) return [...NUMOPS, 'today', 'this_week', 'this_month', 'past_days', 'next_days']
  if (t === 'single_select' || t === 'multi_select' || USER.includes(t)) return ['any_of', 'none_of', 'empty']
  if (t === 'checkbox') return ['is']
  return ['empty', 'not_empty']
}
const NO_VALUE = ['empty', 'not_empty', 'today', 'this_week', 'this_month']
const baseOp = (op: string) => op.split(':')[0]
export const filterable = (f: Field) => f.type !== 'attachment'

function Value({ f, rule, onChange }: { f: Field; rule: Rule; onChange: (v: unknown) => void }) {
  const { members } = useShell()
  const op = baseOp(rule.op)
  if (NO_VALUE.includes(op)) return null
  const label = `Value for ${f.name}`
  if (f.type === 'checkbox')
    return (
      <select
        aria-label={label}
        value={String(rule.value ?? true)}
        onChange={(e) => onChange(e.target.value === 'true')}
        className={inputCls}
      >
        <option value="true">Checked</option>
        <option value="false">Unchecked</option>
      </select>
    )
  if (op === 'any_of' || op === 'none_of') {
    const items =
      f.type === 'single_select' || f.type === 'multi_select'
        ? (f.config.options ?? []).map((o) => ({ id: o.id, name: o.name }))
        : [{ id: 'me', name: 'Me' }, ...members.map((m) => ({ id: m.user_id, name: m.name }))]
    const cur = (Array.isArray(rule.value) ? rule.value : []) as string[]
    return (
      <fieldset
        aria-label={label}
        className="m-0 max-h-28 w-full overflow-auto rounded-md border border-line p-1"
      >
        {items.map((it) => (
          <label key={it.id} className="flex items-center gap-2 px-1 py-0.5 text-[13px]">
            <input
              type="checkbox"
              checked={cur.includes(it.id)}
              onChange={(e) => onChange(e.target.checked ? [...cur, it.id] : cur.filter((x) => x !== it.id))}
            />
            {it.name}
          </label>
        ))}
      </fieldset>
    )
  }
  if (op === 'past_days' || op === 'next_days')
    return (
      <input
        aria-label="Number of days"
        type="number"
        min={1}
        value={rule.op.split(':')[1] ?? ''}
        onChange={(e) => onChange({ op: `${op}:${e.target.value}` })}
        className={inputCls}
      />
    )
  const num = NUM.includes(f.type)
  const money = f.type === 'currency'
  return (
    <input
      aria-label={label}
      type={DATE.includes(f.type) ? 'date' : num || money ? 'number' : 'text'}
      value={money && typeof rule.value === 'number' ? rule.value / 100 : String(rule.value ?? '')}
      onChange={(e) => {
        const v = e.target.value
        onChange(
          num ? (v === '' ? '' : Number(v)) : money ? (v === '' ? '' : Math.round(Number(v) * 100)) : v,
        )
      }}
      className={inputCls}
    />
  )
}

export function FilterMenu({
  fields,
  config,
  onChange,
}: {
  fields: Field[]
  config: ViewConfig
  onChange: (p: Partial<ViewConfig>) => void
}) {
  const usable = fields.filter(filterable)
  const f: Filter = config.filters ?? { match: 'and', rules: [] }
  const set = (rules: Rule[], match = f.match) => onChange({ filters: { match, rules } })
  const n = f.rules.length
  return (
    <Popover
      label="Filter"
      active={n > 0}
      className="w-[22rem]"
      trigger={
        <>
          <FilterIcon size={14} /> Filter{n > 0 && <span className="text-muted">{n}</span>}
        </>
      }
    >
      {() => (
        <div className="space-y-2">
          {n === 0 && <p className="text-[13px] text-muted">No filters. Every record is shown.</p>}
          {f.rules.map((r, i) => {
            const fld = fields.find((x) => x.id === r.field) ?? usable[0]
            const ops = opsFor(fld.type)
            return (
              // biome-ignore lint/suspicious/noArrayIndexKey: rules have no id; order is the identity
              <div key={i} className="space-y-1 rounded-md border border-line p-2">
                <div className="flex items-center gap-1.5">
                  {i === 0 ? (
                    <span className="w-14 text-[12px] text-muted">Where</span>
                  ) : n > 1 && i === 1 ? (
                    <select
                      aria-label="Match all or any rule"
                      value={f.match}
                      onChange={(e) => set(f.rules, e.target.value as 'and' | 'or')}
                      className="w-14 rounded-md border border-line bg-canvas px-1 py-1 text-[12px]"
                    >
                      <option value="and">and</option>
                      <option value="or">or</option>
                    </select>
                  ) : (
                    <span className="w-14 text-[12px] text-muted">{f.match}</span>
                  )}
                  <select
                    aria-label="Field"
                    value={fld.id}
                    onChange={(e) => {
                      const nf = fields.find((x) => x.id === e.target.value) as Field
                      set(f.rules.map((x, j) => (j === i ? { field: nf.id, op: opsFor(nf.type)[0] } : x)))
                    }}
                    className={`${inputCls} py-1`}
                  >
                    {usable.map((x) => (
                      <option key={x.id} value={x.id}>
                        {x.name}
                      </option>
                    ))}
                  </select>
                  <button
                    type="button"
                    aria-label={`Remove filter ${i + 1}`}
                    onClick={() => set(f.rules.filter((_, j) => j !== i))}
                    className="rounded p-1 hover:bg-hover"
                  >
                    <X size={14} />
                  </button>
                </div>
                <select
                  aria-label="Condition"
                  value={baseOp(r.op)}
                  onChange={(e) =>
                    set(
                      f.rules.map((x, j) =>
                        j === i
                          ? {
                              field: r.field,
                              op:
                                e.target.value === 'past_days' || e.target.value === 'next_days'
                                  ? `${e.target.value}:7`
                                  : e.target.value,
                            }
                          : x,
                      ),
                    )
                  }
                  className={`${inputCls} py-1`}
                >
                  {ops.map((o) => (
                    <option key={o} value={o}>
                      {OP_LABEL[o] ?? o}
                    </option>
                  ))}
                </select>
                <Value
                  f={fld}
                  rule={r}
                  onChange={(v) =>
                    set(
                      f.rules.map((x, j) =>
                        j === i
                          ? v && typeof v === 'object' && 'op' in v
                            ? { ...x, op: (v as { op: string }).op }
                            : { ...x, value: v }
                          : x,
                      ),
                    )
                  }
                />
              </div>
            )
          })}
          <div className="flex justify-between">
            <button
              type="button"
              disabled={n >= 10 || usable.length === 0}
              onClick={() => set([...f.rules, { field: usable[0].id, op: opsFor(usable[0].type)[0] }])}
              className={btn}
            >
              <Plus size={14} /> Add filter
            </button>
            {n > 0 && (
              <button type="button" onClick={() => set([])} className={btn}>
                Clear filters
              </button>
            )}
          </div>
        </div>
      )}
    </Popover>
  )
}
