'use client'
import { GripVertical, Plus, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls, Field as Labelled, Modal } from '@/components/ui'
import {
  type ConversionPreview,
  createField,
  deleteField,
  type Field,
  type FieldConfig,
  type FieldType,
  type Role,
  type SelectOption,
  type Table,
  updateField,
} from '@/lib/tables'

export const TYPES: { type: FieldType; label: string; hint: string }[] = [
  { type: 'text', label: 'Single line text', hint: 'A short name or code' },
  { type: 'long_text', label: 'Long text', hint: 'Notes over several lines' },
  { type: 'number', label: 'Number', hint: 'A count or measure' },
  { type: 'currency', label: 'Currency', hint: 'An amount in rupees' },
  { type: 'percent', label: 'Percent', hint: 'A share, such as 18%' },
  { type: 'date', label: 'Date', hint: 'A day, shown as DD-MM-YYYY' },
  { type: 'checkbox', label: 'Checkbox', hint: 'Yes or no' },
  { type: 'single_select', label: 'Single select', hint: 'One choice from a list' },
  { type: 'multi_select', label: 'Multiple select', hint: 'Several choices from a list' },
  { type: 'link', label: 'Link to another table', hint: 'Records from another table' },
  { type: 'user', label: 'Person', hint: 'A member of this workspace' },
  { type: 'phone', label: 'Phone', hint: 'A phone number with Call and WhatsApp' },
  { type: 'email', label: 'Email', hint: 'An email address' },
  { type: 'url', label: 'URL', hint: 'A web address' },
  { type: 'attachment', label: 'Attachment', hint: 'Pictures and PDF files' },
  { type: 'autonumber', label: 'Auto number', hint: 'A running number, set by the system' },
  { type: 'created_time', label: 'Created time', hint: 'When the record was added' },
  { type: 'modified_time', label: 'Last modified time', hint: 'When the record last changed' },
  { type: 'created_by', label: 'Created by', hint: 'Who added the record' },
  { type: 'modified_by', label: 'Last modified by', hint: 'Who last changed the record' },
]
export const typeLabel = (t: FieldType) => TYPES.find((x) => x.type === t)?.label ?? t
export const isComputed = (t: FieldType) =>
  ['autonumber', 'created_time', 'modified_time', 'created_by', 'modified_by'].includes(t)

/** Types a field may be converted to without losing data (design 17.4). */
const LOSSLESS: Partial<Record<FieldType, FieldType[]>> = {
  text: ['single_select', 'multi_select', 'phone', 'email', 'url'],
  phone: ['text'],
  email: ['text'],
  url: ['text'],
  number: ['currency', 'percent', 'text'],
  currency: ['number', 'percent', 'text'],
  percent: ['number', 'currency', 'text'],
  single_select: ['multi_select', 'text'],
}
const convertTargets = (from: FieldType): FieldType[] =>
  [...new Set<FieldType>([...(LOSSLESS[from] ?? []), 'text'])].filter((t) => t !== from)

// Colour keys for select options, the same ones cells.tsx renders; anything unknown reads as gray.
export const OPTION_COLORS = ['gray', 'blue', 'green', 'yellow', 'orange', 'red', 'purple'] as const
const DOT: Record<string, string> = {
  green: 'bg-ok',
  yellow: 'bg-warn',
  orange: 'bg-warn',
  red: 'bg-bad',
  blue: 'bg-accent',
  purple: 'bg-accent',
}
const CHIP: Record<string, string> = {
  green: 'bg-ok-bg text-ok',
  yellow: 'bg-warn-bg text-warn',
  orange: 'bg-warn-bg text-warn',
  red: 'bg-bad-bg text-bad',
  blue: 'bg-accent/15 text-accent',
  purple: 'bg-accent/15 text-accent',
}
export const dotCls = (c: string) => DOT[c] ?? 'bg-muted'
export const chipCls = (c: string) => CHIP[c] ?? 'bg-hover text-muted'

