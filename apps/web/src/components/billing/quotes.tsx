'use client'
import { Plus, Trash2 } from 'lucide-react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { use, useState } from 'react'
import { NeedSpace } from '@/components/crm/contacts'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { Confirm, run } from './bits'
import { Totals } from './editor'
import { FilterBanner } from './filter'
import { type Contact, type Line, money, parseMinor, toRupees } from './fmt'
import { ShareButton } from './share-panel'

type Quote = {
  id: string
  number: string | null
  status: string
  contact_id: string | null
  valid_until: string | null
  subtotal_minor: number
  cgst_minor: number
  sgst_minor: number
  igst_minor: number
  total_minor: number
  converted_invoice_id: string | null
  converted_item_id: string | null
}
type QuoteDetail = Quote & {
  contact_name: string | null
  lines: Line[]
  accepted_via: 'staff' | 'link' | null
  accepted_by_name: string | null
  accepted_note: string | null
}

const LABEL: Record<string, string> = {
  draft: 'Draft',
  sent: 'Sent',
  accepted: 'Accepted',
  rejected: 'Rejected',
  expired: 'Expired',
  converted: 'Converted',
}
const TONE: Record<string, 'ok' | 'warn' | 'bad' | 'neutral'> = {
  accepted: 'ok',
  converted: 'ok',
  sent: 'warn',
  rejected: 'bad',
  expired: 'bad',
}
const QuotePill = ({ status }: { status: string }) => (
  <Pill tone={TONE[status]}>{LABEL[status] ?? status}</Pill>
)

