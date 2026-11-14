'use client'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { STATES } from './fmt'

type Ws = { settings: Record<string, string | boolean | undefined> }

/** Seller identity used on every invoice. The server validates the GSTIN when an invoice is issued. */
export function BillingSettings() {
  const { ws } = useShell()
  const { data, reload } = useCmd<Ws>(ws, 'workspace.get')
  const [msg, setMsg] = useState<string>()
  const [err, setErr] = useState<string>()
  if (!data) return null
  const s = data.settings
  const str = (k: string) => (typeof s[k] === 'string' ? (s[k] as string) : '')
  return (
    <form
      aria-label="Billing settings"
      className="max-w-xl space-y-3 rounded-lg border border-line p-4"
      onSubmit={async (e) => {
        e.preventDefault()
        const f = new FormData(e.currentTarget)
        const text = (k: string) => String(f.get(k) ?? '').trim()
        // Empty gstin/state_code are left out: the server rejects blanks and keeps the stored value.
        const settings = {
          ...(text('gstin') && { gstin: text('gstin').toUpperCase() }),
          ...(text('state_code') && { state_code: text('state_code') }),
          legal_name: text('legal_name'),
          address: text('address'),
          upi_vpa: text('upi_vpa'),
          upi_payee_name: text('upi_payee_name'),
          bank_details: text('bank_details'),
          aato_above_10cr: f.get('aato') === 'on',
        }
        try {
          await cmd(ws, 'workspace.update', { settings })
          setErr(undefined)
          setMsg('Saved')
          reload()
        } catch (x) {
          setMsg(undefined)
          setErr((x as Error).message)
        }
      }}
    >
      <h2 className="text-[14px] font-semibold">Seller details</h2>
      <Field label="Legal name">
        {(id) => <input id={id} name="legal_name" defaultValue={str('legal_name')} className={inputCls} />}
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="GSTIN">
          {(id) => (
            <input
              id={id}
              name="gstin"
              maxLength={15}
              defaultValue={str('gstin')}
              className={`${inputCls} font-mono uppercase`}
            />
          )}
        </Field>
        <Field label="State code">
          {(id) => (
            <select id={id} name="state_code" defaultValue={str('state_code')} className={inputCls}>
              <option value="">From GSTIN</option>
              {STATES.map(([c, n]) => (
                <option key={c} value={c}>
                  {c} {n}
                </option>
              ))}
            </select>
          )}
        </Field>
      </div>
      <Field label="Address">
        {(id) => (
          <textarea id={id} name="address" rows={3} defaultValue={str('address')} className={inputCls} />
        )}
      </Field>
      <h2 className="pt-2 text-[14px] font-semibold">Collect payments</h2>
      <div className="grid grid-cols-2 gap-3">
        <Field label="UPI ID (VPA)">
          {(id) => (
            <input
              id={id}
              name="upi_vpa"
              placeholder="yourshop@bank"
              defaultValue={str('upi_vpa')}
              className={inputCls}
            />
          )}
        </Field>
        <Field label="UPI payee name">
          {(id) => (
            <input id={id} name="upi_payee_name" defaultValue={str('upi_payee_name')} className={inputCls} />
          )}
        </Field>
      </div>
      <Field label="Bank details shown on shared invoices">
        {(id) => (
          <textarea
            id={id}
            name="bank_details"
            rows={2}
            defaultValue={str('bank_details')}
            className={inputCls}
          />
        )}
      </Field>
      <label className="flex items-start gap-2 text-[13px]">
        <input type="checkbox" name="aato" defaultChecked={s.aato_above_10cr === true} className="mt-1" />
        <span>
          Annual turnover above Rs 10 crore
          <span className="block text-[12px] text-muted">Enforces the 30-day IRN reporting window.</span>
        </span>
      </label>
      <ErrorText error={err} />
      <div className="flex items-center gap-3">
        <button type="submit" className={btnPrimary}>
          Save billing settings
        </button>
        <span role="status" className="text-[13px] text-muted">
          {msg}
        </span>
      </div>
    </form>
  )
}
