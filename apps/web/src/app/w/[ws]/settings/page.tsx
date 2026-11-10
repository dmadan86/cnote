'use client'
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

const TABS = ['Workspace', 'Members', 'API keys', 'Agents', 'Tool groups', 'Confirmations', 'Usage'] as const
type TabName = (typeof TABS)[number]

type Ws = { name: string; settings: { agent_tool_groups?: string[] } }
type Key = {
  id: string
  name: string
  prefix: string
  agent_id: string | null
  revoked_at: string | null
  last_used_at: string | null
}
type Agent = { id: string; name: string; kind: string; scopes: string[] }
type Confirmation = { id: string; command: string; requested_by: string; created_at: string; input: unknown }

const GROUPS: [string, string][] = [
  ['read', 'Read notes, spaces and search'],
  ['write_notes', 'Create and edit pages and comments'],
  ['write_items', 'Create and edit work items'],
  ['manage_crm', 'Manage contacts and deals'],
  ['manage_invoices', 'Money-moving: create and issue invoices'],
  ['send_messages', 'Outbound: email and WhatsApp'],
  ['admin', 'Members, keys and settings'],
  ['destructive', 'Delete data'],
]
const DEFAULT_GROUPS = ['read', 'write_notes', 'write_items', 'manage_crm']

function Secret({ value, onDone }: { value: string; onDone: () => void }) {
  const [copied, setCopied] = useState(false)
  return (
    <div role="status" className="space-y-2 rounded-md border border-line bg-warn-bg p-3">
      <p className="text-[13px] font-medium">Copy this key now. It will not be shown again.</p>
      <div className="flex items-center gap-2">
        <code
          data-testid="secret"
          className="min-w-0 flex-1 break-all rounded bg-canvas px-2 py-1 text-[13px]"
        >
          {value}
        </code>
        <button
          type="button"
          className={btn}
          onClick={async () => {
            await navigator.clipboard.writeText(value).catch(() => {})
            setCopied(true)
          }}
        >
          {copied ? <Check size={14} /> : <Copy size={14} />} {copied ? 'Copied' : 'Copy'}
        </button>
        <button type="button" className={btn} onClick={onDone}>
          Done
        </button>
      </div>
    </div>
  )
}

function WorkspaceTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Ws>(ws, 'workspace.get')
  const shell = useShell()
  const [msg, setMsg] = useState<string>()
  if (!data) return null
  return (
    <form
      className="max-w-sm space-y-3"
      onSubmit={async (e) => {
        e.preventDefault()
        try {
          await cmd(ws, 'workspace.update', { name: new FormData(e.currentTarget).get('name'), settings: {} })
          setMsg('Saved')
          reload()
          shell.reload()
        } catch (x) {
          setMsg((x as Error).message)
        }
      }}
    >
      <Field label="Workspace name">
        {(id) => <input id={id} name="name" defaultValue={data.name} required className={inputCls} />}
      </Field>
      <button type="submit" className={btnPrimary}>
        Save
      </button>
      <p role="status" className="text-[13px] text-muted">
        {msg}
      </p>
    </form>
  )
}

function MembersTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<{ user_id: string; email: string; name: string; role: string }[]>(
    ws,
    'member.list',
  )
  const [err, setErr] = useState<string>()
  return (
    <div className="space-y-4">
      <ul className="divide-y divide-line rounded-md border border-line">
        {data?.map((m) => (
          <li key={m.user_id} className="flex items-center gap-3 px-3 py-2">
            <span className="font-medium">{m.name}</span>
            <span className="text-muted">{m.email}</span>
            <span className="ml-auto rounded bg-hover px-1.5 text-[12px]">{m.role}</span>
          </li>
        ))}
      </ul>
      <form
        className="flex max-w-xl items-end gap-2"
        onSubmit={async (e) => {
          e.preventDefault()
          const form = e.currentTarget
          const f = new FormData(form)
          try {
            await cmd(ws, 'member.add', { email: f.get('email'), role: f.get('role') })
            form.reset()
            setErr(undefined)
            reload()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <div className="flex-1">
          <Field label="Email">
            {(id) => <input id={id} name="email" type="email" required className={inputCls} />}
          </Field>
        </div>
        <Field label="Role">
          {(id) => (
            <select id={id} name="role" defaultValue="member" className={inputCls}>
              <option>admin</option>
              <option>member</option>
              <option>guest</option>
            </select>
          )}
        </Field>
        <button type="submit" className={btnPrimary}>
          Add member
        </button>
      </form>
      <ErrorText error={err} />
    </div>
  )
}

function KeysTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Key[]>(ws, 'apikey.list')
  const [secret, setSecret] = useState<string>()
  const [err, setErr] = useState<string>()
  return (
    <div className="space-y-4">
      {secret && <Secret value={secret} onDone={() => setSecret(undefined)} />}
      <form
        className="flex max-w-xl items-end gap-2"
        onSubmit={async (e) => {
          e.preventDefault()
          const form = e.currentTarget
          try {
            const r = await cmd<{ api_key: string }>(ws, 'apikey.create', {
              name: new FormData(form).get('name'),
            })
            setSecret(r.api_key)
            form.reset()
            setErr(undefined)
            reload()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <div className="flex-1">
          <Field label="Key name">
            {(id) => <input id={id} name="name" required className={inputCls} />}
          </Field>
        </div>
        <button type="submit" className={btnPrimary}>
          Create API key
        </button>
      </form>
      <ErrorText error={err} />
      <ul className="divide-y divide-line rounded-md border border-line">
        {data
          ?.filter((k) => !k.agent_id)
          .map((k) => (
            <li key={k.id} className="flex items-center gap-3 px-3 py-2">
              <span className="font-medium">{k.name}</span>
              <code className="text-muted">cn_{k.prefix}_...</code>
              <span className="text-[12px] text-muted">
                {k.last_used_at ? `used ${ago(k.last_used_at)}` : 'never used'}
              </span>
              {k.revoked_at ? (
                <span className="ml-auto text-[12px] text-bad">Revoked</span>
              ) : (
                <button
                  type="button"
                  className={`${btn} ml-auto`}
                  aria-label={`Revoke ${k.name}`}
                  onClick={async () => {
                    await cmd(ws, 'apikey.revoke', { id: k.id })
                    reload()
                  }}
                >
                  Revoke
                </button>
              )}
            </li>
          ))}
      </ul>
    </div>
  )
}

function AgentsTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Agent[]>(ws, 'agent.list')
  const [secret, setSecret] = useState<string>()
  const [err, setErr] = useState<string>()
  return (
    <div className="space-y-4">
      {secret && <Secret value={secret} onDone={() => setSecret(undefined)} />}
      <form
        className="flex max-w-xl items-end gap-2"
        onSubmit={async (e) => {
          e.preventDefault()
          const form = e.currentTarget
          try {
            const r = await cmd<{ api_key: string }>(ws, 'agent.create', {
              name: new FormData(form).get('name'),
            })
            setSecret(r.api_key)
            form.reset()
            setErr(undefined)
            reload()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <div className="flex-1">
          <Field label="Agent name">
            {(id) => <input id={id} name="name" required className={inputCls} />}
          </Field>
        </div>
        <button type="submit" className={btnPrimary}>
          Create agent
        </button>
      </form>
      <ErrorText error={err} />
      <ul className="divide-y divide-line rounded-md border border-line">
        {data?.map((a) => (
          <li key={a.id} className="px-3 py-2">
            <span className="font-medium">{a.name}</span> <span className="text-muted">{a.kind}</span>
            <div className="text-[12px] text-muted">{a.scopes.join(', ')}</div>
          </li>
        ))}
      </ul>
    </div>
  )
}

function GroupsTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Ws>(ws, 'workspace.get')
  const [err, setErr] = useState<string>()
  const [picked, setPicked] = useState<string[]>() // optimistic
  if (!data) return null
  const enabled = picked ?? data.settings.agent_tool_groups ?? DEFAULT_GROUPS
  return (
    <fieldset className="max-w-xl space-y-2">
      <legend className="mb-2 text-[13px] text-muted">
        Tool groups agents and API keys may use. Money-moving, outbound and destructive groups are off by
        default.
      </legend>
      {GROUPS.map(([g, desc]) => (
        <label key={g} className="flex cursor-pointer items-start gap-2 rounded px-2 py-1.5 hover:bg-hover">
          <input
            type="checkbox"
            checked={enabled.includes(g)}
            className="mt-1"
            onChange={async (e) => {
              const next = e.target.checked ? [...enabled, g] : enabled.filter((x) => x !== g)
              setPicked(next)
              try {
                await cmd(ws, 'workspace.update', { settings: { agent_tool_groups: next } })
                setErr(undefined)
                reload()
              } catch (x) {
                setErr((x as Error).message)
              }
            }}
          />
          <span>
            <span className="font-medium">{g}</span>
            <span className="block text-[12px] text-muted">{desc}</span>
          </span>
        </label>
      ))}
      <ErrorText error={err} />
    </fieldset>
  )
}

function ConfirmationsTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Confirmation[]>(ws, 'confirmation.list')
  const decide = async (id: string, approve: boolean) => {
    await cmd(ws, 'confirmation.decide', { id, approve })
    reload()
  }
  if (data?.length === 0) return <p className="text-muted">No pending confirmations.</p>
  return (
    <ul className="space-y-2">
      {data?.map((c) => (
        <li key={c.id} className="rounded-md border border-line p-3">
          <div className="flex items-center gap-2">
            <code className="font-medium">{c.command}</code>
            <span className="text-[12px] text-muted">
              by {c.requested_by} · {ago(c.created_at)}
            </span>
            <div className="ml-auto flex gap-2">
              <button type="button" className={btn} onClick={() => decide(c.id, false)}>
                Reject
              </button>
              <button type="button" className={btnPrimary} onClick={() => decide(c.id, true)}>
                Approve
              </button>
            </div>
          </div>
          <pre className="mt-2 overflow-x-auto rounded bg-side p-2 text-[12px]">
            {JSON.stringify(c.input, null, 2)}
          </pre>
        </li>
      ))}
    </ul>
  )
}

function UsageTab({ ws }: { ws: string }) {
  const { data } = useCmd<{ kind: string; quantity: number }[]>(ws, 'usage.summary')
  if (data?.length === 0) return <p className="text-muted">No usage in the last 30 days.</p>
  return (
    <table className="w-full max-w-md text-left">
      <caption className="mb-2 text-left text-[13px] text-muted">Last 30 days</caption>
      <tbody>
        {data?.map((u) => (
          <tr key={u.kind} className="border-b border-line">
            <th scope="row" className="py-1.5 font-normal">
              {u.kind}
            </th>
            <td className="text-right tabular-nums">{u.quantity.toLocaleString()}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

export default function Settings() {
  const { ws } = useShell()
  const [tab, setTab] = useState<TabName>('Workspace')
  return (
    <div className="mx-auto max-w-3xl px-6 py-8">
      <h1 className="mb-4 text-xl font-semibold">Settings</h1>
      <div
        role="tablist"
        aria-label="Settings sections"
        className="mb-6 flex flex-wrap gap-1 border-b border-line"
      >
        {TABS.map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            onClick={() => setTab(t)}
            className="px-3 py-2 text-[13px] text-muted hover:text-fg aria-selected:border-b-2 aria-selected:border-accent aria-selected:font-medium aria-selected:text-fg"
          >
            {t}
          </button>
        ))}
      </div>
      <div role="tabpanel">
        {tab === 'Workspace' && <WorkspaceTab ws={ws} />}
        {tab === 'Members' && <MembersTab ws={ws} />}
        {tab === 'API keys' && <KeysTab ws={ws} />}
        {tab === 'Agents' && <AgentsTab ws={ws} />}
        {tab === 'Tool groups' && <GroupsTab ws={ws} />}
        {tab === 'Confirmations' && <ConfirmationsTab ws={ws} />}
        {tab === 'Usage' && <UsageTab ws={ws} />}
      </div>
    </div>
  )
}
