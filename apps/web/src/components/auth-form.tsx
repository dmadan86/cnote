'use client'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useEffect, useState } from 'react'
import { auth } from '@/lib/api'
import { btnPrimary, ErrorText, Field, inputCls } from './ui'

export function AuthForm({ mode }: { mode: 'login' | 'signup' }) {
  const router = useRouter()
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const signup = mode === 'signup'
  const [challenge, setChallenge] = useState<string>()
  const [recovery, setRecovery] = useState(false)
  const [sso, setSso] = useState(false)

  // The SSO callback sends the browser back here with the reason when it refuses a sign-in.
  useEffect(() => {
    const m = new URLSearchParams(window.location.search).get('sso_error')
    if (m) setError(m)
  }, [])

  function done(r: { mfa_setup_required?: boolean }) {
    if (r.mfa_setup_required) router.push('/account/security')
    else router.push(sameOriginPath(new URLSearchParams(window.location.search).get('next')))
  }

  async function submit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    const get = (k: string) => String(f.get(k) ?? '')
    setBusy(true)
    setError(undefined)
    try {
      if (signup) {
        const r = await auth.signup({
          name: get('name'),
          email: get('email'),
          password: get('password'),
          workspace_name: get('workspace'),
        })
        router.push(r.workspace_id ? `/w/${r.workspace_id}` : '/')
      } else {
        if (challenge) {
          done(await auth.mfaVerify({ mfa_token: challenge, code: get('code') }))
          return
        }
        const r = await auth.login({ email: get('email'), password: get('password') })
        if (r.mfa_required && r.mfa_token) {
          setChallenge(r.mfa_token)
          setBusy(false)
          return
        }
        done(r)
      }
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  if (sso) {
    return (
      <main className="flex min-h-screen items-center justify-center bg-side p-4">
        <form
          action="/api/auth/sso/start"
          method="get"
          className="w-full max-w-sm space-y-4 rounded-lg border border-line bg-canvas p-6"
        >
          <h1 className="text-lg font-semibold">Company single sign-on</h1>
          <Field label="Workspace address">
            {(id) => (
              <input
                id={id}
                name="workspace"
                required
                placeholder="e.g. acme"
                autoComplete="off"
                className={inputCls}
              />
            )}
          </Field>
          <p className="text-[12px] text-muted">
            The short name in your workspace settings. Your administrator can tell you.
          </p>
          <button type="submit" className={`${btnPrimary} w-full`}>
            Continue
          </button>
          <button
            type="button"
            className="block w-full text-center text-[13px] text-accent underline"
            onClick={() => setSso(false)}
          >
            Back to password sign-in
          </button>
        </form>
      </main>
    )
  }

  if (challenge) {
    return (
      <main className="flex min-h-screen items-center justify-center bg-side p-4">
        <form
          onSubmit={submit}
          className="w-full max-w-sm space-y-4 rounded-lg border border-line bg-canvas p-6"
        >
          <h1 className="text-lg font-semibold">
            {recovery ? 'Use a recovery code' : 'Enter your 6-digit code'}
          </h1>
          <p className="text-[13px] text-muted">
            {recovery
              ? 'Each recovery code works once.'
              : 'Open your authenticator app and type the code for C Note.'}
          </p>
          <Field label={recovery ? 'Recovery code' : 'Authentication code'}>
            {(id) => (
              <input
                id={id}
                name="code"
                required
                autoComplete="one-time-code"
                inputMode={recovery ? 'text' : 'numeric'}
                className={inputCls}
              />
            )}
          </Field>
          <ErrorText error={error} />
          <button type="submit" disabled={busy} className={`${btnPrimary} w-full`}>
            Verify
          </button>
          <button
            type="button"
            className="block w-full text-center text-[13px] text-accent underline"
            onClick={() => {
              setRecovery(!recovery)
              setError(undefined)
            }}
          >
            {recovery ? 'Use an authenticator code' : 'Use a recovery code'}
          </button>
          <button
            type="button"
            className="block w-full text-center text-[13px] text-muted underline"
            onClick={() => {
              setChallenge(undefined)
              setRecovery(false)
              setError(undefined)
            }}
          >
            Back
          </button>
        </form>
      </main>
    )
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-side p-4">
      <form
        onSubmit={submit}
        className="w-full max-w-sm space-y-4 rounded-lg border border-line bg-canvas p-6"
      >
        <h1 className="text-lg font-semibold">{signup ? 'Create your workspace' : 'Sign in to C Note'}</h1>
        {signup && (
          <Field label="Your name">
            {(id) => <input id={id} name="name" required className={inputCls} />}
          </Field>
        )}
        <Field label="Email">
          {(id) => (
            <input id={id} name="email" type="email" required autoComplete="email" className={inputCls} />
          )}
        </Field>
        <Field label="Password">
          {(id) => (
            <input
              id={id}
              name="password"
              type="password"
              required
              minLength={8}
              autoComplete={signup ? 'new-password' : 'current-password'}
              className={inputCls}
            />
          )}
        </Field>
        {signup && (
          <Field label="Workspace name">
            {(id) => <input id={id} name="workspace" required className={inputCls} />}
          </Field>
        )}
        <ErrorText error={error} />
        <button type="submit" disabled={busy} className={`${btnPrimary} w-full`}>
          {signup ? 'Sign up' : 'Sign in'}
        </button>
        {!signup && (
          <button
            type="button"
            className="block w-full text-center text-[13px] text-accent underline"
            onClick={() => setSso(true)}
          >
            Use company single sign-on
          </button>
        )}
        <p className="text-center text-[13px] text-muted">
          {signup ? (
            <>
              Have an account?{' '}
              <Link href="/login" className="text-accent underline">
                Sign in
              </Link>
            </>
          ) : (
            <>
              New here?{' '}
              <Link href="/signup" className="text-accent underline">
                Create a workspace
              </Link>
            </>
          )}
        </p>
      </form>
    </main>
  )
}

/** `?next=` after sign-in: resolve like the browser does (tabs/backslashes included) and keep it only if same-origin. */
export function sameOriginPath(next: string | null): string {
  if (!next) return '/'
  try {
    const u = new URL(next, window.location.origin)
    if (u.origin !== window.location.origin) return '/'
    // Dot-segment normalisation can yield "//host" ("/.//host"), which the router would treat as another site.
    return `/${u.pathname.replace(/^[/\\]+/, '')}${u.search}${u.hash}`
  } catch {
    return '/'
  }
}
