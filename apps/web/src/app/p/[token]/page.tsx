'use client'
import { use, useEffect, useRef, useState } from 'react'
import { money } from '@/components/billing/fmt'
import { QrSvg } from '@/components/share/qr-svg'
import { api, type CmdFailure } from '@/lib/api'

// Public, logged-out page for a shared quotation or invoice. It fetches once on the client (link-preview bots that
// only read HTML do not count as views). Sized for a phone first; the print stylesheet keeps only the document.

type Line = {
  description: string
  hsn_sac: string | null
  quantity: string
  unit_price_minor: number
  gst_rate_bp: number
  taxable_minor: number
}
type Doc = {
  kind: 'quotation' | 'invoice'
  number: string | null
  status: string
  currency: string
  date: string | null
  valid_until?: string
  due_date?: string
  place_of_supply: string | null
  reverse_charge: boolean
  seller: { name: string; gstin: string | null }
  buyer: { name: string; gstin: string | null }
  lines: Line[]
  subtotal_minor: number
  cgst_minor: number
  sgst_minor: number
  igst_minor: number
  total_minor: number
  can_decide?: boolean
  decision?: { status: string; by_name: string | null; at: string | null }
  paid_minor?: number
  amount_due_minor?: number
  irn?: string
  signed_qr?: string
  payment?: { upi_uri?: string; upi_payee?: string; bank_details?: string }
}

const PRINT = `@media print {
  .no-print { display: none !important; }
  body { background: #fff !important; color: #000 !important; }
  .doc { border: 0 !important; padding: 0 !important; }
  tr, .keep { break-inside: avoid; }
  thead { display: table-header-group; }
}`
const big =
  'inline-flex min-h-11 items-center justify-center rounded-md px-4 text-[15px] font-medium disabled:opacity-50'
const primary = `${big} bg-accent text-accent-fg hover:opacity-90`
const secondary = `${big} border border-line hover:bg-hover`
const field = 'min-h-11 w-full rounded-md border border-line bg-canvas px-3 text-[16px]'

const STATUS: Record<string, string> = {
  sent: 'Awaiting your response',
  accepted: 'Accepted',
  rejected: 'Declined',
  expired: 'Expired',
  converted: 'Accepted',
  issued: 'Issued',
  irn_generated: 'Issued',
  partly_paid: 'Partly paid',
  paid: 'Paid',
  cancelled: 'Cancelled',
}

export default function PublicDocument({ params }: { params: Promise<{ token: string }> }) {
  const { token } = use(params)
  const [doc, setDoc] = useState<Doc>()
  const [gone, setGone] = useState<string>()
  const started = useRef(false)
  useEffect(() => {
    if (started.current) return // StrictMode double-effect would count a second view
    started.current = true
    api<Doc>(`/api/public/${token}`).then(setDoc, (e: CmdFailure) =>
      setGone(
        e.status === 429
          ? 'Too many requests. Please wait a moment and reload.'
          : 'This link is not available. It may have expired or been withdrawn. Please ask the sender for a new one.',
      ),
    )
  }, [token])
  return (
    <main className="mx-auto max-w-3xl px-4 py-6 sm:py-10">
      <style>{PRINT}</style>
      {gone && (
        <div role="alert" className="rounded-lg border border-line p-6 text-center">
          <h1 className="mb-2 text-lg font-semibold">Link unavailable</h1>
          <p className="text-muted">{gone}</p>
        </div>
      )}
      {!doc && !gone && <p className="text-muted">Loading...</p>}
      {doc && <Document doc={doc} token={token} onChange={setDoc} />}
    </main>
  )
}

