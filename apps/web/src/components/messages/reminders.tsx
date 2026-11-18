'use client'
import { useEffect, useState } from 'react'
import { btnPrimary, ErrorText, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import type { EventRow } from './customer-messages'

export type ReminderStep = { kind: 'before' | 'on' | 'after'; days: number; event: string; enabled: boolean }
export type ReminderSettings = { enabled: boolean; steps: ReminderStep[] }

export const SWITCH =
  'relative h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full bg-line transition-colors checked:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent after:absolute after:top-0.5 after:left-0.5 after:h-4 after:w-4 after:rounded-full after:bg-canvas after:shadow-sm after:transition-transform checked:after:translate-x-4 disabled:opacity-50'

export function Switch({
  label,
  checked,
  onChange,
  disabled,
}: {
  label: string
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
}) {
  return (
    <input
      type="checkbox"
      role="switch"
      aria-label={label}
      aria-checked={checked}
      checked={checked}
      disabled={disabled}
      onChange={(e) => onChange(e.target.checked)}
      className={SWITCH}
    />
  )
}

/** Settings > Customer messages > Payment reminders: master switch plus a numbered chain of steps. */
export function PaymentReminders({
  ws,
  events,
  onOpen,
}: {
  ws: string
  events: EventRow[]
  onOpen: (event: string) => void
}) {
  const { data, error } = useCmd<ReminderSettings>(ws, 'reminder.settings_get')
  const [v, setV] = useState<ReminderSettings>()
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  useEffect(() => {
    if (data) setV(data)
  }, [data])
  if (error) return <ErrorText error={error} />
  if (!v) return <div aria-busy="true" className="h-40 animate-pulse rounded-lg bg-hover" />
  const edit = (f: (s: ReminderSettings) => ReminderSettings) => {
    setMsg(undefined)
    setV(f(v))
  }
  const setStep = (i: number, p: Partial<ReminderStep>) =>
    edit((s) => ({ ...s, steps: s.steps.map((st, j) => (j === i ? { ...st, ...p } : st)) }))
  const bad = v.steps.some(
    (s) => s.kind !== 'on' && !(Number.isInteger(s.days) && s.days >= 1 && s.days <= 90),
  )
  return (
    <form
      aria-label="Payment reminders"
      className="space-y-4 rounded-lg border border-line p-4"
      onSubmit={async (e) => {
        e.preventDefault()
        try {
          await cmd(ws, 'reminder.settings_set', { enabled: v.enabled, steps: v.steps })
          setErr(undefined)
          setMsg('Saved')
        } catch (x) {
          setMsg(undefined)
          setErr((x as Error).message)
        }
      }}
    >
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <h3 className="text-[14px] font-semibold">Payment reminders</h3>
          <p className="text-[12px] text-muted">
            Emailed at 10:00 am IST. Stops when the invoice is paid or cancelled.
          </p>
        </div>
        <Switch
          label="Send payment reminders"
          checked={v.enabled}
          onChange={(on) => edit((s) => ({ ...s, enabled: on }))}
        />
      </div>
      <ol className={`space-y-0 ${v.enabled ? '' : 'opacity-50'}`}>
        {v.steps.map((s, i) => {
          const row = events.find((e) => e.event === s.event)
          return (
            <li key={s.event} className="flex gap-3">
              <div className="flex flex-col items-center">
                <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-line text-[12px] font-medium tabular-nums">
                  {i + 1}
                </span>
                {i < v.steps.length - 1 && <span aria-hidden className="w-px flex-1 bg-line" />}
              </div>
              <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-1.5 gap-y-1 pb-4 text-[14px]">
                <span>Send</span>
                <button
                  type="button"
                  onClick={() => onOpen(s.event)}
                  className="font-medium text-accent hover:underline"
                >
                  {row?.label ?? s.event}
                </button>
                {s.kind === 'on' ? (
                  <span>on the due date</span>
                ) : (
                  <>
                    <input
                      type="number"
                      min={1}
                      max={90}
                      aria-label={`Step ${i + 1} days`}
                      value={Number.isNaN(s.days) ? '' : s.days}
                      onChange={(e) => setStep(i, { days: e.target.valueAsNumber })}
                      className="w-14 rounded-md border border-line bg-canvas px-2 py-0.5 text-center tabular-nums"
                    />
                    <span>
                      {s.days === 1 ? 'day' : 'days'}{' '}
                      {s.kind === 'before' ? 'before the due date' : 'after the due date'}
                    </span>
                  </>
                )}
                {!s.enabled && <Pill>Off</Pill>}
                <span className="ml-auto">
                  <Switch
                    label={`Step ${i + 1} on`}
                    checked={s.enabled}
                    onChange={(on) => setStep(i, { enabled: on })}
                  />
                </span>
              </div>
            </li>
          )
        })}
      </ol>
      <p className="text-[12px] text-muted">
        Missed days are not sent later: if a step's day has already passed, that reminder is skipped.
      </p>
      <ErrorText error={bad ? 'Days must be a whole number from 1 to 90' : err} />
      <div className="flex items-center gap-3">
        <button type="submit" className={btnPrimary} disabled={bad}>
          Save reminders
        </button>
        <span role="status" className="text-[13px] text-ok">
          {msg}
        </span>
      </div>
    </form>
  )
}
