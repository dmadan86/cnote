'use client'
import { useEffect, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { run } from './bits'
import { money } from './fmt'

type Account = {
  id: string
  name: string
  account_hint: string | null
  active: boolean
  unmatched_lines: number
  lines: number
}
type Suggestion = {
  kind: string
  id: string
  label: string
  date: string
  amount_minor: number
  reference_match: boolean
}
type Line = {
  id: string
  date: string
  amount_minor: number
  narration: string
  reference: string | null
  status: 'unmatched' | 'matched' | 'ignored'
  match_label: string | null
  ignore_reason: string | null
  suggestions: Suggestion[]
  combos: Suggestion[][]
}
type Mapping = {
  date?: string
  narration?: string
  amount?: string
  debit?: string
  credit?: string
  type?: string
  balance?: string
  reference?: string
  date_format?: string
}
type Preview = {
  ok: boolean
  reason?: string
  rows: string[][]
  headers?: string[]
  header_row?: number
  detected: Mapping
  used?: Mapping
  source?: string
  lines?: number
  skipped?: number
  errors?: string[]
  sample?: { row: number; date: string; amount_minor: number; narration: string }[]
}
type Summary = {
  lines: number
  matched: number
  unmatched: number
  ignored: number
  credits_minor: number
  debits_minor: number
  unmatched_credits_minor: number
  unmatched_debits_minor: number
  closing_balance_minor: number | null
  closing_balance_on: string | null
}
type ImportResult = { imported: number; duplicates: number; skipped: number; errors: string[] }
type Contact = { id: string; name: string }
const dmy = (iso: string) => iso.split('-').reverse().join('/')

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-line p-2.5">
      <div className="text-[12px] text-muted">{label}</div>
      <div className="text-[15px] font-semibold">{value}</div>
    </div>
  )
}

/** Debit or credit as text and colour, so direction never depends on colour alone. */
const Amount = ({ minor }: { minor: number }) => (
  <span className={`whitespace-nowrap font-medium ${minor < 0 ? 'text-red-600' : 'text-green-700'}`}>
    {minor < 0 ? 'Debit ' : 'Credit '}
    {money(Math.abs(minor))}
  </span>
)

