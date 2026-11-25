'use client'
import { ArrowUpDown, Plus, X } from 'lucide-react'
import { btn, inputCls } from '@/components/ui'
import type { Field, Sort, ViewConfig } from '@/lib/tables'
import { Popover } from './popover'

const NUMERIC = [
  'number',
  'currency',
  'percent',
  'autonumber',
  'date',
  'created_time',
  'modified_time',
  'checkbox',
]
export const sortable = (f: Field) => f.type !== 'link' && f.type !== 'attachment'
export const dirLabel = (f: Field, desc?: boolean) =>
  NUMERIC.includes(f.type) ? (desc ? '9 to 1' : '1 to 9') : desc ? 'Z to A' : 'A to Z'

export function SortMenu({
  fields,
  config,
  onChange,
}: {
  fields: Field[]
  config: ViewConfig
  onChange: (p: Partial<ViewConfig>) => void
}) {
  const usable = fields.filter(sortable)
  const sorts = config.sorts ?? []
  const set = (s: Sort[]) => onChange({ sorts: s })
  return (
    <Popover
      label="Sort"
      active={sorts.length > 0}
      className="w-80"
      trigger={
        <>
          <ArrowUpDown size={14} /> Sort
          {sorts.length > 0 && <span className="text-muted">{sorts.length}</span>}
        </>
      }
    >
      {() => (
        <div className="space-y-2">
          {sorts.length === 0 && (
            <p className="text-[13px] text-muted">No sorting. Records show in the order they were added.</p>
          )}
          {sorts.map((s, i) => {
            const f = fields.find((x) => x.id === s.field) ?? usable[0]
            return (
              <div key={s.field} className="flex items-center gap-1.5">
                <select
                  aria-label={`Sort ${i + 1} field`}
                  value={f.id}
                  onChange={(e) => set(sorts.map((x, j) => (j === i ? { ...x, field: e.target.value } : x)))}
                  className={`${inputCls} py-1`}
                >
                  {usable.map((x) => (
                    <option
                      key={x.id}
                      value={x.id}
                      disabled={x.id !== f.id && sorts.some((y) => y.field === x.id)}
                    >
                      {x.name}
                    </option>
                  ))}
                </select>
                <select
                  aria-label={`Sort ${i + 1} direction`}
                  value={s.desc ? 'desc' : 'asc'}
                  onChange={(e) =>
                    set(sorts.map((x, j) => (j === i ? { ...x, desc: e.target.value === 'desc' } : x)))
                  }
                  className="w-24 rounded-md border border-line bg-canvas px-1 py-1 text-[13px]"
                >
                  <option value="asc">{dirLabel(f)}</option>
                  <option value="desc">{dirLabel(f, true)}</option>
                </select>
                <button
                  type="button"
                  aria-label={`Remove sort ${i + 1}`}
                  onClick={() => set(sorts.filter((_, j) => j !== i))}
                  className="rounded p-1 hover:bg-hover"
                >
                  <X size={14} />
                </button>
              </div>
            )
          })}
          <button
            type="button"
            disabled={sorts.length >= 3 || sorts.length >= usable.length}
            onClick={() =>
              set([
                ...sorts,
                { field: (usable.find((f) => !sorts.some((s) => s.field === f.id)) ?? usable[0]).id },
              ])
            }
            className={btn}
          >
            <Plus size={14} /> Add sort
          </button>
        </div>
      )}
    </Popover>
  )
}
