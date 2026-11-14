'use client'
import { useCallback, useEffect, useState } from 'react'
import { auth, type SessionRow } from '@/lib/api'
import { btn, ErrorText } from '../ui'

/** "Chrome on macOS" from a user agent; "Unknown device" when nothing is recognisable. */
export function device(ua: string): string {
  const browser = /Edg\//.test(ua)
    ? 'Edge'
    : /Firefox\//.test(ua)
      ? 'Firefox'
      : /Chrome\//.test(ua)
        ? 'Chrome'
        : /Safari\//.test(ua)
          ? 'Safari'
          : ''
  const os = /iPhone|iPad/.test(ua)
    ? 'iOS'
    : /Android/.test(ua)
      ? 'Android'
      : /Mac OS X|Macintosh/.test(ua)
        ? 'macOS'
        : /Windows/.test(ua)
          ? 'Windows'
          : /Linux/.test(ua)
            ? 'Linux'
            : ''
  return browser && os ? `${browser} on ${os}` : browser || os || 'Unknown device'
}

const METHOD = { pwd: 'Password', mfa: 'Password + 2FA', sso: 'Single sign-on' } as const
const when = (iso: string | null) =>
  iso ? new Date(iso).toLocaleString(undefined, { timeZoneName: 'short' }) : 'unknown'

/** Where this person is signed in; revoke one other session or all of them. */
export function SessionsCard() {
  const [rows, setRows] = useState<SessionRow[]>()
  const [error, setError] = useState<Error>()
  const [msg, setMsg] = useState<string>()
  const [busy, setBusy] = useState(false)
  const load = useCallback(() => {
    auth
      .sessions()
      .then((r) => setRows(r.sessions))
      .catch((e) => setError(e as Error))
  }, [])
  useEffect(load, [load])

  async function run(f: () => Promise<string>) {
    setBusy(true)
    setError(undefined)
    try {
      setMsg(await f())
      load()
    } catch (e) {
      setError(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const others = rows?.filter((r) => !r.current).length ?? 0

  return (
    <section
      className="space-y-3 rounded-lg border border-line bg-canvas p-4 sm:p-6"
      aria-labelledby="sessions-h"
    >
      <h2 id="sessions-h" className="text-[15px] font-semibold">
        Active sessions
      </h2>
      <p className="text-[13px] text-muted">
        Everywhere your account is signed in. Revoke any you do not recognise.
      </p>
      {!rows && !error && <p className="text-[13px] text-muted">Loading...</p>}
      <ul className="space-y-2">
        {rows?.map((s) => (
          <li key={s.id} data-testid="session-row" className="rounded-md border border-line p-3 text-[13px]">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{device(s.user_agent)}</span>
              {s.current && <span className="rounded bg-side px-1.5 py-0.5 text-[12px]">This device</span>}
              <span className="rounded bg-side px-1.5 py-0.5 text-[12px]">{METHOD[s.amr]}</span>
            </div>
            <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-2 text-muted">
              <dt>IP address</dt>
              <dd className="break-all">{s.ip || 'unknown'}</dd>
              <dt>Signed in</dt>
              <dd>{when(s.created_at)}</dd>
              <dt>Last active</dt>
              <dd>{when(s.last_seen_at)}</dd>
            </dl>
            {!s.current && (
              <button
                type="button"
                disabled={busy}
                className={`${btn} mt-2`}
                aria-label={`Revoke session: ${device(s.user_agent)}, ${s.ip || 'unknown IP'}`}
                onClick={() => run(async () => (await auth.revokeSession(s.id), 'Session revoked.'))}
              >
                Revoke
              </button>
            )}
          </li>
        ))}
      </ul>
      <ErrorText error={error} />
      <div role="status" className="text-[13px] text-muted">
        {msg}
      </div>
      {others > 0 && (
        <button
          type="button"
          disabled={busy}
          className={btn}
          onClick={() => {
            if (
              !window.confirm(
                `Sign out ${others} other session${others === 1 ? '' : 's'}? This device stays signed in.`,
              )
            )
              return
            run(async () => {
              const r = await auth.revokeOtherSessions()
              return `Signed out ${r.revoked} other session${r.revoked === 1 ? '' : 's'}.`
            })
          }}
        >
          Sign out all other devices
        </button>
      )}
    </section>
  )
}
