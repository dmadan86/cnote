'use client'
import { useState } from 'react'
import {
  applyBusinessType,
  BUSINESS_TYPES,
  COMPANY_SIZES,
  defaultModules,
  MODULE_WHY,
  saveCompanySize,
} from '@/lib/business-types'
import { btn, btnPrimary, ErrorText, Modal } from './ui'

const OTHER = { id: 'other', name: 'Other', blurb: 'Something else: pick your own modules next.' }
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

/** First-run "Set up your company": step 1 type + size, step 2 modules. */
export function BusinessTypeChooser({
  ws,
  onDone,
  onClose,
}: {
  ws: string
  onDone: () => void
  onClose: () => void
}) {
  const [step, setStep] = useState(1)
  const [type, setType] = useState<string>()
  const [size, setSize] = useState<string>()
  const [mods, setMods] = useState<string[]>([])
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const save = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
      onDone()
    } catch (x) {
      setErr((x as Error).message)
      setBusy(false)
    }
  }
  const pick = (id: string) => {
    setType(id)
    setMods(defaultModules(id))
  }
  return (
    <Modal
      open
      title="Set up your company"
      onClose={onClose}
      className="max-h-[90dvh] max-w-2xl overflow-y-auto"
    >
      <p className="mb-3 text-[13px] text-muted" aria-live="polite">
        Step {step} of 2 - {step === 1 ? 'Company profile' : 'Choose modules'}. This only suggests a setup;
        you can change all of this later in Settings.
      </p>
      {step === 1 ? (
        <div className="space-y-3">
          <h3 className="text-[15px] font-semibold">What does your company do?</h3>
          <BusinessTypePicker current={type} onPick={pick} withOther />
          <CompanySizePicker current={size} onPick={setSize} />
          <p className="text-[13px] text-muted">Country: India (more countries soon)</p>
        </div>
      ) : (
        <fieldset className="space-y-2">
          <legend className="text-[15px] font-semibold">Recommended modules</legend>
          <p className="text-[13px] text-muted">
            Switched on for your kind of company. Turn off what you do not need.
          </p>
          {Object.entries(MODULE_WHY).map(([k, m]) => (
            <label
              key={k}
              className="flex items-start gap-3 rounded-lg border border-line p-3 hover:bg-hover"
            >
              <input
                type="checkbox"
                className="mt-1 size-4"
                checked={mods.includes(k)}
                onChange={(e) => setMods(e.target.checked ? [...mods, k] : mods.filter((x) => x !== k))}
              />
              <span>
                <span className="block text-[14px] font-medium">{m.label}</span>
                <span className="block text-[12px] text-muted">{m.why}</span>
              </span>
            </label>
          ))}
        </fieldset>
      )}
      <ErrorText error={err} />
      <div className="mt-3 flex flex-wrap justify-end gap-2">
        <button
          type="button"
          className={btn}
          disabled={busy}
          onClick={() => save(() => applyBusinessType(ws, 'other'))}
        >
          Skip
        </button>
        {step === 2 && (
          <button type="button" className={btn} disabled={busy} onClick={() => setStep(1)}>
            Back
          </button>
        )}
        {step === 1 ? (
          <button type="button" className={btnPrimary} disabled={!type} onClick={() => setStep(2)}>
            Next
          </button>
        ) : (
          <button
            type="button"
            className={btnPrimary}
            disabled={busy}
            onClick={() =>
              save(() => applyBusinessType(ws, type ?? 'other', { company_size: size, modules: mods }))
            }
          >
            Continue
          </button>
        )}
      </div>
    </Modal>
  )
}
