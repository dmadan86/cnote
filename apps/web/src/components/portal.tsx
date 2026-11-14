'use client'
import { useRouter } from 'next/navigation'
import { useCallback, useEffect, useState } from 'react'
import { btnPrimary, ErrorText, Field, inputCls, Pill } from '@/components/ui'
import { api, CmdFailure } from '@/lib/api'

type Msg = { from: 'you' | 'support'; body: string; created_at: string }
type PTicket = {
  ref: string
  number: number
  title: string
  status: string
  created_at: string
  messages: Msg[]
}

const base = (ws: string) => `/api/public/portal/${ws}`
const cls = `${inputCls} min-h-11 text-[16px]`
const shell = 'mx-auto max-w-2xl space-y-4 px-4 py-8'
const when = (s: string) => new Date(s).toLocaleString('en-IN', { dateStyle: 'medium', timeStyle: 'short' })

function SignIn({ ws }: { ws: string }) {
  const [name, setName] = useState<string>()
  const [missing, setMissing] = useState(false)
  const [sent, setSent] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  useEffect(() => {
    api<{ workspace: string }>(`${base(ws)}/info`).then(
      (r) => setName(r.workspace),
      () => setMissing(true),
    )
  }, [ws])
  if (missing) return <main className={shell}>This support portal is not available.</main>
  if (!name) return <main className={`${shell} text-muted`}>Loading...</main>
  if (sent)
    return (
      <main className={shell}>
        <h1 className="text-xl font-semibold">Check your inbox</h1>
        <p role="status">
          If that address belongs to a customer of {name}, we have sent a sign-in link. It works once and
          expires in 15 minutes.
        </p>
        <button type="button" className="min-h-11 text-[14px] underline" onClick={() => setSent(false)}>
          Use a different address
        </button>
      </main>
    )
  return (
    <main className={shell}>
      <h1 className="text-xl font-semibold">{name} support</h1>
      <p className="text-[14px] text-muted">Enter the email address you use with us. No password needed.</p>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          setBusy(true)
          try {
            await api(`${base(ws)}/request`, { body: { email: new FormData(e.currentTarget).get('email') } })
            setSent(true)
            setErr(undefined)
          } catch (x) {
            setErr((x as Error).message)
          }
          setBusy(false)
        }}
      >
        <Field label="Email address">
          {(id) => <input id={id} name="email" type="email" required autoComplete="email" className={cls} />}
        </Field>
        <ErrorText error={err} />
        <button type="submit" disabled={busy} className={`${btnPrimary} min-h-11 w-full text-[15px]`}>
          Email me a sign-in link
        </button>
      </form>
    </main>
  )
}

function Ticket({ ws, t, onSent }: { ws: string; t: PTicket; onSent: () => void }) {
  const [body, setBody] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  return (
    <li className="rounded-lg border border-line">
      <details>
        <summary className="flex min-h-11 cursor-pointer flex-wrap items-center gap-x-3 gap-y-1 p-3">
          <span className="font-mono text-[12px] text-muted">{t.ref}</span>
          <span className="min-w-0 break-words font-medium">{t.title}</span>
          <Pill>{t.status[0].toUpperCase() + t.status.slice(1)}</Pill>
        </summary>
        <div className="space-y-3 border-t border-line p-3">
          <ol className="space-y-2">
            {t.messages.map((m) => (
              <li
                key={`${m.created_at}${m.from}${m.body}`}
                className={`rounded-lg border p-3 text-[14px] ${m.from === 'support' ? 'border-line' : 'border-line bg-surface'}`}
              >
                <div className="text-[12px] text-muted">
                  <span className="font-medium text-fg">{m.from === 'you' ? 'You' : 'Support'}</span> ·{' '}
                  {when(m.created_at)}
                </div>
                <p className="whitespace-pre-wrap break-words">{m.body}</p>
              </li>
            ))}
          </ol>
          <form
            className="space-y-2"
            onSubmit={async (e) => {
              e.preventDefault()
              setBusy(true)
              try {
                await api(`${base(ws)}/reply`, { body: { ref: t.number, body } })
                setBody('')
                setErr(undefined)
                onSent()
              } catch (x) {
                setErr((x as Error).message) // the draft stays
              }
              setBusy(false)
            }}
          >
            <Field label={`Reply to ${t.ref}`}>
              {(id) => (
                <textarea
                  id={id}
                  rows={3}
                  maxLength={5000}
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  className={`${inputCls} text-[16px]`}
                />
              )}
            </Field>
            <ErrorText error={err} />
            <button
              type="submit"
              disabled={busy || !body.trim()}
              className={`${btnPrimary} min-h-11 px-4 text-[14px]`}
            >
              Send reply
            </button>
          </form>
        </div>
      </details>
    </li>
  )
}

export function Portal({ ws }: { ws: string }) {
  const [state, setState] = useState<'loading' | 'out' | 'in' | 'error'>('loading')
  const [data, setData] = useState<{ workspace: string; tickets: PTicket[] }>()
  const load = useCallback(() => {
    api<{ workspace: string; tickets: PTicket[] }>(`${base(ws)}/tickets`).then(
      (r) => {
        setData(r)
        setState('in')
      },
      (x) => setState(x instanceof CmdFailure && x.status === 401 ? 'out' : 'error'),
    )
  }, [ws])
  useEffect(load, [load])
  if (state === 'loading') return <main className={`${shell} text-muted`}>Loading...</main>
  if (state === 'error')
    return <main className={shell}>Something went wrong. Reload the page to try again.</main>
  if (state === 'out' || !data) return <SignIn ws={ws} />
  return (
    <main className={shell}>
      <h1 className="text-xl font-semibold">{data.workspace} support</h1>
      <section aria-label="Your tickets">
        {data.tickets.length === 0 ? (
          <p className="text-muted">You have no tickets yet. Email us and your request will show up here.</p>
        ) : (
          <ul className="space-y-2">
            {data.tickets.map((t) => (
              <Ticket key={t.ref} ws={ws} t={t} onSent={load} />
            ))}
          </ul>
        )}
      </section>
    </main>
  )
}

/** Trades the fragment token for a session, then drops it from the address bar. */
export function Verify({ ws }: { ws: string }) {
  const router = useRouter()
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    const token = window.location.hash.slice(1)
    window.history.replaceState(null, '', window.location.pathname)
    if (!token) return setFailed(true)
    api(`${base(ws)}/verify`, { body: { token } }).then(
      () => router.replace(`/portal/${ws}`),
      () => setFailed(true),
    )
  }, [ws, router])
  if (!failed) return <main className={`${shell} text-muted`}>Signing you in...</main>
  return (
    <main className={shell}>
      <h1 className="text-xl font-semibold">This link no longer works</h1>
      <p role="alert">Sign-in links work once and expire after 15 minutes.</p>
      <a href={`/portal/${ws}`} className="inline-flex min-h-11 items-center underline">
        Get a new link
      </a>
    </main>
  )
}
