'use client'
import { useState } from 'react'
import { applyBusinessType, BUSINESS_TYPES } from '@/lib/business-types'
import { btn, ErrorText, Modal } from './ui'

/** Radio-group card grid. Native radios give arrow-key navigation; picking a card applies it. */
export function BusinessTypePicker({
  ws,
  current,
  onDone,
}: {
  ws: string
  current?: string
  onDone: () => void
}) {
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [sel, setSel] = useState(current)
  const pick = async (id: string) => {
    setSel(id)
    setBusy(true)
    try {
      await applyBusinessType(ws, id)
      onDone()
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <fieldset disabled={busy} className="space-y-2">
      <legend className="sr-only">Business type</legend>
      <div className="grid gap-2 sm:grid-cols-2">
        {BUSINESS_TYPES.map((t) => (
          <label
            key={t.id}
            className="cursor-pointer rounded-lg border border-line p-3 hover:bg-hover has-[:checked]:border-accent has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent"
          >
            <input
              type="radio"
              name="business_type"
              value={t.id}
              checked={sel === t.id}
              onChange={() => pick(t.id)}
              className="sr-only"
            />
            <span className="block text-[14px] font-medium">{t.name}</span>
            <span className="block text-[12px] text-muted">{t.blurb}</span>
          </label>
        ))}
      </div>
      <ErrorText error={err} />
    </fieldset>
  )
}

/** First-run chooser for owners/admins. */
export function BusinessTypeChooser({
  ws,
  onDone,
  onClose,
}: {
  ws: string
  onDone: () => void
  onClose: () => void
}) {
  return (
    <Modal open title="What kind of business do you run?" onClose={onClose} className="max-w-2xl">
      <p className="mb-3 text-[13px] text-muted">
        We switch on the right modules and name your deal stages to match. You can change this later in
        Settings.
      </p>
      <BusinessTypePicker ws={ws} onDone={onDone} />
      <div className="mt-3 text-right">
        <button
          type="button"
          className={btn}
          onClick={() => applyBusinessType(ws, 'other').then(onDone, onDone)}
        >
          Skip
        </button>
      </div>
    </Modal>
  )
}
