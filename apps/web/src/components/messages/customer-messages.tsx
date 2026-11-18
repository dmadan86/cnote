'use client'
import { ChevronRight } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { btnPrimary, ErrorText, Field, inputCls, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { MessageEditor } from './editor'

export type EventRow = {
  event: string
  label: string
  group: string
  sent_when: string
  subject: string
  body: string
  default_subject: string
  default_body: string
  customised: boolean
  version: number
  fields: { key: string; label: string; example: string }[]
}
type Sender = { from_name: string; reply_to: string; accent: string; signature: string; footer: string }
type List = { events: EventRow[]; sender: Sender }

const SWATCHES = ['#2563eb', '#0f766e', '#15803d', '#b45309', '#be123c', '#6d28d9', '#334155']
const HEX = /^#[0-9a-fA-F]{6}$/

/** Settings > Customer messages: sender and look, the wording of each message, WhatsApp wording. */
export function CustomerMessages({ ws }: { ws: string }) {
  const { data, error, reload } = useCmd<List>(ws, 'email_template.list')
  const [open, setOpen] = useState<string>()
  if (error) return <ErrorText error={error} />
  if (!data) return <div aria-busy="true" className="h-40 animate-pulse rounded-lg bg-hover" />
  const row = data.events.find((e) => e.event === open)
  if (row)
    return (
      <MessageEditor
        key={row.event}
        ws={ws}
        row={row}
        fromName={data.sender.from_name}
        onBack={() => setOpen(undefined)}
        onChanged={reload}
      />
    )
  const groups = [...new Set(data.events.map((e) => e.group))]
  return (
    <div className="space-y-8">
      <SenderCard ws={ws} sender={data.sender} onSaved={reload} />
      <section aria-label="Messages" className="space-y-5">
        {groups.map((g) => (
          <div key={g}>
            <h3 className="mb-2 text-[12px] font-medium uppercase tracking-wide text-muted">{g}</h3>
            <ul className="divide-y divide-line rounded-lg border border-line">
              {data.events
                .filter((e) => e.group === g)
                .map((e) => (
                  <li key={e.event}>
                    <button
                      type="button"
                      onClick={() => setOpen(e.event)}
                      className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-hover"
                    >
                      <span className="min-w-0 flex-1">
                        <span className="block text-[14px] font-medium">{e.label}</span>
                        <span className="block text-[12px] text-muted">{e.sent_when}</span>
                      </span>
                      {e.customised && <Pill tone="warn">Customised</Pill>}
                      <ChevronRight size={15} aria-hidden className="shrink-0 text-muted" />
                    </button>
                  </li>
                ))}
            </ul>
          </div>
        ))}
      </section>
      <WhatsAppCard ws={ws} />
    </div>
  )
}

function SenderCard({ ws, sender, onSaved }: { ws: string; sender: Sender; onSaved: () => void }) {
  const [v, setV] = useState(sender)
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  const set = (k: keyof Sender) => (e: { target: { value: string } }) => {
    setMsg(undefined)
    setV({ ...v, [k]: e.target.value })
  }
  const bad = v.accent !== '' && !HEX.test(v.accent)
  return (
    <form
      aria-label="Look and sender"
      className="space-y-4 rounded-lg border border-line p-4"
      onSubmit={async (e) => {
        e.preventDefault()
        try {
          await cmd(ws, 'company.update', {
            email_from_name: v.from_name.trim(),
            email_reply_to: v.reply_to.trim(),
            email_accent: v.accent.trim(),
            email_signature: v.signature.trim(),
            email_footer: v.footer.trim(),
          })
          setErr(undefined)
          setMsg('Saved')
          onSaved()
        } catch (x) {
          setMsg(undefined)
          setErr((x as Error).message)
        }
      }}
    >
      <h3 className="text-[14px] font-semibold">Look and sender</h3>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Sender name">
          {(id) => (
            <input
              id={id}
              maxLength={80}
              value={v.from_name}
              onChange={set('from_name')}
              className={inputCls}
            />
          )}
        </Field>
        <Field label="Replies go to">
          {(id) => (
            <input
              id={id}
              type="email"
              placeholder="you@yourbusiness.in"
              value={v.reply_to}
              onChange={set('reply_to')}
              className={inputCls}
            />
          )}
        </Field>
      </div>
      <Field label="Accent colour (hex)">
        {(id) => (
          <div className="flex flex-wrap items-center gap-2">
            {SWATCHES.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={`Use ${c}`}
                aria-pressed={v.accent.toLowerCase() === c}
                onClick={() => setV({ ...v, accent: c })}
                style={{ backgroundColor: c }}
                className="h-6 w-6 rounded-full border border-line aria-pressed:ring-2 aria-pressed:ring-fg aria-pressed:ring-offset-1"
              />
            ))}
            <input
              id={id}
              value={v.accent}
              placeholder="#2563eb"
              onChange={set('accent')}
              aria-invalid={bad}
              className={`${inputCls} w-28 font-mono`}
            />
          </div>
        )}
      </Field>
      <Field label="Sign-off">
        {(id) => (
          <textarea
            id={id}
            rows={3}
            maxLength={500}
            placeholder={'Thank you,\nYour business name'}
            value={v.signature}
            onChange={set('signature')}
            className={inputCls}
          />
        )}
      </Field>
      <Field label="Footer note">
        {(id) => (
          <textarea
            id={id}
            rows={2}
            maxLength={500}
            value={v.footer}
            onChange={set('footer')}
            className={inputCls}
          />
        )}
      </Field>
      <p className="text-[12px] text-muted">
        Legal name, GSTIN and address in the footer come from{' '}
        <Link href={`/w/${ws}/settings?tab=Company`} className="underline">
          Company profile
        </Link>
        .
      </p>
      <ErrorText error={bad ? 'Accent colour looks like #2563eb' : err} />
      <div className="flex items-center gap-3">
        <button type="submit" className={btnPrimary} disabled={bad}>
          Save look and sender
        </button>
        <span role="status" className="text-[13px] text-ok">
          {msg}
        </span>
      </div>
    </form>
  )
}

