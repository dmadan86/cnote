'use client'
import { useId, useState } from 'react'
import { type EmailMode, type NotifyPrefs, notifyPrefsSet } from '@/lib/docs'
import { useCmd } from '@/lib/hooks'
import { ErrorText, inputCls } from './ui'

const MODES: { value: EmailMode; label: string; help: string }[] = [
  {
    value: 'smart',
    label: 'Smart',
    help: 'Mentions, tasks assigned to you, shares and access requests within a few minutes. Comments and page updates in the daily digest.',
  },
  { value: 'digest', label: 'Daily digest only', help: 'Everything in one email a day.' },
  { value: 'off', label: 'No email', help: 'Nothing is sent. Notifications stay in the bell.' },
]

const hours = Array.from({ length: 24 }, (_, h) => h)
const hourLabel = (h: number) => `${String(h).padStart(2, '0')}:00`

/** Email settings of the signed-in person in this workspace. Every change is saved at once; a failed save restores the page. */
export function NotificationSettings({ ws }: { ws: string }) {
  const { data: loaded, error: loadError } = useCmd<NotifyPrefs>(ws, 'notify_prefs.get')
  const [mine, setMine] = useState<Partial<NotifyPrefs>>({}) // what this page saved, shown at once
  const [error, setError] = useState<Error>()
  const [saved, setSaved] = useState('')
  const hourId = useId()
  if (loadError) return <ErrorText error={loadError} />
  if (!loaded) return null
  const data = { ...loaded, ...mine }

  async function save(input: Parameters<typeof notifyPrefsSet>[1], done: string) {
    setError(undefined)
    setSaved('')
    const before = mine
    setMine({ ...mine, ...input })
    try {
      await notifyPrefsSet(ws, input)
      setSaved(done)
    } catch (e) {
      setMine(before)
      setError(e as Error)
    }
  }
  const off = data.email_mode === 'off'

  return (
    <div className="space-y-6">
      {!data.email_configured && (
        <p role="status" className="rounded-md border border-line p-3 text-[13px] text-muted">
          This server cannot send email yet, so these settings have no effect. Notifications still appear in
          the bell.
        </p>
      )}
      {(data.direct_off || data.digest_off) && (
        <div role="status" className="rounded-md border border-line p-3 text-[13px]">
          <p className="font-medium">You unsubscribed from some of these emails.</p>
          <p className="mt-1 text-muted">
            {data.direct_off && data.digest_off
              ? 'Quick emails and the daily digest are both off.'
              : data.direct_off
                ? 'Quick emails are off.'
                : 'The daily digest is off.'}{' '}
            You can switch them back on below.
          </p>
        </div>
      )}
      <fieldset className="space-y-2">
        <legend className="mb-2 text-[13px] font-medium">When should we email you?</legend>
        {MODES.map((m) => (
          <label
            key={m.value}
            className="flex cursor-pointer items-start gap-3 rounded-md border border-line p-3 has-[:checked]:border-accent"
          >
            <input
              type="radio"
              name="email_mode"
              value={m.value}
              checked={data.email_mode === m.value}
              onChange={() => save({ email_mode: m.value }, 'Saved.')}
              className="mt-1"
            />
            <span>
              <span className="block text-[14px] font-medium">{m.label}</span>
              <span className="block text-[13px] text-muted">{m.help}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <fieldset disabled={off} className="space-y-3 disabled:opacity-60">
        <legend className="mb-1 text-[13px] font-medium">Email types</legend>
        <label className="flex items-start gap-3 text-[14px]">
          <input
            type="checkbox"
            checked={!data.direct_off}
            disabled={data.email_mode !== 'smart'}
            onChange={(e) => save({ direct_off: !e.target.checked }, 'Saved.')}
            className="mt-1"
          />
          <span>
            Quick emails
            <span className="block text-[13px] text-muted">
              Mentions, assigned tasks, shares and access requests. Only in Smart mode.
            </span>
          </span>
        </label>
        <label className="flex items-start gap-3 text-[14px]">
          <input
            type="checkbox"
            checked={!data.digest_off}
            onChange={(e) => save({ digest_off: !e.target.checked }, 'Saved.')}
            className="mt-1"
          />
          <span>
            Daily digest
            <span className="block text-[13px] text-muted">
              One email a day with what you missed. Nothing is sent on a quiet day.
            </span>
          </span>
        </label>
        <div>
          <label htmlFor={hourId} className="mb-1 block text-[13px]">
            Digest time (India Standard Time)
          </label>
          <select
            id={hourId}
            className={`${inputCls} max-w-40`}
            value={data.digest_hour}
            disabled={data.digest_off}
            onChange={(e) => save({ digest_hour: Number(e.target.value) }, 'Saved.')}
          >
            {hours.map((h) => (
              <option key={h} value={h}>
                {hourLabel(h)}
              </option>
            ))}
          </select>
        </div>
      </fieldset>
      <p className="text-[12px] text-muted">
        Emails name the person and the page, never its text or comments. A page you cannot open any more is
        left out. An email already sent cannot be taken back.
      </p>
      <p role="status" aria-live="polite" className="min-h-5 text-[13px] text-muted">
        {saved}
      </p>
      <ErrorText error={error} />
    </div>
  )
}
