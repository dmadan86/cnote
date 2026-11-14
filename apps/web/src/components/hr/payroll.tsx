'use client'
import Link from 'next/link'
import { useState } from 'react'
import { download } from '@/components/audit/log'
import { money, parseMinor, STATES, toRupees } from '@/components/billing/fmt'
import { BranchSelect } from '@/components/company/branch-filter'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { PeopleTabs, useRole } from './shared'

type Run = {
  id: string
  month: string
  branch_id: string | null
  status: 'draft' | 'approved' | 'finalised' | 'paid' | 'reversed'
  employees: number
  gross_minor: number
  deductions_minor: number
  net_minor: number
  employer_minor: number
  warnings: string[]
  reverse_reason: string | null
  paid_reference: string | null
}
type Line = {
  id: string
  employee_id: string
  name: string
  code: string | null
  lop_days: number
  gross_minor: number
  epf_ee_minor: number
  esi_ee_minor: number
  pt_minor: number
  tds_minor: number
  adj_earnings_minor: number
  adj_deductions_minor: number
  deductions_minor: number
  net_minor: number
  warnings: string[]
}
type Adjustment = {
  id: string
  employee_id: string
  kind: 'earning' | 'deduction'
  amount_minor: number
  reason: string
}
type RunDetailData = Run & { lines: Line[]; adjustments: Adjustment[] }
type Structure = {
  employee_id: string
  gross_minor: number
  pt_state: string | null
  pf_applicable: boolean
  esi_applicable: boolean
  regime: string
  pan: string | null
  bank_account: string | null
  ifsc: string | null
  has_bank: boolean
}
type StructureRow = {
  employee_id: string
  name: string
  code: string | null
  active: boolean
  structure: Structure | null
}

const statusTone = {
  draft: 'warn',
  approved: 'neutral',
  finalised: 'ok',
  paid: 'ok',
  reversed: 'bad',
} as const
const monthName = (m: string) =>
  new Date(`${m}-01T00:00:00Z`).toLocaleDateString('en-IN', {
    month: 'long',
    year: 'numeric',
    timeZone: 'UTC',
  })

/** Warning codes from the engine, in words. blocks = stops approval. */
const WARN: Record<string, { text: string; blocks?: boolean }> = {
  no_pt_state: { text: 'Configure PT: no professional tax table for this employee' },
  unmarked_days: { text: 'Attendance not marked on some working days (they are paid)' },
  old_regime_tds_not_computed: { text: 'Old tax regime: TDS is not computed' },
  no_pan_higher_rate_not_applied: { text: 'No PAN: the higher TDS rate is not applied' },
  surcharge_not_computed: { text: 'Income above Rs 50 lakh: surcharge is not computed' },
  negative_net: { text: 'Deductions exceed pay', blocks: true },
}

function Warn({ code }: { code: string }) {
  const w = WARN[code]
  return (
    <span
      className={`inline-block rounded px-1.5 py-0.5 text-[12px] ${w?.blocks ? 'bg-bad-bg text-bad' : 'bg-warn-bg text-warn'}`}
    >
      {w?.blocks ? 'Blocks approval: ' : 'Warning: '}
      {w?.text ?? code}
    </span>
  )
}

function Denied({ error }: { error?: Error }) {
  if (!error) return null
  return (
    <p role="alert" className="rounded-md border border-line p-4 text-[14px]">
      Payroll is for owners and admins with finance access. {error.message}
    </p>
  )
}

// ---------- Payroll home: runs and salary structures ----------