function WhatsAppCard({ ws }: { ws: string }) {
  const { data, reload } = useCmd<{ settings: { share_message?: string } }>(ws, 'workspace.get')
  const [text, setText] = useState<string>()
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  if (!data) return null
  const v = text ?? data.settings.share_message ?? ''
  return (
    <form
      aria-label="WhatsApp wording"
      className="space-y-3 rounded-lg border border-line p-4"
      onSubmit={async (e) => {
        e.preventDefault()
        try {
          await cmd(ws, 'workspace.update', { settings: { share_message: v.trim() } })
          setErr(undefined)
          setMsg('Saved')
          reload()
        } catch (x) {
          setMsg(undefined)
          setErr((x as Error).message)
        }
      }}
    >
      <h3 className="text-[14px] font-semibold">WhatsApp wording</h3>
      <Field label="Message">
        {(id) => (
          <textarea
            id={id}
            rows={3}
            maxLength={1000}
            value={v}
            onChange={(e) => {
              setMsg(undefined)
              setText(e.target.value)
            }}
            placeholder="Hello {name}, here is your {doc} {number} for {amount} from {seller}: {link}"
            className={inputCls}
          />
        )}
      </Field>
      <p className="text-[12px] text-muted">
        You can use {'{name} {doc} {number} {amount} {seller} {link}'}. Leave empty for the standard wording.
        Reminders always use the reminder wording.
      </p>
      <ErrorText error={err} />
      <div className="flex items-center gap-3">
        <button type="submit" className={btnPrimary}>
          Save WhatsApp wording
        </button>
        <span role="status" className="text-[13px] text-ok">
          {msg}
        </span>
      </div>
    </form>
  )
}
