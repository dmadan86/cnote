'use client'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, inputCls } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { Confirm, run } from './bits'
import { type Contact, type InvoiceDetail, money, parseMinor, STATES, toRupees } from './fmt'

const RATES = [0, 5, 12, 18, 28]
type Row = {
  k: number
  description: string
  hsn: string
  qty: string
  price: string
  rate: number
  item: string
  total: number | null // server taxable amount; null while edited
}
type Preview = {
  intra_state: boolean
  reverse_charge: boolean
  cgst_minor: number
  sgst_minor: number
  igst_minor: number
  total_minor: number
  subtotal_minor: number
}
type Item = { id: string; key: string; title: string }

let seq = 0
const blank = (): Row => ({
  k: ++seq,
  description: '',
  hsn: '',
  qty: '1',
  price: '',
  rate: 18,
  item: '',
  total: null,
})
const fromServer = (inv: InvoiceDetail): Row[] =>
  inv.lines.map((l) => ({
    k: ++seq,
    description: l.description,
    hsn: l.hsn_sac ?? '',
    qty: String(Number(l.quantity)),
    price: toRupees(l.unit_price_minor),
    rate: l.gst_rate_bp / 100,
    item: l.item_note_id ?? '',
    total: l.taxable_minor,
  }))

/** Rows the API will accept; null when something is still incomplete. */
function toPayload(rows: Row[]) {
  const out = []
  for (const r of rows) {
    const price = parseMinor(r.price || '0')
    const qty = Number(r.qty)
    if (!r.description.trim() || price === null || !(qty > 0)) return null
    out.push({
      description: r.description,
      hsn_sac: r.hsn || undefined,
      quantity: qty,
      unit_price_minor: price,
      gst_rate_bp: r.rate * 100,
      item_note_id: r.item || undefined,
    })
  }
  return out
}

const estimate = (r: Row) => Math.round(Number(r.qty) * (parseMinor(r.price || '0') ?? 0))

export function Totals({ subtotal, tax, hint }: { subtotal: number; tax?: Preview; hint?: string }) {
  return (
    <dl className="space-y-1.5 text-[14px]" aria-label="Totals">
      <div className="flex justify-between">
        <dt className="text-muted">Subtotal</dt>
        <dd className="tabular-nums">{money(subtotal)}</dd>
      </div>
      {tax &&
        (tax.intra_state ? (
          <>
            <div className="flex justify-between">
              <dt className="text-muted">CGST</dt>
              <dd className="tabular-nums">{money(tax.cgst_minor)}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-muted">SGST</dt>
              <dd className="tabular-nums">{money(tax.sgst_minor)}</dd>
            </div>
          </>
        ) : (
          <div className="flex justify-between">
            <dt className="text-muted">IGST</dt>
            <dd className="tabular-nums">{money(tax.igst_minor)}</dd>
          </div>
        ))}
      <div className="flex justify-between border-t border-line pt-1.5 text-[15px] font-semibold">
        <dt>Total</dt>
        <dd className="tabular-nums">{money(tax ? tax.total_minor : subtotal)}</dd>
      </div>
      {tax?.reverse_charge && (
        <p className="text-[12px] text-muted">Reverse charge: tax is paid by the buyer.</p>
      )}
      {hint && <p className="text-[12px] text-warn">{hint}</p>}
    </dl>
  )
}