function Document({ doc, token, onChange }: { doc: Doc; token: string; onChange: (d: Doc) => void }) {
  const isInvoice = doc.kind === 'invoice'
  const intra = doc.igst_minor === 0
  const [step, setStep] = useState<'accept' | 'decline'>()
  const due = doc.amount_due_minor ?? 0
  const label = isInvoice ? 'Tax invoice' : 'Quotation'
  const cur = (n: number) => (doc.currency === 'INR' ? money(n) : `${doc.currency} ${(n / 100).toFixed(2)}`)
  return (
    <div className="space-y-5">
      <header className="space-y-1">
        <p className="text-[13px] text-muted">{doc.seller.name}</p>
        <h1 className="text-xl font-semibold">
          {label} {doc.number}
        </h1>
        <p className="flex flex-wrap items-center gap-2 text-[13px]">
          <span data-testid="doc-status" className="rounded-full bg-hover px-2 py-0.5 font-medium">
            {STATUS[doc.status] ?? doc.status}
          </span>
          {doc.date && <span className="text-muted">Date {doc.date}</span>}
          {doc.due_date && <span className="text-muted">Due {doc.due_date}</span>}
          {doc.valid_until && <span className="text-muted">Valid until {doc.valid_until}</span>}
        </p>
      </header>

      {isInvoice && due > 0 && doc.status !== 'cancelled' && (
        <section aria-label="Amount due" className="keep rounded-lg border border-line p-4">
          <p className="text-[13px] text-muted">Amount due</p>
          <p data-testid="amount-due" className="text-3xl font-semibold tabular-nums">
            {cur(due)}
          </p>
          {doc.payment?.upi_uri && (
            <a href={doc.payment.upi_uri} className={`${primary} no-print mt-3 w-full sm:w-auto`}>
              Pay by UPI
            </a>
          )}
        </section>
      )}
      {isInvoice && due <= 0 && doc.status === 'paid' && (
        <p className="rounded-lg bg-ok-bg p-3 text-ok">Paid in full. Thank you.</p>
      )}

      <article
        className="doc space-y-5 rounded-lg border border-line p-4 sm:p-6"
        aria-label={`${label} details`}
      >
        <div className="grid gap-4 text-[13px] sm:grid-cols-2">
          <div>
            <p className="text-[12px] text-muted">From</p>
            <p className="font-medium">{doc.seller.name}</p>
            {doc.seller.gstin && <p className="font-mono text-[12px]">GSTIN {doc.seller.gstin}</p>}
          </div>
          <div>
            <p className="text-[12px] text-muted">{isInvoice ? 'Billed to' : 'Prepared for'}</p>
            <p className="font-medium">{doc.buyer.name}</p>
            {doc.buyer.gstin && <p className="font-mono text-[12px]">GSTIN {doc.buyer.gstin}</p>}
            {doc.place_of_supply && <p className="text-muted">Place of supply: {doc.place_of_supply}</p>}
          </div>
        </div>

        <ul className="divide-y divide-line sm:hidden" aria-label="Items">
          {doc.lines.map((l, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: lines have no public id and never reorder
            <li key={i} className="py-2 text-[14px]">
              <div className="flex justify-between gap-3">
                <span>{l.description}</span>
                <span className="tabular-nums">{cur(l.taxable_minor)}</span>
              </div>
              <p className="text-[12px] text-muted">
                {Number(l.quantity)} x {cur(l.unit_price_minor)} · GST {l.gst_rate_bp / 100}%
                {l.hsn_sac ? ` · HSN/SAC ${l.hsn_sac}` : ''}
              </p>
            </li>
          ))}
        </ul>
        <table className="hidden w-full text-left text-[13px] sm:table">
          <thead className="border-b border-line text-[12px] text-muted">
            <tr>
              <th className="py-1.5 font-medium">Description</th>
              <th className="font-medium">HSN/SAC</th>
              <th className="text-right font-medium">Qty</th>
              <th className="text-right font-medium">Unit price</th>
              <th className="text-right font-medium">GST</th>
              <th className="text-right font-medium">Amount</th>
            </tr>
          </thead>
          <tbody>
            {doc.lines.map((l, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: lines have no public id and never reorder
              <tr key={i} className="border-b border-line">
                <td className="py-1.5">{l.description}</td>
                <td>{l.hsn_sac}</td>
                <td className="text-right tabular-nums">{Number(l.quantity)}</td>
                <td className="text-right tabular-nums">{cur(l.unit_price_minor)}</td>
                <td className="text-right">{l.gst_rate_bp / 100}%</td>
                <td className="text-right tabular-nums">{cur(l.taxable_minor)}</td>
              </tr>
            ))}
          </tbody>
        </table>

        <dl className="keep ml-auto max-w-xs space-y-1 text-[14px]">
          <Row k="Subtotal" v={cur(doc.subtotal_minor)} />
          {intra ? (
            <>
              <Row k="CGST" v={cur(doc.cgst_minor)} />
              <Row k="SGST" v={cur(doc.sgst_minor)} />
            </>
          ) : (
            <Row k="IGST" v={cur(doc.igst_minor)} />
          )}
          <div className="flex justify-between border-t border-line pt-1 font-semibold">
            <dt>Total</dt>
            <dd data-testid="doc-total" className="tabular-nums">
              {cur(doc.total_minor)}
            </dd>
          </div>
          {isInvoice && <Row k="Paid" v={cur(doc.paid_minor ?? 0)} />}
        </dl>
        {doc.reverse_charge && <p className="text-[13px]">Reverse charge applies.</p>}
      </article>

      {doc.decision && (
        <p role="status" data-testid="decision" className="keep rounded-lg bg-hover p-3 text-[14px]">
          {doc.decision.status === 'rejected' ? 'Declined' : 'Accepted'} by {doc.decision.by_name}
          {doc.decision.at ? ` on ${new Date(doc.decision.at).toLocaleString('en-IN')}` : ''}. The sender has
          been informed. The name was typed on this page and is not verified.
        </p>
      )}
      {doc.can_decide && !doc.decision && (
        <Decide doc={doc} token={token} step={step} setStep={setStep} onChange={onChange} />
      )}

      {isInvoice && doc.payment?.upi_uri && due > 0 && (
        <section aria-label="Pay by UPI" className="keep space-y-2 rounded-lg border border-line p-4">
          <h2 className="font-semibold">Scan to pay by UPI</h2>
          <div className="flex flex-wrap items-center gap-4">
            <QrSvg text={doc.payment.upi_uri} label="UPI payment QR code" />
            <p className="max-w-xs text-[13px] text-muted">
              Pay {doc.payment.upi_payee} with any UPI app. The amount and the invoice number are filled in.
              Payment is confirmed by the seller; this page does not change when you pay.
            </p>
          </div>
        </section>
      )}
      {doc.payment?.bank_details && (
        <section aria-label="Bank details" className="keep rounded-lg border border-line p-4">
          <h2 className="mb-1 font-semibold">Bank transfer</h2>
          <p className="whitespace-pre-line text-[13px]">{doc.payment.bank_details}</p>
        </section>
      )}
      {doc.irn && (
        <section aria-label="E-invoice" className="keep space-y-2 rounded-lg border border-line p-4">
          <h2 className="font-semibold">GST e-invoice</h2>
          <p className="break-all font-mono text-[12px]">IRN {doc.irn}</p>
          {doc.signed_qr && (
            <div className="flex flex-wrap items-center gap-3">
              <QrSvg text={doc.signed_qr} ecc="L" label="Signed e-invoice QR code" size={144} />
              <p className="max-w-xs text-[12px] text-muted">
                GST e-invoice verification code. Not for payment.
              </p>
            </div>
          )}
        </section>
      )}

      <div className="no-print">
        <button type="button" className={secondary} onClick={() => window.print()}>
          Print or save as PDF
        </button>
      </div>
    </div>
  )
}