function NewQuote({ onClose }: { onClose: () => void }) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const [err, setErr] = useState<string>()
  return (
    <Modal open title="New quote" onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          try {
            const d = await run<{ id: string }>(ws, 'quote.create_draft', {
              space_id: spaces[0].id,
              contact_id: String(new FormData(e.currentTarget).get('contact')),
              lines: [{ description: 'Services', unit_price_minor: 0, gst_rate_bp: 1800 }],
            })
            router.push(`/w/${ws}/billing/quotes/${d.id}`)
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <Field label="Customer">
          {(id) => (
            <select id={id} name="contact" required className={inputCls}>
              <option value="">Choose a contact</option>
              {contacts.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          )}
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

export function QuoteList() {
  const { ws, spaces } = useShell()
  const [adding, setAdding] = useState(false)
  const status = useSearchParams().get('status') // home tile: ?status=sent
  const { data, error } = useCmd<Quote[]>(ws, 'quote.list', { limit: 500, ...(status ? { status } : {}) })
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const names = new Map(contacts.data?.map((c) => [c.id, c.name]))
  return (
    <div>
      <div className="mb-3 flex">
        <button
          type="button"
          className={`${btnPrimary} ml-auto`}
          disabled={!spaces.length}
          onClick={() => setAdding(true)}
        >
          <Plus size={14} /> New quote
        </button>
      </div>
      {status && <FilterBanner label={`${status} quotes`} href={`/w/${ws}/billing/quotes`} />}
      <ErrorText error={error} />
      {!spaces.length && <NeedSpace />}
      {data?.length === 0 && spaces.length > 0 && (
        <p className="py-8 text-center text-muted">{status ? 'Nothing matches.' : 'No quotes yet.'}</p>
      )}
      {!!data?.length && (
        <table className="w-full text-left text-[14px]">
          <thead className="text-[12px] text-muted">
            <tr className="border-b border-line">
              <th className="py-2 font-medium">Number</th>
              <th className="font-medium">Customer</th>
              <th className="font-medium">Valid until</th>
              <th className="font-medium">Status</th>
              <th className="text-right font-medium">Total</th>
            </tr>
          </thead>
          <tbody>
            {data.map((q) => (
              <tr key={q.id} className="border-b border-line hover:bg-hover">
                <td className="py-2 font-medium">
                  <Link href={`/w/${ws}/billing/quotes/${q.id}`} className="hover:underline">
                    {q.number ?? 'Draft'}
                  </Link>
                </td>
                <td>{q.contact_id ? names.get(q.contact_id) : ''}</td>
                <td className="text-muted">{q.valid_until}</td>
                <td>
                  <QuotePill status={q.status} />
                </td>
                <td className="text-right tabular-nums">{money(q.total_minor)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && <NewQuote onClose={() => setAdding(false)} />}
    </div>
  )
}

type Row = { k: number; description: string; hsn: string; qty: string; price: string; rate: number }
let seq = 0
const RATES = [0, 5, 12, 18, 28]

function DraftForm({ q, reload }: { q: QuoteDetail; reload: () => void }) {
  const { ws } = useShell()
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const [rows, setRows] = useState<Row[]>(() =>
    q.lines.map((l) => ({
      k: ++seq,
      description: l.description,
      hsn: l.hsn_sac ?? '',
      qty: String(Number(l.quantity)),
      price: toRupees(l.unit_price_minor),
      rate: l.gst_rate_bp / 100,
    })),
  )
  const [contactId, setContactId] = useState(q.contact_id ?? '')
  const [valid, setValid] = useState(q.valid_until ?? '')
  const [err, setErr] = useState<string>()
  const [saved, setSaved] = useState(false)
  const [sending, setSending] = useState(false)
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
    await run(ws, 'quote.update_draft', {
      id: q.id,
      contact_id: contactId || undefined,
      valid_until: valid || undefined,
      lines,
    })
    setSaved(true)
  }
  const cell = `${inputCls} !px-2 !py-1 !text-[13px]`
  return (
    <div className="space-y-4">
      <ErrorText error={err} />
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Customer</span>
          <select
            value={contactId}
            onChange={(e) => {
              setContactId(e.target.value)
              setSaved(false)
            }}
            className={inputCls}
          >
            {contacts.data?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Valid until</span>
          <input
            type="date"
            value={valid}
            onChange={(e) => {
              setValid(e.target.value)
              setSaved(false)
            }}
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
      <div className="flex items-center gap-2">
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
        <button
          type="button"
          className={`${btn} ml-auto`}
          onClick={() =>
            save().then(
              () => {
                setErr(undefined)
                reload()
              },
              (x) => setErr((x as Error).message),
            )
          }
        >
          Save draft
        </button>
        <button
          type="button"
          className={btnPrimary}
          onClick={() =>
            save().then(
              () => {
                setErr(undefined)
                setSending(true)
              },
              (x) => setErr((x as Error).message),
            )
          }
        >
          Send quote
        </button>
      </div>
      {sending && (
        <Confirm
          title="Send quotation"
          action="Send quote"
          onClose={() => setSending(false)}
          onDone={async () => {
            await run(ws, 'quote.send', { id: q.id })
            reload()
          }}
          body="Sending assigns a quotation number and fixes the GST. It does not create an invoice or post to the ledger."
        />
      )}
    </div>
  )
}

export function QuoteView({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws } = useShell()
  const { data: q, error, reload } = useCmd<QuoteDetail>(ws, 'quote.get', { id })
  const [err, setErr] = useState<string>()
  const act = (name: string) =>
    run(ws, name, { id }).then(
      () => {
        setErr(undefined)
        reload()
      },
      (x) => setErr((x as Error).message),
    )
  if (error) return <ErrorText error={error} />
  if (!q) return <p className="text-muted">Loading...</p>
  const tax = {
    intra_state: q.igst_minor === 0,
    reverse_charge: false,
    cgst_minor: q.cgst_minor,
    sgst_minor: q.sgst_minor,
    igst_minor: q.igst_minor,
    total_minor: q.total_minor,
    subtotal_minor: q.subtotal_minor,
  }
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <h2 className="text-lg font-semibold">
          {q.number ? `Quotation ${q.number}` : 'Draft quotation'}
          {q.contact_name ? ` for ${q.contact_name}` : ''}
        </h2>
        <QuotePill status={q.status} />
      </div>
      <ErrorText error={err} />
      {q.status === 'draft' ? (
        <DraftForm key={q.id} q={q} reload={reload} />
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
              {q.lines.map((l) => (
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
            <Totals subtotal={q.subtotal_minor} tax={tax} />
          </div>
          <p className="text-[13px] text-muted">Valid until {q.valid_until}</p>
          {q.accepted_via === 'link' && (
            <p className="text-[13px]" data-testid="decided-by">
              {q.status === 'rejected' ? 'Declined' : 'Accepted'} by {q.accepted_by_name} on the shared link
              {q.accepted_note ? `: "${q.accepted_note}"` : ''}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <ShareButton noteId={q.id} kind="quotation" />
            {q.status === 'sent' && (
              <>
                <button type="button" className={btnPrimary} onClick={() => act('quote.accept')}>
                  Mark accepted
                </button>
                <button type="button" className={btn} onClick={() => act('quote.reject')}>
                  Mark rejected
                </button>
              </>
            )}
            {(q.status === 'accepted' || q.status === 'converted') && !q.converted_invoice_id && (
              <button type="button" className={btnPrimary} onClick={() => act('quote.convert_to_invoice')}>
                Convert to invoice
              </button>
            )}
            {(q.status === 'accepted' || q.status === 'converted') && !q.converted_item_id && (
              <button type="button" className={btn} onClick={() => act('quote.convert_to_project')}>
                Convert to project
              </button>
            )}
            {q.converted_invoice_id && (
              <Link href={`/w/${ws}/billing/${q.converted_invoice_id}`} className={btn}>
                Open invoice draft
              </Link>
            )}
          </div>
        </>
      )}
    </div>
  )
}
