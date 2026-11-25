'use client'
import { Check, Copy, Send } from 'lucide-react'
import { useState } from 'react'
import { btn, btnPrimary, ErrorText, Field, inputCls, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

type Pending = {
  id: string
  email: string
  role: string
  invited_by_name: string
  created_at: string
  expires_at: string
  expired: boolean
}
type Issued = { message: string; mailed: boolean; links: { email: string; path: string }[] }

const ROLES = [
  { value: 'admin', label: 'Admin', hint: 'Manages settings, members and money' },
  { value: 'member', label: 'Member', hint: 'Works in spaces they can see' },
  { value: 'guest', label: 'Guest', hint: 'Sees only what is shared with them' },
]
const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

function expiry(p: Pending) {
  if (p.expired) return 'Expired'
  const days = Math.ceil((new Date(p.expires_at).getTime() - Date.now()) / 86_400_000)
  return `Expires in ${days} ${days === 1 ? 'day' : 'days'}`
}

/** One link per address when the server cannot send mail. It exists only in this component's state: nothing stores it. */
function Links({ links, onDone }: { links: Issued['links']; onDone: () => void }) {
  const [copied, setCopied] = useState<string>()
  return (
    <div role="status" className="space-y-2 rounded-md border border-line bg-warn-bg p-3">
      <p className="text-[13px] font-medium">
        Email is not set up on this server. Copy each link now and send it yourself. It will not be shown
        again.
      </p>
      <ul className="space-y-2">
        {links.map((l) => {
          const url = `${window.location.origin}${l.path}`
          return (
            <li key={l.email} className="flex flex-wrap items-center gap-2">
              <span className="text-[13px]">{l.email}</span>
              <input
                readOnly
                aria-label={`Invitation link for ${l.email}`}
                value={url}
                onFocus={(e) => e.currentTarget.select()}
                className={`${inputCls} min-w-0 flex-1 basis-56`}
              />
              <button
                type="button"
                className={`${btn} min-h-11`}
                onClick={async () => {
                  await navigator.clipboard.writeText(url).catch(() => {})
                  setCopied(l.email)
                }}
              >
                {copied === l.email ? <Check size={14} aria-hidden /> : <Copy size={14} aria-hidden />}
                {copied === l.email ? 'Copied' : `Copy link for ${l.email}`}
              </button>
            </li>
          )
        })}
      </ul>
      <button type="button" className={`${btn} min-h-11`} onClick={onDone}>
        Done
      </button>
    </div>
  )
}

/** Members settings: invite by email, and the pending invitations with Resend and Revoke. */
export function InvitePeople({ ws, owner }: { ws: string; owner: boolean }) {
  const pending = useCmd<Pending[]>(ws, 'member.invite_list')
  const [note, setNote] = useState<string>()
  const [links, setLinks] = useState<Issued['links']>([])
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)

  const done = (r: Issued) => {
    setNote(r.message)
    setLinks(r.links)
    setErr(undefined)
    pending.reload()
  }
  const run = async (f: () => Promise<void>) => {
    setBusy(true)
    try {
      await f()
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const roles = owner
    ? [...ROLES.slice(0, 1), { value: 'owner', label: 'Owner', hint: 'Full control' }, ...ROLES.slice(1)]
    : ROLES

  return (
    <div className="space-y-4">
      <form
        className="rounded-lg border border-line p-4"
        onSubmit={(e) => {
          e.preventDefault()
          const form = e.currentTarget
          const f = new FormData(form)
          const emails = String(f.get('emails') ?? '')
            .split(/[\s,;]+/)
            .filter(Boolean)
          void run(async () => {
            done(await cmd<Issued>(ws, 'member.invite', { emails, role: f.get('role') }))
            form.reset()
          })
        }}
      >
        <h3 className="text-[14px] font-medium">Invite people</h3>
        <p className="mt-0.5 mb-3 text-[12px] text-muted">
          They get an email with a link that works for 7 days. Nothing is created until they accept.
        </p>
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-56 flex-1">
            <Field label="Email addresses">
              {(id) => (
                <textarea
                  id={id}
                  name="emails"
                  required
                  rows={2}
                  placeholder="name@company.com, other@company.com"
                  className={inputCls}
                />
              )}
            </Field>
          </div>
          <Field label="Role">
            {(id) => (
              <select id={id} name="role" defaultValue="member" className={inputCls}>
                {roles.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label} · {r.hint}
                  </option>
                ))}
              </select>
            )}
          </Field>
          <button type="submit" disabled={busy} className={`${btnPrimary} min-h-11`}>
            <Send size={14} aria-hidden /> Send invitations
          </button>
        </div>
      </form>
      <ErrorText error={err} />
      {note && links.length === 0 && (
        <p role="status" className="text-[13px] text-muted">
          {note}
        </p>
      )}
      {links.length > 0 && <Links links={links} onDone={() => setLinks([])} />}
      <section aria-label="Pending invitations">
        <h3 className="mb-2 text-[13px] font-medium">
          Pending invitations <span className="font-normal text-muted">· {pending.data?.length ?? 0}</span>
        </h3>
        {pending.data?.length ? (
          <ul className="divide-y divide-line overflow-hidden rounded-lg border border-line">
            {pending.data.map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2.5 text-[13px]">
                <div className="min-w-0 flex-1 basis-48">
                  <div className="truncate font-medium">{p.email}</div>
                  <div className="text-[12px] text-muted">
                    {p.invited_by_name ? `Invited by ${p.invited_by_name} · ` : ''}
                    {expiry(p)}
                  </div>
                </div>
                <Pill tone={p.expired ? 'warn' : 'neutral'}>{cap(p.role)}</Pill>
                <div className="flex gap-2">
                  <button
                    type="button"
                    disabled={busy}
                    aria-label={`Resend invitation to ${p.email}`}
                    className={`${btn} min-h-11`}
                    onClick={() =>
                      run(async () => done(await cmd<Issued>(ws, 'member.invite_resend', { id: p.id })))
                    }
                  >
                    Resend
                  </button>
                  <button
                    type="button"
                    disabled={busy}
                    aria-label={`Revoke invitation to ${p.email}`}
                    className={`${btn} min-h-11`}
                    onClick={() =>
                      run(async () => {
                        await cmd(ws, 'member.invite_revoke', { id: p.id })
                        setNote(`Invitation to ${p.email} revoked.`)
                        setLinks([])
                        pending.reload()
                      })
                    }
                  >
                    Revoke
                  </button>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-[13px] text-muted">No pending invitations.</p>
        )}
      </section>
    </div>
  )
}
