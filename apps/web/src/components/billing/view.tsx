'use client'
import Link from 'next/link'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { UseTemplateButton } from '@/components/templates/use-template'
import { btn, btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { Confirm, run } from './bits'
import { type InvoiceDetail, money, parseMinor, STATES, toRupees } from './fmt'
import { StatusPill } from './invoice-list'
import { ShareButton, UpiCard } from './share-panel'

type Item = { id: string; key: string; title: string }
const OPEN = ['issued', 'irn_generated', 'sent', 'partly_paid']
const METHODS: [string, string][] = [
  ['upi', 'UPI'],
  ['bank', 'Bank transfer'],
  ['card', 'Card'],
  ['cash', 'Cash'],
]
const DAY = 86_400_000

// Print: hide the app shell and show only the document (browser "Save as PDF").
const PRINT_CSS = `@media print {
  body * { visibility: hidden; }
  #invoice-print, #invoice-print * { visibility: visible; }
  #invoice-print { position: absolute; inset: 0; padding: 24px; background: white; color: black; }
  .no-print { display: none !important; }
}`

function IrnPanel({ inv, strict, reload }: { inv: InvoiceDetail; strict: boolean; reload: () => void }) {
  const { ws } = useShell()
  const [err, setErr] = useState<string>()
  const [warn, setWarn] = useState<string>()
  const [asking, setAsking] = useState(false)
  if (!inv.buyer_gstin || !inv.number) return null
  const elapsed = inv.invoice_date ? Math.floor((Date.now() - new Date(inv.invoice_date).getTime()) / DAY) : 0
  const left = 30 - elapsed
  const hoursLeft = inv.ack_date ? 24 - (Date.now() - new Date(inv.ack_date).getTime()) / 3_600_000 : 0
  const active = inv.irn && !inv.irn_cancelled_at
  return (
    <section aria-label="E-invoice IRN" className="rounded-lg border border-line p-4">
      <h2 className="mb-2 text-[14px] font-semibold">E-invoice (IRN)</h2>
      {inv.irn ? (
        <dl className="space-y-1 text-[13px]">
          <div>
            <dt className="text-muted">IRN {inv.irn_cancelled_at && '(cancelled)'}</dt>
            <dd data-testid="irn" className="break-all font-mono text-[12px]">
              {inv.irn}
            </dd>
          </div>
          <div className="flex gap-6">
            <div>
              <dt className="text-muted">Ack no.</dt>
              <dd>{inv.ack_no}</dd>
            </div>
            <div>
              <dt className="text-muted">Ack date</dt>
              <dd>{inv.ack_date && new Date(inv.ack_date).toLocaleString('en-IN')}</dd>
            </div>
          </div>
        </dl>
      ) : (
        <p className={`text-[13px] ${left <= 10 ? 'text-warn' : 'text-muted'}`}>
          {left > 0
            ? `${left} day${left === 1 ? '' : 's'} left in the 30-day IRN reporting window.`
            : 'The 30-day IRN reporting window has passed.'}
          {strict && elapsed >= 29 && ' Generation is blocked for your turnover.'}
        </p>
      )}
      <ErrorText error={err} />
      {warn && <p className="mt-2 text-[13px] text-warn">{warn}</p>}
      <div className="no-print mt-3 flex gap-2">
        {!inv.irn && inv.status !== 'cancelled' && (
          <button
            type="button"
            className={btnPrimary}
            onClick={async () => {
              try {
                const r = await run<{ warning: string | null }>(ws, 'invoice.generate_irn', { id: inv.id })
                setWarn(r.warning ?? undefined)
                setErr(undefined)
                reload()
              } catch (x) {
                setErr((x as Error).message)
              }
            }}
          >
            Generate IRN
          </button>
        )}
        {active && hoursLeft > 0 && (
          <button type="button" className={btn} onClick={() => setAsking(true)}>
            Cancel IRN ({Math.floor(hoursLeft)}h left)
          </button>
        )}
        {active && hoursLeft <= 0 && (
          <span className="text-[12px] text-muted">IRN can no longer be cancelled; issue a credit note.</span>
        )}
      </div>
      {asking && (
        <Confirm
          title="Cancel IRN"
          action="Cancel IRN"
          body="The IRN can be cancelled only within 24 hours of acknowledgement."
          onClose={() => setAsking(false)}
          onDone={async () => {
            await run(ws, 'invoice.cancel_irn', { id: inv.id, reason: 'Cancelled by user' })
            reload()
          }}
        />
      )}
    </section>
  )
}

export function IssuedView({ inv, reload }: { inv: InvoiceDetail; reload: () => void }) {
  const { ws } = useShell()
  const items = useCmd<Item[]>(ws, 'item.list', { limit: 200 })
  const settings = useCmd<{ name: string; settings: Record<string, unknown> }>(ws, 'workspace.get')
  const [dialog, setDialog] = useState<'pay' | 'credit' | 'cancel'>()
  const [text, setText] = useState({ amount: '', ref: '', reason: '', rate: '18' })
  const [method, setMethod] = useState('upi')
  const open = inv.kind === 'invoice' && OPEN.includes(inv.status)
  const itemOf = (id: string | null) => items.data?.find((i) => i.id === id)
  const set = (k: keyof typeof text) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setText({ ...text, [k]: e.target.value })
  const amountMinor = parseMinor(text.amount)
  const need = (v: number | null) => {
    if (v === null || v <= 0) throw new Error('Enter an amount like 5000 or 1250.50')
    return v
  }
  const intra = inv.cgst_minor + inv.sgst_minor > 0 || inv.igst_minor === 0
  const strict = settings.data?.settings.aato_above_10cr === true

  return (
    <div className="space-y-4">
      <style>{PRINT_CSS}</style>
      <div className="no-print flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">
          {inv.kind === 'credit_note' ? 'Credit note' : 'Invoice'} {inv.number}
        </h1>
        <StatusPill status={inv.status} />
        <div className="ml-auto flex flex-wrap gap-2">
          <button type="button" className={btn} onClick={() => window.print()}>
            Print / PDF
          </button>
          <UseTemplateButton sourceKind="invoice" sourceId={inv.id} />
          {inv.kind === 'invoice' && inv.number && inv.status !== 'draft' && inv.status !== 'cancelled' && (
            <ShareButton
              noteId={inv.id}
              kind="invoice"
              overdue={open && !!inv.due_date && inv.due_date < new Date().toISOString().slice(0, 10)}
            />
          )}
          {open && (
            <>
              <button
                type="button"
                className={btnPrimary}
                onClick={() => {
                  setText({ ...text, amount: toRupees(inv.outstanding_minor) })
                  setDialog('pay')
                }}
              >
                Record payment
              </button>
              <button type="button" className={btn} onClick={() => setDialog('credit')}>
                Credit note
              </button>
              {inv.paid_minor === 0 && inv.credited_minor === 0 && (
                <button type="button" className={btn} onClick={() => setDialog('cancel')}>
                  Cancel invoice
                </button>
              )}
            </>
          )}
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <article id="invoice-print" className="space-y-5 rounded-lg border border-line p-6">
          <header className="flex justify-between">
            <div>
              <p className="text-[12px] text-muted">Billed to</p>
              <p className="font-medium">{inv.contact_name}</p>
              {inv.buyer_gstin && <p className="font-mono text-[12px]">GSTIN {inv.buyer_gstin}</p>}
              <p className="text-[13px] text-muted">
                Place of supply: {inv.place_of_supply} {STATES.find(([c]) => c === inv.place_of_supply)?.[1]}
              </p>
            </div>
            <div className="text-right text-[13px]">
              <p className="text-[12px] text-muted">From GSTIN</p>
              <p className="font-mono text-[12px]">{inv.supplier_gstin}</p>
              <p>Date: {inv.invoice_date}</p>
              <p>Due: {inv.due_date}</p>
              {inv.reverse_charge && <p>Reverse charge applies</p>}
            </div>
          </header>
          <table className="w-full text-left text-[13px]">
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
              {inv.lines.map((l) => {
                const it = itemOf(l.item_note_id)
                return (
                  <tr key={l.id} className="border-b border-line">
                    <td className="py-1.5">
                      {l.description}
                      {l.item_note_id && (
                        <Link
                          href={`/w/${ws}/n/${l.item_note_id}`}
                          className="no-print ml-2 text-[12px] text-accent hover:underline"
                        >
                          {it ? it.key : 'Work item'}
                        </Link>
                      )}
                    </td>
                    <td>{l.hsn_sac}</td>
                    <td className="text-right tabular-nums">{Number(l.quantity)}</td>
                    <td className="text-right tabular-nums">{money(l.unit_price_minor)}</td>
                    <td className="text-right">{l.gst_rate_bp / 100}%</td>
                    <td className="text-right tabular-nums">{money(l.taxable_minor)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
          <dl className="ml-auto max-w-xs space-y-1 text-[14px]">
            <div className="flex justify-between">
              <dt className="text-muted">Subtotal</dt>
              <dd className="tabular-nums">{money(inv.subtotal_minor)}</dd>
            </div>
            {intra ? (
              <>
                <div className="flex justify-between">
                  <dt className="text-muted">CGST</dt>
                  <dd className="tabular-nums">{money(inv.cgst_minor)}</dd>
                </div>
                <div className="flex justify-between">
                  <dt className="text-muted">SGST</dt>
                  <dd className="tabular-nums">{money(inv.sgst_minor)}</dd>
                </div>
              </>
            ) : (
              <div className="flex justify-between">
                <dt className="text-muted">IGST</dt>
                <dd className="tabular-nums">{money(inv.igst_minor)}</dd>
              </div>
            )}
            <div className="flex justify-between border-t border-line pt-1 font-semibold">
              <dt>Total</dt>
              <dd className="tabular-nums">{money(inv.total_minor)}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-muted">Paid</dt>
              <dd className="tabular-nums">{money(inv.paid_minor)}</dd>
            </div>
            <div className="flex justify-between font-medium">
              <dt>Outstanding</dt>
              <dd data-testid="outstanding" className="tabular-nums">
                {money(inv.outstanding_minor)}
              </dd>
            </div>
          </dl>
          {inv.irn && (
            <p className="break-all text-[11px] text-muted">
              IRN {inv.irn} · Ack {inv.ack_no}
            </p>
          )}
        </article>

        <div className="no-print space-y-4">
          <IrnPanel inv={inv} strict={strict} reload={reload} />
          {open && <UpiCard invoiceId={inv.id} outstanding={inv.outstanding_minor} />}
          <section aria-label="Payments" className="rounded-lg border border-line p-4">
            <h2 className="mb-2 text-[14px] font-semibold">Payments</h2>
            {inv.payments.length === 0 && <p className="text-[13px] text-muted">No payments yet.</p>}
            <ul className="divide-y divide-line">
              {inv.payments.map((p) => (
                <li key={p.id} className="flex justify-between py-1.5 text-[13px]">
                  <span>
                    {METHODS.find(([k]) => k === p.method)?.[1] ?? p.method}
                    {p.reference && <span className="text-muted"> · {p.reference}</span>}
                  </span>
                  <span className="tabular-nums">{money(p.amount_minor)}</span>
                </li>
              ))}
            </ul>
          </section>
        </div>
      </div>

      {dialog === 'pay' && (
        <Confirm
          title="Record payment"
          action="Record payment"
          body={`Outstanding: ${money(inv.outstanding_minor)}. This posts to the ledger.`}
          onClose={() => setDialog(undefined)}
          onDone={async () => {
            await run(ws, 'payment.record', {
              invoice_id: inv.id,
              amount_minor: need(amountMinor),
              method,
              reference: text.ref || undefined,
            })
            reload()
          }}
        >
          <Field label="Amount (INR)">
            {(id) => (
              <input
                id={id}
                value={text.amount}
                onChange={set('amount')}
                inputMode="decimal"
                className={inputCls}
              />
            )}
          </Field>
          <Field label="Method">
            {(id) => (
              <select id={id} value={method} onChange={(e) => setMethod(e.target.value)} className={inputCls}>
                {METHODS.map(([k, l]) => (
                  <option key={k} value={k}>
                    {l}
                  </option>
                ))}
              </select>
            )}
          </Field>
          <Field label="Reference (UTR)">
            {(id) => <input id={id} value={text.ref} onChange={set('ref')} className={inputCls} />}
          </Field>
        </Confirm>
      )}
      {dialog === 'credit' && (
        <Confirm
          title="Issue credit note"
          action="Issue credit note"
          body="Credits part of this invoice (amount before tax). Tax uses the original place of supply."
          onClose={() => setDialog(undefined)}
          onDone={async () => {
            await run(ws, 'credit_note.create', {
              invoice_id: inv.id,
              reason: text.reason,
              lines: [
                {
                  description: text.reason,
                  unit_price_minor: need(amountMinor),
                  gst_rate_bp: Number(text.rate) * 100,
                },
              ],
            })
            reload()
          }}
        >
          <Field label="Amount before tax (INR)">
            {(id) => (
              <input
                id={id}
                value={text.amount}
                onChange={set('amount')}
                inputMode="decimal"
                className={inputCls}
              />
            )}
          </Field>
          <Field label="GST rate (%)">
            {(id) => (
              <select
                id={id}
                value={text.rate}
                onChange={(e) => setText({ ...text, rate: e.target.value })}
                className={inputCls}
              >
                {[0, 5, 12, 18, 28].map((r) => (
                  <option key={r}>{r}</option>
                ))}
              </select>
            )}
          </Field>
          <Field label="Reason">
            {(id) => (
              <input id={id} value={text.reason} onChange={set('reason')} required className={inputCls} />
            )}
          </Field>
        </Confirm>
      )}
      {dialog === 'cancel' && (
        <Confirm
          title="Cancel invoice"
          action="Cancel invoice"
          body="This issues a full credit note and reverses the ledger entry. Cancel the IRN first if one exists."
          onClose={() => setDialog(undefined)}
          onDone={async () => {
            await run(ws, 'invoice.cancel', { id: inv.id, reason: text.reason })
            reload()
          }}
        >
          <Field label="Reason">
            {(id) => (
              <input id={id} value={text.reason} onChange={set('reason')} required className={inputCls} />
            )}
          </Field>
        </Confirm>
      )}
    </div>
  )
}
