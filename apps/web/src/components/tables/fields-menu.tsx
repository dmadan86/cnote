'use client'
import { ArrowDown, ArrowUp, EyeOff } from 'lucide-react'
import { btn } from '@/components/ui'
import type { Field, Table, ViewConfig } from '@/lib/tables'
import { Popover } from './popover'

/** Shared view: which fields are hidden for everyone, and their order. Per-person hiding is in the grid. */
export function FieldsMenu({
  table,
  fields,
  config,
  onChange,
}: {
  table: Table
  fields: Field[]
  config: ViewConfig
  onChange: (p: Partial<ViewConfig>) => void
}) {
  const hidden = config.hidden ?? []
  const ids = fields.map((f) => f.id)
  const order = [
    ...(config.order ?? []).filter((i) => ids.includes(i)),
    ...ids.filter((i) => !(config.order ?? []).includes(i)),
  ]
  const byId = (i: string) => fields.find((f) => f.id === i) as Field
  const move = (i: number, d: number) => {
    const o = [...order]
    ;[o[i], o[i + d]] = [o[i + d], o[i]]
    onChange({ order: o })
  }
  const toggle = (id: string) =>
    onChange({ hidden: hidden.includes(id) ? hidden.filter((x) => x !== id) : [...hidden, id] })
  return (
    <Popover
      label="Hide fields"
      active={hidden.length > 0}
      trigger={
        <>
          <EyeOff size={14} /> {hidden.length > 0 ? `${hidden.length} hidden` : 'Hide fields'}
        </>
      }
    >
      {() => (
        <div className="space-y-2">
          <ul className="max-h-64 space-y-0.5 overflow-auto">
            {order.map((id, i) => {
              const f = byId(id)
              const primary = f.id === table.primary_field_id
              return (
                <li key={id} className="flex items-center gap-1">
                  <label className="flex flex-1 items-center gap-2 px-1 py-0.5 text-[13px]">
                    <input
                      type="checkbox"
                      disabled={primary}
                      checked={!hidden.includes(id)}
                      onChange={() => toggle(id)}
                    />
                    {f.name}
                  </label>
                  <button
                    type="button"
                    disabled={i === 0 || primary}
                    aria-label={`Move ${f.name} up`}
                    onClick={() => move(i, -1)}
                    className="rounded p-1 hover:bg-hover disabled:opacity-30"
                  >
                    <ArrowUp size={13} />
                  </button>
                  <button
                    type="button"
                    disabled={i === order.length - 1 || primary}
                    aria-label={`Move ${f.name} down`}
                    onClick={() => move(i, 1)}
                    className="rounded p-1 hover:bg-hover disabled:opacity-30"
                  >
                    <ArrowDown size={13} />
                  </button>
                </li>
              )
            })}
          </ul>
          <div className="flex gap-2">
            <button
              type="button"
              className={btn}
              onClick={() => onChange({ hidden: ids.filter((i) => i !== table.primary_field_id) })}
            >
              Hide all
            </button>
            <button type="button" className={btn} onClick={() => onChange({ hidden: [] })}>
              Show all
            </button>
          </div>
        </div>
      )}
    </Popover>
  )
}
