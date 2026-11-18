'use client'
import { Clock, LogIn } from 'lucide-react'
import { useState } from 'react'
import { ErrorText } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { timeOf, useRole } from './shared'

type Today = { day: string; me: { status: string; in_at: string | null; out_at: string | null } | null }

/** The staff member's own check in / out, one row with the button (44px tap target on phones). Nothing shows as done until the server confirms. */
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
    <section
      aria-label="Your attendance"
      className="flex flex-wrap items-center gap-3 rounded-lg border border-line p-3"
    >
      <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-line bg-hover text-muted">
        <Clock size={16} aria-hidden />
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-[12px] text-muted">Today, {data.day}</p>
        <p className="text-[14px] font-medium" aria-live="polite">
          {me.status === 'leave'
            ? 'You are on leave today.'
            : checkedIn
              ? `Checked in at ${timeOf(me.in_at)}${done ? `, out at ${timeOf(me.out_at)}` : ''}`
              : 'You have not checked in yet.'}
        </p>
      </div>
      <button
        type="button"
        disabled={busy}
        onClick={() => act(checkedIn ? 'attendance.check_out' : 'attendance.check_in')}
        className="inline-flex min-h-11 items-center justify-center gap-1.5 rounded-md bg-accent px-4 text-[14px] font-medium text-accent-fg hover:opacity-90 disabled:opacity-50 sm:min-h-9"
      >
        <LogIn size={15} aria-hidden />
        {busy ? 'Saving...' : checkedIn ? (done ? 'Check out again' : 'Check out') : 'Check in'}
      </button>
      <ErrorText error={err} />
    </section>
  )
}
