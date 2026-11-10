'use client'
import { Download } from 'lucide-react'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, ErrorText, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { money } from './fmt'

const today = () => new Date().toISOString().slice(0, 10)
const monthsAgo = (n: number) => {
  const d = new Date()
  d.setMonth(d.getMonth() - n, 1)
  return d.toISOString().slice(0, 10)
}

function download(name: string, mime: string, body: string) {
  const url = URL.createObjectURL(new Blob([body], { type: mime }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

const th = 'py-1.5 font-medium'
const Card = ({ title, children }: { title: string; children: React.ReactNode }) => (
  <section aria-label={title} className="rounded-lg border border-line p-4">
    <h2 className="mb-3 text-[14px] font-semibold">{title}</h2>
    {children}
  </section>
)

type Ageing = {
  total_minor: number
  buckets: { bucket: string; outstanding_minor: number; count: number }[]
  invoices: {
    id: string
    number: string
    contact: string | null
    age_days: number
    outstanding_minor: number
  }[]
}
type Summary = {
  rows: {
    period: string
    gst_rate_bp: number
    taxable_minor: number
    cgst_minor: number
    sgst_minor: number
    igst_minor: number
  }[]
  totals: { taxable_minor: number; cgst_minor: number; sgst_minor: number; igst_minor: number }
}
type Trial = {
  accounts: { code: string; name: string; debit_minor: number; credit_minor: number }[]
  total_debit_minor: number
  total_credit_minor: number
}

function AgeingCard() {
  const { ws } = useShell()
  const { data, error } = useCmd<Ageing>(ws, 'report.receivables_ageing')
  return (
    <Card title="Receivables ageing">
      <ErrorText error={error} />
      {data && (
        <>
          <ul className="grid grid-cols-4 gap-2">
            {data.buckets.map((b) => (
              <li key={b.bucket} className="rounded-md bg-side p-2">
                <p className="text-[12px] text-muted">{b.bucket} days</p>
                <p className="tabular-nums font-medium">{money(b.outstanding_minor)}</p>
                <p className="text-[12px] text-muted">
                  {b.count} invoice{b.count === 1 ? '' : 's'}
                </p>
              </li>
            ))}
          </ul>
          <p className="mt-2 text-[13px]" data-testid="ageing-total">
            Total outstanding: <b className="tabular-nums">{money(data.total_minor)}</b>
          </p>
          {data.invoices.length === 0 ? (
            <p className="mt-2 text-[13px] text-muted">Nothing outstanding.</p>
          ) : (
            <table className="mt-2 w-full text-left text-[13px]">
              <thead className="text-[12px] text-muted">
                <tr>
                  <th className={th}>Invoice</th>
                  <th className="font-medium">Customer</th>
                  <th className="text-right font-medium">Age (days)</th>
                  <th className="text-right font-medium">Outstanding</th>
                </tr>
              </thead>
              <tbody>
                {data.invoices.map((i) => (
                  <tr key={i.id} className="border-t border-line">
                    <td className="py-1.5">{i.number}</td>
                    <td>{i.contact}</td>
                    <td className="text-right">{i.age_days}</td>
                    <td className="text-right tabular-nums">{money(i.outstanding_minor)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </Card>
  )
}

function RangeInputs({ from, to, set }: { from: string; to: string; set: (f: string, t: string) => void }) {
  return (
    <div className="mb-3 flex items-center gap-2 text-[13px]">
      <label>
        <span className="sr-only">From</span>
        <input
          type="date"
          aria-label="From"
          value={from}
          onChange={(e) => set(e.target.value, to)}
          className={`${inputCls} w-auto`}
        />
      </label>
      to
      <input
        type="date"
        aria-label="To"
        value={to}
        onChange={(e) => set(from, e.target.value)}
        className={`${inputCls} w-auto`}
      />
    </div>
  )
}

function GstCard() {
  const { ws } = useShell()
  const [r, setR] = useState([monthsAgo(11), today()])
  const { data, error } = useCmd<Summary>(ws, 'report.gst_summary', { from: r[0], to: r[1] })
  return (
    <Card title="GST summary">
      <RangeInputs from={r[0]} to={r[1]} set={(f, t) => setR([f, t])} />
      <ErrorText error={error} />
      {data?.rows.length === 0 && <p className="text-[13px] text-muted">No issued invoices in this range.</p>}
      {!!data?.rows.length && (
        <table className="w-full text-left text-[13px]">
          <thead className="text-[12px] text-muted">
            <tr>
              <th className={th}>Period</th>
              <th className="font-medium">Rate</th>
              <th className="text-right font-medium">Taxable</th>
              <th className="text-right font-medium">CGST</th>
              <th className="text-right font-medium">SGST</th>
              <th className="text-right font-medium">IGST</th>
            </tr>
          </thead>
          <tbody>
            {data.rows.map((x) => (
              <tr key={`${x.period}${x.gst_rate_bp}`} className="border-t border-line tabular-nums">
                <td className="py-1.5">{x.period}</td>
                <td>{x.gst_rate_bp / 100}%</td>
                <td className="text-right">{money(x.taxable_minor)}</td>
                <td className="text-right">{money(x.cgst_minor)}</td>
                <td className="text-right">{money(x.sgst_minor)}</td>
                <td className="text-right">{money(x.igst_minor)}</td>
              </tr>
            ))}
            <tr className="border-t border-line font-medium tabular-nums">
              <td className="py-1.5" colSpan={2}>
                Total
              </td>
              <td className="text-right">{money(data.totals.taxable_minor)}</td>
              <td className="text-right">{money(data.totals.cgst_minor)}</td>
              <td className="text-right">{money(data.totals.sgst_minor)}</td>
              <td className="text-right">{money(data.totals.igst_minor)}</td>
            </tr>
          </tbody>
        </table>
      )}
    </Card>
  )
}

function TrialCard() {
  const { ws } = useShell()
  const { data, error } = useCmd<Trial>(ws, 'ledger.trial_balance')
  return (
    <Card title="Trial balance">
      <ErrorText error={error} />
      {data && (
        <table className="w-full text-left text-[13px]">
          <thead className="text-[12px] text-muted">
            <tr>
              <th className={th}>Account</th>
              <th className="text-right font-medium">Debit</th>
              <th className="text-right font-medium">Credit</th>
            </tr>
          </thead>
          <tbody className="tabular-nums">
            {data.accounts.map((a) => (
              <tr key={a.code} className="border-t border-line">
                <td className="py-1.5">
                  {a.code} {a.name}
                </td>
                <td className="text-right">{money(a.debit_minor)}</td>
                <td className="text-right">{money(a.credit_minor)}</td>
              </tr>
            ))}
            <tr className="border-t border-line font-medium">
              <td className="py-1.5">Total</td>
              <td className="text-right">{money(data.total_debit_minor)}</td>
              <td className="text-right">{money(data.total_credit_minor)}</td>
            </tr>
          </tbody>
        </table>
      )}
    </Card>
  )
}

function ExportCard() {
  const { ws } = useShell()
  const [period, setPeriod] = useState(today().slice(0, 7))
  const [r, setR] = useState([monthsAgo(0), today()])
  const [err, setErr] = useState<string>()
  const go = async (fn: () => Promise<void>) => {
    try {
      await fn()
      setErr(undefined)
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <Card title="Exports">
      <ErrorText error={err} />
      <div className="mb-4 flex items-center gap-2 text-[13px]">
        <label className="flex items-center gap-2">
          GSTR-1 month
          <input
            type="month"
            value={period}
            onChange={(e) => setPeriod(e.target.value)}
            className={`${inputCls} w-auto`}
          />
        </label>
        <button
          type="button"
          className={btn}
          onClick={() =>
            go(async () =>
              download(
                `GSTR1-${period}.json`,
                'application/json',
                JSON.stringify(await cmd(ws, 'report.gstr1', { period }), null, 2),
              ),
            )
          }
        >
          <Download size={14} /> Download GSTR-1 JSON
        </button>
      </div>
      <RangeInputs from={r[0]} to={r[1]} set={(f, t) => setR([f, t])} />
      <button
        type="button"
        className={btn}
        onClick={() =>
          go(async () => {
            const x = await cmd<{ xml: string }>(ws, 'export.tally', { from: r[0], to: r[1] })
            download(`tally-${r[0]}-${r[1]}.xml`, 'application/xml', x.xml)
          })
        }
      >
        <Download size={14} /> Download Tally XML
      </button>
    </Card>
  )
}

export function Reports() {
  return (
    <div className="space-y-4">
      <AgeingCard />
      <GstCard />
      <TrialCard />
      <ExportCard />
    </div>
  )
}
