'use client'
import { useState } from 'react'
import { applyBusinessType, BUSINESS_TYPES, COMPANY_SIZES, saveCompanySize } from '@/lib/business-types'
import { ErrorText } from './ui'

const OTHER = {
  id: 'other',
  name: 'Other',
  blurb: 'Something else. You can change this any time in Settings.',
}
const card =
  'relative cursor-pointer rounded-lg border border-line p-3 hover:bg-hover has-[:checked]:border-accent has-[:checked]:bg-hover has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent'

/** Radio-group card grid. Native radios give arrow-key navigation. */
export function BusinessTypePicker({
  current,
  onPick,
  withOther,
  disabled,
}: {
  current?: string
  onPick: (id: string) => void
  withOther?: boolean
  disabled?: boolean
}) {
  return (
    <fieldset disabled={disabled} className="grid gap-2 sm:grid-cols-2">
      <legend className="sr-only">Business type</legend>
      {[...BUSINESS_TYPES, ...(withOther ? [OTHER] : [])].map((t) => (
        <label key={t.id} className={card}>
          <input
            type="radio"
            name="business_type"
            value={t.id}
            checked={current === t.id}
            onChange={() => onPick(t.id)}
            className="sr-only"
          />
          <span className="block text-[14px] font-medium">{t.name}</span>
          <span className="block text-[12px] text-muted">{t.blurb}</span>
        </label>
      ))}
    </fieldset>
  )
}

/** Company size chips; same radio pattern. */
export function CompanySizePicker({
  current,
  onPick,
  disabled,
}: {
  current?: string
  onPick: (s: string) => void
  disabled?: boolean
}) {
  return (
    <fieldset disabled={disabled} className="flex flex-wrap gap-2">
      <legend className="mb-1 text-[13px] font-medium">Company size (people)</legend>
      {COMPANY_SIZES.map((s) => (
        <label key={s} className={`${card} px-3 py-1.5 text-[14px]`}>
          <input
            type="radio"
            name="company_size"
            value={s}
            checked={current === s}
            onChange={() => onPick(s)}
            className="sr-only"
          />
          {s}
        </label>
      ))}
    </fieldset>
  )
}

/** Settings section: business type applies on pick (as before); size saves on pick. */
export function CompanyProfile({
  ws,
  type,
  size,
  onDone,
}: {
  ws: string
  type?: string
  size?: string
  onDone: () => void
}) {
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const run = async (p: Promise<unknown>) => {
    setBusy(true)
    try {
      await p
      onDone()
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-3">
      <BusinessTypePicker current={type} disabled={busy} onPick={(id) => run(applyBusinessType(ws, id))} />
      <CompanySizePicker current={size} disabled={busy} onPick={(s) => run(saveCompanySize(ws, s))} />
      <p className="text-[13px] text-muted">Country: India</p>
      <ErrorText error={err} />
    </div>
  )
}