export function Editor({ inv, onIssued }: { inv: InvoiceDetail; onIssued: () => void }) {
  const { ws } = useShell()
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const items = useCmd<Item[]>(ws, 'item.list', { limit: 200 })
  const [rows, setRows] = useState<Row[]>(() => (inv.lines.length ? fromServer(inv) : [blank()]))
  const [contactId, setContactId] = useState(inv.contact_id ?? '')
  const [place, setPlace] = useState(inv.place_of_supply ?? '')
  const [placeTouched, setPlaceTouched] = useState(false)
  const [date, setDate] = useState(inv.invoice_date ?? '')
  const [due, setDue] = useState(inv.due_date ?? '')
  const [rc, setRc] = useState(inv.reverse_charge)
  const [subtotal, setSubtotal] = useState(inv.subtotal_minor)
  const [tax, setTax] = useState<Preview>()
  const [hint, setHint] = useState<string>()
  const [status, setStatus] = useState<'saved' | 'saving' | 'dirty' | 'error'>('saved')
  const [err, setErr] = useState<string>()
  const [reviewing, setReviewing] = useState(false)
  const latest = useRef({ rows, contactId, place, placeTouched, date, due, rc })
  latest.current = { rows, contactId, place, placeTouched, date, due, rc }

  const refreshTax = useCallback(async () => {
    try {
      setTax(await run<Preview>(ws, 'invoice.preview', { id: inv.id }))
      setHint(undefined)
    } catch (x) {
      setTax(undefined)
      setHint((x as Error).message)
    }
  }, [ws, inv.id])

  const persist = useCallback(async () => {
    const s = latest.current
    const lines = toPayload(s.rows)
    if (!lines) {
      setStatus('dirty')
      return false
    }
    setStatus('saving')
    try {
      const d = await run<InvoiceDetail>(ws, 'invoice.update_draft', {
        id: inv.id,
        contact_id: s.contactId || undefined,
        lines,
        invoice_date: s.date || undefined,
        due_date: s.due || undefined,
        place_of_supply: s.placeTouched ? s.place || undefined : undefined,
        reverse_charge: s.rc,
      })
      setSubtotal(d.subtotal_minor)
      setPlace(d.place_of_supply ?? '')
      setRows((rs) =>
        rs.map((r, i) => ({ ...r, total: r.total === null ? (d.lines[i]?.taxable_minor ?? null) : r.total })),
      )
      setErr(undefined)
      setStatus('saved')
      await refreshTax()
      return true
    } catch (x) {
      setErr((x as Error).message)
      setStatus('error')
      return false
    }
  }, [ws, inv.id, refreshTax])

  // Debounced autosave; bump only moves on user input, so opening a draft does not save.
  const [bump, setBump] = useState(0)
  useEffect(() => {
    if (!bump) return
    const t = setTimeout(persist, 700)
    return () => clearTimeout(t)
  }, [bump, persist])
  // biome-ignore lint/correctness/useExhaustiveDependencies: run once on open
  useEffect(() => {
    refreshTax()
  }, [])

  const touch = () => {
    setStatus('dirty')
    setBump((n) => n + 1)
  }
  const patch = (k: number, p: Partial<Row>) => {
    setRows((rs) => rs.map((r) => (r.k === k ? { ...r, total: null, ...p } : r)))
    touch()
  }
  const move = (i: number, d: number) => {
    setRows((rs) => {
      const n = [...rs]
      ;[n[i], n[i + d]] = [n[i + d], n[i]]
      return n
    })
    touch()
  }

  const label = { saved: 'Draft saved', saving: 'Saving...', dirty: 'Unsaved changes', error: 'Not saved' }[
    status
  ]
  const cell = `${inputCls} !px-2 !py-1 !text-[13px]`
  const contact = contacts.data?.find((c) => c.id === contactId)

  return (
    <div className="grid gap-4 lg:grid-cols-[1fr_300px]">
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <h1 className="text-xl font-semibold">
            Draft invoice{inv.contact_name ? ` for ${inv.contact_name}` : ''}
          </h1>
          <span role="status" className="text-[13px] text-muted">
            {label}
          </span>
          <button
            type="button"
            className={`${btnPrimary} ml-auto`}
            onClick={async () => {
              if (toPayload(rows) && (await persist())) setReviewing(true)
              else setErr((e) => e ?? 'Complete every line (description, quantity, price) before issuing.')
            }}
          >
            Review &amp; issue
          </button>
        </div>
        <ErrorText error={err} />
        <div className="overflow-x-auto rounded-lg border border-line">
          <table className="w-full min-w-[760px] text-left text-[13px]">
            <thead className="border-b border-line text-[12px] text-muted">
              <tr>
                <th className="p-2 font-medium">Description</th>
                <th className="font-medium">HSN/SAC</th>
                <th className="w-16 font-medium">Qty</th>
                <th className="w-28 font-medium">Unit price</th>
                <th className="w-20 font-medium">GST %</th>
                <th className="w-28 text-right font-medium">Line total</th>
                <th className="w-24" />
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={r.k} className="border-b border-line align-top last:border-0">
                  <td className="space-y-1 p-2">
                    <input
                      aria-label={`Line ${i + 1} description`}
                      value={r.description}
                      onChange={(e) => patch(r.k, { description: e.target.value })}
                      className={cell}
                    />
                    <select
                      aria-label={`Line ${i + 1} work item`}
                      value={r.item}
                      onChange={(e) => patch(r.k, { item: e.target.value })}
                      className={`${cell} text-muted`}
                    >
                      <option value="">No linked work item</option>
                      {items.data?.map((it) => (
                        <option key={it.id} value={it.id}>
                          {it.key} {it.title}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td className="pt-2">
                    <input
                      aria-label={`Line ${i + 1} HSN/SAC`}
                      value={r.hsn}
                      inputMode="numeric"
                      onChange={(e) => patch(r.k, { hsn: e.target.value })}
                      className={`${cell} w-24`}
                    />
                  </td>
                  <td className="pt-2">
                    <input
                      aria-label={`Line ${i + 1} quantity`}
                      value={r.qty}
                      inputMode="decimal"
                      onChange={(e) => patch(r.k, { qty: e.target.value })}
                      className={cell}
                    />
                  </td>
                  <td className="pt-2">
                    <input
                      aria-label={`Line ${i + 1} unit price`}
                      value={r.price}
                      inputMode="decimal"
                      onChange={(e) => patch(r.k, { price: e.target.value })}
                      className={cell}
                    />
                  </td>
                  <td className="pt-2">
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
                  <td className="pt-3 text-right tabular-nums">{money(r.total ?? estimate(r))}</td>
                  <td className="pt-2 text-right">
                    <button
                      type="button"
                      aria-label={`Move line ${i + 1} up`}
                      disabled={i === 0}
                      onClick={() => move(i, -1)}
                      className="rounded p-1 hover:bg-hover disabled:opacity-30"
                    >
                      <ArrowUp size={14} />
                    </button>
                    <button
                      type="button"
                      aria-label={`Move line ${i + 1} down`}
                      disabled={i === rows.length - 1}
                      onClick={() => move(i, 1)}
                      className="rounded p-1 hover:bg-hover disabled:opacity-30"
                    >
                      <ArrowDown size={14} />
                    </button>
                    <button
                      type="button"
                      aria-label={`Remove line ${i + 1}`}
                      disabled={rows.length === 1}
                      onClick={() => {
                        setRows((rs) => rs.filter((x) => x.k !== r.k))
                        touch()
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
        <button
          type="button"
          className={btn}
          onClick={() => {
            setRows((rs) => [...rs, blank()])
            touch()
          }}
        >
          <Plus size={14} /> Add line
        </button>
        <div className="ml-auto max-w-xs rounded-lg border border-line p-4">
          <Totals subtotal={subtotal} tax={tax} hint={hint} />
        </div>
      </div>

      <aside aria-label="Invoice settings" className="h-fit space-y-3 rounded-lg border border-line p-4">
        <h2 className="text-[13px] font-semibold">Settings at a glance</h2>
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Customer</span>
          <select
            value={contactId}
            onChange={(e) => {
              setContactId(e.target.value)
              setPlaceTouched(false)
              touch()
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
        {contact && (
          <p className="text-[12px] text-muted">
            {contact.gstin ? `GSTIN ${contact.gstin}` : 'No GSTIN (B2C: no e-invoice)'}
          </p>
        )}
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Place of supply</span>
          <select
            value={place}
            onChange={(e) => {
              setPlace(e.target.value)
              setPlaceTouched(true)
              touch()
            }}
            className={inputCls}
          >
            <option value="">Not set</option>
            {STATES.map(([c, n]) => (
              <option key={c} value={c}>
                {c} {n}
              </option>
            ))}
          </select>
        </label>
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Invoice date</span>
          <input
            type="date"
            value={date}
            onChange={(e) => {
              setDate(e.target.value)
              touch()
            }}
            className={inputCls}
          />
        </label>
        <label className="block space-y-1">
          <span className="text-[12px] font-medium text-muted">Due date</span>
          <input
            type="date"
            value={due}
            onChange={(e) => {
              setDue(e.target.value)
              touch()
            }}
            className={inputCls}
          />
        </label>
        <label className="flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            checked={rc}
            onChange={(e) => {
              setRc(e.target.checked)
              touch()
            }}
          />
          Reverse charge
        </label>
      </aside>

      {reviewing && (
        <Confirm
          title="Review and issue"
          action="Issue invoice"
          onClose={() => setReviewing(false)}
          onDone={async () => {
            await run(ws, 'invoice.issue', { id: inv.id })
            onIssued()
          }}
          body={
            <div className="space-y-3">
              <p>
                Issuing assigns an invoice number and posts to the ledger. It cannot be edited afterwards; use
                a credit note to correct it.
              </p>
              <p className="text-muted">
                {contact?.name} · place of supply {place} · {rows.length} line{rows.length === 1 ? '' : 's'}
              </p>
              <Totals subtotal={subtotal} tax={tax} hint={hint} />
            </div>
          }
        />
      )}
    </div>
  )
}
