'use client'
import { use, useEffect, useState } from 'react'
import { btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { api } from '@/lib/api'

/** Public inquiry form: no session, no app shell. The token in the link is the only authorization. */
export default function Page({ params }: { params: Promise<{ token: string }> }) {
  const { token } = use(params)
  const [name, setName] = useState<string>()
  const [missing, setMissing] = useState(false)
  const [state, setState] = useState<'idle' | 'sending' | 'done'>('idle')
  const [err, setErr] = useState<string>()
  useEffect(() => {
    api<{ workspace: string }>(`/api/public/inquiry/${token}`).then(
      (r) => setName(r.workspace),
      () => setMissing(true),
    )
  }, [token])
  if (missing) return <main className="mx-auto max-w-md p-6">This form is not available.</main>
  if (!name) return <main className="mx-auto max-w-md p-6 text-muted">Loading...</main>
  if (state === 'done')
    return (
      <main className="mx-auto max-w-md space-y-2 p-6">
        <h1 className="text-xl font-semibold">Thank you</h1>
        <p role="status">{name} has your message and will get back to you soon.</p>
      </main>
    )
  return (
    <main className="mx-auto max-w-md space-y-4 px-4 py-8">
      <h1 className="text-xl font-semibold">Contact {name}</h1>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>
          setState('sending')
          try {
            await api(`/api/public/inquiry/${token}`, { body: f })
            setState('done')
          } catch (x) {
            setErr((x as Error).message)
            setState('idle') // the typed values stay in the fields
          }
        }}
      >
        <Field label="Your name">
          {(id) => (
            <input
              id={id}
              name="name"
              required
              maxLength={100}
              autoComplete="name"
              className={`${inputCls} min-h-11 text-[16px]`}
            />
          )}
        </Field>
        <Field label="Phone number">
          {(id) => (
            <input
              id={id}
              name="phone"
              type="tel"
              required
              autoComplete="tel"
              className={`${inputCls} min-h-11 text-[16px]`}
            />
          )}
        </Field>
        <Field label="Email (optional)">
          {(id) => (
            <input
              id={id}
              name="email"
              type="email"
              autoComplete="email"
              className={`${inputCls} min-h-11 text-[16px]`}
            />
          )}
        </Field>
        <Field label="How can we help?">
          {(id) => (
            <textarea
              id={id}
              name="message"
              rows={4}
              maxLength={2000}
              className={`${inputCls} text-[16px]`}
            />
          )}
        </Field>
        {/* honeypot: off-screen, out of tab order and hidden from screen readers; people never fill it */}
        <div aria-hidden="true" style={{ position: 'absolute', left: -9999 }}>
          <input name="hp" tabIndex={-1} autoComplete="off" defaultValue="" />
        </div>
        <ErrorText error={err} />
        <button
          type="submit"
          disabled={state === 'sending'}
          className={`${btnPrimary} min-h-11 w-full text-[15px]`}
        >
          Send
        </button>
        <p className="text-[12px] text-muted">
          {name} will use your details only to respond to this inquiry.
        </p>
      </form>
    </main>
  )
}
