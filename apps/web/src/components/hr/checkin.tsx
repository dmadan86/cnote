'use client'
import { useState } from 'react'
import { ErrorText } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { timeOf, useRole } from './shared'

type Today = { day: string; me: { status: string; in_at: string | null; out_at: string | null } | null }

/** The staff member's own check in / out, one big button. Nothing shows as done until the server confirms. */
export function CheckInCard() {
  const { ws, role } = useRole()
  const { data, reload } = useCmd<Today>(ws, 'attendance.today', {}, role !== 'guest')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  if (role === 'guest' || !data?.me) return null
  const { me } = data
  const checkedIn = !!me.in_at
  const done = checkedIn && !!me.out_at
  const act = async (name: 'attendance.check_in' | 'attendance.check_out') => {
    setBusy(true)
    setErr(undefined)
    try {
      await cmd(ws, name)
      reload()
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <section aria-label="Your attendance" className="rounded-lg border border-line p-3">
      <p className="text-[13px] font-medium text-muted">Today, {data.day}</p>
      <p className="mt-1 text-[15px]" aria-live="polite">
        {me.status === 'leave'
          ? 'You are on leave today.'
          : checkedIn
            ? `Checked in at ${timeOf(me.in_at)}${done ? `, out at ${timeOf(me.out_at)}` : ''}`
            : 'You have not checked in yet.'}
      </p>
      <button
        type="button"
        disabled={busy}
        onClick={() => act(checkedIn ? 'attendance.check_out' : 'attendance.check_in')}
        className="mt-3 inline-flex min-h-14 w-full items-center justify-center rounded-md bg-accent px-4 text-[16px] font-semibold text-accent-fg hover:opacity-90 disabled:opacity-50"
      >
        {busy ? 'Saving...' : checkedIn ? (done ? 'Check out again' : 'Check out') : 'Check in'}
      </button>
      <ErrorText error={err} />
    </section>
  )
}