const newId = () => `o${Math.random().toString(36).slice(2, 8)}`

export function FieldDialog({
  ws,
  table,
  tables,
  field,
  role,
  onClose,
  onSaved,
}: {
  ws: string
  table: Table
  tables: Table[]
  field?: Field
  role: Role
  onClose: () => void
  onSaved: () => void
}) {
  const manager = role === 'manage'
  const isNew = !field
  const [name, setName] = useState(field?.name ?? '')
  const [type, setType] = useState<FieldType>(field?.type ?? 'text')
  const [q, setQ] = useState('')
  const [cfg, setCfg] = useState<FieldConfig>(field?.config ?? {})
  const [locked, setLocked] = useState(field?.locked ?? false)
  const [inverse, setInverse] = useState(false)
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState<ConversionPreview>()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const canConfig = isNew || manager || !field?.locked
  const typeChanged = !!field && type !== field.type
  const choices = useMemo(() => {
    const all = isNew
      ? TYPES
      : TYPES.filter((t) => t.type === field.type || convertTargets(field.type).includes(t.type))
    const s = q.trim().toLowerCase()
    return s ? all.filter((t) => `${t.label} ${t.hint}`.toLowerCase().includes(s)) : all
  }, [isNew, field, q])
  const isSelect = type === 'single_select' || type === 'multi_select'
  const opts = cfg.options ?? []
  const setOpts = (o: SelectOption[]) => setCfg({ ...cfg, options: o })

  function pickType(t: FieldType) {
    setType(t)
    setPreview(undefined)
    // Keep what carries over (options on a select, decimals on a number); start the rest clean.
    const next: FieldConfig = {}
    if (t === 'single_select' || t === 'multi_select') next.options = cfg.options ?? []
    if (['number', 'currency', 'percent'].includes(t))
      next.decimals = cfg.decimals ?? (t === 'currency' ? 2 : 0)
    if (t === 'link') next.table_id = tables.find((x) => x.id !== table.id)?.id ?? table.id
    if (t === 'link' || t === 'user') next.multiple = cfg.multiple ?? false
    if (t === 'autonumber') next.prefix = cfg.prefix ?? ''
    setCfg(next)
  }

  async function save(confirm = false) {
    setErr(undefined)
    if (!name.trim()) return setErr('Give the field a name.')
    if (isSelect && opts.some((o) => !o.name.trim())) return setErr('Every option needs a name.')
    if (type === 'link' && !cfg.table_id) return setErr('Choose the table to link to.')
    setBusy(true)
    try {
      if (!field) {
        await createField(ws, table.id, {
          name: name.trim(),
          type,
          config: cfg,
          inverse: type === 'link' && inverse,
        })
      } else {
        const patch: Parameters<typeof updateField>[2] = {}
        if (name.trim() !== field.name) patch.name = name.trim()
        if (canConfig && JSON.stringify(cfg) !== JSON.stringify(field.config)) patch.config = cfg
        if (manager && locked !== field.locked) patch.locked = locked
        if (typeChanged) Object.assign(patch, { type, confirm })
        if (Object.keys(patch).length) {
          const r = await updateField(ws, field.id, patch)
          if (typeChanged && !confirm) {
            setPreview(r as ConversionPreview)
            setBusy(false)
            return
          }
        }
      }
      onSaved()
      onClose()
    } catch (x) {
      setErr((x as Error).message)
    }
    setBusy(false)
  }

  async function remove() {
    if (!field) return
    setBusy(true)
    try {
      await deleteField(ws, field.id)
      onSaved()
      onClose()
    } catch (x) {
      setErr((x as Error).message)
      setBusy(false)
    }
  }

  const total = preview ? preview.kept + preview.changed + preview.cleared : 0
  return (
    <Modal open onClose={onClose} title={isNew ? 'Add field' : 'Edit field'} className="max-w-lg">
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          save(!!preview)
        }}
      >
        <Labelled label="Name">
          {(id) => (
            <input id={id} value={name} onChange={(e) => setName(e.target.value)} className={inputCls} />
          )}
        </Labelled>

        {(isNew || manager) && (
          <fieldset className="space-y-1.5">
            <legend className="text-[12px] font-medium text-muted">Type</legend>
            {(isNew || choices.length > 1) && (
              <>
                <input
                  aria-label="Search field types"
                  placeholder="Search types"
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  className={inputCls}
                />
                <div className="max-h-44 space-y-0.5 overflow-auto rounded-md border border-line p-1">
                  {choices.map((t) => (
                    <button
                      key={t.type}
                      type="button"
                      aria-pressed={type === t.type}
                      onClick={() => pickType(t.type)}
                      className={`flex w-full items-baseline gap-2 rounded px-2 py-1 text-left text-[13px] hover:bg-hover ${type === t.type ? 'bg-hover font-medium' : ''}`}
                    >
                      {t.label}
                      <span className="text-[12px] font-normal text-muted">{t.hint}</span>
                    </button>
                  ))}
                  {choices.length === 0 && (
                    <p className="px-2 py-1 text-[13px] text-muted">No type matches.</p>
                  )}
                </div>
              </>
            )}
            {!isNew && choices.length <= 1 && <p className="text-[13px]">{typeLabel(type)}</p>}
            {!isNew && (
              <p className="text-[12px] text-muted">
                Only changes that keep every value are offered. For anything else, add a new field and copy
                the values.
              </p>
            )}
          </fieldset>
        )}
        {!isNew && !manager && (
          <p className="text-[12px] text-muted">Type: {typeLabel(type)}. Managers can change it.</p>
        )}

        {['number', 'currency', 'percent'].includes(type) && (
          <Labelled label="Decimal places">
            {(id) => (
              <input
                id={id}
                type="number"
                min={0}
                max={8}
                disabled={!canConfig}
                value={cfg.decimals ?? 0}
                onChange={(e) =>
                  setCfg({ ...cfg, decimals: Math.max(0, Math.min(8, Number(e.target.value) || 0)) })
                }
                className={inputCls}
              />
            )}
          </Labelled>
        )}

        {isSelect && (
          <div className="space-y-1.5">
            <p className="text-[12px] font-medium text-muted">Options</p>
            {typeChanged && field?.type === 'text' && (
              <p className="text-[12px] text-muted">
                Options are made from the distinct values already in the field.
              </p>
            )}
            <ul className="space-y-1">
              {opts.map((o, i) => (
                <li key={o.id} className="flex items-center gap-1.5">
                  <GripVertical size={14} aria-hidden="true" className="text-muted" />
                  <select
                    aria-label={`Colour of ${o.name || 'option'}`}
                    disabled={!canConfig}
                    value={OPTION_COLORS.includes(o.color as never) ? o.color : 'gray'}
                    onChange={(e) =>
                      setOpts(opts.map((x) => (x.id === o.id ? { ...x, color: e.target.value } : x)))
                    }
                    className="w-20 rounded-md border border-line bg-canvas px-1 py-1 text-[12px]"
                  >
                    {OPTION_COLORS.map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </select>
                  <span
                    aria-hidden="true"
                    className={`h-2.5 w-2.5 shrink-0 rounded-full ${dotCls(o.color)}`}
                  />
                  <input
                    aria-label={`Option ${i + 1}`}
                    disabled={!canConfig}
                    value={o.name}
                    onChange={(e) =>
                      setOpts(opts.map((x) => (x.id === o.id ? { ...x, name: e.target.value } : x)))
                    }
                    className={`${inputCls} py-1`}
                  />
                  <button
                    type="button"
                    disabled={!canConfig}
                    aria-label={`Remove option ${o.name || i + 1}`}
                    onClick={() => setOpts(opts.filter((x) => x.id !== o.id))}
                    className="rounded p-1 hover:bg-hover disabled:opacity-50"
                  >
                    <Trash2 size={14} />
                  </button>
                </li>
              ))}
            </ul>
            {canConfig && (
              <button
                type="button"
                onClick={() =>
                  setOpts([
                    ...opts,
                    { id: newId(), name: '', color: OPTION_COLORS[opts.length % OPTION_COLORS.length] },
                  ])
                }
                className={btn}
              >
                <Plus size={14} /> Add option
              </button>
            )}
          </div>
        )}

        {type === 'link' && (
          <>
            <Labelled label="Link to table">
              {(id) => (
                <select
                  id={id}
                  disabled={!isNew}
                  value={cfg.table_id ?? ''}
                  onChange={(e) => setCfg({ ...cfg, table_id: e.target.value })}
                  className={inputCls}
                >
                  {tables.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name}
                    </option>
                  ))}
                </select>
              )}
            </Labelled>
            {isNew && (
              <label className="flex items-center gap-2 text-[13px]">
                <input type="checkbox" checked={inverse} onChange={(e) => setInverse(e.target.checked)} />
                Also show in {tables.find((t) => t.id === cfg.table_id)?.name ?? 'the other table'}
              </label>
            )}
          </>
        )}
        {(type === 'link' || type === 'user') && (
          <label className="flex items-center gap-2 text-[13px]">
            <input
              type="checkbox"
              disabled={!canConfig}
              checked={!!cfg.multiple}
              onChange={(e) => setCfg({ ...cfg, multiple: e.target.checked })}
            />
            {type === 'link' ? 'Allow several records' : 'Allow several people'}
          </label>
        )}
        {type === 'autonumber' && (
          <Labelled label="Prefix">
            {(id) => (
              <input
                id={id}
                value={cfg.prefix ?? ''}
                disabled={!canConfig}
                placeholder="ORD-"
                onChange={(e) => setCfg({ ...cfg, prefix: e.target.value })}
                className={inputCls}
              />
            )}
          </Labelled>
        )}

        {manager && !isNew && (
          <label className="flex items-center gap-2 text-[13px]">
            <input type="checkbox" checked={locked} onChange={(e) => setLocked(e.target.checked)} />
            Only managers can edit values
          </label>
        )}

        {preview && (
          <p role="status" className="rounded-md bg-warn-bg p-2 text-[13px] text-warn">
            {preview.changed} changed, {preview.kept} kept, {preview.cleared} cleared.
            {preview.samples.length > 0 &&
              ` For example ${JSON.stringify(preview.samples[0].from)} becomes ${JSON.stringify(preview.samples[0].to)}.`}
          </p>
        )}
        <ErrorText error={err} />

        {confirmDelete ? (
          <div
            role="alertdialog"
            aria-label="Delete this field"
            className="space-y-2 rounded-md border border-bad p-2"
          >
            <p className="text-[13px]">
              Delete {field?.name}? Its values are kept for 30 days and come back if you restore the field.
            </p>
            <div className="flex justify-end gap-2">
              <button type="button" onClick={() => setConfirmDelete(false)} className={btn}>
                Keep field
              </button>
              <button type="button" disabled={busy} onClick={remove} className={`${btn} text-bad`}>
                Delete field
              </button>
            </div>
          </div>
        ) : (
          <div className="flex items-center justify-between gap-2">
            {manager && field && field.id !== table.primary_field_id ? (
              <button type="button" onClick={() => setConfirmDelete(true)} className={`${btn} text-bad`}>
                Delete field
              </button>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
              <button type="button" onClick={onClose} className={btn}>
                Cancel
              </button>
              <button type="submit" disabled={busy} className={btnPrimary}>
                {preview ? `Convert ${total} records` : 'Save field'}
              </button>
            </div>
          </div>
        )}
      </form>
    </Modal>
  )
}
