'use client'
import { useState } from 'react'
import { STATES } from '@/components/billing/fmt'
import { btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

type Profile = Record<string, string | number>
const TEXT = [
  'legal_name',
  'trade_name',
  'pan',
  'gstin',
  'address',
  'pincode',
  'phone',
  'email',
  'upi_vpa',
  'bank_name',
  'bank_account',
  'bank_ifsc',
] as const
const UPPER = new Set(['pan', 'gstin', 'bank_ifsc'])
const MONTHS = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
]

/** Settings > Company: the legal profile that prints on quotes, invoices and receipts. Server validates formats. */
export function CompanyTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Profile>(ws, 'company.get')
  if (!data) return null
  return <CompanyForm ws={ws} initial={data} onSaved={reload} />
}

function CompanyForm({ ws, initial, onSaved }: { ws: string; initial: Profile; onSaved: () => void }) {
  const [v, setV] = useState<Profile>(initial)
  const [fy, setFy] = useState(Number(initial.fy_start_month) || 4)
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  const s = (k: string) => String(v[k] ?? '')
  const set = (k: string, val: string) => {
    setMsg(undefined)
    setV({ ...v, [k]: UPPER.has(k) ? val.toUpperCase() : val })
  }
  const dirty =
    TEXT.some((k) => s(k) !== String(initial[k] ?? '')) || fy !== (Number(initial.fy_start_month) || 4)
  const state = STATES.find(([c]) => c === s('gstin').slice(0, 2))
  const input =
    (k: (typeof TEXT)[number], extra: React.InputHTMLAttributes<HTMLInputElement> = {}) =>
    (id: string) => (
      <input id={id} value={s(k)} onChange={(e) => set(k, e.target.value)} className={inputCls} {...extra} />
    )
  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_320px]">
      <form
        aria-label="Company profile"
        className="space-y-6"
        onSubmit={async (e) => {
          e.preventDefault()
          try {
            const body: Record<string, string | number> = { fy_start_month: fy }
            for (const k of TEXT) if (s(k) !== String(initial[k] ?? '')) body[k] = s(k)
            setV(await cmd<Profile>(ws, 'company.update', body)) // the server's normalised values
            setErr(undefined)
            setMsg('Saved')
            onSaved()
          } catch (x) {
            setMsg(undefined)
            setErr((x as Error).message)
          }
        }}
      >
        <fieldset className="space-y-3">
          <legend className="mb-1 text-[14px] font-semibold">Identity</legend>
          <Field label="Legal name">{input('legal_name', { autoComplete: 'organization' })}</Field>
          <Field label="Trade name (optional)">{input('trade_name')}</Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="PAN">{input('pan', { maxLength: 10, className: `${inputCls} font-mono` })}</Field>
            <Field label="GSTIN">
              {input('gstin', { maxLength: 15, className: `${inputCls} font-mono` })}
            </Field>
          </div>
          {state && (
            <p className="text-[12px] text-muted">
              State from GSTIN: {state[0]} {state[1]}
            </p>
          )}
          <p className="text-[12px] text-muted">
            Formats and the GSTIN check digit are verified. That does not prove the registration exists.
          </p>
        </fieldset>
        <fieldset className="space-y-3">
          <legend className="mb-1 text-[14px] font-semibold">Address and contact</legend>
          <Field label="Address">
            {(id) => (
              <textarea
                id={id}
                rows={3}
                value={s('address')}
                onChange={(e) => set('address', e.target.value)}
                className={inputCls}
              />
            )}
          </Field>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="PIN code">{input('pincode', { inputMode: 'numeric', maxLength: 6 })}</Field>
            <Field label="Phone">{input('phone', { type: 'tel', placeholder: '+91 98765 43210' })}</Field>
            <Field label="Email">{input('email', { type: 'email' })}</Field>
          </div>
        </fieldset>
        <fieldset className="space-y-3">
          <legend className="mb-1 text-[14px] font-semibold">Payments and banking</legend>
          <Field label="UPI ID">{input('upi_vpa', { placeholder: 'yourshop@bank' })}</Field>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Bank name">{input('bank_name')}</Field>
            <Field label="Account number">{input('bank_account', { inputMode: 'numeric' })}</Field>
            <Field label="IFSC">
              {input('bank_ifsc', { maxLength: 11, className: `${inputCls} font-mono` })}
            </Field>
          </div>
        </fieldset>
        <fieldset>
          <legend className="mb-1 text-[14px] font-semibold">Financial year</legend>
          <Field label="Starts in">
            {(id) => (
              <select
                id={id}
                value={fy}
                onChange={(e) => setFy(Number(e.target.value))}
                className={`${inputCls} max-w-xs`}
              >
                {MONTHS.map((m, i) => (
                  <option key={m} value={i + 1}>
                    {m}
                  </option>
                ))}
              </select>
            )}
          </Field>
        </fieldset>
        <ErrorText error={err} />
        <div className="flex items-center gap-3">
          <button type="submit" className={btnPrimary} disabled={!dirty}>
            Save company details
          </button>
          <span role="status" className="text-[13px] text-muted">
            {msg ?? (dirty ? 'Unsaved changes' : '')}
          </span>
        </div>
      </form>
      <aside aria-label="Invoice header preview" className="lg:sticky lg:top-4 lg:self-start">
        <h2 className="mb-2 text-[12px] font-medium text-muted">Invoice header preview</h2>
        <div className="space-y-0.5 break-words rounded-md border border-line bg-canvas p-4 text-[12px]">
          <p className="text-[15px] font-semibold">{s('legal_name') || 'Your company name'}</p>
          {s('trade_name') && <p className="text-muted">Trading as {s('trade_name')}</p>}
          {s('address') && <p className="whitespace-pre-line">{s('address')}</p>}
          {s('pincode') && <p>PIN {s('pincode')}</p>}
          {s('gstin') && <p>GSTIN {s('gstin')}</p>}
          {s('pan') && <p>PAN {s('pan')}</p>}
          {(s('phone') || s('email')) && <p>{[s('phone'), s('email')].filter(Boolean).join(' · ')}</p>}
          {s('upi_vpa') && <p className="pt-2">Pay by UPI: {s('upi_vpa')}</p>}
          {(s('bank_name') || s('bank_account')) && (
            <p>
              {[
                s('bank_name'),
                s('bank_account') && `A/c ${s('bank_account')}`,
                s('bank_ifsc') && `IFSC ${s('bank_ifsc')}`,
              ]
                .filter(Boolean)
                .join(', ')}
            </p>
          )}
        </div>
      </aside>
    </div>
  )
}
