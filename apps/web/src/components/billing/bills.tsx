'use client'
import { Plus, Trash2 } from 'lucide-react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { use, useState } from 'react'
import { ApprovalBanner } from '@/components/approvals/banner'
import { NeedSpace } from '@/components/crm/contacts'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { Confirm, run } from './bits'
import { Totals } from './editor'
import { FilterBanner } from './filter'
import { type Line, money, parseMinor, toRupees } from './fmt'

type Vendor = { id: string; name: string; outstanding_minor: number }
type Bill = {
  id: string
  vendor_id: string
  vendor_bill_number: string
  bill_date: string
  due_date: string
  status: string
  subtotal_minor: number
  cgst_minor: number
  sgst_minor: number
  igst_minor: number
  total_minor: number
  paid_minor: number
  outstanding_minor: number
  itc_at_risk: boolean
}
type BillDetail = Bill & {
  vendor_name: string | null
  lines: Line[]
  payments: {
    id: string
    amount_minor: number
    method: string
    reference: string | null
    tds_minor: number
  }[]
}

const LABEL: Record<string, string> = {
  draft: 'Draft',
  recorded: 'Recorded',
  partly_paid: 'Partly paid',
  paid: 'Paid',
  cancelled: 'Cancelled',
}
const TONE: Record<string, 'ok' | 'warn' | 'bad' | 'neutral'> = {
  paid: 'ok',
  recorded: 'warn',
  partly_paid: 'warn',
  cancelled: 'bad',
}
const BillPill = ({ status }: { status: string }) => (
  <Pill tone={TONE[status]}>{LABEL[status] ?? status}</Pill>
)
const ItcRisk = () => (
  <span title="Unpaid for over 150 days. Input tax credit is lost if the bill stays unpaid 180 days after its date.">
    <Pill tone="bad">ITC at risk</Pill>
  </span>
)

