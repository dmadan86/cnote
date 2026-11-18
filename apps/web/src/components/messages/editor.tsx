'use client'
import { ChevronRight, Mail, RotateCcw } from 'lucide-react'
import { useRef, useState } from 'react'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import type { EventRow } from './customer-messages'
import { PreviewPane } from './preview'

/** One message: wording form on the left, live server-rendered preview on the right. */
export function MessageEditor({
  ws,
  row,
  fromName,
  onBack,
  onChanged,
}: {
  ws: string
  row: EventRow
  fromName: string
  onBack: () => void
  onChanged: () => void
}) {
  const [subject, setSubject] = useState(row.subject)
  const [body, setBody] = useState(row.body)
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const area = useRef<HTMLTextAreaElement>(null)
  const dirty = subject !== row.subject || body !== row.body
  const guard = async (f: () => Promise<string | undefined>) => {
    setBusy(true)
    setErr(undefined)
    setMsg(undefined)
    try {
      setMsg(await f())
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const insert = (key: string) => {
    const el = area.current
    const tok = `{{${key}}}`
    const [a, b] = el ? [el.selectionStart, el.selectionEnd] : [body.length, body.length]
    setBody(body.slice(0, a) + tok + body.slice(b))
    requestAnimationFrame(() => {
      el?.focus()
      el?.setSelectionRange(a + tok.length, a + tok.length)
    })
  }
  const save = () =>
    guard(async () => {
      await cmd(ws, 'email_template.save', { event: row.event, subject, body, version: row.version })
      onChanged()
      return 'Saved'
    })
  const test = () =>
    guard(async () => {
      const r = await cmd<{ sent_to: string }>(ws, 'email_template.test', { event: row.event, subject, body })
      return `Test sent to ${r.sent_to}`
    })
  const reset = () =>
    guard(async () => {
      await cmd(ws, 'email_template.reset', { event: row.event, version: row.version })
      setSubject(row.default_subject)
      setBody(row.default_body)
      setConfirm(false)
      onChanged()
      return 'Reset to the default wording'
    })
  return (
    <div className="space-y-4">
      <nav aria-label="Breadcrumb" className="flex items-center gap-1 text-[13px] text-muted">
        <button type="button" onClick={onBack} className="rounded px-1 hover:bg-hover hover:text-fg">
          Customer messages
        </button>
        <ChevronRight size={13} aria-hidden />
        <span className="font-medium text-fg">{row.label}</span>
        {row.customised && <Pill tone="warn">Customised</Pill>}
      </nav>
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_340px]">
        <form
          aria-label={`Edit ${row.label}`}
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            save()
          }}
        >
          <p className="text-[13px] text-muted">{row.sent_when}</p>
          <Field label="Subject">
            {(id) => (
              <>
                <input
                  id={id}
                  value={subject}
                  maxLength={200}
                  onChange={(e) => setSubject(e.target.value)}
                  className={inputCls}
                />
                <p className="text-right text-[12px] text-muted">{subject.length}/200</p>
              </>
            )}
          </Field>
          <Field label="Message">
            {(id) => (
              <textarea
                id={id}
                ref={area}
                rows={12}
                value={body}
                onChange={(e) => setBody(e.target.value)}
                className={`${inputCls} font-mono text-[13px]`}
              />
            )}
          </Field>
          <section aria-label="Insert a detail">
            <p className="mb-1.5 text-[12px] font-medium text-muted">
              Insert a detail (added where your cursor is; **bold** works)
            </p>
            <div className="flex flex-wrap gap-1.5">
              {row.fields.map((f) => (
                <button
                  key={f.key}
                  type="button"
                  title={`{{${f.key}}} e.g. ${f.example}`}
                  onClick={() => insert(f.key)}
                  className="rounded-full border border-line px-2.5 py-0.5 text-[12px] hover:bg-hover"
                >
                  {f.label}
                </button>
              ))}
            </div>
          </section>
          <ErrorText error={err} />
          <p role="status" className="text-[13px] text-ok">
            {msg}
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="submit"
              className={btnPrimary}
              disabled={busy || !dirty || !subject.trim() || !body.trim()}
            >
              Save
            </button>
            <button type="button" className={btn} disabled={busy} onClick={test}>
              <Mail size={14} aria-hidden /> Send test to me
            </button>
            {row.customised && (
              <button
                type="button"
                className={`${btn} ml-auto`}
                disabled={busy}
                onClick={() => setConfirm(true)}
              >
                <RotateCcw size={14} aria-hidden /> Reset to default
              </button>
            )}
          </div>
        </form>
        <PreviewPane ws={ws} event={row.event} subject={subject} body={body} fromName={fromName} />
      </div>
      <Modal open={confirm} onClose={() => setConfirm(false)} title="Reset to default wording?">
        <div className="space-y-3">
          <p className="text-[13px] text-muted">Your wording for {row.label} is replaced by this:</p>
          <div className="space-y-1 rounded-md border border-line bg-side p-3 text-[13px]">
            <p className="font-medium">{row.default_subject}</p>
            <pre className="whitespace-pre-wrap font-sans text-muted">{row.default_body}</pre>
          </div>
          <ErrorText error={err} />
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setConfirm(false)}>
              Keep my wording
            </button>
            <button type="button" className={btnPrimary} disabled={busy} onClick={reset}>
              Reset to default
            </button>
          </div>
        </div>
      </Modal>
    </div>
  )
}
