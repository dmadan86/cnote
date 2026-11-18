'use client'
import { useState } from 'react'
import { Switch } from '@/components/messages/reminders'
import { useShell } from '@/components/shell'
import { ErrorText, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

type Entry = { step: string; label?: string; status: 'queued' | 'sent' | 'failed' | 'cancelled'; at: string }
type Log = { paused: boolean; next: { label: string; date: string } | null; items: Entry[] }
const TONE = { sent: 'ok', queued: 'neutral', failed: 'bad', cancelled: 'warn' } as const
const when = (d: string) =>
  new Date(d).toLocaleDateString('en-IN', { day: 'numeric', month: 'short', timeZone: 'Asia/Kolkata' })

/** Invoice view: what is scheduled next, what was sent, and a Pause/Resume switch. */
export function RemindersPanel({ invoiceId }: { invoiceId: string }) {
  const { ws } = useShell()
  const { data, error, reload } = useCmd<Log>(ws, 'reminder.log_list', { invoice_id: invoiceId })
  const [err, setErr] = useState<string>()
  if (error) return null // no finance access or reminders unavailable: hide quietly
  if (!data) return null
  return (
    <section aria-label="Reminders" className="rounded-lg border border-line p-4">
      <div className="mb-1 flex items-center gap-3">
        <h2 className="flex-1 text-[14px] font-semibold">Reminders</h2>
        <span className="text-[12px] text-muted">{data.paused ? 'Paused' : 'Pause'}</span>
        <Switch
          label="Pause reminders"
          checked={data.paused}
          onChange={async (paused) => {
            try {
              await cmd(ws, 'reminder.pause', { id: invoiceId, paused })
              setErr(undefined)
              reload()
            } catch (x) {
              setErr((x as Error).message)
            }
          }}
        />
      </div>
      <p className="text-[13px] text-muted">
        {data.paused
          ? 'Reminders are paused for this invoice.'
          : data.next
            ? `Next: ${data.next.label} on ${when(data.next.date)}`
            : 'No more reminders scheduled.'}
      </p>
      <ErrorText error={err} />
      {data.items.length > 0 && (
        <ul className="mt-2 divide-y divide-line">
          {data.items.map((e) => (
            <li key={`${e.step}-${e.at}`} className="flex items-center gap-2 py-1.5 text-[13px]">
              <span className="flex-1">{e.label ?? e.step}</span>
              <Pill tone={TONE[e.status]}>{e.status}</Pill>
              <span className="w-14 text-right text-muted">{when(e.at)}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
