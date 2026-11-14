'use client'
import { useCallback, useEffect, useRef, useState } from 'react'
import { auth } from '@/lib/api'
import { qrMatrix, qrPath } from '@/lib/qr'
import { btn, btnPrimary, ErrorText, Field, inputCls } from '../ui'

type Phase = 'loading' | 'off' | 'setup' | 'codes' | 'on' | 'disable' | 'regen'

export function MfaCard() {
  const [phase, setPhase] = useState<Phase>('loading')
  const [left, setLeft] = useState(0)
  const [setup, setSetup] = useState<{ secret: string; otpauth_uri: string }>()
  const [codes, setCodes] = useState<string[]>([])
  const [error, setError] = useState<Error>()
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)
  const heading = useRef<HTMLHeadingElement>(null)

  const load = useCallback(async () => {
    try {
      const s = await auth.mfaStatus()
      setLeft(s.recovery_codes_left)
      setPhase(s.enabled ? 'on' : 'off')
    } catch (e) {
      setError(e as Error)
      setPhase('off')
    }
  }, [])
  useEffect(() => {
    load()
  }, [load])
  // Move focus to each new step's heading (screen readers announce the change).
  // biome-ignore lint/correctness/useExhaustiveDependencies: phase is the trigger
  useEffect(() => heading.current?.focus(), [phase])

  const run = async (f: () => Promise<void>) => {
    setBusy(true)
    setError(undefined)
    try {
      await f()
    } catch (e) {
      setError(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const val = (e: React.FormEvent<HTMLFormElement>, k: string) =>
    String(new FormData(e.currentTarget).get(k) ?? '')

  const h2 = (t: string) => (
    <h2 ref={heading} tabIndex={-1} className="text-[15px] font-semibold outline-none">
      {t}
    </h2>
  )

  return (
    <section className="space-y-4 rounded-lg border border-line bg-canvas p-4 sm:p-6" aria-live="polite">
      {phase === 'loading' && <p className="text-[13px] text-muted">Loading...</p>}

      {phase === 'off' && (
        <>
          {h2('Two-factor authentication is off')}
          <p className="text-[13px] text-muted">
            Add a second step to sign-in: a 6-digit code from an authenticator app such as Google
            Authenticator, 1Password or Authy. Workspaces can require it for admins and owners.
          </p>
          <ErrorText error={error} />
          <button
            type="button"
            className={btnPrimary}
            disabled={busy}
            onClick={() =>
              run(async () => {
                setSetup(await auth.mfaSetup())
                setPhase('setup')
              })
            }
          >
            Set up an authenticator app
          </button>
        </>
      )}

      {phase === 'setup' && setup && (
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            const code = val(e, 'code')
            run(async () => {
              setCodes((await auth.mfaConfirm(code)).recovery_codes)
              setPhase('codes')
            })
          }}
        >
          {h2('Set up two-factor authentication')}
          <p className="text-[13px]">
            1. Scan this code with your authenticator app, or type the key below into it.
          </p>
          <Qr text={setup.otpauth_uri} />
          <div>
            <div className="text-[12px] font-medium text-muted">Setup key</div>
            <code data-testid="mfa-secret" className="block break-all rounded bg-side px-2 py-1 text-[13px]">
              {setup.secret}
            </code>
          </div>
          <Field label="2. Enter the 6-digit code from the app">
            {(id) => (
              <input
                id={id}
                name="code"
                required
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9 ]{6,7}"
                className={inputCls}
              />
            )}
          </Field>
          <ErrorText error={error} />
          <div className="flex flex-wrap gap-2">
            <button type="submit" disabled={busy} className={btnPrimary}>
              Turn on two-factor authentication
            </button>
            <button type="button" className={btn} onClick={() => setPhase('off')}>
              Cancel
            </button>
          </div>
        </form>
      )}

      {phase === 'codes' && (
        <div className="space-y-3">
          {h2('Save your recovery codes')}
          <p className="text-[13px] text-muted">
            Each code works once if you lose your phone. This is the only time they are shown, so keep them
            somewhere safe.
          </p>
          <ul
            data-testid="recovery-codes"
            className="grid grid-cols-2 gap-1 rounded bg-side p-3 font-mono text-[13px]"
          >
            {codes.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className={btn}
              onClick={async () => {
                await navigator.clipboard.writeText(codes.join('\n')).catch(() => {})
                setCopied(true)
              }}
            >
              {copied ? 'Copied' : 'Copy codes'}
            </button>
            <a
              className={btn}
              download="cnote-recovery-codes.txt"
              href={`data:text/plain;charset=utf-8,${encodeURIComponent(codes.join('\n'))}`}
            >
              Download
            </a>
            <button
              type="button"
              className={btnPrimary}
              onClick={() => {
                setCodes([])
                load()
              }}
            >
              I saved my recovery codes
            </button>
          </div>
        </div>
      )}

      {phase === 'on' && (
        <>
          <div className="flex items-center gap-2">
            {h2('Two-factor authentication is on')}
            <span className="rounded bg-side px-1.5 py-0.5 text-[12px]">Enabled</span>
          </div>
          <p className="text-[13px] text-muted">
            {left} recovery code{left === 1 ? '' : 's'} left.
          </p>
          <div className="flex flex-wrap gap-2">
            <button type="button" className={btn} onClick={() => setPhase('regen')}>
              Regenerate recovery codes...
            </button>
            <button type="button" className={btn} onClick={() => setPhase('disable')}>
              Turn off...
            </button>
          </div>
        </>
      )}

      {phase === 'regen' && (
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            const b = { password: val(e, 'password'), code: val(e, 'code') }
            run(async () => {
              setCodes((await auth.mfaRecovery(b)).recovery_codes)
              setPhase('codes')
            })
          }}
        >
          {h2('Regenerate recovery codes')}
          <p className="text-[13px] text-muted">
            This makes a new set of 10 codes. Every code you have now, used or not, stops working. Confirm
            with your password and a current code from your authenticator app.
          </p>
          <Field label="Password">
            {(id) => (
              <input
                id={id}
                name="password"
                type="password"
                required
                autoComplete="current-password"
                className={inputCls}
              />
            )}
          </Field>
          <Field label="Authenticator app code">
            {(id) => (
              <input
                id={id}
                name="code"
                required
                inputMode="numeric"
                autoComplete="one-time-code"
                className={inputCls}
              />
            )}
          </Field>
          <ErrorText error={error} />
          <div className="flex flex-wrap gap-2">
            <button type="submit" disabled={busy} className={btnPrimary}>
              Generate new codes
            </button>
            <button type="button" className={btn} onClick={() => setPhase('on')}>
              Cancel
            </button>
          </div>
        </form>
      )}

      {phase === 'disable' && (
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            const b = { password: val(e, 'password'), code: val(e, 'code') }
            run(async () => {
              await auth.mfaDisable(b)
              await load()
            })
          }}
        >
          {h2('Turn off two-factor authentication')}
          <p className="text-[13px] text-muted">
            Your account will be protected by your password alone. Confirm with your password and a current
            code (or a recovery code). A workspace that requires it for admins will refuse.
          </p>
          <Field label="Password">
            {(id) => (
              <input
                id={id}
                name="password"
                type="password"
                required
                autoComplete="current-password"
                className={inputCls}
              />
            )}
          </Field>
          <Field label="Authentication or recovery code">
            {(id) => <input id={id} name="code" required autoComplete="one-time-code" className={inputCls} />}
          </Field>
          <ErrorText error={error} />
          <div className="flex flex-wrap gap-2">
            <button type="submit" disabled={busy} className={btnPrimary}>
              Turn off
            </button>
            <button type="button" className={btn} onClick={() => setPhase('on')}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </section>
  )
}

function Qr({ text }: { text: string }) {
  const { d, size } = qrPath(qrMatrix(text, 'M'))
  return (
    <svg
      role="img"
      aria-label="QR code for your authenticator app"
      viewBox={`0 0 ${size} ${size}`}
      className="h-44 w-44 max-w-full rounded bg-white"
      shapeRendering="crispEdges"
    >
      <path d={d} fill="#000" />
    </svg>
  )
}
