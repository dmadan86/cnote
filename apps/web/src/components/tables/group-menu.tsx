'use client'
import { Rows3 } from 'lucide-react'
import { inputCls } from '@/components/ui'
import type { Field, ViewConfig } from '@/lib/tables'
import { Popover } from './popover'

const GROUPABLE = ['single_select', 'checkbox', 'user', 'text', 'date']

export function GroupMenu({
  fields,
  config,
  onChange,
}: {
  fields: Field[]
  config: ViewConfig
  onChange: (p: Partial<ViewConfig>) => void
}) {
  const cur = config.group?.field ?? ''
  return (
    <Popover
      label="Group"
      active={!!cur}
      trigger={
        <>
          <Rows3 size={14} /> Group{cur && <span className="text-muted">1</span>}
        </>
      }
    >
      {() => (
        <label className="block space-y-1 text-[12px] font-medium text-muted">
          Group records by
          <select
            value={cur}
            onChange={(e) => onChange({ group: e.target.value ? { field: e.target.value } : null })}
            className={`${inputCls} text-[14px] font-normal text-fg`}
          >
            <option value="">No grouping</option>
            {fields
              .filter((f) => GROUPABLE.includes(f.type))
              .map((f) => (
                <option key={f.id} value={f.id}>
                  {f.name}
                </option>
              ))}
          </select>
        </label>
      )}
    </Popover>
  )
}
