'use client'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { EmailFrame, type Preview } from './preview'

type Share = Preview & { event: string; to: string; body: string }
const WORDING = ['Invoice sent', 'Reminder 1: friendly', 'Reminder 2: second', 'Reminder 3: final']

/** Email tab of the Send dialog: wording picked by state, editable for this one send, server preview. */
export function SendEmail({
  ws,
  noteId,
  kind,
  overdue,
  busy,
  onSend,
}: {
  ws: string
  noteId: string
  kind: 'quotation' | 'invoice' | 'receipt'
  overdue: boolean
  busy: boolean
  onSend: (a: { to: string; subject?: string; message?: string; reminder_level?: number }) => void
}) {
  const canRemind = kind === 'invoice'
  const [level, setLevel] = useState(overdue && canRemind ? 1 : 0)
  const [p, setP] = useState<Share>()
  const [err, setErr] = useState<string>()
  const [to, setTo] = useState('')
  const [subject, setSubject] = useState('')
  const [message, setMessage] = useState('')
  const [html, setHtml] = useState('')
  const [edited, setEdited] = useState({ subject: false, message: false })
  useEffect(() => {
    let live = true
    setP(undefined)
    setEdited({ subject: false, message: false })
    cmd<Share>(ws, 'share.preview', { note_id: noteId, ...(level ? { reminder_level: level } : {}) })
      .then((d) => {
        if (!live) return
        setP(d)
        setErr(undefined)
        setTo((t) => t || d.to)
        setHtml(d.html)
        setSubject(d.subject)
        setMessage(d.body)
        setEdited({ subject: false, message: false })
      })
      .catch((e) => live && setErr((e as Error).message))
    return () => {
      live = false
    }
  }, [ws, noteId, level])
  // unsaved edits re-render the preview (server side, so signature, footer and link stay right)
  useEffect(() => {
    if (!edited.subject && !edited.message) return
    let live = true
    const t = setTimeout(() => {
      cmd<Share>(ws, 'share.preview', {
        note_id: noteId,
        ...(level ? { reminder_level: level } : {}),
        ...(edited.subject && subject.trim() ? { subject } : {}),
        ...(edited.message && message.trim() ? { message } : {}),
      })
        .then((d) => live && setHtml(d.html))
        .catch((e) => live && setErr((e as Error).message))
    }, 400)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [ws, noteId, level, edited, subject, message])
  const noEmail = p && !p.to && !to.trim()
  return (
    <form
      aria-label="Send by email"
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault()
        onSend({
          to: to.trim(),
          ...(edited.subject && { subject }),
          // the server swaps the sample-link marker for the real link
          ...(edited.message && { message }),
          ...(level && { reminder_level: level }),
        })
      }}
    >
      {canRemind && (
        <Field label="Wording">
          {(id) => (
            <select
              id={id}
              value={level}
              onChange={(e) => setLevel(Number(e.target.value))}
              className={inputCls}
            >
              {WORDING.map((w, i) => (
                <option key={w} value={i}>
                  {w}
                </option>
              ))}
            </select>
          )}
        </Field>
      )}
      <Field label="To">
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
      {noEmail && (
        <p role="status" className="text-[12px] text-warn">
          This customer has no email address yet. Type one above, or{' '}
          <Link href={`/w/${ws}/crm`} className="underline">
            add it to their CRM contact
          </Link>
          .
        </p>
      )}
      <ErrorText error={err && `Preview unavailable: ${err}`} />
      {p ? (
        <>
          <Field label="Subject">
            {(id) => (
              <input
                id={id}
                value={subject}
                maxLength={200}
                onChange={(e) => {
                  setSubject(e.target.value)
                  setEdited((x) => ({ ...x, subject: true }))
                }}
                className={inputCls}
              />
            )}
          </Field>
          <Field label="Message">
            {(id) => (
              <textarea
                id={id}
                rows={6}
                value={message}
                onChange={(e) => {
                  setMessage(e.target.value)
                  setEdited((x) => ({ ...x, message: true }))
                }}
                className={inputCls}
              />
            )}
          </Field>
          <p className="text-[12px] text-muted">
            Changes here apply to this send only. The link is created when you send.{' '}
            <Link href={`/w/${ws}/settings?tab=Messages`} className="underline">
              Edit wording for all {kind === 'quotation' ? 'quotes' : `${kind}s`}
            </Link>
          </p>
          <details>
            <summary className="cursor-pointer text-[13px] text-muted hover:text-fg">Preview</summary>
            <div className="mt-2">
              <EmailFrame html={html} height={300} />
            </div>
          </details>
        </>
      ) : (
        !err && <div aria-busy="true" className="h-24 animate-pulse rounded-lg bg-hover" />
      )}
      <button type="submit" className={btnPrimary} disabled={busy || !p}>
        Send email
      </button>
    </form>
  )
}
