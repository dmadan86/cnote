'use client'
import { LayoutList } from 'lucide-react'
import { btn, inputCls } from '@/components/ui'
import type { Field, Table, TableView, ViewConfig } from '@/lib/tables'
import { FieldsMenu } from './fields-menu'
import { FilterMenu } from './filter-menu'
import { GroupMenu } from './group-menu'
import { Popover } from './popover'
import { SortMenu } from './sort-menu'

/** Toolbar for the shared view. Writers and managers only: it edits the view for everyone. */
export function ViewBar({
  table,
  fields,
  view,
  onChange,
}: {
  table: Table
  fields: Field[]
  view: TableView
  onChange: (patch: Partial<ViewConfig>) => void
}) {
  const c = view.config
  const stacks = fields.filter((f) => f.type === 'single_select')
  const k = c.kanban
  const card = k?.card_fields ?? []
  return (
    <div role="toolbar" aria-label="View settings" className="flex flex-wrap items-center gap-2">
      {view.type === 'grid' ? (
        <>
          <FieldsMenu table={table} fields={fields} config={c} onChange={onChange} />
          <FilterMenu fields={fields} config={c} onChange={onChange} />
          <GroupMenu fields={fields} config={c} onChange={onChange} />
          <SortMenu fields={fields} config={c} onChange={onChange} />
        </>
      ) : (
        <>
          <label className="flex items-center gap-1.5 text-[13px]">
            Stack by
            <select
              value={k?.stack ?? ''}
              onChange={(e) => onChange({ kanban: { stack: e.target.value, card_fields: card } })}
              className={`${inputCls} w-auto py-1`}
            >
              {!k?.stack && <option value="">Choose a field</option>}
              {stacks.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.name}
                </option>
              ))}
            </select>
          </label>
          <Popover
            label="Card fields"
            trigger={
              <>
                <LayoutList size={14} /> Card fields
                {card.length > 0 && <span className="text-muted">{card.length}</span>}
              </>
            }
          >
            {() => (
              <fieldset className="space-y-0.5">
                <legend className="mb-1 text-[12px] text-muted">Up to four fields show on each card.</legend>
                {fields
                  .filter((f) => f.id !== table.primary_field_id && f.id !== k?.stack)
                  .map((f) => (
                    <label key={f.id} className="flex items-center gap-2 px-1 py-0.5 text-[13px]">
                      <input
                        type="checkbox"
                        checked={card.includes(f.id)}
                        disabled={!card.includes(f.id) && card.length >= 4}
                        onChange={(e) =>
                          onChange({
                            kanban: {
                              stack: k?.stack ?? '',
                              card_fields: e.target.checked
                                ? [...card, f.id]
                                : card.filter((x) => x !== f.id),
                            },
                          })
                        }
                      />
                      {f.name}
                    </label>
                  ))}
              </fieldset>
            )}
          </Popover>
          <FilterMenu fields={fields} config={c} onChange={onChange} />
          <SortMenu fields={fields} config={c} onChange={onChange} />
        </>
      )}
      {(c.filters?.rules.length ?? 0) > 0 && (
        <button
          type="button"
          onClick={() => onChange({ filters: { match: 'and', rules: [] } })}
          className={btn}
        >
          Clear filters
        </button>
      )}
    </div>
  )
}