const Row = ({ k, v }: { k: string; v: string }) => (
  <div className="flex justify-between">
    <dt className="text-muted">{k}</dt>
    <dd className="tabular-nums">{v}</dd>
  </div>
)

/** Two steps on purpose: opening the link or pressing Enter never accepts; the review step restates what is agreed. */
function Decide({
  doc,
  token,
  step,
  setStep,
  onChange,
}: {
  doc: Doc
  token: string
  step?: 'accept' | 'decline'
  setStep: (s?: 'accept' | 'decline') => void
  onChange: (d: Doc) => void
}) {
  const [name, setName] = useState('')
  const [note, setNote] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      onChange(await api<Doc>(`/api/public/${token}/quote`, { body: { decision: step, name, note } }))
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }
  if (!step)
    return (
      <section
        aria-label="Your response"
        className="no-print keep space-y-3 rounded-lg border border-line p-4"
      >
        <h2 className="font-semibold">Your response</h2>
        <div className="flex flex-col gap-2 sm:flex-row">
          <button type="button" className={primary} onClick={() => setStep('accept')}>
            Accept quotation
          </button>
          <button type="button" className={secondary} onClick={() => setStep('decline')}>
            Decline
          </button>
        </div>
      </section>
    )
  const accept = step === 'accept'
  return (
    <section
      aria-label={accept ? 'Confirm acceptance' : 'Confirm decline'}
      className="no-print keep space-y-3 rounded-lg border-2 border-accent p-4"
    >
      <h2 className="font-semibold">{accept ? 'Confirm acceptance' : 'Confirm decline'}</h2>
      <p className="text-[14px]">
        {accept ? 'You are accepting' : 'You are declining'} quotation {doc.number} for{' '}
        <strong>{money(doc.total_minor)}</strong> from {doc.seller.name}.
      </p>
      <div className="space-y-1">
        <label htmlFor="who" className="text-[13px] font-medium">
          Your name
        </label>
        <input
          id="who"
          className={field}
          value={name}
          maxLength={120}
          autoComplete="name"
          onChange={(e) => setName(e.target.value)}
        />
      </div>
      <div className="space-y-1">
        <label htmlFor="why" className="text-[13px] font-medium">
          Note for the seller (optional)
        </label>
        <textarea
          id="why"
          className={field}
          rows={2}
          maxLength={1000}
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      </div>
      {err && (
        <p role="alert" className="text-[13px] text-bad">
          {err}
        </p>
      )}
      <div className="flex flex-col gap-2 sm:flex-row">
        <button type="button" className={primary} disabled={busy || !name.trim()} onClick={submit}>
          {accept ? 'Confirm acceptance' : 'Confirm decline'}
        </button>
        <button type="button" className={secondary} disabled={busy} onClick={() => setStep(undefined)}>
          Back
        </button>
      </div>
    </section>
  )
}
