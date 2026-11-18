'use client'
import { Bot, Info, Plus, Send, Trash2, Webhook as WebhookIcon } from 'lucide-react'
import { useState } from 'react'
import { Secret } from '@/components/secret'
import { btn, btnPrimary, EmptyState, ErrorText, Field, inputCls, Pill } from '@/components/ui'
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
  const [adding, setAdding] = useState(false)
  const showForm = adding || data?.length === 0
  const chip =
    'flex cursor-pointer items-center gap-1.5 rounded-full border border-line px-2.5 py-1 has-[:checked]:border-accent has-[:checked]:bg-hover has-[:checked]:font-medium'
  return (
    <div className="space-y-6">
      <div className="flex items-start gap-3">
        <p className="flex flex-1 gap-2 text-[13px] text-muted">
          <Info size={15} aria-hidden className="mt-0.5 shrink-0" />
          <span>
            Send workspace activity to other systems when events happen, on a schedule, or both. Webhooks
            receive a signed JSON POST; MCP servers get a tool call with the same JSON in the{' '}
            <code>cnote</code> argument.
          </span>
        </p>
        {!showForm && (
          <button type="button" className={btnPrimary} onClick={() => setAdding(true)}>
            <Plus size={14} aria-hidden /> New webhook
          </button>
        )}
      </div>
      {secret && <Secret value={secret} onDone={() => setSecret(undefined)} />}
      {showForm && (
        <form
          aria-label="New webhook"
          className="space-y-4 rounded-lg border border-line p-4"
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
              setAdding(false)
              reload()
            } catch (x) {
              setErr((x as Error).message)
            }
          }}
        >
          <div>
            <h3 className="text-[14px] font-medium">New webhook</h3>
            <p className="mt-0.5 text-[12px] text-muted">Pick where to send, then what should trigger it.</p>
          </div>
          <fieldset className="text-[13px]">
            <legend className="mb-1.5 text-[12px] font-medium text-muted">Send to</legend>
            <div className="grid gap-2 sm:grid-cols-2">
              {(['http', 'mcp'] as const).map((t) => (
                <label
                  key={t}
                  className="flex cursor-pointer items-start gap-2 rounded-md border border-line p-3 has-[:checked]:border-accent has-[:checked]:bg-hover"
                >
                  <input
                    type="radio"
                    name="target"
                    className="mt-0.5"
                    checked={target === t}
                    onChange={() => setTarget(t)}
                  />
                  <span>
                    <span className="block font-medium">{t === 'http' ? 'Webhook URL' : 'MCP server'}</span>
                    <span className="text-[12px] text-muted">
                      {t === 'http'
                        ? 'A signed JSON POST to your endpoint'
                        : 'Call a tool on a remote MCP server'}
                    </span>
                  </span>
                </label>
              ))}
            </div>
          </fieldset>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Name">{(id) => <input id={id} name="name" required className={inputCls} />}</Field>
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
                  {(id) => (
                    <input id={id} name="token" type="password" autoComplete="off" className={inputCls} />
                  )}
                </Field>
              </>
            )}
          </div>
          <fieldset className="text-[13px]">
            <legend className="mb-1.5 text-[12px] font-medium text-muted">When these happen</legend>
            <div className="flex flex-wrap gap-2">
              {EVENTS.map(([k, label]) => (
                <label key={k} className={chip}>
                  <input type="checkbox" name="events" value={k} className="accent-accent" />
                  {label}
                </label>
              ))}
            </div>
          </fieldset>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="And/or every (minutes)">
              {(id) => (
                <input
                  id={id}
                  name="every_minutes"
                  type="number"
                  min={5}
                  placeholder="60"
                  className={inputCls}
                />
              )}
            </Field>
            <Field label="Starting at">
              {(id) => <input id={id} name="start_at" type="datetime-local" className={inputCls} />}
            </Field>
          </div>
          <ErrorText error={err} />
          <div className="flex gap-2">
            <button type="submit" className={btnPrimary}>
              Create webhook
            </button>
            {!!data?.length && (
              <button type="button" className={btn} onClick={() => setAdding(false)}>
                Cancel
              </button>
            )}
          </div>
        </form>
      )}
      {data?.length === 0 ? (
        <div className="rounded-lg border border-line">
          <EmptyState icon={WebhookIcon} title="No webhooks yet" compact>
            Webhooks you create above appear here with their last delivery.
          </EmptyState>
        </div>
      ) : (
        <ul aria-label="Webhooks" className="space-y-3">
          {data?.map((h) => (
            <li key={h.id} className="rounded-lg border border-line p-4 text-[13px]">
              <div className="flex flex-wrap items-start gap-3">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-line bg-hover text-muted">
                  {h.target === 'mcp' ? <Bot size={16} aria-hidden /> : <WebhookIcon size={16} aria-hidden />}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{h.name}</span>
                    <Pill>{h.target === 'mcp' ? `MCP · ${h.tool}` : 'Webhook'}</Pill>
                    {h.last_status && (
                      <Pill tone={h.last_status === 'failed' ? 'bad' : 'ok'}>
                        {h.last_status === 'failed' ? 'Last delivery failed' : 'Last delivery ok'}
                      </Pill>
                    )}
                  </div>
                  <code className="mt-1 block truncate text-[12px] text-muted">{h.url}</code>
                  <div className="mt-1 text-[12px] text-muted">
                    {when(h)}
                    {h.last_run_at && ` · last ${h.last_status} ${ago(h.last_run_at)}`}
                    {h.last_status === 'failed' && h.last_error && (
                      <span className="text-bad"> ({h.last_error})</span>
                    )}
                    {tested[h.id] && <span> · {tested[h.id]}</span>}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <label className="flex items-center gap-2 text-[12px] text-muted">
                    <input
                      type="checkbox"
                      role="switch"
                      aria-checked={h.enabled}
                      checked={h.enabled}
                      aria-label={`${h.name} enabled`}
                      className="relative h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full bg-line transition-colors checked:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent after:absolute after:top-0.5 after:left-0.5 after:h-4 after:w-4 after:rounded-full after:bg-canvas after:shadow-sm after:transition-transform checked:after:translate-x-4"
                      onChange={async (e) => {
                        await cmd(ws, 'hook.set_enabled', { id: h.id, enabled: e.target.checked })
                        reload()
                      }}
                    />
                    {h.enabled ? 'On' : 'Off'}
                  </label>
                  <button
                    type="button"
                    className={btn}
                    aria-label={`Test ${h.name}`}
                    onClick={async () => {
                      const r = await cmd<{ ok: boolean; error?: string }>(ws, 'hook.test', { id: h.id })
                      setTested((t) => ({
                        ...t,
                        [h.id]: r.ok ? 'Test delivered' : `Test failed: ${r.error}`,
                      }))
                      reload()
                    }}
                  >
                    <Send size={13} aria-hidden /> Test
                  </button>
                  <button
                    type="button"
                    title="Delete"
                    className="rounded-md p-1.5 text-muted hover:bg-hover hover:text-bad"
                    aria-label={`Delete ${h.name}`}
                    onClick={async () => {
                      await cmd(ws, 'hook.delete', { id: h.id })
                      reload()
                    }}
                  >
                    <Trash2 size={15} aria-hidden />
                  </button>
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
