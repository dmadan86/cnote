'use client'
import { useState } from 'react'
import { Secret } from '@/components/secret'
import { btn, btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

type Hook = {
  id: string
  name: string
  target: 'http' | 'mcp'
  url: string
  tool: string | null
  events: string[]
  every_minutes: number | null
  next_run_at: string | null
  enabled: boolean
  last_run_at: string | null
  last_status: string | null
  last_error: string | null
}

const EVENTS: [string, string][] = [
  ['note.', 'Pages'],
  ['comment.', 'Comments'],
  ['mention.', 'Mentions'],
  ['item.', 'Work items'],
  ['sprint.', 'Sprints'],
  ['contact.', 'Contacts'],
  ['deal.', 'Deals'],
  ['invoice.', 'Invoices'],
  ['payment.', 'Payments'],
  ['doc.', 'Docs'],
  ['*', 'Everything'],
]

function when(h: Hook) {
  const parts = []
  if (h.events.length)
    parts.push(`On ${h.events.map((e) => EVENTS.find(([k]) => k === e)?.[1] ?? e).join(', ')}`)
  if (h.every_minutes) parts.push(`every ${h.every_minutes} min`)
  return parts.join(' · ')
}

/** Settings → Webhooks: outbound HTTP webhooks and remote MCP tool calls, on events and/or a schedule. */
export function HooksTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Hook[]>(ws, 'hook.list')
  const [target, setTarget] = useState<'http' | 'mcp'>('http')
  const [secret, setSecret] = useState<string>()
  const [err, setErr] = useState<string>()
  const [tested, setTested] = useState<Record<string, string>>({})
  return (
    <div className="space-y-6">
      <p className="text-[13px] text-muted">
        Send workspace activity to other systems when events happen, on a schedule, or both. Webhooks receive
        a signed JSON POST; MCP servers get a tool call with the same JSON in the <code>cnote</code> argument.
      </p>
      {secret && <Secret value={secret} onDone={() => setSecret(undefined)} />}
      <form
        aria-label="New webhook"
        className="max-w-xl space-y-3 rounded-md border border-line p-4"
        onSubmit={async (e) => {
          e.preventDefault()
          const form = e.currentTarget
          const f = new FormData(form)
          const s = (k: string) => (f.get(k) as string | null)?.trim() || undefined
          const every = s('every_minutes')
          const start = s('start_at')
          try {
            const r = await cmd<{ signing_secret: string | null }>(ws, 'hook.create', {
              name: s('name'),
              target,
              url: s('url'),
              events: f.getAll('events'),
              every_minutes: every ? Number(every) : undefined,
              start_at: start ? new Date(start).toISOString() : undefined,
              ...(target === 'mcp' ? { tool: s('tool'), token: s('token') } : {}),
            })
            if (r.signing_secret) setSecret(r.signing_secret)
            form.reset()
            setErr(undefined)
            reload()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <Field label="Name">{(id) => <input id={id} name="name" required className={inputCls} />}</Field>
        <fieldset className="flex gap-4 text-[13px]">
          <legend className="mb-1 font-medium">Send to</legend>
          {(['http', 'mcp'] as const).map((t) => (
            <label key={t} className="flex items-center gap-1.5">
              <input type="radio" name="target" checked={target === t} onChange={() => setTarget(t)} />
              {t === 'http' ? 'Webhook URL' : 'MCP server'}
            </label>
          ))}
        </fieldset>
        <Field label={target === 'http' ? 'Payload URL' : 'MCP server URL'}>
          {(id) => (
            <input id={id} name="url" type="url" required placeholder="https://" className={inputCls} />
          )}
        </Field>
        {target === 'mcp' && (
          <>
            <Field label="Tool name">
              {(id) => <input id={id} name="tool" required className={inputCls} />}
            </Field>
            <Field label="Bearer token (optional)">
              {(id) => <input id={id} name="token" type="password" autoComplete="off" className={inputCls} />}
            </Field>
          </>
        )}
        <fieldset className="text-[13px]">
          <legend className="mb-1 font-medium">When these happen</legend>
          <div className="grid grid-cols-2 gap-1 sm:grid-cols-3">
            {EVENTS.map(([k, label]) => (
              <label key={k} className="flex items-center gap-1.5">
                <input type="checkbox" name="events" value={k} />
                {label}
              </label>
            ))}
          </div>
        </fieldset>
        <div className="grid grid-cols-2 gap-3">
          <Field label="And/or every (minutes)">
            {(id) => <input id={id} name="every_minutes" type="number" min={5} className={inputCls} />}
          </Field>
          <Field label="Starting at">
            {(id) => <input id={id} name="start_at" type="datetime-local" className={inputCls} />}
          </Field>
        </div>
        <ErrorText error={err} />
        <button type="submit" className={btnPrimary}>
          Create webhook
        </button>
      </form>
      <ul aria-label="Webhooks" className="divide-y divide-line rounded-md border border-line">
        {data?.length === 0 && <li className="px-3 py-2 text-[13px] text-muted">No webhooks yet.</li>}
        {data?.map((h) => (
          <li key={h.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-[13px]">
            <div className="min-w-0 flex-1">
              <div className="font-medium">
                {h.name}{' '}
                <span className="text-[12px] font-normal text-muted">
                  {h.target === 'mcp' ? `MCP · ${h.tool}` : 'Webhook'}
                </span>
              </div>
              <div className="truncate text-[12px] text-muted">{h.url}</div>
              <div className="text-[12px] text-muted">
                {when(h)}
                {h.last_run_at && ` · last ${h.last_status} ${ago(h.last_run_at)}`}
                {h.last_status === 'failed' && h.last_error && (
                  <span className="text-bad"> ({h.last_error})</span>
                )}
                {tested[h.id] && <span> · {tested[h.id]}</span>}
              </div>
            </div>
            <label className="flex items-center gap-1.5 text-[12px]">
              <input
                type="checkbox"
                checked={h.enabled}
                aria-label={`${h.name} enabled`}
                onChange={async (e) => {
                  await cmd(ws, 'hook.set_enabled', { id: h.id, enabled: e.target.checked })
                  reload()
                }}
              />
              Enabled
            </label>
            <button
              type="button"
              className={btn}
              aria-label={`Test ${h.name}`}
              onClick={async () => {
                const r = await cmd<{ ok: boolean; error?: string }>(ws, 'hook.test', { id: h.id })
                setTested((t) => ({ ...t, [h.id]: r.ok ? 'Test delivered' : `Test failed: ${r.error}` }))
                reload()
              }}
            >
              Test
            </button>
            <button
              type="button"
              className={btn}
              aria-label={`Delete ${h.name}`}
              onClick={async () => {
                await cmd(ws, 'hook.delete', { id: h.id })
                reload()
              }}
            >
              Delete
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}
