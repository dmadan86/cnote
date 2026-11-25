'use client'
import { Link2, MessageCircle, Phone, Trash2, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, ErrorText, inputCls } from '@/components/ui'
import {
  deleteRecords,
  type Field,
  getRecord,
  type HistoryEntry,
  type LinkChip,
  type Role,
  recordHistory,
  type Table,
  type TableRecordWithLinks,
  updateRecord,
} from '@/lib/tables'
import { cellText } from './cells'
import { typeLabel } from './field-dialog'
import { LinkPicker } from './link-picker'

const fmtAt = (iso: string) =>
  new Date(iso).toLocaleString('en-IN', {
    day: 'numeric',
    month: 'short',
    hour: 'numeric',
    minute: '2-digit',
  })

export function useDisplay() {
  const { members } = useShell()
  const name = (id: unknown) => members.find((m) => m.user_id === id)?.name ?? String(id)
  return (f: Field | undefined, v: unknown): string => {
    if (v === null || v === undefined || v === '') return 'empty'
    if (!f) return String(v)
    if (f.type === 'single_select' || f.type === 'multi_select') {
      const ids = Array.isArray(v) ? v : [v]
      return ids.map((i) => f.config.options?.find((o) => o.id === i)?.name ?? i).join(', ')
    }
    if (f.type === 'user' || f.type === 'created_by' || f.type === 'modified_by')
      return (Array.isArray(v) ? v : [v]).map(name).join(', ')
    if (f.type === 'currency' && typeof v === 'number') return (v / 100).toFixed(f.config.decimals ?? 2)
    if (f.type === 'checkbox') return v ? 'checked' : 'unchecked'
    return Array.isArray(v) ? v.join(', ') : String(v)
  }
}