function RecordModal({ line, onClose, onDone }: { line: Line; onClose: () => void; onDone: () => void }) {
  const { ws } = useShell()
  const debit = line.amount_minor < 0
  const cats = useCmd<{ key: string; label: string }[]>(ws, 'expense.categories', {}, debit)
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 }, !debit)
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  return (
    <Modal open title={debit ? 'Record as expense' : 'Record as receipt'} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          setBusy(true)
          try {
            await run(
              ws,
              debit ? 'bank.record_expense' : 'bank.record_receipt',
              debit
                ? {
                    line_id: line.id,
                    category: String(f.get('category')),
                    vendor_name: String(f.get('who') ?? ''),
                  }
                : {
                    line_id: line.id,
                    contact_id: String(f.get('contact')),
                    supply_type: String(f.get('supply')),
                  },
            )
            onDone()
            onClose()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <p className="text-[13px]">
          {dmy(line.date)} · <Amount minor={line.amount_minor} />
          <br />
          <span className="text-muted">{line.narration}</span>
        </p>
        {debit ? (
          <>
            <Field label="Category">
              {(id) => (
                <select id={id} name="category" required className={inputCls}>
                  {cats.data?.map((c) => (
                    <option key={c.key} value={c.key}>
                      {c.label}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Paid to (optional)">
              {(id) => <input id={id} name="who" className={inputCls} />}
            </Field>
          </>
        ) : (
          <>
            <Field label="Customer">
              {(id) => (
                <select id={id} name="contact" required className={inputCls} defaultValue="">
                  <option value="" disabled>
                    Choose a customer
                  </option>
                  {contacts.data?.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Supply">
              {(id) => (
                <select id={id} name="supply" className={inputCls}>
                  <option value="services">Services (18% GST on the advance)</option>
                  <option value="goods">Goods (no GST on an advance)</option>
                </select>
              )}
            </Field>
          </>
        )}
        <p className="text-[12px] text-muted">
          This posts to the books at the statement date and links the line. Unmatching later removes the link
          only; the
          {debit ? ' expense' : ' receipt voucher'} stays.
        </p>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy}>
            {debit ? 'Record expense' : 'Record receipt'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

const REASONS = ['Bank charge, not recorded', 'Internal transfer between own accounts']

function IgnoreModal({ line, onClose, onDone }: { line: Line; onClose: () => void; onDone: () => void }) {
  const { ws } = useShell()
  const [err, setErr] = useState<string>()
  return (
    <Modal open title="Ignore this line" onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          const pick = String(f.get('reason'))
          try {
            await run(ws, 'bank.ignore', {
              line_id: line.id,
              reason: pick === 'other' ? String(f.get('other') ?? '') : pick,
            })
            onDone()
            onClose()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <p className="text-[13px]">
          {dmy(line.date)} · <Amount minor={line.amount_minor} />
          <br />
          <span className="text-muted">{line.narration}</span>
        </p>
        <Field label="Reason">
          {(id) => (
            <select id={id} name="reason" className={inputCls} defaultValue={REASONS[0]}>
              {REASONS.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
              <option value="other">Other (write below)</option>
            </select>
          )}
        </Field>
        <Field label="Other reason">
          {(id) => <input id={id} name="other" maxLength={200} className={inputCls} />}
        </Field>
        <p className="text-[12px] text-muted">
          The line counts as reconciled and nothing is posted to the books. To keep a bank charge in the
          books, use Record as expense instead. You can put an ignored line back.
        </p>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Ignore line
          </button>
        </div>
      </form>
    </Modal>
  )
}

function LineCard({ l, onChange }: { l: Line; onChange: () => void }) {
  const { ws } = useShell()
  const [rec, setRec] = useState(false)
  const [ign, setIgn] = useState(false)
  const [picked, setPicked] = useState<Record<string, boolean>>({})
  const [err, setErr] = useState<string>()
  const act = async (name: string, input: object) => {
    setErr(undefined)
    try {
      await run(ws, name, input)
      onChange()
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  const s = l.suggestions[0]
  const combo = l.combos?.[0]
  const chosen = combo?.filter((p) => picked[p.id] !== false) ?? []
  const total = chosen.reduce((t, p) => t + p.amount_minor, 0)
  const gap = Math.abs(l.amount_minor) - total
  return (
    <li className="space-y-2 rounded-md border border-line p-3">
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1">
        <div className="min-w-0 flex-1">
          <div className="text-[12px] text-muted">{dmy(l.date)}</div>
          <div className="break-words text-[14px]">{l.narration || '(no narration)'}</div>
        </div>
        <Amount minor={l.amount_minor} />
      </div>
      {l.status === 'ignored' ? (
        <div className="flex flex-wrap items-center justify-between gap-2 text-[13px]">
          <span>
            Ignored: <strong>{l.ignore_reason}</strong>
          </span>
          <button
            type="button"
            className={`${btn} min-h-[44px] sm:min-h-0`}
            onClick={() => act('bank.unignore', { line_id: l.id })}
          >
            Put back
          </button>
        </div>
      ) : l.status === 'matched' ? (
        <div className="flex flex-wrap items-center justify-between gap-2 text-[13px]">
          <span>
            Matched to <strong>{l.match_label ?? 'a recorded payment'}</strong>
          </span>
          <button
            type="button"
            className={`${btn} min-h-[44px] sm:min-h-0`}
            onClick={() => act('bank.unmatch', { line_id: l.id })}
          >
            Unmatch
          </button>
        </div>
      ) : (
        <div className="space-y-2">
          {s && (
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-hover p-2 text-[13px]">
              <span>
                <span className="text-muted">Suggested match: </span>
                {s.label} · {dmy(s.date)}{' '}
                {s.reference_match && <Pill tone="ok">Transaction reference matches</Pill>}
                {l.suggestions.length > 1 && (
                  <span className="text-muted"> (+{l.suggestions.length - 1} more)</span>
                )}
              </span>
              <button
                type="button"
                className={`${btnPrimary} min-h-[44px] sm:min-h-0`}
                onClick={() => act('bank.match', { line_id: l.id, match_kind: s.kind, match_id: s.id })}
              >
                Match
              </button>
            </div>
          )}
          {combo && (
            <fieldset className="space-y-1 rounded-md bg-hover p-2 text-[13px]">
              <legend className="px-1 text-muted">
                Possible match: {combo.length} payments together equal this line
              </legend>
              {combo.map((p) => (
                <label key={p.id} className="flex min-h-[44px] items-center gap-2 sm:min-h-0">
                  <input
                    type="checkbox"
                    checked={picked[p.id] !== false}
                    onChange={(e) => setPicked({ ...picked, [p.id]: e.target.checked })}
                  />
                  <span className="min-w-0 flex-1 break-words">
                    {p.label} · {dmy(p.date)}
                  </span>
                  <span className="whitespace-nowrap">{money(p.amount_minor)}</span>
                </label>
              ))}
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span role="status">
                  Selected {money(total)}
                  {gap !== 0 && `, ${money(Math.abs(gap))} ${gap > 0 ? 'short' : 'over'}`}
                </span>
                <button
                  type="button"
                  className={`${btnPrimary} min-h-[44px] sm:min-h-0`}
                  disabled={gap !== 0 || chosen.length < 2}
                  onClick={() =>
                    act('bank.match', {
                      line_id: l.id,
                      matches: chosen.map((p) => ({ kind: p.kind, id: p.id })),
                    })
                  }
                >
                  Match {chosen.length} selected payments
                </button>
              </div>
            </fieldset>
          )}
          <div className="flex flex-wrap gap-2">
            <button type="button" className={`${btn} min-h-[44px] sm:min-h-0`} onClick={() => setRec(true)}>
              {l.amount_minor < 0 ? 'Record as expense' : 'Record as receipt'}
            </button>
            <button type="button" className={`${btn} min-h-[44px] sm:min-h-0`} onClick={() => setIgn(true)}>
              Ignore
            </button>
          </div>
        </div>
      )}
      <ErrorText error={err} />
      {ign && <IgnoreModal line={l} onClose={() => setIgn(false)} onDone={onChange} />}
      {rec && <RecordModal line={l} onClose={() => setRec(false)} onDone={onChange} />}
    </li>
  )
}

type Mode = 'signed' | 'split' | 'marker'
const FIELDS: [keyof Mapping, string][] = [
  ['narration', 'Narration'],
  ['balance', 'Balance'],
  ['reference', 'Reference'],
]
const modeOf = (m: Mapping): Mode => (m.debit || m.credit ? 'split' : m.type ? 'marker' : 'signed')

function MappingEditor({
  account,
  csv,
  onClose,
  onDone,
}: {
  account: Account
  csv: string
  onClose: () => void
  onDone: (r: ImportResult) => void
}) {
  const { ws } = useShell()
  const [first, setFirst] = useState<Preview>()
  const [map, setMap] = useState<Mapping>({})
  const [mode, setMode] = useState<Mode>('signed')
  const [prev, setPrev] = useState<Preview>()
  const [err, setErr] = useState<string>()
  useEffect(() => {
    run<Preview>(ws, 'bank.csv_preview', { csv, account_id: account.id })
      .then((p) => {
        setFirst(p)
        const m = { ...p.detected, ...(p.used ?? {}) }
        setMap(m)
        setMode(modeOf(m))
      })
      .catch((x) => setErr((x as Error).message))
  }, [ws, csv, account.id])
  useEffect(() => {
    if (!first) return
    const t = setTimeout(
      () =>
        run<Preview>(ws, 'bank.csv_preview', { csv, mapping: map })
          .then(setPrev)
          .catch((x) => setErr((x as Error).message)),
      250,
    )
    return () => clearTimeout(t)
  }, [ws, csv, map, first])
  const headers = first?.headers ?? []
  const pick = (k: keyof Mapping, label: string, required = false) => (
    <Field label={required ? `${label} (required)` : label}>
      {(id) => (
        <select
          id={id}
          className={inputCls}
          value={map[k] ?? ''}
          onChange={(e) => setMap({ ...map, [k]: e.target.value })}
        >
          <option value="">{required ? 'Choose a column' : 'Not provided'}</option>
          {headers.map((h, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: header cells can repeat
            <option key={i} value={h}>
              {h || `Column ${i + 1}`}
            </option>
          ))}
        </select>
      )}
    </Field>
  )
  const setModeKeep = (m: Mode) => {
    setMode(m)
    setMap({ ...map, amount: '', debit: '', credit: '', type: '' })
  }
  return (
    <Modal open title="Map statement columns" onClose={onClose}>
      <div className="space-y-3">
        {first && !first.ok && (
          <div role="alert" className="rounded-md border border-amber-400 bg-amber-50 p-2.5 text-[13px]">
            Automatic detection failed: {first.reason}
          </div>
        )}
        {first && first.ok && (
          <p className="text-[13px] text-muted">
            Columns were read{' '}
            {first.source === 'given' ? "with this account's saved mapping" : 'automatically'}. Change them if
            the preview below looks wrong.
          </p>
        )}
        <div className="overflow-x-auto rounded-md border border-line">
          <table className="w-full text-[12px]">
            <caption className="sr-only">First rows of the file</caption>
            <tbody>
              {first?.rows.map((r, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: raw rows
                <tr key={i} className={first.header_row === i + 1 ? 'bg-hover font-semibold' : ''}>
                  <th scope="row" className="px-2 py-1 text-left font-normal text-muted">
                    {i + 1}
                  </th>
                  {r.map((c, j) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: raw cells
                    <td key={j} className="whitespace-nowrap px-2 py-1">
                      {c}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {first?.header_row && (
          <p className="text-[12px] text-muted">Header row: {first.header_row} (highlighted).</p>
        )}
        <div className="grid gap-2 sm:grid-cols-2">
          {pick('date', 'Date', true)}
          <Field label="Date order">
            {(id) => (
              <select
                id={id}
                className={inputCls}
                value={map.date_format || 'dmy'}
                onChange={(e) => setMap({ ...map, date_format: e.target.value })}
              >
                <option value="dmy">Day first: 03/04/2026 is 3 Apr 2026</option>
                <option value="mdy">Month first: 03/04/2026 is 4 Mar 2026</option>
                <option value="ymd">Year first: 2026/04/03 is 3 Apr 2026</option>
              </select>
            )}
          </Field>
          <Field label="Amounts come as">
            {(id) => (
              <select
                id={id}
                className={inputCls}
                value={mode}
                onChange={(e) => setModeKeep(e.target.value as Mode)}
              >
                <option value="signed">One signed amount column</option>
                <option value="split">Separate debit and credit columns</option>
                <option value="marker">One amount column plus a Dr/Cr marker</option>
              </select>
            )}
          </Field>
          {mode === 'split' ? (
            <>
              {pick('debit', 'Debit (money out)', true)}
              {pick('credit', 'Credit (money in)', true)}
            </>
          ) : (
            <>
              {pick('amount', 'Amount', true)}
              {mode === 'marker' && pick('type', 'Dr/Cr marker', true)}
            </>
          )}
          {FIELDS.map(([k, label]) => pick(k, label))}
        </div>
        <div aria-live="polite" className="space-y-1 text-[13px]">
          {prev && !prev.ok && <p className="text-red-600">{prev.reason}</p>}
          {prev?.ok && (
            <>
              <p>
                {prev.lines} lines would be read, {prev.skipped} rows skipped.
              </p>
              <ul className="space-y-1">
                {prev.sample?.map((x) => (
                  <li
                    key={x.row}
                    className="flex flex-wrap justify-between gap-2 rounded-md border border-line p-2"
                  >
                    <span className="min-w-0 break-words">
                      {dmy(x.date)} · {x.narration || '(no narration)'}
                    </span>
                    <Amount minor={x.amount_minor} />
                  </li>
                ))}
              </ul>
              {prev.errors?.map((e) => (
                <p key={e} className="text-red-600">
                  {e}
                </p>
              ))}
            </>
          )}
        </div>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className={btnPrimary}
            disabled={!prev?.ok}
            onClick={async () => {
              setErr(undefined)
              try {
                onDone(
                  await run<ImportResult>(ws, 'bank.import', { account_id: account.id, csv, mapping: map }),
                )
                onClose()
              } catch (x) {
                setErr((x as Error).message)
              }
            }}
          >
            Save mapping and import
          </button>
        </div>
        <p className="text-[12px] text-muted">
          The mapping is saved for {account.name} and used for its next imports.
        </p>
      </div>
    </Modal>
  )
}

function ImportBox({ account, onDone }: { account: Account; onDone: () => void }) {
  const { ws } = useShell()
  const [res, setRes] = useState<ImportResult>()
  const [err, setErr] = useState<string>()
  const [csv, setCsv] = useState<string>()
  const [edit, setEdit] = useState(false)
  return (
    <div className="space-y-2">
      <Field label="Import a statement (CSV)">
        {(id) => (
          <input
            id={id}
            type="file"
            accept=".csv,text/csv"
            className={inputCls}
            onChange={async (e) => {
              const file = e.target.files?.[0]
              e.target.value = ''
              if (!file) return
              setErr(undefined)
              const text = await file.text()
              setCsv(text)
              try {
                const p = await run<Preview>(ws, 'bank.csv_preview', { csv: text, account_id: account.id })
                if (!p.ok) {
                  setRes(undefined)
                  setEdit(true)
                  return
                }
                setRes(await run<ImportResult>(ws, 'bank.import', { account_id: account.id, csv: text }))
                onDone()
              } catch (x) {
                setRes(undefined)
                setErr((x as Error).message)
              }
            }}
          />
        )}
      </Field>
      <p className="text-[12px] text-muted">
        Columns are detected from the header and remembered for this account. Dates are read day first unless
        you map them otherwise. Importing the same lines again is safe.
      </p>
      <ErrorText error={err} />
      {res && (
        <div role="status" className="rounded-md border border-line p-2.5 text-[13px]">
          {res.imported} lines imported, {res.duplicates} already there, {res.skipped} rows skipped.
          {res.errors.length > 0 && (
            <ul className="mt-1 list-disc pl-5 text-red-600">
              {res.errors.map((x) => (
                <li key={x}>{x}</li>
              ))}
            </ul>
          )}
        </div>
      )}
      {csv && (
        <button type="button" className={`${btn} min-h-[44px] sm:min-h-0`} onClick={() => setEdit(true)}>
          Map columns
        </button>
      )}
      {edit && csv && (
        <MappingEditor
          account={account}
          csv={csv}
          onClose={() => setEdit(false)}
          onDone={(r) => {
            setRes(r)
            onDone()
          }}
        />
      )}
    </div>
  )
}

function Reconcile({ account, reload }: { account: Account; reload: () => void }) {
  const { ws } = useShell()
  const [tab, setTab] = useState<'unmatched' | 'matched' | 'ignored'>('unmatched')
  const lines = useCmd<Line[]>(ws, 'bank.line_list', { account_id: account.id, status: tab })
  const sum = useCmd<Summary>(ws, 'bank.summary', { account_id: account.id })
  const changed = () => {
    lines.reload()
    sum.reload()
    reload()
  }
  const s = sum.data
  return (
    <div className="space-y-4">
      <ImportBox account={account} onDone={changed} />
      {s && (
        <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
          <Stat label="Money in (all lines)" value={money(s.credits_minor)} />
          <Stat label="Money out (all lines)" value={money(s.debits_minor)} />
          <Stat label="Unmatched" value={`${s.unmatched} of ${s.lines}`} />
          <Stat
            label={
              s.closing_balance_on ? `Statement balance, ${dmy(s.closing_balance_on)}` : 'Statement balance'
            }
            value={s.closing_balance_minor == null ? 'Not in the file' : money(s.closing_balance_minor)}
          />
        </div>
      )}
      <div role="tablist" aria-label="Statement lines" className="flex gap-1">
        {(['unmatched', 'matched', 'ignored'] as const).map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            className={`${tab === t ? btnPrimary : btn} min-h-[44px] sm:min-h-0`}
            onClick={() => setTab(t)}
          >
            {t === 'unmatched'
              ? 'Unmatched'
              : t === 'matched'
                ? 'Matched'
                : `Ignored${s ? ` (${s.ignored})` : ''}`}
          </button>
        ))}
      </div>
      <ErrorText error={lines.error} />
      {lines.data?.length === 0 && (
        <p className="text-[14px] text-muted">
          {account.lines === 0
            ? 'Upload a statement to start.'
            : tab === 'unmatched'
              ? 'All statement lines are matched.'
              : tab === 'ignored'
                ? 'No ignored lines.'
                : 'Nothing matched yet.'}
        </p>
      )}
      <ul className="space-y-2" aria-label={`${tab} statement lines`}>
        {lines.data?.map((l) => (
          <LineCard key={l.id} l={l} onChange={changed} />
        ))}
      </ul>
    </div>
  )
}

export function BankReconcile() {
  const { ws } = useShell()
  const accounts = useCmd<Account[]>(ws, 'bank.account_list', {})
  const [pick, setPick] = useState<string>()
  const [err, setErr] = useState<string>()
  const list = accounts.data ?? []
  const current = list.find((a) => a.id === pick) ?? list.find((a) => a.active)
  return (
    <div className="space-y-4">
      <ErrorText error={accounts.error} />
      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={async (e) => {
          e.preventDefault()
          const form = e.currentTarget
          const f = new FormData(form)
          setErr(undefined)
          try {
            const a = await run<Account>(ws, 'bank.account_create', {
              name: String(f.get('name')),
              account_hint: String(f.get('hint') ?? ''),
            })
            form.reset()
            setPick(a.id)
            accounts.reload()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        {list.length > 0 && (
          <Field label="Bank account">
            {(id) => (
              <select
                id={id}
                value={current?.id ?? ''}
                onChange={(e) => setPick(e.target.value)}
                className={inputCls}
              >
                {list.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                    {a.account_hint ? ` ····${a.account_hint}` : ''} ({a.unmatched_lines} unmatched)
                  </option>
                ))}
              </select>
            )}
          </Field>
        )}
        <Field label={list.length ? 'New account name' : 'Create your first bank account'}>
          {(id) => (
            <input
              id={id}
              name="name"
              required
              maxLength={120}
              className={inputCls}
              placeholder="HDFC Current"
            />
          )}
        </Field>
        <Field label="Last 4 digits (optional)">
          {(id) => <input id={id} name="hint" maxLength={4} inputMode="numeric" className={inputCls} />}
        </Field>
        <button type="submit" className={`${btn} min-h-[44px] sm:min-h-0`}>
          Add account
        </button>
      </form>
      <ErrorText error={err} />
      {current && <Reconcile key={current.id} account={current} reload={accounts.reload} />}
    </div>
  )
}
