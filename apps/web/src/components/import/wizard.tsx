'use client'
import { Check, Download, FileUp, Loader2, Monitor, Undo2 } from 'lucide-react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, EmptyState, ErrorText, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { KIND_LABEL, saveText, uploadFile } from './lib'
import type { DataJob, DataKind, Preview, RowStatus, Template } from './types'

const STEPS = ['Upload', 'Match columns', 'Review', 'Import', 'Done']
const TALLY_STEPS = ['Export', 'What we found', 'Review', 'Import', 'Done']
const TALLY_STEPS_HOW = [
  'Open your company in TallyPrime.',
  'Press Alt+E (Export), then choose Masters.',
  'Choose All Masters.',
  'Set the format to XML (Data Interchange) and press Enter to save the file.',
  'Upload that file below. Vouchers and transactions are not imported.',
]
const FOUND: [string, string, string][] = [
  ['customers', 'customers', 'Sundry Debtors become customers'],
  ['vendors', 'vendors', 'Sundry Creditors become vendors'],
  ['ledgers', 'ledgers with balances', 'Balances go to your opening balances draft, never posted directly'],
  ['items', 'stock items', 'With units, godowns and opening stock'],
]
const RULES: Record<string, string[]> = {
  contacts: [
    'CSV file, up to 5,000 rows and 10 MB.',
    'First row has the column names. Dates as DD-MM-YYYY.',
    'GSTIN is checked for format only. State is read from the GSTIN when blank.',
    'Contacts already in C Note (same GSTIN, or same name and phone or email) are skipped and listed.',
  ],
  items: [
    'CSV file, up to 5,000 rows and 10 MB.',
    'Every item needs a SKU and a GST rate (0, 5, 12, 18, 28 or 40). We never guess either.',
    'HSN or SAC is 4, 6 or 8 digits. Opening stock quantity has up to 3 decimals.',
    'Items with a SKU already in C Note are skipped and listed.',
  ],
  opening_docs: [
    'CSV file, up to 5,000 rows and 10 MB.',
    'One row per unpaid invoice or bill: party, number, date, due date and amount outstanding.',
    'Parties must already be in C Note. Amounts such as 1,18,000.00 are fine.',
  ],
}
const TABS: [RowStatus, string][] = [
  ['ready', 'Ready'],
  ['fix', 'Needs fixing'],
  ['existing', 'Already in C Note'],
]
const BACK = { contacts: 'contacts', items: 'items', opening_docs: 'opening' } as const

function Steps({ at, names }: { at: number; names: string[] }) {
  return (
    <ol aria-label="Steps" className="mb-6 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px]">
      {names.map((s, i) => (
        <li
          key={s}
          aria-current={i === at ? 'step' : undefined}
          className={`flex items-center gap-1.5 ${i === at ? 'font-semibold' : 'text-muted'}`}
        >
          <span
            className={`flex h-5 w-5 items-center justify-center rounded-full border text-[11px] ${i === at ? 'border-accent bg-accent text-accent-fg' : 'border-line'}`}
          >
            {i < at ? <Check size={11} /> : i + 1}
          </span>
          {s}
        </li>
      ))}
    </ol>
  )
}