function Editor({
  f,
  value,
  chips,
  computed,
  disabled,
  onSave,
  onLink,
}: {
  f: Field
  computed: string
  value: unknown
  chips: LinkChip[]
  disabled: boolean
  onSave: (v: unknown) => void
  onLink: () => void
}) {
  const { members } = useShell()
  const cls = `${inputCls} min-h-11 sm:min-h-0`
  const [text, setText] = useState(
    value == null
      ? ''
      : f.type === 'currency' && typeof value === 'number'
        ? String(value / 100)
        : String(value),
  )
  const label = f.name
  const commit = (raw: string) => {
    const same =
      raw ===
      (value == null
        ? ''
        : f.type === 'currency' && typeof value === 'number'
          ? String(value / 100)
          : String(value))
    if (same) return
    if (raw === '') return onSave(null)
    if (f.type === 'currency') return onSave(Math.round(Number(raw) * 100))
    if (f.type === 'number' || f.type === 'percent') return onSave(Number(raw))
    onSave(raw)
  }
  switch (f.type) {
    case 'long_text':
      return (
        <textarea
          aria-label={label}
          rows={3}
          disabled={disabled}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onBlur={() => commit(text)}
          className={cls}
        />
      )
    case 'checkbox':
      return (
        <label className="flex min-h-11 items-center gap-2 text-[14px] sm:min-h-0">
          <input
            type="checkbox"
            aria-label={label}
            disabled={disabled}
            checked={!!value}
            onChange={(e) => onSave(e.target.checked)}
          />
          {value ? 'Checked' : 'Unchecked'}
        </label>
      )
    case 'single_select':
      return (
        <select
          aria-label={label}
          disabled={disabled}
          value={(value as string) ?? ''}
          onChange={(e) => onSave(e.target.value || null)}
          className={cls}
        >
          <option value="">Empty</option>
          {(f.config.options ?? []).map((o) => (
            <option key={o.id} value={o.id}>
              {o.name}
            </option>
          ))}
        </select>
      )
    case 'multi_select':
    case 'user': {
      const items =
        f.type === 'user'
          ? members.map((m) => ({ id: m.user_id, name: m.name }))
          : (f.config.options ?? []).map((o) => ({ id: o.id, name: o.name }))
      const multi = f.type === 'multi_select' || f.config.multiple
      const cur = (Array.isArray(value) ? value : value ? [value] : []) as string[]
      if (!multi)
        return (
          <select
            aria-label={label}
            disabled={disabled}
            value={cur[0] ?? ''}
            onChange={(e) => onSave(e.target.value || null)}
            className={cls}
          >
            <option value="">Empty</option>
            {items.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
          </select>
        )
      return (
        <fieldset aria-label={label} className="m-0 flex min-w-0 flex-wrap gap-1.5 border-0 p-0">
          {items.map((o) => (
            <label
              key={o.id}
              className="flex min-h-11 items-center gap-1.5 rounded-full border border-line px-2.5 text-[13px] sm:min-h-0 sm:py-0.5"
            >
              <input
                type="checkbox"
                disabled={disabled}
                checked={cur.includes(o.id)}
                onChange={(e) => onSave(e.target.checked ? [...cur, o.id] : cur.filter((x) => x !== o.id))}
              />
              {o.name}
            </label>
          ))}
        </fieldset>
      )
    }
    case 'link':
      return (
        <div className="flex flex-wrap items-center gap-1.5">
          {chips.map((c) => (
            <span key={c.id} className="rounded-full bg-hover px-2 py-0.5 text-[13px]">
              {c.title}
            </span>
          ))}
          {!disabled && (
            <button type="button" onClick={onLink} className={`${btn} min-h-11 sm:min-h-0`}>
              <Link2 size={14} /> {chips.length ? 'Change' : 'Add link'}
            </button>
          )}
          {disabled && chips.length === 0 && <span className="text-[14px] text-muted">empty</span>}
        </div>
      )
    case 'attachment':
      return <p className="text-[13px] text-muted">Attachments are not available yet.</p>
    case 'autonumber':
    case 'created_time':
    case 'modified_time':
    case 'created_by':
    case 'modified_by':
      return <p className="min-h-11 py-2 text-[14px] sm:min-h-0 sm:py-0">{computed || '-'}</p>
    default: {
      const t =
        (
          {
            number: 'number',
            percent: 'number',
            currency: 'number',
            date: 'date',
            email: 'email',
            url: 'url',
            phone: 'tel',
          } as Record<string, string>
        )[f.type] ?? 'text'
      const digits = text.replace(/\D/g, '')
      return (
        <div className="space-y-1">
          <input
            aria-label={label}
            type={t}
            step={t === 'number' ? 'any' : undefined}
            inputMode={f.type === 'phone' ? 'tel' : undefined}
            disabled={disabled}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onBlur={() => commit(text)}
            className={cls}
          />
          {f.type === 'phone' && digits.length >= 8 && (
            <div className="flex gap-2">
              <a href={`tel:${text.replace(/[^\d+]/g, '')}`} className={`${btn} min-h-11 sm:min-h-0`}>
                <Phone size={14} /> Call
              </a>
              <a
                href={`https://wa.me/${digits.length === 10 ? `91${digits}` : digits}`}
                target="_blank"
                rel="noreferrer"
                className={`${btn} min-h-11 sm:min-h-0`}
              >
                <MessageCircle size={14} /> WhatsApp
              </a>
            </div>
          )}
        </div>
      )
    }
  }
}

/** Full screen below 640 px, a side panel above. Values save as you leave each field. */
export function RecordPanel({
  ws,
  table,
  fields,
  tables,
  hidden,
  recordId,
  role,
  onClose,
  onChanged,
}: {
  ws: string
  table: Table
  fields: Field[]
  tables: Table[]
  hidden: string[]
  recordId: string
  role: Role
  onClose: () => void
  onChanged: () => void
}) {
  const { members } = useShell()
  const [rec, setRec] = useState<TableRecordWithLinks>()
  const [tab, setTab] = useState<'details' | 'history'>('details')
  const [hist, setHist] = useState<HistoryEntry[]>()
  const [errs, setErrs] = useState<Record<string, string>>({})
  const [err, setErr] = useState<string>()
  const [picking, setPicking] = useState<Field>()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const dirty = useRef(false)
  const head = useRef<HTMLHeadingElement>(null)
  const display = useDisplay()
  const write = role !== 'read'
  const manager = role === 'manage'

  const load = useCallback(
    () =>
      getRecord(ws, recordId)
        .then(setRec)
        .catch((e) => setErr((e as Error).message)),
    [ws, recordId],
  )
  useEffect(() => {
    load()
  }, [load])
  useEffect(() => {
    if (tab === 'history')
      recordHistory(ws, recordId)
        .then(setHist)
        .catch((e) => setErr((e as Error).message))
  }, [tab, ws, recordId])
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    head.current?.focus()
    return () => prev?.focus?.()
  }, [])
  // Saves in flight, so closing can wait for them: the edit is not lost and the grid refreshes after the last one.
  const pending = useRef(new Set<Promise<boolean>>())
  const track = (p: Promise<boolean>) => {
    pending.current.add(p)
    p.finally(() => pending.current.delete(p))
    return p
  }
  const close = useCallback(async () => {
    setErr(undefined)
    const results = await Promise.all([...pending.current])
    if (results.includes(false)) {
      setErr('A change could not be saved. Fix it or reload before closing')
      return // stay open: the field shows the reason
    }
    if (dirty.current) onChanged()
    onClose()
  }, [onChanged, onClose])

  const save = (f: Field, v: unknown) => track(saveValue(f, v))
  const saveLinks = (f: Field, chips: LinkChip[]) => track(saveChips(f, chips))
  async function saveValue(f: Field, v: unknown) {
    setErrs((e) => ({ ...e, [f.id]: '' }))
    try {
      const r = await updateRecord(
        ws,
        recordId,
        v === null || v === '' ? { clear: [f.id] } : { values: { [f.id]: v } },
      )
      setRec(r)
      dirty.current = true
      return true
    } catch (x) {
      setErrs((e) => ({ ...e, [f.id]: (x as Error).message }))
      return false
    }
  }
  async function saveChips(f: Field, chips: LinkChip[]) {
    try {
      const r = await updateRecord(ws, recordId, { links: { [f.id]: chips.map((c) => c.id) } })
      setRec(r)
      dirty.current = true
      return true
    } catch (x) {
      setErrs((e) => ({ ...e, [f.id]: (x as Error).message }))
      return false
    }
  }

  const title = rec ? String(rec.data[table.primary_field_id] ?? '') || `Record ${rec.seq}` : 'Record'
  const shown = fields.filter((f) => !hidden.includes(f.id))
  const hiddenFields = fields.filter((f) => hidden.includes(f.id))
  const who = (id: string | null) => members.find((m) => m.user_id === id)?.name ?? 'someone'
  const row = (f: Field) => {
    const locked = f.locked && !manager
    return (
      <div key={f.id} className="space-y-1">
        <p className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted">
          {f.name}
          <span className="font-normal normal-case">{typeLabel(f.type)}</span>
          {f.locked && <span className="font-normal normal-case">(locked)</span>}
        </p>
        {rec && (
          <Editor
            key={f.id}
            f={f}
            value={rec.data[f.id]}
            chips={rec.links[f.id] ?? []}
            computed={cellText(f, rec, undefined, members)}
            disabled={!write || locked}
            onSave={(v) => save(f, v)}
            onLink={() => setPicking(f)}
          />
        )}
        {errs[f.id] && (
          <p role="alert" className="text-[13px] text-bad">
            {errs[f.id]}
          </p>
        )}
      </div>
    )
  }

  const target = picking && tables.find((t) => t.id === picking.config.table_id)
  return (
    <>
      <div
        role="dialog"
        aria-label={`Record ${title}`}
        onKeyDown={(e) => e.key === 'Escape' && !picking && close()}
        className="fixed inset-0 z-40 flex flex-col bg-canvas sm:inset-y-0 sm:left-auto sm:right-0 sm:w-[28rem] sm:border-l sm:border-line sm:shadow-xl"
      >
        <header className="flex items-center gap-2 border-b border-line px-4 py-3">
          <h2
            ref={head}
            tabIndex={-1}
            className="min-w-0 flex-1 truncate text-[15px] font-semibold focus:outline-none"
          >
            {title}
          </h2>
          {write && (
            <button
              type="button"
              aria-label="Delete record"
              onClick={() => setConfirmDelete(true)}
              className="rounded p-2 hover:bg-hover"
            >
              <Trash2 size={16} />
            </button>
          )}
          <button
            type="button"
            aria-label="Close record"
            onClick={close}
            className="rounded p-2 hover:bg-hover"
          >
            <X size={16} />
          </button>
        </header>
        <div
          role="tablist"
          aria-label="Record sections"
          className="flex gap-1 border-b border-line px-3 pt-2"
        >
          {(['details', 'history'] as const).map((t) => (
            <button
              key={t}
              type="button"
              role="tab"
              aria-selected={tab === t}
              onClick={() => setTab(t)}
              className={`rounded-t px-3 py-1.5 text-[13px] ${tab === t ? 'border-b-2 border-accent font-medium' : 'text-muted'}`}
            >
              {t === 'details' ? 'Details' : 'History'}
            </button>
          ))}
        </div>
        <div role="tabpanel" className="flex-1 space-y-4 overflow-auto px-4 py-4">
          <ErrorText error={err} />
          {confirmDelete && (
            <div
              role="alertdialog"
              aria-label="Delete this record"
              className="space-y-2 rounded-md border border-bad p-2"
            >
              <p className="text-[13px]">Delete this record? It can be restored for 30 days.</p>
              <div className="flex justify-end gap-2">
                <button type="button" className={btn} onClick={() => setConfirmDelete(false)}>
                  Keep record
                </button>
                <button
                  type="button"
                  className={`${btn} text-bad`}
                  onClick={async () => {
                    try {
                      await deleteRecords(ws, [recordId])
                      dirty.current = true
                      close()
                    } catch (x) {
                      setErr((x as Error).message)
                    }
                  }}
                >
                  Delete record
                </button>
              </div>
            </div>
          )}
          {tab === 'details' ? (
            <>
              {!rec && !err && <p className="text-[13px] text-muted">Loading record.</p>}
              {rec && shown.map(row)}
              {rec && hiddenFields.length > 0 && (
                <details>
                  <summary className="cursor-pointer text-[13px] text-muted">
                    {hiddenFields.length} hidden fields
                  </summary>
                  <div className="mt-3 space-y-4">{hiddenFields.map(row)}</div>
                </details>
              )}
            </>
          ) : (
            <ol aria-label="Change history" className="space-y-3">
              {!hist && <li className="text-[13px] text-muted">Loading history.</li>}
              {hist?.length === 0 && <li className="text-[13px] text-muted">No changes yet.</li>}
              {hist?.map((h) => (
                <li key={h.id} className="text-[13px]">
                  <p className="font-medium">
                    {h.actor_type === 'user' ? who(h.actor_id) : h.actor_type}{' '}
                    <span className="font-normal text-muted">{fmtAt(h.at)}</span>
                  </p>
                  <ul className="text-muted">
                    {Object.entries(h.change).map(([fid, [from, to]]) => {
                      const f = fields.find((x) => x.id === fid)
                      return (
                        <li key={fid}>
                          {f?.name ?? 'A removed field'}: {display(f, from)} to {display(f, to)}
                        </li>
                      )
                    })}
                  </ul>
                </li>
              ))}
            </ol>
          )}
        </div>
        {rec && (
          <footer className="border-t border-line px-4 py-2 text-[12px] text-muted">
            Changed by {who(rec.updated_by)}, {fmtAt(rec.updated_at)}
          </footer>
        )}
      </div>
      {picking && target && rec && (
        <LinkPicker
          ws={ws}
          target={target}
          value={rec.links[picking.id] ?? []}
          multiple={!!picking.config.multiple}
          canCreate={write}
          onDone={(c) => saveLinks(picking, c)}
          onClose={() => setPicking(undefined)}
        />
      )}
    </>
  )
}
