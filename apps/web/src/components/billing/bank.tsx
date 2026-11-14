'use client'
import { useState } from 'react'
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
  status: 'unmatched' | 'matched'
  match_label: string | null
  suggestions: Suggestion[]
}
type Summary = {
  lines: number
  matched: number
  unmatched: number
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

function LineCard({ l, onChange }: { l: Line; onChange: () => void }) {
  const { ws } = useShell()
  const [rec, setRec] = useState(false)
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
  return (
    <li className="space-y-2 rounded-md border border-line p-3">
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1">
        <div className="min-w-0 flex-1">
          <div className="text-[12px] text-muted">{dmy(l.date)}</div>
          <div className="break-words text-[14px]">{l.narration || '(no narration)'}</div>
        </div>
        <Amount minor={l.amount_minor} />
      </div>
      {l.status === 'matched' ? (
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
          <button type="button" className={`${btn} min-h-[44px] sm:min-h-0`} onClick={() => setRec(true)}>
            {l.amount_minor < 0 ? 'Record as expense' : 'Record as receipt'}
          </button>
        </div>
      )}
      <ErrorText error={err} />
      {rec && <RecordModal line={l} onClose={() => setRec(false)} onDone={onChange} />}
    </li>
  )
}

function ImportBox({ account, onDone }: { account: Account; onDone: () => void }) {
  const { ws } = useShell()
  const [res, setRes] = useState<ImportResult>()
  const [err, setErr] = useState<string>()
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
              try {
                setRes(
                  await run<ImportResult>(ws, 'bank.import', {
                    account_id: account.id,
                    csv: await file.text(),
                  }),
                )
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
        Columns are detected from the header and remembered for this account. Dates are read as DD/MM/YYYY.
        Importing the same lines again is safe.
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
    </div>
  )
}

function Reconcile({ account, reload }: { account: Account; reload: () => void }) {
  const { ws } = useShell()
  const [tab, setTab] = useState<'unmatched' | 'matched'>('unmatched')
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
        {(['unmatched', 'matched'] as const).map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            className={`${tab === t ? btnPrimary : btn} min-h-[44px] sm:min-h-0`}
            onClick={() => setTab(t)}
          >
            {t === 'unmatched' ? 'Unmatched' : 'Matched'}
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