export function PayrollHome() {
  const { ws } = useRole()
  const runs = useCmd<Run[]>(ws, 'payroll.run_list')
  const prev = new Date()
  prev.setMonth(prev.getMonth() - 1)
  const [month, setMonth] = useState(prev.toISOString().slice(0, 7))
  const [branch, setBranch] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    setErr(undefined)
    setBusy(true)
    try {
      const r = await cmd<Run>(ws, 'payroll.run_create', { month, branch_id: branch || undefined })
      window.location.assign(`/w/${ws}/people/payroll/${r.id}`)
    } catch (x) {
      setErr((x as Error).message)
      setBusy(false)
    }
  }
  return (
    <div className="mx-auto max-w-5xl space-y-6 px-4 py-8 sm:px-6">
      <h1 className="text-xl font-semibold">People</h1>
      <PeopleTabs current="payroll" />
      <Denied error={runs.error} />
      {!runs.error && (
        <>
          <section aria-labelledby="runs" className="space-y-3">
            <h2 id="runs" className="text-[15px] font-semibold">
              Payroll runs
            </h2>
            <form onSubmit={create} className="flex flex-wrap items-end gap-2">
              <Field label="Month">
                {(id) => (
                  <input
                    id={id}
                    type="month"
                    required
                    value={month}
                    onChange={(e) => setMonth(e.target.value)}
                    className={`${inputCls} w-auto`}
                  />
                )}
              </Field>
              <BranchSelect ws={ws} value={branch} onChange={setBranch} allLabel="All branches" />
              <button type="submit" disabled={busy} className={btnPrimary}>
                Create draft payroll
              </button>
            </form>
            <p className="text-[12px] text-muted">
              Creates a draft only: nothing is posted until you finalise. A month is run once for everyone or
              once per branch.
            </p>
            <ErrorText error={err} />
            {runs.data && runs.data.length === 0 && (
              <p className="text-[14px] text-muted">
                No payroll runs yet. Set salary structures below, then create a draft.
              </p>
            )}
            {runs.data && runs.data.length > 0 && (
              <ul className="divide-y divide-line rounded-lg border border-line" aria-label="Payroll runs">
                {runs.data.map((r) => (
                  <li key={r.id}>
                    <Link
                      href={`/w/${ws}/people/payroll/${r.id}`}
                      className="flex flex-wrap items-center justify-between gap-2 p-3 hover:bg-hover"
                    >
                      <span className="font-medium">{monthName(r.month)}</span>
                      <span className="flex flex-wrap items-center gap-3 text-[13px] text-muted">
                        <span>{r.employees} people</span>
                        <span>Net {money(r.net_minor)}</span>
                        <Pill tone={statusTone[r.status]}>{r.status}</Pill>
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </section>
          <Structures />
        </>
      )}
    </div>
  )
}

function Structures() {
  const { ws } = useRole()
  const list = useCmd<StructureRow[]>(ws, 'payroll.structure_list')
  const [edit, setEdit] = useState<StructureRow>()
  return (
    <section aria-labelledby="structures" className="space-y-3">
      <h2 id="structures" className="text-[15px] font-semibold">
        Salary structures
      </h2>
      <p className="text-[12px] text-muted">Monthly amounts. PAN and bank details are masked here.</p>
      <ErrorText error={list.error} />
      <ul className="divide-y divide-line rounded-lg border border-line" aria-label="Salary structures">
        {(list.data ?? [])
          .filter((r) => r.active)
          .map((r) => (
            <li key={r.employee_id} className="flex flex-wrap items-center justify-between gap-2 p-3">
              <div className="min-w-0">
                <p className="truncate font-medium">{r.name}</p>
                <p className="text-[12px] text-muted">
                  {r.structure
                    ? `${money(r.structure.gross_minor)} a month · ${r.structure.pf_applicable ? 'PF' : 'no PF'} · ${r.structure.esi_applicable ? 'ESI' : 'no ESI'} · ${r.structure.regime} regime · bank ${r.structure.bank_account ?? 'not set'}`
                    : 'No salary structure: not included in payroll'}
                </p>
              </div>
              <button
                type="button"
                className={btn}
                onClick={() => setEdit(r)}
                aria-label={`${r.structure ? 'Edit' : 'Set'} salary structure of ${r.name}`}
              >
                {r.structure ? 'Edit' : 'Set structure'}
              </button>
            </li>
          ))}
      </ul>
      {edit && (
        <StructureForm
          row={edit}
          onClose={() => setEdit(undefined)}
          onSaved={() => {
            setEdit(undefined)
            list.reload()
          }}
        />
      )}
    </section>
  )
}

type Full = Structure & {
  basic_minor: number
  hra_minor: number
  special_minor: number
  other_minor: number
  uan: string | null
  esic_no: string | null
}

function StructureForm({
  row,
  onClose,
  onSaved,
}: {
  row: StructureRow
  onClose: () => void
  onSaved: () => void
}) {
  const { ws } = useRole()
  const full = useCmd<Full>(ws, 'payroll.structure_get', { employee_id: row.employee_id }, !!row.structure)
  return (
    <Modal open onClose={onClose} title={`Salary structure: ${row.name}`} className="max-w-lg">
      {row.structure && !full.data ? (
        <p className="text-[13px] text-muted">Loading…</p>
      ) : (
        <StructureFields row={row} init={full.data} onClose={onClose} onSaved={onSaved} />
      )}
    </Modal>
  )
}

function StructureFields({
  row,
  init,
  onClose,
  onSaved,
}: {
  row: StructureRow
  init?: Full
  onClose: () => void
  onSaved: () => void
}) {
  const { ws } = useRole()
  const r = (v?: number) => (v === undefined ? '' : toRupees(v))
  const [f, setF] = useState({
    basic: r(init?.basic_minor),
    hra: r(init?.hra_minor),
    special: r(init?.special_minor),
    other: r(init?.other_minor),
    pt_state: init?.pt_state ?? '',
    pf: init?.pf_applicable ?? true,
    esi: init?.esi_applicable ?? false,
    regime: init?.regime ?? 'new',
    pan: init?.pan ?? '',
    bank: init?.bank_account ?? '',
    ifsc: init?.ifsc ?? '',
    uan: init?.uan ?? '',
    esic: init?.esic_no ?? '',
  })
  const [err, setErr] = useState<string>()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF({
      ...f,
      [k]: e.target.type === 'checkbox' ? (e.target as HTMLInputElement).checked : e.target.value,
    })
  const amounts = [f.basic, f.hra, f.special, f.other].map((v) => (v === '' ? 0 : parseMinor(v)))
  const total = amounts.every((x) => x !== null) ? (amounts as number[]).reduce((a, b) => a + b, 0) : null
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    setErr(undefined)
    if (amounts.some((x) => x === null)) return setErr('Amounts are rupees like 25000 or 25000.50')
    try {
      await cmd(ws, 'payroll.structure_set', {
        employee_id: row.employee_id,
        basic_minor: amounts[0],
        hra_minor: amounts[1],
        special_minor: amounts[2],
        other_minor: amounts[3],
        pt_state: f.pt_state,
        pf_applicable: f.pf,
        esi_applicable: f.esi,
        regime: f.regime,
        pan: f.pan,
        bank_account: f.bank,
        ifsc: f.ifsc,
        uan: f.uan,
        esic_no: f.esic,
      })
      onSaved()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const money_ = (label: string, k: 'basic' | 'hra' | 'special' | 'other') => (
    <Field label={`${label} (Rs a month)`}>
      {(id) => (
        <input
          id={id}
          inputMode="decimal"
          value={f[k]}
          onChange={set(k)}
          required={k === 'basic'}
          className={inputCls}
        />
      )}
    </Field>
  )
  return (
    <form onSubmit={save} className="max-h-[70vh] space-y-4 overflow-y-auto pr-1">
      <fieldset className="space-y-2">
        <legend className="text-[13px] font-semibold">Monthly earnings</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          {money_('Basic', 'basic')}
          {money_('HRA', 'hra')}
          {money_('Special allowance', 'special')}
          {money_('Other fixed allowances', 'other')}
        </div>
        <p className="text-[13px]">
          Monthly gross: <strong>{total === null ? '–' : money(total)}</strong>
        </p>
      </fieldset>
      <fieldset className="space-y-2">
        <legend className="text-[13px] font-semibold">Statutory</legend>
        <Field label="State for professional tax">
          {(id) => (
            <select id={id} value={f.pt_state} onChange={set('pt_state')} className={inputCls}>
              <option value="">None</option>
              {STATES.map(([c, n]) => (
                <option key={c} value={c}>
                  {n}
                </option>
              ))}
            </select>
          )}
        </Field>
        <label className="flex items-center gap-2 text-[14px]">
          <input type="checkbox" checked={f.pf} onChange={set('pf')} /> Provident fund applies
        </label>
        <label className="flex items-center gap-2 text-[14px]">
          <input type="checkbox" checked={f.esi} onChange={set('esi')} /> ESI applies (only while monthly
          gross is within the ceiling)
        </label>
        <div className="grid gap-2 sm:grid-cols-2">
          <Field label="UAN">
            {(id) => <input id={id} value={f.uan} onChange={set('uan')} className={inputCls} />}
          </Field>
          <Field label="ESIC number">
            {(id) => <input id={id} value={f.esic} onChange={set('esic')} className={inputCls} />}
          </Field>
        </div>
      </fieldset>
      <fieldset className="space-y-2">
        <legend className="text-[13px] font-semibold">Tax</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          <Field label="Tax regime">
            {(id) => (
              <select id={id} value={f.regime} onChange={set('regime')} className={inputCls}>
                <option value="new">New regime</option>
                <option value="old">Old regime (TDS not computed)</option>
              </select>
            )}
          </Field>
          <Field label="PAN">
            {(id) => (
              <input id={id} value={f.pan} onChange={set('pan')} className={inputCls} autoComplete="off" />
            )}
          </Field>
        </div>
      </fieldset>
      <fieldset className="space-y-2">
        <legend className="text-[13px] font-semibold">Bank account</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          <Field label="Account number">
            {(id) => (
              <input id={id} value={f.bank} onChange={set('bank')} className={inputCls} autoComplete="off" />
            )}
          </Field>
          <Field label="IFSC">
            {(id) => (
              <input id={id} value={f.ifsc} onChange={set('ifsc')} className={inputCls} autoComplete="off" />
            )}
          </Field>
        </div>
      </fieldset>
      <p className="text-[12px] text-muted">
        Changes apply to the next draft or recompute; finalised payslips never change.
      </p>
      <ErrorText error={err} />
      <div className="flex justify-end gap-2">
        <button type="button" className={btn} onClick={onClose}>
          Cancel
        </button>
        <button type="submit" className={btnPrimary}>
          Save structure
        </button>
      </div>
    </form>
  )
}

// ---------- Run detail ----------

const STEPS = ['draft', 'approved', 'finalised', 'paid'] as const

export function RunDetail({ id }: { id: string }) {
  const { ws, role } = useRole()
  const run = useCmd<RunDetailData>(ws, 'payroll.run_get', { id })
  const [err, setErr] = useState<string>()
  const [note, setNote] = useState<string>()
  const [modal, setModal] = useState<'finalise' | 'paid' | 'reverse' | 'delete'>()
  const [adjust, setAdjust] = useState<Line>()
  const [reason, setReason] = useState('')
  const [ref, setRef] = useState('')
  const [busy, setBusy] = useState(false)
  const d = run.data
  const act = async (name: string, extra: Record<string, unknown> = {}) => {
    setErr(undefined)
    setNote(undefined)
    setBusy(true)
    try {
      const r = await cmd<{ approval_required?: boolean; deleted?: boolean }>(ws, name, { id, ...extra })
      if (r.approval_required) setNote('Sent for approval. You will be notified when it is decided.')
      if (r.deleted) return window.location.assign(`/w/${ws}/people/payroll`)
      setModal(undefined)
      run.reload()
    } catch (x) {
      setErr((x as Error).message)
    }
    setBusy(false)
  }
  const file = async (name: string, extra: Record<string, unknown> = {}) => {
    setErr(undefined)
    try {
      const r = await cmd<{ csv: string; filename: string; missing_bank_details?: string[] }>(ws, name, {
        id,
        ...extra,
      })
      download(r.filename, r.csv, 'text/csv')
      if (r.missing_bank_details?.length)
        setNote(`Not in the file (no bank details): ${r.missing_bank_details.join(', ')}`)
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  if (run.error)
    return (
      <div className="mx-auto max-w-5xl px-4 py-8">
        <Denied error={run.error} />
      </div>
    )
  if (!d) return <p className="p-6 text-muted">Loading…</p>
  const blockers = d.lines.filter((l) => l.warnings.includes('negative_net')).length
  const warned = d.lines.filter((l) => l.warnings.some((w) => w !== 'negative_net')).length
  const idx = STEPS.indexOf(d.status as (typeof STEPS)[number])
  const editable = d.status === 'draft' || d.status === 'approved'
  const adjFor = (eid: string) => d.adjustments.filter((a) => a.employee_id === eid)
  return (
    <div className="mx-auto max-w-5xl space-y-5 px-4 py-8 sm:px-6">
      <Link href={`/w/${ws}/people/payroll`} className="text-[13px] text-muted hover:underline">
        Back to payroll
      </Link>
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Payroll for {monthName(d.month)}</h1>
        <Pill tone={statusTone[d.status]}>{d.status}</Pill>
      </div>
      {d.status === 'reversed' ? (
        <p role="status" className="rounded-md border border-line p-3 text-[14px]">
          Reversed by an owner: {d.reverse_reason}. The journal was reversed; any money already sent is not
          recovered by this.
        </p>
      ) : (
        <ol aria-label="Progress" className="flex flex-wrap gap-2 text-[13px]">
          {STEPS.map((s, i) => (
            <li
              key={s}
              aria-current={i === idx ? 'step' : undefined}
              className={`rounded-full border px-3 py-1 ${i < idx ? 'border-line text-muted' : i === idx ? 'border-accent font-semibold' : 'border-line text-muted'}`}
            >
              {i < idx ? '✓ ' : ''}
              {`${i + 1}. ${s[0].toUpperCase()}${s.slice(1)}`}
            </li>
          ))}
        </ol>
      )}
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {[
          ['Gross', d.gross_minor],
          ['Deductions', d.deductions_minor],
          ['Net pay', d.net_minor],
          ['Employer contributions', d.employer_minor],
        ].map(([k, v]) => (
          <div key={k as string} className="rounded-lg border border-line p-3">
            <dt className="text-[12px] text-muted">{k}</dt>
            <dd className="text-[16px] font-semibold">{money(v as number)}</dd>
          </div>
        ))}
      </dl>
      {(d.warnings.length > 0 || blockers > 0 || warned > 0) && (
        <div role="status" className="space-y-1 rounded-md border border-line p-3 text-[13px]">
          <p className="font-semibold">
            {blockers} blocking, {warned} with warnings
          </p>
          <ul className="list-disc pl-5">
            {d.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </div>
      )}
      <ErrorText error={err} />
      {note && (
        <p role="status" className="text-[13px]">
          {note}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {editable && (
          <button type="button" disabled={busy} className={btn} onClick={() => act('payroll.run_recompute')}>
            Recompute from attendance
          </button>
        )}
        {d.status === 'draft' && (
          <button
            type="button"
            disabled={busy || blockers > 0}
            className={btnPrimary}
            onClick={() => act('payroll.approve')}
          >
            Approve run
          </button>
        )}
        {d.status === 'approved' && (
          <button type="button" disabled={busy} className={btnPrimary} onClick={() => setModal('finalise')}>
            Finalise…
          </button>
        )}
        {d.status === 'finalised' && (
          <button type="button" disabled={busy} className={btnPrimary} onClick={() => setModal('paid')}>
            Record payment…
          </button>
        )}
        {(d.status === 'finalised' || d.status === 'paid') && (
          <button type="button" className={btn} onClick={() => file('payroll.payout_csv')}>
            Download bank payout CSV
          </button>
        )}
        {editable && (
          <button type="button" className={btn} onClick={() => setModal('delete')}>
            Delete run
          </button>
        )}
      </div>
      {d.status === 'draft' && (
        <p className="text-[12px] text-muted">
          Approving sends the run through your approval rule if one is set for payroll.
        </p>
      )}
      {(d.status === 'finalised' || d.status === 'paid') && <Statutory month={d.month} />}

      <section aria-labelledby="lines" className="space-y-2">
        <h2 id="lines" className="text-[15px] font-semibold">
          Employees
        </h2>
        <div className="hidden overflow-x-auto md:block">
          <table className="w-full min-w-[760px] text-[13px]">
            <caption className="sr-only">Pay of each employee for the month</caption>
            <thead>
              <tr className="border-b border-line text-left text-muted">
                {['Employee', 'LOP days', 'Gross', 'EPF', 'ESI', 'PT', 'TDS', 'One-offs', 'Net', ''].map(
                  (h) => (
                    <th key={h} scope="col" className="px-2 py-2 font-medium">
                      {h}
                    </th>
                  ),
                )}
              </tr>
            </thead>
            <tbody>
              {d.lines.map((l) => (
                <tr key={l.id} className="border-b border-line align-top">
                  <th scope="row" className="px-2 py-2 text-left font-medium">
                    {l.name}
                    <div className="space-y-1">
                      {l.warnings.map((w) => (
                        <div key={w}>
                          <Warn code={w} />
                        </div>
                      ))}
                    </div>
                  </th>
                  <td className="px-2 py-2">{l.lop_days}</td>
                  <td className="px-2 py-2">{money(l.gross_minor)}</td>
                  <td className="px-2 py-2">{money(l.epf_ee_minor)}</td>
                  <td className="px-2 py-2">{money(l.esi_ee_minor)}</td>
                  <td className="px-2 py-2">{money(l.pt_minor)}</td>
                  <td className="px-2 py-2">{money(l.tds_minor)}</td>
                  <td className="px-2 py-2">
                    {adjFor(l.employee_id).map((a) => (
                      <div key={a.id} title={a.reason}>
                        {a.kind === 'earning' ? '+' : '-'}
                        {money(a.amount_minor)} {a.reason}
                      </div>
                    ))}
                  </td>
                  <td className="px-2 py-2 font-semibold">{money(l.net_minor)}</td>
                  <td className="px-2 py-2">
                    {editable && (
                      <button
                        type="button"
                        className={btn}
                        onClick={() => setAdjust(l)}
                        aria-label={`Adjust pay of ${l.name}`}
                      >
                        Adjust
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <ul className="space-y-2 md:hidden" aria-label="Employees">
          {d.lines.map((l) => (
            <li key={l.id} className="rounded-lg border border-line p-3">
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium">{l.name}</span>
                <span className="font-semibold">{money(l.net_minor)}</span>
              </div>
              {l.warnings.length > 0 && (
                <div className="mt-1 space-y-1">
                  {l.warnings.map((w) => (
                    <div key={w}>
                      <Warn code={w} />
                    </div>
                  ))}
                </div>
              )}
              <details className="mt-1 text-[13px]">
                <summary className="cursor-pointer py-1 text-muted">Breakdown</summary>
                <dl className="grid grid-cols-2 gap-x-3 gap-y-1">
                  <dt>Loss of pay</dt>
                  <dd>{l.lop_days} days</dd>
                  <dt>Gross</dt>
                  <dd>{money(l.gross_minor)}</dd>
                  <dt>EPF</dt>
                  <dd>{money(l.epf_ee_minor)}</dd>
                  <dt>ESI</dt>
                  <dd>{money(l.esi_ee_minor)}</dd>
                  <dt>PT</dt>
                  <dd>{money(l.pt_minor)}</dd>
                  <dt>TDS</dt>
                  <dd>{money(l.tds_minor)}</dd>
                </dl>
                {adjFor(l.employee_id).map((a) => (
                  <p key={a.id}>
                    {a.kind === 'earning' ? '+' : '-'}
                    {money(a.amount_minor)} {a.reason}
                  </p>
                ))}
              </details>
              {editable && (
                <button
                  type="button"
                  className={`${btn} mt-2`}
                  onClick={() => setAdjust(l)}
                  aria-label={`Adjust pay of ${l.name}`}
                >
                  Adjust
                </button>
              )}
            </li>
          ))}
        </ul>
        {d.lines.length === 0 && (
          <p className="text-[14px] text-muted">No employee has a salary structure yet.</p>
        )}
      </section>

      {(d.status === 'finalised' || d.status === 'paid') && role === 'owner' && (
        <details className="rounded-md border border-line p-3">
          <summary className="cursor-pointer text-[13px] font-medium text-bad">
            Reverse this run (owner only)
          </summary>
          <p className="my-2 text-[13px]">
            Posts a reversing journal and removes the payslips. If salary was already sent, the money is not
            recovered by this.
          </p>
          <button type="button" className={btn} onClick={() => setModal('reverse')}>
            Reverse run…
          </button>
        </details>
      )}

      <Modal
        open={modal === 'finalise'}
        onClose={() => setModal(undefined)}
        title={`Finalise ${monthName(d.month)} payroll`}
      >
        <p className="text-[14px]">
          This locks the run for {d.employees} people and posts salary expense {money(d.gross_minor)},
          employer contributions {money(d.employer_minor)} and a net salary payable of {money(d.net_minor)} to
          the books. After this the month can only be reversed by an owner.
        </p>
        {(blockers > 0 || warned > 0) && (
          <p className="mt-2 text-[13px] text-warn">{warned} employee(s) still have warnings.</p>
        )}
        <ErrorText error={err} />
        <div className="mt-3 flex justify-end gap-2">
          <button type="button" className={btn} onClick={() => setModal(undefined)}>
            Cancel
          </button>
          <button
            type="button"
            disabled={busy}
            className={btnPrimary}
            onClick={() => act('payroll.finalise')}
          >
            Finalise and post
          </button>
        </div>
      </Modal>
      <Modal open={modal === 'paid'} onClose={() => setModal(undefined)} title="Record salary payment">
        <p className="text-[14px]">
          This records that {money(d.net_minor)} was paid from the bank. It does not send any money; upload
          the payout CSV at your bank first.
        </p>
        <div className="mt-2">
          <Field label="Payment reference (optional)">
            {(fid) => (
              <input id={fid} value={ref} onChange={(e) => setRef(e.target.value)} className={inputCls} />
            )}
          </Field>
        </div>
        <ErrorText error={err} />
        <div className="mt-3 flex justify-end gap-2">
          <button type="button" className={btn} onClick={() => setModal(undefined)}>
            Cancel
          </button>
          <button
            type="button"
            disabled={busy}
            className={btnPrimary}
            onClick={() => act('payroll.mark_paid', { reference: ref })}
          >
            Record payment
          </button>
        </div>
      </Modal>
      <Modal
        open={modal === 'reverse'}
        onClose={() => setModal(undefined)}
        title={`Reverse ${monthName(d.month)} payroll`}
      >
        <Field label="Reason (required)">
          {(fid) => (
            <textarea
              id={fid}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              className={inputCls}
              rows={3}
            />
          )}
        </Field>
        <ErrorText error={err} />
        <div className="mt-3 flex justify-end gap-2">
          <button type="button" className={btn} onClick={() => setModal(undefined)}>
            Cancel
          </button>
          <button
            type="button"
            disabled={busy || !reason.trim()}
            className={`${btnPrimary} !bg-bad`}
            onClick={() => act('payroll.reverse', { reason })}
          >
            Reverse run
          </button>
        </div>
      </Modal>
      <Modal open={modal === 'delete'} onClose={() => setModal(undefined)} title="Delete this run?">
        <p className="text-[14px]">The draft and its adjustments are removed. Nothing was posted.</p>
        <div className="mt-3 flex justify-end gap-2">
          <button type="button" className={btn} onClick={() => setModal(undefined)}>
            Cancel
          </button>
          <button
            type="button"
            disabled={busy}
            className={btnPrimary}
            onClick={() => act('payroll.run_delete')}
          >
            Delete run
          </button>
        </div>
      </Modal>
      {adjust && (
        <AdjustDialog
          line={adjust}
          runId={id}
          existing={adjFor(adjust.employee_id)}
          onClose={() => setAdjust(undefined)}
          onDone={() => {
            setAdjust(undefined)
            run.reload()
          }}
        />
      )}
    </div>
  )
}

function AdjustDialog({
  line,
  runId,
  existing,
  onClose,
  onDone,
}: {
  line: Line
  runId: string
  existing: Adjustment[]
  onClose: () => void
  onDone: () => void
}) {
  const { ws } = useRole()
  const [kind, setKind] = useState('earning')
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [err, setErr] = useState<string>()
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const minor = parseMinor(amount)
    if (!minor) return setErr('Enter an amount in rupees')
    try {
      await cmd(ws, 'payroll.adjust', {
        id: runId,
        employee_id: line.employee_id,
        kind,
        amount_minor: minor,
        reason,
      })
      onDone()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const remove = async (id: string) => {
    try {
      await cmd(ws, 'payroll.adjust_delete', { id })
      onDone()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <Modal open onClose={onClose} title={`Adjust pay: ${line.name}`}>
      {existing.length > 0 && (
        <ul className="mb-3 space-y-1 text-[13px]">
          {existing.map((a) => (
            <li key={a.id} className="flex items-center justify-between gap-2">
              <span>
                {a.kind === 'earning' ? '+' : '-'}
                {money(a.amount_minor)} {a.reason}
              </span>
              <button
                type="button"
                className={btn}
                onClick={() => remove(a.id)}
                aria-label={`Remove adjustment ${a.reason}`}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={submit} className="space-y-3">
        <Field label="Type">
          {(id) => (
            <select id={id} value={kind} onChange={(e) => setKind(e.target.value)} className={inputCls}>
              <option value="earning">One-off earning (taxable, not PF wages)</option>
              <option value="deduction">One-off deduction (e.g. advance recovery)</option>
            </select>
          )}
        </Field>
        <Field label="Amount (Rs)">
          {(id) => (
            <input
              id={id}
              inputMode="decimal"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              required
              className={inputCls}
            />
          )}
        </Field>
        <Field label="Reason (required)">
          {(id) => (
            <input
              id={id}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              required
              maxLength={200}
              className={inputCls}
            />
          )}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Close
          </button>
          <button type="submit" className={btnPrimary}>
            Add adjustment
          </button>
        </div>
      </form>
    </Modal>
  )
}

function Statutory({ month }: { month: string }) {
  const { ws } = useRole()
  const [err, setErr] = useState<string>()
  const get = async (kind: string) => {
    setErr(undefined)
    try {
      const r = await cmd<{ csv: string; filename: string }>(ws, 'payroll.statutory', { month, kind })
      download(r.filename, r.csv, 'text/csv')
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <section aria-labelledby="stat" className="space-y-2">
      <h2 id="stat" className="text-[15px] font-semibold">
        Statutory exports for your CA
      </h2>
      <div className="flex flex-wrap gap-2">
        {[
          ['pf', 'PF (ECR-style)'],
          ['esi', 'ESI'],
          ['pt', 'Professional tax by state'],
          ['tds', 'TDS (24Q-style)'],
        ].map(([k, label]) => (
          <button key={k} type="button" className={btn} onClick={() => get(k)}>
            {label} CSV
          </button>
        ))}
      </div>
      <p className="text-[12px] text-muted">Exports only. Nothing is filed from here.</p>
      <ErrorText error={err} />
    </section>
  )
}

// ---------- My payslips ----------

type Slip = {
  id: string
  month: string
  run_status: string
  gross_minor: number
  deductions_minor: number
  net_minor: number
}

export function Payslips() {
  const { ws } = useRole()
  const list = useCmd<Slip[]>(ws, 'payslip.list')
  return (
    <div className="mx-auto max-w-4xl space-y-6 px-4 py-8 sm:px-6">
      <h1 className="text-xl font-semibold">People</h1>
      <PeopleTabs current="payslips" />
      <section aria-labelledby="slips" className="space-y-3">
        <h2 id="slips" className="text-[15px] font-semibold">
          My payslips
        </h2>
        <ErrorText error={list.error} />
        {list.data?.length === 0 && (
          <p className="text-[14px] text-muted">
            No payslips yet. They appear here once payroll for a month is finalised.
          </p>
        )}
        <ul
          className="divide-y divide-line rounded-lg border border-line empty:hidden"
          aria-label="My payslips"
        >
          {(list.data ?? []).map((s) => (
            <li key={s.id}>
              <Link
                href={`/w/${ws}/people/payslips/${s.id}`}
                className="flex flex-wrap items-center justify-between gap-2 p-3 hover:bg-hover"
              >
                <span className="font-medium">{monthName(s.month)}</span>
                <span className="flex items-center gap-3 text-[13px] text-muted">
                  <span>Net {money(s.net_minor)}</span>
                  <Pill tone={s.run_status === 'paid' ? 'ok' : 'neutral'}>
                    {s.run_status === 'paid' ? 'Paid' : 'Payment pending'}
                  </Pill>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}

type Slipdata = {
  month: string
  run_status: string
  employee_name: string
  employee_code: string | null
  pan: string | null
  uan: string | null
  bank_account: string | null
  ifsc: string | null
  company: string
  company_gstin: string | null
  days_in_month: number
  lop_days: number
  basic_minor: number
  hra_minor: number
  special_minor: number
  other_minor: number
  adj_earnings_minor: number
  gross_minor: number
  epf_ee_minor: number
  esi_ee_minor: number
  pt_minor: number
  tds_minor: number
  adj_deductions_minor: number
  deductions_minor: number
  net_minor: number
  epf_er_minor: number
  eps_er_minor: number
  esi_er_minor: number
  adjustments: Adjustment[]
}

const PRINT_CSS = `@media print {
  body * { visibility: hidden; }
  #payslip-print, #payslip-print * { visibility: visible; }
  #payslip-print { position: absolute; inset: 0; padding: 24px; background: white; color: black; }
  #payslip-print tr { break-inside: avoid; }
  .no-print { display: none !important; }
}`

export function PayslipView({ id }: { id: string }) {
  const { ws } = useRole()
  const slip = useCmd<Slipdata>(ws, 'payslip.get', { id })
  const s = slip.data
  if (slip.error)
    return (
      <p role="alert" className="p-6 text-bad">
        {slip.error.message}
      </p>
    )
  if (!s) return <p className="p-6 text-muted">Loading…</p>
  const row = (label: string, v: number) =>
    v !== 0 && (
      <tr key={label}>
        <th scope="row" className="py-1 text-left font-normal">
          {label}
        </th>
        <td className="py-1 text-right tabular-nums">{money(v)}</td>
      </tr>
    )
  const er = s.epf_er_minor + s.eps_er_minor + s.esi_er_minor
  return (
    <div className="mx-auto max-w-3xl space-y-4 px-4 py-8 sm:px-6">
      <style>{PRINT_CSS}</style>
      <div className="no-print flex items-center gap-3">
        <Link href={`/w/${ws}/people/payslips`} className="text-[13px] text-muted hover:underline">
          Back to payslips
        </Link>
        <button type="button" className={btn} onClick={() => window.print()}>
          Print or save as PDF
        </button>
      </div>
      <article id="payslip-print" className="space-y-5 rounded-lg border border-line p-6">
        <header className="flex flex-wrap items-start justify-between gap-2">
          <div>
            <p className="text-xl font-semibold">{s.company}</p>
            {s.company_gstin && <p className="text-[12px] text-muted">GSTIN {s.company_gstin}</p>}
          </div>
          <div className="text-right">
            <h1 className="text-[15px] font-semibold">Payslip, {monthName(s.month)}</h1>
            <p className="text-[12px] text-muted">{s.run_status === 'paid' ? 'Paid' : 'Payment pending'}</p>
          </div>
        </header>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-[13px] sm:grid-cols-3">
          <dt className="sr-only">Employee</dt>
          <dd className="col-span-2 font-medium sm:col-span-3">
            {s.employee_name}
            {s.employee_code ? ` (${s.employee_code})` : ''}
          </dd>
          {s.pan && (
            <>
              <dt className="text-muted">PAN</dt>
              <dd>{s.pan}</dd>
            </>
          )}
          {s.uan && (
            <>
              <dt className="text-muted">UAN</dt>
              <dd>{s.uan}</dd>
            </>
          )}
          {s.bank_account && (
            <>
              <dt className="text-muted">Bank</dt>
              <dd>
                {s.bank_account} {s.ifsc}
              </dd>
            </>
          )}
          <dt className="text-muted">Days paid</dt>
          <dd>
            {s.days_in_month - s.lop_days} of {s.days_in_month}
            {s.lop_days ? ` (${s.lop_days} loss of pay)` : ''}
          </dd>
        </dl>
        <p className="text-[13px] text-muted">Net pay</p>
        <p className="-mt-4 text-3xl font-semibold">{money(s.net_minor)}</p>
        <div className="grid gap-6 sm:grid-cols-2">
          <table className="w-full text-[14px]">
            <caption className="mb-1 text-left text-[13px] font-semibold">Earnings</caption>
            <tbody>
              {row('Basic', s.basic_minor)}
              {row('HRA', s.hra_minor)}
              {row('Special allowance', s.special_minor)}
              {row('Other allowances', s.other_minor)}
              {s.adjustments.filter((a) => a.kind === 'earning').map((a) => row(a.reason, a.amount_minor))}
              <tr className="border-t border-line font-semibold">
                <th scope="row" className="py-1 text-left">
                  Gross
                </th>
                <td className="py-1 text-right">{money(s.gross_minor)}</td>
              </tr>
            </tbody>
          </table>
          <table className="w-full text-[14px]">
            <caption className="mb-1 text-left text-[13px] font-semibold">Deductions</caption>
            <tbody>
              {row('Provident fund', s.epf_ee_minor)}
              {row('ESI', s.esi_ee_minor)}
              {row('Professional tax', s.pt_minor)}
              {row('Income tax (TDS)', s.tds_minor)}
              {s.adjustments.filter((a) => a.kind === 'deduction').map((a) => row(a.reason, a.amount_minor))}
              <tr className="border-t border-line font-semibold">
                <th scope="row" className="py-1 text-left">
                  Total deductions
                </th>
                <td className="py-1 text-right">{money(s.deductions_minor)}</td>
              </tr>
            </tbody>
          </table>
        </div>
        {er > 0 && (
          <p className="text-[12px] text-muted">
            Employer contributions, not deducted from you: {money(er)} (provident fund and ESI).
          </p>
        )}
      </article>
    </div>
  )
}
