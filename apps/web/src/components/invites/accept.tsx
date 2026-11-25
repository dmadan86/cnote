'use client'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useCallback, useEffect, useState } from 'react'
import { btn, btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { api, auth, CmdFailure } from '@/lib/api'

type Peek =
  | {
      state: 'ok'
      workspace_name: string
      inviter_name: string
      email: string
      role: string
      account: 'new' | 'existing'
      signed_in_as: string
    }
  | { state: 'invalid' | 'expired' | 'member' | 'deactivated' | 'busy'; message: string }

const Shell = ({ children }: { children: React.ReactNode }) => (
  <main className="flex min-h-screen items-center justify-center bg-side p-4">
    <div className="w-full max-w-sm space-y-4 rounded-lg border border-line bg-canvas p-6">{children}</div>
  </main>
)

/**
 * /invite/{token}: three states (sign in as the invited address, choose a password for a new address, or a plain message).
 * The token stays in this page's own requests: it is never put in another URL (the sign-in form is here, not behind ?next=).
 */
export function AcceptInvite({ token }: { token: string }) {
  const router = useRouter()
  const [peek, setPeek] = useState<Peek>()
  const [failed, setFailed] = useState(false)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [mfa, setMfa] = useState<string>()
  const url = `/api/invite/${token}`

  const load = useCallback(() => {
    api<Peek>(url)
      .then(setPeek)
      .catch((e) => {
        // a rate limit still carries a state and message
        setFailed(true)
        setError((e as Error).message)
      })
  }, [url])
  // biome-ignore lint/correctness/useExhaustiveDependencies: load once per link
  useEffect(load, [])

  const joined = (r: { workspace_id: string; mfa_setup_required?: boolean }) =>
    router.push(r.mfa_setup_required ? '/account/security' : `/w/${r.workspace_id}`)

  async function accept(body?: object) {
    setBusy(true)
    setError(undefined)
    try {
      const r = await api<Peek | { state: 'joined'; workspace_id: string; mfa_setup_required?: boolean }>(
        `${url}/accept`,
        {
          body: body ?? {},
        },
      )
      if (r.state === 'joined') joined(r as { workspace_id: string; mfa_setup_required?: boolean })
      else setPeek(r as Peek)
    } catch (e) {
      if (e instanceof CmdFailure && e.status === 401)
        load() // not signed in as the invited address: the page shows the sign-in form
      else setError((e as Error).message)
    }
    setBusy(false)
  }

  if (failed)
    return (
      <Shell>
        <h1 className="text-lg font-semibold">Invitation</h1>
        <p role="alert" className="text-[13px]">
          {error ?? 'Something went wrong.'}
        </p>
        <button
          type="button"
          className={btn}
          onClick={() => {
            setFailed(false)
            load()
          }}
        >
          Try again
        </button>
      </Shell>
    )
  if (!peek)
    return (
      <Shell>
        <div aria-busy="true" className="h-24 animate-pulse rounded-lg bg-hover" />
      </Shell>
    )

  if (peek.state !== 'ok')
    return (
      <Shell>
        <h1 className="text-lg font-semibold">Invitation</h1>
        <p className="text-[14px]">{peek.message}</p>
        <Link href="/" className="block text-center text-[13px] text-accent underline">
          Go to C Note
        </Link>
      </Shell>
    )

  const heading = (
    <>
      <h1 className="text-lg font-semibold">Join {peek.workspace_name}</h1>
      <p className="text-[13px] text-muted">
        {peek.inviter_name || 'An admin'} invited <strong>{peek.email}</strong> as{' '}
        {peek.role === 'admin' ? 'an' : 'a'} {peek.role}.
      </p>
    </>
  )
  const same = peek.signed_in_as.toLowerCase() === peek.email.toLowerCase()

  // 1. Signed in as the invited address: one button.
  if (same)
    return (
      <Shell>
        {heading}
        <ErrorText error={error} />
        <button
          type="button"
          disabled={busy}
          onClick={() => accept()}
          className={`${btnPrimary} min-h-11 w-full`}
        >
          Join {peek.workspace_name}
        </button>
      </Shell>
    )

  // 2. A new address: choose a password. The account is created verified, because the link reached this inbox.
  if (peek.account === 'new')
    return (
      <Shell>
        {heading}
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            const f = new FormData(e.currentTarget)
            void accept({ name: String(f.get('name') ?? ''), password: String(f.get('password') ?? '') })
          }}
        >
          <Field label="Your name">
            {(id) => <input id={id} name="name" required autoComplete="name" className={inputCls} />}
          </Field>
          <Field label="Choose a password">
            {(id) => (
              <input
                id={id}
                name="password"
                type="password"
                required
                minLength={8}
                autoComplete="new-password"
                className={inputCls}
              />
            )}
          </Field>
          <ErrorText error={error} />
          <button type="submit" disabled={busy} className={`${btnPrimary} min-h-11 w-full`}>
            Create account and join
          </button>
        </form>
      </Shell>
    )

  // 3. An existing account: sign in as that address first.
  return (
    <Shell>
      {heading}
      {peek.signed_in_as && (
        <p role="status" className="text-[13px]">
          You are signed in as {peek.signed_in_as}. This invitation is for {peek.email}.{' '}
          <button
            type="button"
            className="text-accent underline"
            onClick={async () => {
              await auth.logout().catch(() => {})
              load()
            }}
          >
            Sign out
          </button>
        </p>
      )}
      <form
        className="space-y-4"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          setBusy(true)
          setError(undefined)
          try {
            if (mfa) {
              await auth.mfaVerify({ mfa_token: mfa, code: String(f.get('code') ?? '') })
              setMfa(undefined)
            } else {
              const r = await auth.login({ email: peek.email, password: String(f.get('password') ?? '') })
              if (r.mfa_required && r.mfa_token) {
                setMfa(r.mfa_token)
                setBusy(false)
                return
              }
            }
            load() // now signed in as that address: the page offers the one button
          } catch (x) {
            setError((x as Error).message)
          }
          setBusy(false)
        }}
      >
        {mfa ? (
          <Field label="Authentication code">
            {(id) => (
              <input
                id={id}
                name="code"
                required
                autoComplete="one-time-code"
                inputMode="numeric"
                className={inputCls}
              />
            )}
          </Field>
        ) : (
          <>
            <Field label="Email">
              {(id) => (
                <input id={id} value={peek.email} readOnly aria-readonly="true" className={inputCls} />
              )}
            </Field>
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
          </>
        )}
        <ErrorText error={error} />
        <button type="submit" disabled={busy} className={`${btnPrimary} min-h-11 w-full`}>
          {mfa ? 'Verify' : 'Sign in to join'}
        </button>
      </form>
      <p className="text-[12px] text-muted">
        This address signs in with company single sign-on? Sign in that way from the{' '}
        <Link href="/login" className="text-accent underline">
          sign-in page
        </Link>
        , then open this link again.
      </p>
    </Shell>
  )
}
