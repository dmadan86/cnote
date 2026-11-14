'use client'
import { Copy, Mail, MessageCircle, Share2 } from 'lucide-react'
import { useState } from 'react'
import { QrSvg } from '@/components/share/qr-svg'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { ago, useCmd } from '@/lib/hooks'
import { run } from './bits'

type Link = {
  id: string
  status: 'active' | 'expired' | 'revoked'
  created_at: string
  created_by_name: string | null
  expires_at: string | null
  view_count: number
  last_viewed_at: string | null
}
type Made = { id: string; url: string; whatsapp_url: string; expires_at: string }

const TONE = { active: 'ok', expired: 'warn', revoked: 'bad' } as const

/** Share button + dialog for a sent quotation or an issued invoice: create a link, send it, see views, revoke. */
export function ShareButton({
  noteId,
  kind,
  overdue = false,
}: {
  noteId: string
  kind: 'quotation' | 'invoice'
  overdue?: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className={btn} onClick={() => setOpen(true)}>
        <Share2 size={14} /> {overdue ? 'Remind or share' : 'Share with customer'}
      </button>
      {open && <ShareDialog noteId={noteId} kind={kind} overdue={overdue} onClose={() => setOpen(false)} />}
    </>
  )
}

function ShareDialog({
  noteId,
  kind,
  overdue,
  onClose,
}: {
  noteId: string
  kind: 'quotation' | 'invoice'
  overdue: boolean
  onClose: () => void
}) {
  const { ws } = useShell()
  const links = useCmd<Link[]>(ws, 'share.list', { note_id: noteId })
  const [days, setDays] = useState('30')
  const [made, setMade] = useState<Made>()
  const [err, setErr] = useState<string>()
  const [note, setNote] = useState<string>()
  const [to, setTo] = useState('')
  const [busy, setBusy] = useState(false)
  const base = { note_id: noteId, origin: window.location.origin, expires_in_days: Number(days) }
  const guard = async (f: () => Promise<void>) => {
    setBusy(true)
    setErr(undefined)
    setNote(undefined)
    try {
      await f()
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const create = () =>
    guard(async () => {
      setMade(await run<Made>(ws, 'share.create', { ...base, reminder: overdue && kind === 'invoice' }))
      links.reload()
    })
  const copy = () =>
    guard(async () => {
      await navigator.clipboard.writeText(made?.url ?? '')
      setNote('Link copied.')
    })
  const email = () =>
    guard(async () => {
      await run(ws, 'share.email', { ...base, to: to.trim() })
      setNote(`Email sent to ${to.trim()} with a new link.`)
      setTo('')
      links.reload()
    })
  const revoke = (id: string) =>
    guard(async () => {
      await run(ws, 'share.revoke', { id })
      if (made?.id === id) setMade(undefined)
      links.reload()
    })
  return (
    <Modal
      open
      title={kind === 'quotation' ? 'Share quotation' : 'Share invoice'}
      onClose={onClose}
      className="max-w-lg"
    >
      <div className="max-h-[75vh] space-y-4 overflow-y-auto">
        <p className="text-[13px] text-muted">
          Anyone with the link can view this {kind}
          {kind === 'quotation' ? ' and accept or decline it' : ' and pay by UPI'}. No login needed. Revoke a
          link at any time.
        </p>
        <div className="flex items-end gap-2">
          <Field label="Link expires after (days)">
            {(id) => (
              <input
                id={id}
                inputMode="numeric"
                value={days}
                onChange={(e) => setDays(e.target.value.replace(/\D/g, ''))}
                className={`${inputCls} w-24`}
              />
            )}
          </Field>
          <button type="button" className={btnPrimary} disabled={busy || !days} onClick={create}>
            {made ? 'Create another link' : 'Create link'}
          </button>
        </div>
        {made && (
          <section aria-label="New link" className="space-y-3 rounded-lg border border-line p-3">
            <div className="flex gap-2">
              <input
                readOnly
                aria-label="Share link"
                value={made.url}
                onFocus={(e) => e.currentTarget.select()}
                className={`${inputCls} font-mono text-[12px]`}
              />
              <button type="button" className={btn} onClick={copy}>
                <Copy size={14} /> Copy
              </button>
            </div>
            <p className="text-[12px] text-muted">
              Shown once: only a fingerprint of this link is stored. Create another to share again.
            </p>
            <div className="flex flex-wrap gap-2">
              <a href={made.whatsapp_url} target="_blank" rel="noreferrer" className={btnPrimary}>
                <MessageCircle size={14} />{' '}
                {overdue && kind === 'invoice' ? 'Remind on WhatsApp' : 'Send on WhatsApp'}
              </a>
            </div>
            <form
              className="flex items-end gap-2"
              onSubmit={(e) => {
                e.preventDefault()
                email()
              }}
            >
              <Field label="Or email it to">
                {(id) => (
                  <input
                    id={id}
                    type="email"
                    required
                    value={to}
                    onChange={(e) => setTo(e.target.value)}
                    placeholder="customer@example.com"
                    className={inputCls}
                  />
                )}
              </Field>
              <button type="submit" className={btn} disabled={busy}>
                <Mail size={14} /> Send email
              </button>
            </form>
          </section>
        )}
        <div role="status" className="text-[13px] text-ok">
          {note}
        </div>
        <ErrorText error={err} />
        <section aria-label="Links">
          <h3 className="mb-1 text-[13px] font-semibold">Links</h3>
          <p className="mb-2 text-[12px] text-muted">Views include previews and your own opens.</p>
          {links.data?.length === 0 && <p className="text-[13px] text-muted">No links yet.</p>}
          <ul className="divide-y divide-line">
            {links.data?.map((l) => (
              <li key={l.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-[13px]">
                <Pill tone={TONE[l.status]}>{l.status}</Pill>
                <span>
                  {l.view_count} view{l.view_count === 1 ? '' : 's'}
                  {l.last_viewed_at && <span className="text-muted"> · last {ago(l.last_viewed_at)}</span>}
                </span>
                <span className="text-muted">
                  created {ago(l.created_at)}
                  {l.expires_at && ` · expires ${new Date(l.expires_at).toLocaleDateString('en-IN')}`}
                </span>
                {l.status === 'active' && (
                  <button
                    type="button"
                    className={`${btn} ml-auto`}
                    disabled={busy}
                    onClick={() => revoke(l.id)}
                    aria-label={`Revoke link created ${ago(l.created_at)}`}
                  >
                    Revoke
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      </div>
    </Modal>
  )
}

/** The UPI block on an issued invoice: QR + copyable details. Hidden when no VPA is set or nothing is owed. */
export function UpiCard({ invoiceId, outstanding }: { invoiceId: string; outstanding: number }) {
  const { ws } = useShell()
  const { data, error } = useCmd<{ upi_uri: string }>(
    ws,
    'invoice.upi_qr',
    { id: invoiceId },
    outstanding > 0,
  )
  if (outstanding <= 0 || error || !data) return null
  return (
    <section aria-label="UPI payment" className="rounded-lg border border-line p-4">
      <h2 className="mb-2 text-[14px] font-semibold">Scan to pay by UPI</h2>
      <div className="flex items-center gap-3">
        <QrSvg text={data.upi_uri} label="UPI payment QR" size={128} />
        <p className="text-[12px] text-muted">
          Static UPI QR with the amount due and the invoice number. It is not the GST e-invoice QR. Payments
          are not matched automatically: record the payment when it arrives.
        </p>
      </div>
    </section>
  )
}