function NewBill({ onClose }: { onClose: () => void }) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const vendors = useCmd<Vendor[]>(ws, 'vendor.list', {})
  const [err, setErr] = useState<string>()
  return (
    <Modal open title="New bill" onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          try {
            const d = await run<{ id: string }>(ws, 'bill.create_draft', {
              space_id: spaces[0].id,
              vendor_id: String(f.get('vendor')),
              vendor_bill_number: String(f.get('number')),
              lines: [{ description: 'Purchase', unit_price_minor: 0, gst_rate_bp: 1800 }],
            })
            router.push(`/w/${ws}/billing/bills/${d.id}`)
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <Field label="Vendor">
          {(id) => (
            <select id={id} name="vendor" required className={inputCls}>
              <option value="">Choose a vendor</option>
              {vendors.data?.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        {vendors.data?.length === 0 && (
          <p className="text-[12px] text-muted">
            No vendors yet. Edit a contact in CRM and tick &quot;This contact is a vendor&quot;.
          </p>
        )}
        <Field label="Vendor bill number">
          {(id) => <input id={id} name="number" required className={inputCls} />}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Create draft
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function BillList() {
  const { ws, spaces } = useShell()
  const [adding, setAdding] = useState(false)
  const [status, setStatus] = useState('')
  const { data: all, error } = useCmd<Bill[]>(ws, 'bill.list', { limit: 500 })
  // home tile: ?due=soon is unpaid bills due within 7 days or overdue
  const soon = useSearchParams().get('due') === 'soon'
  const today = new Date().toISOString().slice(0, 10)
  const limit = new Date(Date.now() + 7 * 86400_000).toISOString().slice(0, 10)
  const data = all?.filter(
    (b) =>
      (!status || b.status === status) &&
      (!soon || (['recorded', 'partly_paid'].includes(b.status) && b.due_date <= limit)),
  )
  const vendors = useCmd<Vendor[]>(ws, 'vendor.list', {})
  const names = new Map(vendors.data?.map((v) => [v.id, v.name]))
  return (
    <div>
      {soon && <FilterBanner label="bills due within 7 days or overdue" href={`/w/${ws}/billing/bills`} />}
      <div className="mb-3 flex gap-2">
        <select
          aria-label="Filter by status"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          className={`${inputCls} !w-auto`}
        >
          <option value="">All statuses</option>
          {Object.entries(LABEL).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </select>
        <button
          type="button"
          className={`${btnPrimary} ml-auto`}
          disabled={!spaces.length}
          onClick={() => setAdding(true)}
        >
          <Plus size={14} /> New bill
        </button>
      </div>
      <ErrorText error={error} />
      {!spaces.length && <NeedSpace />}
      {data?.length === 0 && spaces.length > 0 && (
        <p className="py-8 text-center text-muted">No bills yet.</p>
      )}
      {!!data?.length && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-left text-[14px]">
            <thead className="text-[12px] text-muted">
              <tr className="border-b border-line">
                <th className="py-2 font-medium">Vendor bill</th>
                <th className="font-medium">Vendor</th>
                <th className="font-medium">Due</th>
                <th className="font-medium">Status</th>
                <th className="text-right font-medium">Total</th>
                <th className="text-right font-medium">Outstanding</th>
              </tr>
            </thead>
            <tbody>
              {data.map((b) => (
                <tr key={b.id} className="border-b border-line hover:bg-hover">
                  <td className="py-2 font-medium">
                    <Link href={`/w/${ws}/billing/bills/${b.id}`} className="hover:underline">
                      {b.vendor_bill_number}
                    </Link>
                  </td>
                  <td>{names.get(b.vendor_id)}</td>
                  <td className="text-muted">
                    {b.due_date}
                    {b.outstanding_minor > 0 && b.due_date < today && (
                      <span className="ml-1 text-bad">Overdue</span>
                    )}
                  </td>
                  <td>
                    <BillPill status={b.status} /> {b.itc_at_risk && <ItcRisk />}
                  </td>
                  <td className="text-right tabular-nums">{money(b.total_minor)}</td>
                  <td
                    className={`text-right tabular-nums ${b.outstanding_minor ? 'font-medium' : 'text-muted'}`}
                  >
                    {money(b.outstanding_minor)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {adding && <NewBill onClose={() => setAdding(false)} />}
    </div>
  )
}

type Row = { k: number; description: string; hsn: string; qty: string; price: string; rate: number }
let seq = 0
const RATES = [0, 5, 12, 18, 28]

function DraftForm({ b, reload }: { b: BillDetail; reload: () => void }) {
  const { ws } = useShell()
  const vendors = useCmd<Vendor[]>(ws, 'vendor.list', {})
  const [rows, setRows] = useState<Row[]>(() =>
    b.lines.map((l) => ({
      k: ++seq,
      description: l.description,
      hsn: l.hsn_sac ?? '',
      qty: String(Number(l.quantity)),
      price: toRupees(l.unit_price_minor),
      rate: l.gst_rate_bp / 100,
    })),
  )
  const [vendorId, setVendorId] = useState(b.vendor_id)
  const [number, setNumber] = useState(b.vendor_bill_number)
  const [billDate, setBillDate] = useState(b.bill_date)
  const [due, setDue] = useState(b.due_date)
  const [err, setErr] = useState<string>()
  const [saved, setSaved] = useState(false)
  const [recording, setRecording] = useState(false)
  const dirty =
    <T,>(set: (v: T) => void) =>
    (v: T) => {
      set(v)
      setSaved(false)
    }
  const patch = (k: number, p: Partial<Row>) => {
    setRows((rs) => rs.map((r) => (r.k === k ? { ...r, ...p } : r)))
    setSaved(false)
  }
  const save = async () => {
    const lines = rows.map((r) => ({
      description: r.description,
      hsn_sac: r.hsn || undefined,
      quantity: Number(r.qty),
      unit_price_minor: parseMinor(r.price || '0'),
      gst_rate_bp: r.rate * 100,
    }))
    if (lines.some((l) => !l.description.trim() || !(l.quantity > 0) || l.unit_price_minor === null))
      throw new Error('Complete every line (description, quantity, price).')
    await run(ws, 'bill.update_draft', {
      id: b.id,
      vendor_id: vendorId,
      vendor_bill_number: number,
      bill_date: billDate,
      due_date: due,
      lines,
    })
    setSaved(true)
  }
  const cell = `${inputCls} !px-2 !py-1 !text-[13px]`
  const saveThen = (then: () => void) =>
    save().then(
      () => {
        setErr(undefined)
        then()
      },
      (x) => setErr((x as Error).message),
    )
  return (
    <div className="space-y-4">
      <ErrorText error={err} />
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Vendor</span>
          <select value={vendorId} onChange={(e) => dirty(setVendorId)(e.target.value)} className={inputCls}>
            {vendors.data?.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name}
              </option>
            ))}
          </select>
        </label>
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Vendor bill number</span>
          <input value={number} onChange={(e) => dirty(setNumber)(e.target.value)} className={inputCls} />
        </label>
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Bill date</span>
          <input
            type="date"
            value={billDate}
            onChange={(e) => dirty(setBillDate)(e.target.value)}
            className={inputCls}
          />
        </label>
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Due date</span>
          <input
            type="date"
            value={due}
            onChange={(e) => dirty(setDue)(e.target.value)}
            className={inputCls}
          />
        </label>
      </div>
      <div className="overflow-x-auto rounded-lg border border-line">
        <table className="w-full min-w-[640px] text-left text-[13px]">
          <thead className="border-b border-line text-[12px] text-muted">
            <tr>
              <th className="p-2 font-medium">Description</th>
              <th className="font-medium">HSN/SAC</th>
              <th className="w-16 font-medium">Qty</th>
              <th className="w-28 font-medium">Unit price</th>
              <th className="w-20 font-medium">GST %</th>
              <th className="w-10" />
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={r.k} className="border-b border-line last:border-0">
                <td className="p-2">
                  <input
                    aria-label={`Line ${i + 1} description`}
                    value={r.description}
                    onChange={(e) => patch(r.k, { description: e.target.value })}
                    className={cell}
                  />
                </td>
                <td>
                  <input
                    aria-label={`Line ${i + 1} HSN/SAC`}
                    value={r.hsn}
                    inputMode="numeric"
                    onChange={(e) => patch(r.k, { hsn: e.target.value })}
                    className={`${cell} w-24`}
                  />
                </td>
                <td>
                  <input
                    aria-label={`Line ${i + 1} quantity`}
                    value={r.qty}
                    inputMode="decimal"
                    onChange={(e) => patch(r.k, { qty: e.target.value })}
                    className={cell}
                  />
                </td>
                <td>
                  <input
                    aria-label={`Line ${i + 1} unit price`}
                    value={r.price}
                    inputMode="decimal"
                    onChange={(e) => patch(r.k, { price: e.target.value })}
                    className={cell}
                  />
                </td>
                <td>
                  <select
                    aria-label={`Line ${i + 1} GST rate`}
                    value={r.rate}
                    onChange={(e) => patch(r.k, { rate: Number(e.target.value) })}
                    className={cell}
                  >
                    {RATES.map((x) => (
                      <option key={x} value={x}>
                        {x}%
                      </option>
                    ))}
                  </select>
                </td>
                <td>
                  <button
                    type="button"
                    aria-label={`Remove line ${i + 1}`}
                    disabled={rows.length === 1}
                    onClick={() => {
                      setRows((rs) => rs.filter((x) => x.k !== r.k))
                      setSaved(false)
                    }}
                    className="rounded p-1 hover:bg-hover disabled:opacity-30"
                  >
                    <Trash2 size={14} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-right text-[13px] text-muted">
        Taxable subtotal{' '}
        {money(rows.reduce((t, r) => t + Math.round(Number(r.qty) * (parseMinor(r.price || '0') ?? 0)), 0))}.
        GST is fixed when the bill is recorded.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          className={btn}
          onClick={() => {
            setRows((rs) => [...rs, { k: ++seq, description: '', hsn: '', qty: '1', price: '', rate: 18 }])
            setSaved(false)
          }}
        >
          <Plus size={14} /> Add line
        </button>
        <span role="status" className="text-[13px] text-muted">
          {saved ? 'Draft saved' : ''}
        </span>
        <button type="button" className={`${btn} ml-auto`} onClick={() => saveThen(reload)}>
          Save draft
        </button>
        <button type="button" className={btnPrimary} onClick={() => saveThen(() => setRecording(true))}>
          Record bill
        </button>
      </div>
      {recording && (
        <Confirm
          title="Record bill"
          action="Record bill"
          onClose={() => setRecording(false)}
          onDone={async () => {
            await run(ws, 'bill.record', { id: b.id })
            reload()
          }}
          body="Recording fixes the GST, posts the expense, input tax credit and amount payable to the ledger."
        />
      )}
    </div>
  )
}

type Tds = {
  tds_code: string
  label?: string
  rate_bp: number
  base_minor: number
  on_minor: number
  tds_minor: number
  net_minor: number
  reason: string
  warnings: string[]
  no_pan: boolean
  applicable: boolean
}

function PayDialog({ b, onClose, onDone }: { b: BillDetail; onClose: () => void; onDone: () => void }) {
  const { ws } = useShell()
  const [amount, setAmount] = useState(toRupees(b.outstanding_minor))
  const [override, setOverride] = useState<string>()
  const [pan, setPan] = useState('')
  const [panErr, setPanErr] = useState<string>()
  const minor = parseMinor(amount)
  const preview = useCmd<Tds>(
    ws,
    'bill.tds_preview',
    { id: b.id, amount_minor: minor ?? 1 },
    !!minor && minor > 0,
  )
  const t = preview.data?.applicable ? preview.data : undefined
  const overrideMinor = override === undefined ? undefined : parseMinor(override)
  const withheld = overrideMinor ?? t?.tds_minor ?? 0
  return (
    <Confirm
      title="Record payment"
      action="Record payment"
      onClose={onClose}
      onDone={async () => {
        if (!minor || minor <= 0) throw new Error('Enter an amount.')
        if (override !== undefined && overrideMinor === null)
          throw new Error('Enter the TDS amount in rupees.')
        await run(ws, 'bill.pay', {
          id: b.id,
          amount_minor: minor,
          ...(t ? { tds_code: t.tds_code } : {}),
          ...(t && overrideMinor !== undefined ? { tds_minor: overrideMinor } : {}),
        })
        onDone()
      }}
      body={`Record a payment already made for bill ${b.vendor_bill_number}${b.vendor_name ? ` from ${b.vendor_name}` : ''}. Outstanding ${money(b.outstanding_minor)}. It posts to the ledger: payable down, bank down.`}
    >
      <label className="block space-y-1">
        <span className="text-[12px] font-medium text-muted">Amount (INR)</span>
        <input
          value={amount}
          inputMode="decimal"
          onChange={(e) => {
            setAmount(e.target.value)
            setOverride(undefined)
          }}
          className={inputCls}
        />
      </label>
      {t && (
        <section aria-label="TDS" className="space-y-2 rounded-md border border-line p-3 text-[13px]">
          <p className="font-medium">TDS: {t.label ?? t.tds_code}</p>
          <p className="text-muted">{t.reason}</p>
          <dl className="grid grid-cols-[1fr_auto] gap-y-1 tabular-nums">
            <dt>Value without GST x {t.rate_bp / 100}%</dt>
            <dd>{money(t.on_minor)}</dd>
            <dt>TDS withheld</dt>
            <dd data-testid="tds-withheld">{money(withheld)}</dd>
            <dt className="font-medium">Cash to pay the vendor</dt>
            <dd className="font-medium" data-testid="tds-net">
              {money((minor ?? 0) - withheld)}
            </dd>
          </dl>
          {t.warnings.map((w) => (
            <p key={w} role="status" className="rounded bg-warn-bg p-2 text-warn">
              {w}
            </p>
          ))}
          {t.no_pan && (
            <div className="flex flex-wrap items-end gap-2">
              <label className="block space-y-1">
                <span className="text-[12px] font-medium text-muted">Vendor PAN</span>
                <input
                  value={pan}
                  onChange={(e) => setPan(e.target.value)}
                  className={`${inputCls} w-40 uppercase`}
                />
              </label>
              <button
                type="button"
                className={btn}
                onClick={async () => {
                  try {
                    await run(ws, 'vendor.set_tax', { id: b.vendor_id, pan })
                    setPanErr(undefined)
                    preview.reload()
                  } catch (x) {
                    setPanErr((x as Error).message)
                  }
                }}
              >
                Save PAN
              </button>
              <ErrorText error={panErr} />
            </div>
          )}
          {override === undefined ? (
            <button type="button" className="underline" onClick={() => setOverride(toRupees(t.tds_minor))}>
              Change the TDS amount
            </button>
          ) : (
            <label className="block space-y-1">
              <span className="text-[12px] font-medium text-muted">TDS to withhold (INR)</span>
              <input
                value={override}
                inputMode="decimal"
                onChange={(e) => setOverride(e.target.value)}
                className={inputCls}
              />
            </label>
          )}
        </section>
      )}
      {!t && minor !== null && preview.data && (
        <p className="text-[12px] text-muted">
          No TDS on this payment. Give the vendor a default TDS code (Vendors) to have it worked out for you.
        </p>
      )}
    </Confirm>
  )
}

export function BillView({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws } = useShell()
  const { data: b, error, reload } = useCmd<BillDetail>(ws, 'bill.get', { id })
  const [err, setErr] = useState<string>()
  const [dialog, setDialog] = useState<'pay' | 'cancel'>()
  if (error) return <ErrorText error={error} />
  if (!b) return <p className="text-muted">Loading...</p>
  const tax = {
    intra_state: b.igst_minor === 0,
    reverse_charge: false,
    cgst_minor: b.cgst_minor,
    sgst_minor: b.sgst_minor,
    igst_minor: b.igst_minor,
    total_minor: b.total_minor,
    subtotal_minor: b.subtotal_minor,
  }
  const open = b.status === 'recorded' || b.status === 'partly_paid'
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <h2 className="text-lg font-semibold">
          Bill {b.vendor_bill_number}
          {b.vendor_name ? ` from ${b.vendor_name}` : ''}
        </h2>
        <BillPill status={b.status} />
        {b.itc_at_risk && <ItcRisk />}
      </div>
      <ErrorText error={err} />
      <ApprovalBanner ws={ws} noteId={b.id} />
      {b.status === 'draft' ? (
        <>
          <DraftForm key={b.id} b={b} reload={reload} />
          <button type="button" className={btn} onClick={() => setDialog('cancel')}>
            Cancel bill
          </button>
        </>
      ) : (
        <>
          <table className="w-full text-left text-[14px]">
            <thead className="text-[12px] text-muted">
              <tr className="border-b border-line">
                <th className="py-2 font-medium">Description</th>
                <th className="font-medium">Qty</th>
                <th className="text-right font-medium">Unit price</th>
                <th className="text-right font-medium">Taxable</th>
              </tr>
            </thead>
            <tbody>
              {b.lines.map((l) => (
                <tr key={l.id} className="border-b border-line">
                  <td className="py-2">{l.description}</td>
                  <td>{Number(l.quantity)}</td>
                  <td className="text-right tabular-nums">{money(l.unit_price_minor)}</td>
                  <td className="text-right tabular-nums">{money(l.taxable_minor)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="ml-auto max-w-xs rounded-lg border border-line p-4">
            <Totals subtotal={b.subtotal_minor} tax={tax} />
            <p className="mt-2 flex justify-between text-[13px]">
              <span className="text-muted">Paid</span>
              <span className="tabular-nums">{money(b.paid_minor)}</span>
            </p>
            <p className="flex justify-between text-[13px]">
              <span className="text-muted">Outstanding</span>
              <span className="tabular-nums">{money(b.outstanding_minor)}</span>
            </p>
          </div>
          <p className="text-[13px] text-muted">
            Dated {b.bill_date}, due {b.due_date}
          </p>
          {!!b.payments.length && (
            <ul aria-label="Payments" className="text-[13px]">
              {b.payments.map((p) => (
                <li key={p.id}>
                  Paid {money(p.amount_minor)} by {p.method}
                  {p.reference ? ` (${p.reference})` : ''}
                  {p.tds_minor > 0 ? `, of which ${money(p.tds_minor)} withheld as TDS` : ''}
                </li>
              ))}
            </ul>
          )}
          {open && (
            <div className="flex flex-wrap gap-2">
              <button type="button" className={btnPrimary} onClick={() => setDialog('pay')}>
                Record payment
              </button>
              {b.paid_minor === 0 && (
                <button type="button" className={btn} onClick={() => setDialog('cancel')}>
                  Cancel bill
                </button>
              )}
            </div>
          )}
        </>
      )}
      {dialog === 'pay' && (
        <PayDialog
          b={b}
          onClose={() => setDialog(undefined)}
          onDone={() => {
            setErr(undefined)
            reload()
          }}
        />
      )}
      {dialog === 'cancel' && (
        <Confirm
          title="Cancel bill"
          action="Cancel bill"
          onClose={() => setDialog(undefined)}
          onDone={async () => {
            await run(ws, 'bill.cancel', { id: b.id })
            reload()
          }}
          body={
            b.status === 'draft'
              ? 'The draft will be cancelled.'
              : 'The bill is cancelled and its ledger entry is reversed.'
          }
        />
      )}
    </div>
  )
}
