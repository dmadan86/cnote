'use client'
import { Check, FileUp, Loader2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls, Modal } from '@/components/ui'
import {
  type Field,
  type FieldType,
  type ImportJob,
  type ImportMapping,
  type ImportPreview,
  importStatus,
  previewImport,
  runImport,
  type Table,
} from '@/lib/tables'
import { isComputed, TYPES } from './field-dialog'

const STEPS = ['Upload', 'Match columns', 'Review', 'Import', 'Done']
const NEW_TYPES: FieldType[] = [
  'text',
  'long_text',
  'number',
  'currency',
  'percent',
  'date',
  'checkbox',
  'single_select',
  'phone',
  'email',
  'url',
]
type Pick = { kind: 'skip' } | { kind: 'field'; id: string } | { kind: 'new'; name: string; type: FieldType }

/** Upload a CSV, match its columns to fields, check the preview, run (design 17.6).  */
export function ImportWizard({
  ws,
  baseId,
  table,
  fields,
  onClose,
  onDone,
}: {
  ws: string
  baseId: string
  table: Table
  fields: Field[]
  onClose: () => void
  onDone: () => void
}) {
  const [step, setStep] = useState(0)
  const [handle, setHandle] = useState('')
  const [pv, setPv] = useState<ImportPreview>()
  const [picks, setPicks] = useState<Record<number, Pick>>({})
  const [job, setJob] = useState<ImportJob>()
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const alive = useRef(true)
  useEffect(
    () => () => {
      alive.current = false
    },
    [],
  )
  const targets = fields.filter((f) => !isComputed(f.type) && f.type !== 'attachment' && f.type !== 'link')

  const mapping = (): ImportMapping => ({
    columns: Object.entries(picks).map(([i, p]) =>
      p.kind === 'skip'
        ? { index: Number(i), skip: true }
        : p.kind === 'field'
          ? { index: Number(i), field_id: p.id }
          : { index: Number(i), new_field: { name: p.name, type: p.type } },
    ),
  })
  const guard = async (fn: () => Promise<void>) => {
    setErr(undefined)
    setBusy(true)
    try {
      await fn()
    } catch (x) {
      setErr((x as Error).message)
    }
    setBusy(false)
  }

  const upload = (file: File) =>
    guard(async () => {
      const q = new URLSearchParams({ ws, base_id: baseId, table_id: table.id, filename: file.name })
      const res = await fetch(`/api/tables/import?${q}`, {
        method: 'POST',
        body: file,
        credentials: 'same-origin',
      })
      const d = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(d?.error?.message ?? res.statusText)
      const h = String(d.handle ?? d.id)
      const p = await previewImport(ws, h)
      setHandle(h)
      setPv(p)
      setPicks(
        Object.fromEntries(
          p.columns.map((c) => [
            c.index,
            c.field_id
              ? { kind: 'field', id: c.field_id }
              : c.new_field
                ? { kind: 'new', name: c.new_field.name, type: c.new_field.type }
                : { kind: 'skip' },
          ]),
        ),
      )
      setStep(1)
    })

  const review = () =>
    guard(async () => {
      setPv(await previewImport(ws, handle, mapping()))
      setStep(2)
    })

  const run = () =>
    guard(async () => {
      let j = await runImport(ws, handle, mapping())
      setJob(j)
      setStep(3)
      while (alive.current && (j.status === 'queued' || j.status === 'running')) {
        await new Promise((r) => setTimeout(r, 1000))
        j = await importStatus(ws, j.id)
        setJob(j)
      }
      if (!alive.current) return
      if (j.status === 'failed') throw new Error(j.error ?? 'The import failed.')
      setStep(4)
      onDone()
    })

  return (
    <Modal open onClose={onClose} title={`Import a CSV into ${table.name}`} className="max-w-2xl">
      <ol aria-label="Steps" className="mb-4 flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
        {STEPS.map((s, i) => (
          <li
            key={s}
            aria-current={i === step ? 'step' : undefined}
            className={i === step ? 'font-semibold' : i < step ? 'text-muted' : 'text-muted'}
          >
            {i < step && <Check size={12} className="mr-1 inline" aria-hidden="true" />}
            {i + 1}. {s}
          </li>
        ))}
      </ol>

      {step === 0 && (
        <div className="space-y-3">
          <ul className="list-disc pl-5 text-[13px] text-muted">
            <li>CSV file, up to 5,000 rows and 10 MB.</li>
            <li>The first row has the column names.</li>
            <li>Imported records cannot be undone in one step. You can select them and delete them.</li>
          </ul>
          <label className={`${btn} cursor-pointer`}>
            {busy ? <Loader2 size={14} className="animate-spin" /> : <FileUp size={14} />} Choose a CSV file
            <input
              type="file"
              accept=".csv,text/csv"
              disabled={busy}
              className="sr-only"
              onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])}
            />
          </label>
        </div>
      )}

      {step === 1 && pv && (
        <div className="space-y-3">
          <div className="max-h-80 overflow-auto rounded-md border border-line">
            <table className="w-full text-left text-[13px]">
              <thead className="bg-side text-[12px] text-muted">
                <tr>
                  <th className="px-2 py-1.5 font-medium">CSV column</th>
                  <th className="px-2 py-1.5 font-medium">Goes to</th>
                </tr>
              </thead>
              <tbody>
                {pv.columns.map((c) => {
                  const p = picks[c.index] ?? { kind: 'skip' }
                  const val = p.kind === 'field' ? `f:${p.id}` : p.kind === 'new' ? 'new' : 'skip'
                  return (
                    <tr key={c.index} className="border-t border-line align-top">
                      <td className="px-2 py-1.5">
                        <p className="font-medium">{c.header || `Column ${c.index + 1}`}</p>
                        <p className="max-w-[16rem] truncate text-[12px] text-muted">
                          {c.samples.filter(Boolean).slice(0, 3).join(', ')}
                        </p>
                      </td>
                      <td className="space-y-1 px-2 py-1.5">
                        <select
                          aria-label={`Where ${c.header || `column ${c.index + 1}`} goes`}
                          value={val}
                          onChange={(e) => {
                            const v = e.target.value
                            setPicks({
                              ...picks,
                              [c.index]:
                                v === 'skip'
                                  ? { kind: 'skip' }
                                  : v === 'new'
                                    ? { kind: 'new', name: c.header, type: c.new_field?.type ?? 'text' }
                                    : { kind: 'field', id: v.slice(2) },
                            })
                          }}
                          className={`${inputCls} py-1`}
                        >
                          <option value="skip">Do not import</option>
                          {targets.map((f) => (
                            <option key={f.id} value={`f:${f.id}`}>
                              {f.name}
                            </option>
                          ))}
                          <option value="new">New field</option>
                        </select>
                        {p.kind === 'new' && (
                          <div className="flex gap-1.5">
                            <input
                              aria-label={`New field name for ${c.header}`}
                              value={p.name}
                              onChange={(e) =>
                                setPicks({ ...picks, [c.index]: { ...p, name: e.target.value } })
                              }
                              className={`${inputCls} py-1`}
                            />
                            <select
                              aria-label={`New field type for ${c.header}`}
                              value={p.type}
                              onChange={(e) =>
                                setPicks({ ...picks, [c.index]: { ...p, type: e.target.value as FieldType } })
                              }
                              className={`${inputCls} w-auto py-1`}
                            >
                              {NEW_TYPES.map((t) => (
                                <option key={t} value={t}>
                                  {TYPES.find((x) => x.type === t)?.label}
                                </option>
                              ))}
                            </select>
                          </div>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setStep(0)} className={btn}>
              Back
            </button>
            <button
              type="button"
              disabled={busy || Object.values(picks).every((p) => p.kind === 'skip')}
              onClick={review}
              className={btnPrimary}
            >
              Review
            </button>
          </div>
        </div>
      )}

      {step === 2 && pv && (
        <div className="space-y-3">
          <p className="text-[14px]">
            <strong>{pv.ready}</strong> records are ready. <strong>{pv.needs_fixing}</strong> need fixing and
            will be left out.
          </p>
          {pv.errors.length > 0 && (
            <ul
              aria-label="Problems found"
              className="max-h-40 space-y-0.5 overflow-auto rounded-md bg-warn-bg p-2 text-[13px] text-warn"
            >
              {pv.errors.map((e) => (
                <li key={`${e.row}:${e.col}`}>
                  Row {e.row}, column {e.col}: {e.reason}
                </li>
              ))}
            </ul>
          )}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setStep(1)} className={btn}>
              Back
            </button>
            <button type="button" disabled={busy || pv.ready === 0} onClick={run} className={btnPrimary}>
              Import {pv.ready} records
            </button>
          </div>
        </div>
      )}

      {step === 3 && (
        <p role="status" className="flex items-center gap-2 text-[14px]">
          <Loader2 size={14} className="animate-spin" /> Importing records.
        </p>
      )}

      {step === 4 && job && (
        <div className="space-y-3">
          <p role="status" className="text-[14px]">
            Imported {job.counts.created.toLocaleString('en-IN')} records.
            {job.counts.skipped + job.counts.failed > 0 &&
              ` ${job.counts.skipped + job.counts.failed} were left out.`}
          </p>
          <div className="flex justify-end">
            <button type="button" onClick={onClose} className={btnPrimary}>
              Done
            </button>
          </div>
        </div>
      )}
      <div className="mt-2">
        <ErrorText error={err} />
      </div>
    </Modal>
  )
}
