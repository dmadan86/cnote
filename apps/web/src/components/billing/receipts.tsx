'use client'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { use, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { Confirm, run } from './bits'
import { money, parseMinor, stateName, toRupees } from './fmt'
import { ShareButton } from './share-panel'

export type Receipt = {
  id: string
  kind: 'receipt' | 'refund'
  number: string
  status: 'issued' | 'partly_applied' | 'applied' | 'refunded'
  contact_id: string
  contact_name?: string | null
  quotation_id: string | null
  original_receipt_id: string | null
  supply_type: 'services' | 'goods'
  supplier_gstin: string
  buyer_gstin: string | null
  place_of_supply: string | null
  description: string | null
  gst_rate_bp: number
  amount_minor: number
  taxable_minor: number
  cgst_minor: number
  sgst_minor: number
  igst_minor: number
  applied_minor: number
  unapplied_minor: number
  method: string
  reference: string | null
  received_at: string
  applications?: {
    id: string
    invoice_id: string
    invoice_number: string | null
    amount_minor: number
    applied_at: string
  }[]
  refunds?: { id: string; number: string; amount_minor: number; received_at: string }[]
}

const LABEL: Record<string, string> = {
  issued: 'Unapplied',
  partly_applied: 'Partly applied',
  applied: 'Applied',
  refunded: 'Refunded',
}
const TONE = { issued: 'warn', partly_applied: 'warn', applied: 'ok', refunded: 'neutral' } as const
const METHODS: [string, string][] = [
  ['upi', 'UPI'],
  ['bank', 'Bank transfer'],
  ['cheque', 'Cheque'],
  ['card', 'Card'],
  ['cash', 'Cash'],
]
// 40% is not offered: invoices accept rates up to 28% only, so a 40% voucher could not match its invoice.
const RATES = [0, 3, 5, 12, 18, 28]
const today = () => new Date(Date.now() + 330 * 60_000).toISOString().slice(0, 10)

export const ReceiptPill = ({ r }: { r: Pick<Receipt, 'status' | 'kind'> }) => (
  <Pill tone={r.kind === 'refund' ? 'neutral' : TONE[r.status]}>
    {r.kind === 'refund' ? 'Refund voucher' : LABEL[r.status]}
  </Pill>
)

/** Record an advance from a customer, optionally against an accepted quotation; opens the new voucher. */
export function RecordAdvanceButton({
  contactId,
  quotationId,
  contactName,
}: {
  contactId: string
  quotationId?: string
  contactName?: string | null
}) {
  const { ws } = useShell()
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const [f, setF] = useState({
    amount: '',
    supply: 'services',
    rate: '18',
    method: 'upi',
    ref: '',
    date: today(),
    desc: '',
  })
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF({ ...f, [k]: e.target.value })
  const gross = parseMinor(f.amount)
  const rateBp = Number(f.rate) * 100
  const tax = gross && f.supply === 'services' ? Math.round((gross * rateBp) / (10000 + rateBp)) : 0
  return (
    <>
      <button type="button" className={btn} onClick={() => setOpen(true)}>
        Record advance
      </button>
      {open && (
        <Confirm
          title="Record advance"
          action="Issue receipt voucher"
          body={
            <>
              Advance from <strong>{contactName ?? 'the customer'}</strong>
              {quotationId && ' against this quotation'}. A numbered receipt voucher is issued and the money
              posts to the ledger.
            </>
          }
          onClose={() => setOpen(false)}
          onDone={async () => {
            if (!gross || gross <= 0) throw new Error('Enter the amount received, like 50000 or 1250.50')
            const r = await run<Receipt & { approval_required?: boolean }>(ws, 'receipt.create', {
              contact_id: contactId,
              quotation_id: quotationId,
              amount_minor: gross,
              supply_type: f.supply,
              gst_rate_bp: f.supply === 'services' ? rateBp : undefined,
              method: f.method,
              reference: f.ref || undefined,
              received_at: f.date,
              description: f.desc || undefined,
            })
            router.push(`/w/${ws}/billing/receipts/${r.id}`)
          }}
        >
          <Field label="Amount received (INR, including GST)">
            {(id) => (
              <input
                id={id}
                value={f.amount}
                onChange={set('amount')}
                inputMode="decimal"
                className={inputCls}
              />
            )}
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Supply">
              {(id) => (
                <select id={id} value={f.supply} onChange={set('supply')} className={inputCls}>
                  <option value="services">Services</option>
                  <option value="goods">Goods (no GST on advance)</option>
                </select>
              )}
            </Field>
            {f.supply === 'services' && (
              <Field label="GST rate (%)">
                {(id) => (
                  <select id={id} value={f.rate} onChange={set('rate')} className={inputCls}>
                    {RATES.map((r) => (
                      <option key={r}>{r}</option>
                    ))}
                  </select>
                )}
              </Field>
            )}
          </div>
          <p role="status" className="rounded-md bg-hover p-2 text-[13px]">
            {gross
              ? f.supply === 'goods'
                ? `No GST on a goods advance. Taxable value ${money(gross)}.`
                : `Taxable value ${money(gross - tax)} + GST ${money(tax)} (CGST+SGST or IGST by place of supply).`
              : 'Enter an amount to see the GST included in it.'}
          </p>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Method">
              {(id) => (
                <select id={id} value={f.method} onChange={set('method')} className={inputCls}>
                  {METHODS.map(([k, l]) => (
                    <option key={k} value={k}>
                      {l}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Received on">
              {(id) => (
                <input
                  id={id}
                  type="date"
                  max={today()}
                  value={f.date}
                  onChange={set('date')}
                  className={inputCls}
                />
              )}
            </Field>
          </div>
          <Field label="Reference (UTR, cheque no.)">
            {(id) => <input id={id} value={f.ref} onChange={set('ref')} className={inputCls} />}
          </Field>
          <Field label="Description on the voucher (optional)">
            {(id) => (
              <input
                id={id}
                value={f.desc}
                onChange={set('desc')}
                placeholder="30% advance on order"
                className={inputCls}
              />
            )}
          </Field>
        </Confirm>
      )}
    </>
  )
}

/** Customer page: unapplied advance balance and the vouchers. */
export function AdvancesCard({ contactId, contactName }: { contactId: string; contactName: string | null }) {
  const { ws } = useShell()
  const list = useCmd<Receipt[]>(ws, 'receipt.list', { contact_id: contactId, kind: 'receipt' })
  if (!list.data) return null
  const open = list.data.reduce((n, r) => n + r.unapplied_minor, 0)
  return (
    <section aria-label="Advances" className="rounded-lg border border-line p-4">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <h2 className="text-[14px] font-semibold">Advances</h2>
        <span data-testid="advance-balance" className="text-[13px] text-muted">
          Unapplied {money(open)}
        </span>
        <span className="ml-auto">
          <RecordAdvanceButton contactId={contactId} contactName={contactName} />
        </span>
      </div>
      {list.data.length === 0 && <p className="text-[13px] text-muted">No advances yet.</p>}
      <ul className="divide-y divide-line">
        {list.data.map((r) => (
          <li key={r.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-1.5 text-[14px]">
            <Link href={`/w/${ws}/billing/receipts/${r.id}`} className="font-medium hover:underline">
              {r.number}
            </Link>
            <ReceiptPill r={r} />
            <span className="ml-auto tabular-nums">{money(r.amount_minor)}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** Invoice page: apply one of the customer's unapplied advances. */
export function ApplyAdvancePanel({
  invoiceId,
  contactId,
  outstanding,
  reload,
}: {
  invoiceId: string
  contactId: string
  outstanding: number
  reload: () => void
}) {
  const { ws } = useShell()
  const list = useCmd<Receipt[]>(ws, 'receipt.list', { contact_id: contactId, status: 'open' })
  const [pick, setPick] = useState<Receipt>()
  const [amount, setAmount] = useState('')
  if (!list.data || list.data.length === 0 || outstanding <= 0) return null
  const amt = parseMinor(amount)
  return (
    <section aria-label="Advances" className="rounded-lg border border-line p-4">
      <h2 className="mb-2 text-[14px] font-semibold">Advances available</h2>
      <ul className="divide-y divide-line">
        {list.data.map((r) => (
          <li key={r.id} className="flex flex-wrap items-center gap-2 py-1.5 text-[13px]">
            <span>
              {r.number} <span className="text-muted">· {money(r.unapplied_minor)} left</span>
            </span>
            <button
              type="button"
              className={`${btn} ml-auto`}
              onClick={() => {
                setPick(r)
                setAmount(toRupees(Math.min(r.unapplied_minor, outstanding)))
              }}
            >
              Apply
            </button>
          </li>
        ))}
      </ul>
      {pick && (
        <Confirm
          title={`Apply advance ${pick.number}`}
          action="Apply advance"
          body={
            <>
              Available {money(pick.unapplied_minor)}. Invoice outstanding {money(outstanding)}
              {amt ? `; after this ${money(Math.max(outstanding - amt, 0))}.` : '.'}
            </>
          }
          onClose={() => setPick(undefined)}
          onDone={async () => {
            const max = Math.min(pick.unapplied_minor, outstanding)
            if (!amt || amt <= 0 || amt > max) throw new Error(`Enter ${money(max)} or less`)
            await run(ws, 'receipt.apply', { id: pick.id, invoice_id: invoiceId, amount_minor: amt })
            list.reload()
            reload()
          }}
        >
          <Field label="Amount to apply (INR)">
            {(id) => (
              <input
                id={id}
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                inputMode="decimal"
                className={inputCls}
              />
            )}
          </Field>
        </Confirm>
      )}
    </section>
  )
}

const PRINT_CSS = `@media print {
  body * { visibility: hidden; }
  #receipt-print, #receipt-print * { visibility: visible; }
  #receipt-print { position: absolute; inset: 0; padding: 24px; background: white; color: black; }
  .no-print { display: none !important; }
}`

export function ReceiptView({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws } = useShell()
  const { data: r, error, reload } = useCmd<Receipt>(ws, 'receipt.get', { id })
  const settings = useCmd<{ name: string; settings: Record<string, unknown> }>(ws, 'workspace.get')
  const [refunding, setRefunding] = useState(false)
  const [amount, setAmount] = useState('')
  if (error) return <ErrorText error={error} />
  if (!r) return <p className="text-muted">Loading...</p>
  const seller = String(settings.data?.settings.legal_name ?? settings.data?.name ?? '')
  const intra = r.igst_minor === 0
  const title = r.kind === 'refund' ? 'Refund voucher' : 'Receipt voucher'
  return (
    <div className="space-y-4">
      <style>{PRINT_CSS}</style>
      <div className="no-print flex flex-wrap items-center gap-3">
        <h2 className="text-lg font-semibold">
          {title} {r.number}
        </h2>
        <ReceiptPill r={r} />
        <div className="ml-auto flex flex-wrap gap-2">
          <button type="button" className={btnPrimary} onClick={() => window.print()}>
            Print / PDF
          </button>
          {r.kind === 'receipt' && <ShareButton noteId={r.id} kind="receipt" />}
          {r.kind === 'receipt' && r.unapplied_minor > 0 && (
            <button
              type="button"
              className={btn}
              onClick={() => {
                setAmount(toRupees(r.unapplied_minor))
                setRefunding(true)
              }}
            >
              Refund unapplied
            </button>
          )}
        </div>
      </div>
      <article id="receipt-print" className="space-y-5 rounded-lg border border-line p-4 sm:p-6">
        <header className="grid gap-4 text-[13px] sm:grid-cols-2">
          <div>
            <p className="text-[12px] text-muted">Supplier</p>
            <p className="font-medium">{seller}</p>
            <p className="font-mono text-[12px]">GSTIN {r.supplier_gstin}</p>
          </div>
          <div className="sm:text-right">
            <p className="text-[12px] text-muted">{r.kind === 'refund' ? 'Refunded to' : 'Received from'}</p>
            <p className="font-medium">{r.contact_name}</p>
            {r.buyer_gstin && <p className="font-mono text-[12px]">GSTIN {r.buyer_gstin}</p>}
            <p className="text-muted">
              Place of supply:{' '}
              {r.place_of_supply
                ? `${r.place_of_supply} ${stateName(r.place_of_supply)}`
                : 'not known (taxed as inter-state)'}
            </p>
            <p>Date: {r.received_at}</p>
            <p>Reverse charge: No</p>
          </div>
        </header>
        <p className="text-[14px]">
          {r.description ?? (r.kind === 'refund' ? 'Refund of advance' : 'Advance received')}
          {r.reference && <span className="text-muted"> · Ref {r.reference}</span>}
        </p>
        <dl className="ml-auto max-w-xs space-y-1 text-[14px]">
          <Row k="Taxable value" v={money(r.taxable_minor)} />
          {r.supply_type === 'services' &&
            (intra ? (
              <>
                <Row k={`CGST ${r.gst_rate_bp / 200}%`} v={money(r.cgst_minor)} />
                <Row k={`SGST ${r.gst_rate_bp / 200}%`} v={money(r.sgst_minor)} />
              </>
            ) : (
              <Row k={`IGST ${r.gst_rate_bp / 100}%`} v={money(r.igst_minor)} />
            ))}
          {r.supply_type === 'goods' && <Row k="GST" v="None on a goods advance" />}
          <div className="flex justify-between border-t border-line pt-1 font-semibold">
            <dt>{r.kind === 'refund' ? 'Amount refunded' : 'Amount received'}</dt>
            <dd data-testid="receipt-amount" className="tabular-nums">
              {money(r.amount_minor)}
            </dd>
          </div>
        </dl>
        <p className="text-[12px] text-muted">
          {r.kind === 'refund'
            ? 'Refund voucher under Rule 51 of the CGST Rules, 2017.'
            : 'Receipt voucher under Rule 50 of the CGST Rules, 2017. GST on the advance is included in the amount.'}
        </p>
      </article>
      {r.kind === 'receipt' && (
        <section aria-label="Use of this advance" className="no-print rounded-lg border border-line p-4">
          <h3 className="mb-2 text-[14px] font-semibold">
            Used <span data-testid="unapplied">· {money(r.unapplied_minor)} still unapplied</span>
          </h3>
          {!r.applications?.length && !r.refunds?.length && (
            <p className="text-[13px] text-muted">
              Not applied to an invoice yet. Open an invoice of this customer and choose Apply.
            </p>
          )}
          <ul className="divide-y divide-line">
            {r.applications?.map((a) => (
              <li key={a.id} className="flex flex-wrap justify-between gap-2 py-1.5 text-[13px]">
                <Link href={`/w/${ws}/billing/${a.invoice_id}`} className="hover:underline">
                  Applied to {a.invoice_number}
                </Link>
                <span className="tabular-nums">{money(a.amount_minor)}</span>
              </li>
            ))}
            {r.refunds?.map((f) => (
              <li key={f.id} className="flex flex-wrap justify-between gap-2 py-1.5 text-[13px]">
                <Link href={`/w/${ws}/billing/receipts/${f.id}`} className="hover:underline">
                  Refunded {f.number}
                </Link>
                <span className="tabular-nums">{money(f.amount_minor)}</span>
              </li>
            ))}
          </ul>
          {r.quotation_id && (
            <Link
              href={`/w/${ws}/billing/quotes/${r.quotation_id}`}
              className="mt-2 inline-block text-[13px] text-accent hover:underline"
            >
              Against quotation
            </Link>
          )}
        </section>
      )}
      {r.kind === 'refund' && r.original_receipt_id && (
        <Link
          href={`/w/${ws}/billing/receipts/${r.original_receipt_id}`}
          className="text-[13px] text-accent hover:underline"
        >
          Original receipt voucher
        </Link>
      )}
      {refunding && (
        <Confirm
          title="Refund advance"
          action="Issue refund voucher"
          body={`Refunds only the unapplied part (${money(r.unapplied_minor)} at most), posts to the ledger and issues a refund voucher.`}
          onClose={() => setRefunding(false)}
          onDone={async () => {
            const v = parseMinor(amount)
            if (!v || v <= 0 || v > r.unapplied_minor)
              throw new Error(`Enter ${money(r.unapplied_minor)} or less`)
            await run(ws, 'receipt.refund', { id: r.id, amount_minor: v })
            reload()
          }}
        >
          <Field label="Amount to refund (INR)">
            {(fid) => (
              <input
                id={fid}
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                inputMode="decimal"
                className={inputCls}
              />
            )}
          </Field>
        </Confirm>
      )}
    </div>
  )
}

const Row = ({ k, v }: { k: string; v: string }) => (
  <div className="flex justify-between gap-4">
    <dt className="text-muted">{k}</dt>
    <dd className="tabular-nums">{v}</dd>
  </div>
)