export function ImportWizard({ kind }: { kind: DataKind }) {
  const { ws } = useShell()
  const router = useRouter()
  const jobId = useSearchParams().get('job') ?? undefined
  const label = KIND_LABEL[kind]
  const [step, setStep] = useState(0)
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [job, setJob] = useState<DataJob>()
  const [preview, setPreview] = useState<Preview>()
  const [mapping, setMapping] = useState<Record<string, string>>({})
  const [unmatchedOnly, setUnmatchedOnly] = useState(true)
  const [tab, setTab] = useState<RowStatus>('ready')
  const [over, setOver] = useState(false)
  const [undoOpen, setUndoOpen] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const tally = kind === 'tally'
  const [dflt, setDflt] = useState('')
  const [include, setInclude] = useState({ customers: true, vendors: true, ledgers: true, items: true })
  const tpl = useCmd<Template>(ws, 'data_import.template', { kind }, !tally)

  const loadPreview = useCallback(
    async (id: string, m?: Record<string, string>, options?: Record<string, string>) => {
      const p = await cmd<Preview>(ws, 'data_import.preview', {
        id,
        ...(m && { mapping: m }),
        ...(options && { options }),
      })
      setPreview(p)
      setMapping(Object.fromEntries(p.columns.map((c) => [c.header, c.field ?? ''])))
      return p
    },
    [ws],
  )

  // Resume from ?job=: pick the step from the job's status.
  useEffect(() => {
    if (!jobId) return
    cmd<DataJob>(ws, 'data_import.status', { id: jobId })
      .then(async (j) => {
        setJob(j)
        if (j.status === 'preview') {
          await loadPreview(j.id)
          setStep(1)
        } else setStep(j.status === 'queued' || j.status === 'running' ? 3 : 4)
      })
      .catch((x) => setErr((x as Error).message))
  }, [ws, jobId, loadPreview])

  // Progress: poll while queued/running; the job keeps going if the page is closed.
  useEffect(() => {
    if (step !== 3 || !job) return
    const t = setInterval(async () => {
      try {
        const j = await cmd<DataJob>(ws, 'data_import.status', { id: job.id })
        setJob(j)
        if (j.status !== 'queued' && j.status !== 'running') setStep(4)
      } catch (x) {
        setErr((x as Error).message)
      }
    }, 1200)
    return () => clearInterval(t)
  }, [step, job, ws])

  async function pick(file?: File) {
    if (!file) return
    setBusy(true)
    setErr(undefined)
    try {
      const { id } = await uploadFile(ws, kind, file)
      router.replace(`?job=${id}`)
      setJob({ id, kind, status: 'preview', created_at: '' })
      await loadPreview(id)
      setStep(1)
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function step2() {
    if (!job) return
    setBusy(true)
    setErr(undefined)
    try {
      await loadPreview(
        job.id,
        mapping,
        dflt ? { [kind === 'contacts' ? 'default_kind' : 'default_type']: dflt } : undefined,
      )
      setStep(2)
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function run() {
    if (!job) return
    setBusy(true)
    setErr(undefined)
    try {
      await cmd(ws, 'data_import.run', tally ? { id: job.id, include } : { id: job.id })
      setJob({ ...job, status: 'queued' })
      setStep(3)
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function undo() {
    if (!job) return
    try {
      await cmd(ws, 'data_import.undo', { id: job.id })
      setUndoOpen(false)
      setJob(await cmd<DataJob>(ws, 'data_import.status', { id: job.id }))
    } catch (x) {
      setUndoOpen(false)
      setErr((x as Error).message)
    }
  }

  async function downloadFix() {
    if (!job) return
    try {
      const r = await cmd<{ csv: string; filename: string }>(ws, 'data_import.errors', { id: job.id })
      saveText(r.csv, r.filename)
    } catch (x) {
      setErr((x as Error).message)
    }
  }

  const cols = preview?.columns ?? []
  const matched = cols.filter((c) => mapping[c.header]).length
  const fields = tpl.data?.fields ?? []
  const missingRequired = fields.filter((f) => f.required && !Object.values(mapping).includes(f.key))
  const counts = preview?.counts ?? { ready: 0, fix: 0, existing: 0 }
  const rows = (preview?.rows ?? []).filter((r) => r.status === tab)
  const showCols = tally
    ? Object.keys(preview?.rows[0]?.values ?? {})
        .slice(0, 4)
        .map((key) => ({ key, label: key.replace(/_/g, ' ') }))
    : fields
        .filter((f) => f.required)
        .concat(fields.filter((f) => !f.required))
        .slice(0, 4)

  return (
    <div className="mx-auto max-w-4xl px-6 py-8">
      <Link href={`/w/${ws}/import`} className="text-[13px] text-muted hover:underline">
        Import data
      </Link>
      <h1 className="mt-1 mb-4 text-xl font-semibold">
        {tally ? 'Import from Tally' : `Import ${label.toLowerCase()}`}
      </h1>
      <div className="md:hidden">
        <EmptyState icon={Monitor} title="Open this on a computer">
          Matching columns needs a bigger screen. Your earlier imports and their Undo are on the Import data
          page.
        </EmptyState>
      </div>
      <div className="hidden md:block">
        <Steps at={step} names={tally ? TALLY_STEPS : STEPS} />
        <ErrorText error={err} />

        {step === 0 && (
          <div className="space-y-5">
            {/* biome-ignore lint/a11y/noStaticElementInteractions: drop target; the button inside is the keyboard path */}
            <div
              onDragOver={(e) => {
                e.preventDefault()
                setOver(true)
              }}
              onDragLeave={() => setOver(false)}
              onDrop={(e) => {
                e.preventDefault()
                setOver(false)
                pick(e.dataTransfer.files[0])
              }}
              className={`flex flex-col items-center gap-2 rounded-lg border border-dashed px-6 py-12 text-center ${over ? 'border-accent bg-hover' : 'border-line'}`}
            >
              <FileUp size={22} className="text-muted" aria-hidden />
              <p className="text-[14px] font-medium">Drop your {tally ? 'Tally XML' : 'CSV'} here</p>
              <input
                ref={input}
                type="file"
                accept={tally ? '.xml,text/xml' : '.csv,text/csv'}
                aria-label={tally ? 'Tally XML file' : 'CSV file'}
                className="sr-only"
                onChange={(e) => pick(e.target.files?.[0])}
              />
              <button
                type="button"
                className={btnPrimary}
                disabled={busy}
                onClick={() => input.current?.click()}
              >
                {busy ? <Loader2 size={14} className="animate-spin" /> : null} Choose file
              </button>
              {!tally && (
                <button
                  type="button"
                  className="text-[13px] text-muted underline"
                  disabled={!tpl.data}
                  onClick={() => tpl.data && saveText(tpl.data.sample_csv, `${kind}-sample.csv`)}
                >
                  Download a sample file
                </button>
              )}
            </div>
            <section className="rounded-lg border border-line p-4">
              <h2 className="mb-2 text-[13px] font-semibold">
                {tally ? 'Export from TallyPrime' : 'Before you upload'}
              </h2>
              {tally ? (
                <ol className="list-decimal space-y-1 pl-5 text-[13px] text-muted">
                  {TALLY_STEPS_HOW.map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ol>
              ) : (
                <ul className="list-disc space-y-1 pl-5 text-[13px] text-muted">
                  {RULES[kind].map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ul>
              )}
            </section>
          </div>
        )}

        {step === 1 && preview?.tally && (
          <div className="space-y-4">
            <h2 className="text-[15px] font-semibold">What we found</h2>
            <ul className="divide-y divide-line rounded-lg border border-line">
              {FOUND.map(([k, noun, hint]) => (
                <li key={k}>
                  <label className="flex items-start gap-3 px-4 py-3 text-[13px]">
                    <input
                      type="checkbox"
                      className="mt-0.5"
                      checked={include[k as keyof typeof include]}
                      onChange={(e) => setInclude({ ...include, [k]: e.target.checked })}
                    />
                    <span>
                      <span className="font-medium tabular-nums">
                        {preview.tally?.found[k as 'customers'] ?? 0} {noun}
                      </span>
                      <span className="block text-muted">{hint}</span>
                    </span>
                  </label>
                </li>
              ))}
            </ul>
            <p className="text-[12px] text-muted">
              Also found: {preview.tally.found.units} units, {preview.tally.found.godowns} godowns.
            </p>
            {preview.tally.warnings.length > 0 && (
              <ul className="space-y-1 rounded-lg border border-line bg-warn-bg p-3 text-[13px] text-warn">
                {preview.tally.warnings.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            )}
            <button type="button" className={btnPrimary} onClick={() => setStep(2)}>
              Continue to review
            </button>
          </div>
        )}

        {step === 1 && preview && !preview.tally && (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-3 text-[13px]">
              <span>
                <strong>{preview.counts.ready + preview.counts.fix + preview.counts.existing}</strong> rows
                found
              </span>
              <Pill tone="ok">{matched} matched</Pill>
              <Pill tone={cols.length - matched ? 'warn' : 'neutral'}>{cols.length - matched} unmatched</Pill>
              <label className="ml-auto flex items-center gap-1.5">
                <input
                  type="checkbox"
                  checked={unmatchedOnly}
                  onChange={(e) => setUnmatchedOnly(e.target.checked)}
                />
                Show only unmatched
              </label>
            </div>
            <div className="overflow-hidden rounded-lg border border-line">
              <table className="w-full text-left text-[13px]">
                <thead className="border-b border-line bg-side text-[12px] text-muted">
                  <tr>
                    <th className="px-3 py-2 font-medium">Your column</th>
                    <th className="px-3 py-2 font-medium">From your file</th>
                    <th className="px-3 py-2 font-medium">Goes to</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {cols
                    .filter((c) => !unmatchedOnly || !mapping[c.header])
                    .map((c) => (
                      <tr key={c.header}>
                        <td className="px-3 py-2 font-medium">{c.header}</td>
                        <td className="max-w-64 truncate px-3 py-2 text-muted">{c.samples.join(', ')}</td>
                        <td className="px-3 py-2">
                          <select
                            aria-label={`Field for ${c.header}`}
                            value={mapping[c.header] ?? ''}
                            onChange={(e) => setMapping({ ...mapping, [c.header]: e.target.value })}
                            className="w-full rounded-md border border-line bg-canvas px-2 py-1 text-[13px]"
                          >
                            <option value="">Do not import</option>
                            {fields.map((f) => (
                              <option key={f.key} value={f.key}>
                                {f.label}
                                {f.required ? ' (required)' : ''}
                              </option>
                            ))}
                          </select>
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
              {unmatchedOnly && matched === cols.length && (
                <p className="px-3 py-4 text-center text-[13px] text-muted">
                  Every column is matched. Untick the box to check them.
                </p>
              )}
            </div>
            {(kind === 'contacts' || kind === 'opening_docs') && (
              <label className="flex items-center gap-2 text-[13px]">
                <span className="text-muted">Rows with no type are</span>
                <select
                  value={dflt}
                  onChange={(e) => setDflt(e.target.value)}
                  className="rounded-md border border-line bg-canvas px-2 py-1 text-[13px]"
                >
                  <option value="">Needs fixing</option>
                  {(kind === 'contacts'
                    ? [
                        ['customer', 'Customers'],
                        ['vendor', 'Vendors'],
                        ['both', 'Both'],
                      ]
                    : [
                        ['invoice', 'Invoices'],
                        ['bill', 'Bills'],
                      ]
                  ).map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {missingRequired.length > 0 && (
              <p role="alert" className="text-[13px] text-warn">
                Still needed: {missingRequired.map((f) => f.label).join(', ')}.
              </p>
            )}
            <button
              type="button"
              className={btnPrimary}
              disabled={busy || missingRequired.length > 0}
              onClick={step2}
            >
              Continue to review
            </button>
          </div>
        )}

        {step === 2 && preview && (
          <div className="space-y-4">
            <div role="tablist" className="flex gap-1 border-b border-line">
              {TABS.map(([k, l]) => (
                <button
                  key={k}
                  type="button"
                  role="tab"
                  aria-selected={tab === k}
                  onClick={() => setTab(k)}
                  className={`-mb-px border-b-2 px-3 py-2 text-[13px] ${tab === k ? 'border-accent font-medium' : 'border-transparent text-muted'}`}
                >
                  {l} <span className="tabular-nums text-muted">{counts[k]}</span>
                </button>
              ))}
            </div>
            {rows.length === 0 ? (
              <p className="py-6 text-center text-[13px] text-muted">Nothing here.</p>
            ) : (
              <div className="overflow-x-auto rounded-lg border border-line">
                <table className="w-full text-left text-[13px]">
                  <thead className="border-b border-line bg-side text-[12px] text-muted">
                    <tr>
                      <th className="px-3 py-2 font-medium">Row</th>
                      {showCols.map((f) => (
                        <th key={f.key} className="px-3 py-2 font-medium">
                          {f.label}
                        </th>
                      ))}
                      {tab !== 'ready' && <th className="px-3 py-2 font-medium">Why</th>}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {rows.map((r) => (
                      <tr key={r.row_no}>
                        <td className="px-3 py-2 tabular-nums text-muted">{r.row_no}</td>
                        {showCols.map((f) => (
                          <td key={f.key} className="px-3 py-2">
                            {r.values?.[f.key]}
                          </td>
                        ))}
                        {tab !== 'ready' && (
                          <td className={`px-3 py-2 ${tab === 'fix' ? 'text-bad' : 'text-muted'}`}>
                            {r.reason}
                          </td>
                        )}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {preview.rows.length < counts.ready + counts.fix + counts.existing && (
              <p className="text-[12px] text-muted">Showing the first {preview.rows.length} rows.</p>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                className={btnPrimary}
                disabled={busy || counts.ready === 0}
                onClick={run}
              >
                Import {counts.ready} {counts.ready === 1 ? 'row' : 'rows'}
              </button>
              <button type="button" className={btn} onClick={() => setStep(1)}>
                Back
              </button>
              {counts.fix > 0 && (
                <button type="button" className={`${btn} ml-auto`} onClick={downloadFix}>
                  <Download size={14} /> Download rows that need fixing
                </button>
              )}
            </div>
            {counts.fix > 0 && (
              <p className="text-[12px] text-muted">
                Rows that need fixing are left out. Fix them in the downloaded file and import it again.
              </p>
            )}
          </div>
        )}

        {step === 3 && (
          <div className="rounded-lg border border-line p-6 text-center" role="status">
            <Loader2 size={22} className="mx-auto animate-spin text-muted" aria-hidden />
            <p className="mt-3 text-[14px] font-medium">Importing {label.toLowerCase()}...</p>
            <p className="mt-1 text-[13px] text-muted tabular-nums">
              {job?.processed ?? 0} of {job?.total ?? counts.ready} rows done
            </p>
            <progress
              className="mx-auto mt-3 block w-64"
              max={job?.total || counts.ready || 1}
              value={job?.processed ?? 0}
            />
            <p className="mt-3 text-[13px] text-muted">
              You can leave this page. The import keeps running and shows up under Recent imports.
            </p>
          </div>
        )}

        {step === 4 && job && <Done ws={ws} kind={kind} job={job} onUndo={() => setUndoOpen(true)} />}
      </div>

      <Modal open={undoOpen} onClose={() => setUndoOpen(false)} title="Undo this import?">
        <div className="space-y-3 text-[13px]">
          <p>
            This removes the {job?.added ?? 0} {label.toLowerCase()} this import added.
            {kind === 'contacts' &&
              ' A contact already used on an invoice or bill cannot be removed, so nothing is undone in that case.'}
            {kind === 'items' && ' Opening stock from this import is reversed.'} Rows that were skipped are
            not touched, and anything you added yourself stays. Undo works for 7 days after the import.
          </p>
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setUndoOpen(false)}>
              Keep it
            </button>
            <button type="button" className={btnPrimary} onClick={undo}>
              Undo import
            </button>
          </div>
        </div>
      </Modal>
    </div>
  )
}

export function Done({
  ws,
  kind,
  job,
  onUndo,
}: {
  ws: string
  kind: DataKind
  job: DataJob
  onUndo: () => void
}) {
  const undone = job.status === 'undone'
  const where =
    kind === 'items'
      ? `/w/${ws}/billing/stock`
      : kind === 'contacts'
        ? `/w/${ws}/crm`
        : `/w/${ws}/import/opening`
  return (
    <div className="space-y-4">
      {job.status === 'failed' ? (
        <ErrorText error={job.error ?? 'The import failed.'} />
      ) : (
        <p className="flex items-center gap-2 text-[14px] font-medium">
          {undone ? 'This import was undone.' : 'Import finished.'}
        </p>
      )}
      <div className="grid max-w-md grid-cols-2 gap-3">
        {[
          ['Added', job.added ?? 0],
          ['Skipped', (job.skipped ?? 0) + (job.failed ?? 0)],
        ].map(([l, n]) => (
          <div key={l} className="rounded-lg border border-line p-4">
            <div className="text-[12px] text-muted">{l}</div>
            <div className="mt-1 text-xl font-semibold tabular-nums">{n}</div>
          </div>
        ))}
      </div>
      <div className="flex flex-wrap gap-2">
        <Link href={where} className={btnPrimary}>
          {kind === 'items'
            ? 'View items'
            : kind === 'contacts'
              ? 'View contacts'
              : kind === 'tally'
                ? 'Review your opening balances'
                : 'Go to opening balances'}
        </Link>
        {job.status === 'done' && (
          <button type="button" className={btn} onClick={onUndo}>
            <Undo2 size={14} /> Undo
          </button>
        )}
        <Link href={`/w/${ws}/import`} className={btn}>
          Back to Import data
        </Link>
      </div>
      {job.status === 'done' && <p className="text-[12px] text-muted">You can undo this for 7 days.</p>}
    </div>
  )
}
