'use client'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { auth } from '@/lib/api'
import { btnPrimary, ErrorText, Field, inputCls } from './ui'

export function AuthForm({ mode }: { mode: 'login' | 'signup' }) {
  const router = useRouter()
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const signup = mode === 'signup'

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
        await auth.login({ email: get('email'), password: get('password') })
        router.push(sameOriginPath(new URLSearchParams(window.location.search).get('next')))
      }
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
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
