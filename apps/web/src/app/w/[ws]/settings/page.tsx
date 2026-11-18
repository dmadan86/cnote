'use client'
import {
  Bot,
  Building2,
  Cpu,
  EyeOff,
  Gauge,
  History,
  KeyRound,
  KeySquare,
  ListChecks,
  Lock,
  type LucideIcon,
  Mail,
  Plug,
  Plus,
  Settings2,
  ShieldCheck,
  Sparkles,
  UserPlus,
  Users,
  Webhook,
  Zap,
} from 'lucide-react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useEffect, useState } from 'react'
import { ApprovalRules } from '@/components/approvals/rules'
import { AuditLog } from '@/components/audit/log'
import { Privacy } from '@/components/audit/privacy'
import { CompanyProfile } from '@/components/business-type'
import { CompanyTab } from '@/components/company/profile'
import { HooksTab } from '@/components/hooks-settings'
import { CustomerMessages } from '@/components/messages/customer-messages'
import { Secret } from '@/components/secret'
import { SignInSecurity } from '@/components/security/sign-in'
import { useShell } from '@/components/shell'
import { Avatar, btn, btnPrimary, EmptyState, ErrorText, Field, inputCls, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { GROUPS } from '@/lib/tool-groups'

// Keys stay stable: they are the ?tab= values other pages and e2e link to.
type SettingsTab = { key: string; label: string; desc: string; icon: LucideIcon }
const SECTIONS: { group: string; tabs: SettingsTab[] }[] = [
  {
    group: 'Workspace',
    tabs: [
      {
        key: 'Workspace',
        icon: Settings2,
        label: 'General',
        desc: 'Workspace name, business type and modules.',
      },
      {
        key: 'Company',
        icon: Building2,
        label: 'Company profile',
        desc: 'Legal name, GSTIN, PAN, bank and branches printed on your documents.',
      },
      {
        key: 'Messages',
        icon: Mail,
        label: 'Customer messages',
        desc: 'The emails and WhatsApp messages your customers get: wording, look and sender.',
      },
      {
        key: 'Members',
        icon: Users,
        label: 'Members',
        desc: 'Who is in this workspace and what they can do.',
      },
      { key: 'Usage', icon: Gauge, label: 'Usage', desc: 'What this workspace used in the last 30 days.' },
    ],
  },
  {
    group: 'Approvals',
    tabs: [
      {
        key: 'Approvals',
        icon: ListChecks,
        label: 'Approval rules',
        desc: 'Which payments and documents need someone to approve them first.',
      },
      {
        key: 'Confirmations',
        icon: ShieldCheck,
        label: 'Agent confirmations',
        desc: 'Actions agents asked to take that are waiting for you.',
      },
    ],
  },
  {
    group: 'AI & integrations',
    tabs: [
      { key: 'Agents', icon: Bot, label: 'Agents', desc: 'AI agents that work in this workspace.' },
      {
        key: 'Tool groups',
        icon: KeyRound,
        label: 'Agent permissions',
        desc: 'What agents and API keys are allowed to do.',
      },
      {
        key: 'Connected apps',
        icon: Plug,
        label: 'Connected apps',
        desc: 'Apps you signed in to with C Note.',
      },
      { key: 'API keys', icon: KeySquare, label: 'API keys', desc: 'Keys for scripts and other software.' },
      {
        key: 'Webhooks',
        icon: Webhook,
        label: 'Webhooks',
        desc: 'Tell other systems when something changes here.',
      },
    ],
  },
  {
    group: 'Security & privacy',
    tabs: [
      {
        key: 'Security',
        icon: Lock,
        label: 'Sign-in & security',
        desc: 'Two-step sign-in, single sign-on and active sessions.',
      },
      {
        key: 'Privacy',
        icon: EyeOff,
        label: 'Privacy',
        desc: 'Personal data requests under the DPDP Act: export and erasure.',
      },
      {
        key: 'Audit log',
        icon: History,
        label: 'Audit log',
        desc: 'A record of who changed what, and when.',
      },
    ],
  },
]
const ALL = SECTIONS.flatMap((s) => s.tabs)
const TABS = ALL.map((t) => t.key)
type TabName = (typeof TABS)[number]

type Ws = {
  name: string
  settings: {
    agent_tool_groups?: string[]
    business_type?: string
    company_size?: string
    chat_agents?: boolean
  }
}
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

const DEFAULT_GROUPS = ['read', 'write_notes', 'write_items', 'manage_crm']

function WorkspaceTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Ws>(ws, 'workspace.get')
  const shell = useShell()
  const [msg, setMsg] = useState<string>()
  if (!data) return null
  return (
    <div className="space-y-8">
      <form
        className="max-w-sm space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          try {
            await cmd(ws, 'workspace.update', {
              name: new FormData(e.currentTarget).get('name'),
              settings: {},
            })
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
      <section className="max-w-2xl space-y-2">
        <h2 className="text-[14px] font-semibold">Business type</h2>
        <p className="text-[13px] text-muted">
          Changing this re-applies the modules shown in the sidebar and the CRM deal stage names.
        </p>
        <CompanyProfile
          ws={ws}
          type={data.settings.business_type}
          size={data.settings.company_size}
          onDone={() => {
            reload()
            shell.reload()
          }}
        />
      </section>
    </div>
  )
}

const ROLES = [
  { value: 'admin', label: 'Admin', hint: 'Manages settings, members and money' },
  { value: 'member', label: 'Member', hint: 'Works in spaces they can see' },
  { value: 'guest', label: 'Guest', hint: 'Sees only what is shared with them' },
]

function MembersTab({ ws }: { ws: string }) {
  const { me } = useShell()
  const { data, reload } = useCmd<{ user_id: string; email: string; name: string; role: string }[]>(
    ws,
    'member.list',
  )
  const [err, setErr] = useState<string>()
  const myRole = me.workspaces.find((w) => w.id === ws)?.role
  const canManage = myRole === 'owner' || myRole === 'admin'
  const setRole = async (user_id: string, role: string) => {
    try {
      await cmd(ws, 'member.update', { user_id, role })
      setErr(undefined)
      reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <div className="space-y-6">
      {canManage && (
        <form
          className="rounded-lg border border-line p-4"
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
          <h3 className="text-[14px] font-medium">Invite someone</h3>
          <p className="mt-0.5 mb-3 text-[12px] text-muted">
            They join this workspace with the role you pick.
          </p>
          <div className="flex flex-wrap items-end gap-2">
            <div className="min-w-56 flex-1">
              <Field label="Email">
                {(id) => (
                  <input
                    id={id}
                    name="email"
                    type="email"
                    required
                    placeholder="name@company.com"
                    className={inputCls}
                  />
                )}
              </Field>
            </div>
            <Field label="Role">
              {(id) => (
                <select id={id} name="role" defaultValue="member" className={inputCls}>
                  {ROLES.map((r) => (
                    <option key={r.value} value={r.value}>
                      {r.label} · {r.hint}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <button type="submit" className={btnPrimary}>
              <UserPlus size={14} aria-hidden /> Add member
            </button>
          </div>
        </form>
      )}
      <ErrorText error={err} />
      <section>
        <h3 className="mb-2 text-[13px] font-medium">
          People <span className="font-normal text-muted">· {data?.length ?? 0}</span>
        </h3>
        <div className="overflow-hidden rounded-lg border border-line">
          <table className="w-full text-left text-[13px]">
            <thead className="border-b border-line bg-side text-[12px] text-muted">
              <tr>
                <th scope="col" className="px-4 py-2 font-medium">
                  Name
                </th>
                <th scope="col" className="w-44 px-4 py-2 font-medium">
                  Role
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {data?.map((m) => (
                <tr key={m.user_id}>
                  <td className="px-4 py-2.5">
                    <div className="flex items-center gap-3">
                      <Avatar name={m.name} size={28} />
                      <div className="min-w-0">
                        <div className="truncate font-medium">
                          {m.name}
                          {m.user_id === me.user.id && (
                            <span className="ml-1.5 font-normal text-muted">(you)</span>
                          )}
                        </div>
                        <div className="truncate text-[12px] text-muted">{m.email}</div>
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-2.5">
                    {canManage && m.role !== 'owner' && m.user_id !== me.user.id ? (
                      <select
                        aria-label={`Role of ${m.name}`}
                        className={`${inputCls.replace('w-full ', '')} w-32 py-1 text-[13px]`}
                        value={m.role}
                        onChange={(e) => setRole(m.user_id, e.target.value)}
                      >
                        {ROLES.map((r) => (
                          <option key={r.value} value={r.value}>
                            {r.label}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <Pill>{m.role.charAt(0).toUpperCase() + m.role.slice(1)}</Pill>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="mt-2 text-[12px] text-muted">
          Departments, managers and leaving dates are on the People page.
        </p>
      </section>
    </div>
  )
}

function KeysTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Key[]>(ws, 'apikey.list')
  const [secret, setSecret] = useState<string>()
  const [err, setErr] = useState<string>()
  const keys = data?.filter((k) => !k.agent_id)
  return (
    <div className="space-y-6">
      {secret && <Secret value={secret} onDone={() => setSecret(undefined)} />}
      <form
        className="rounded-lg border border-line p-4"
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
        <h3 className="text-[14px] font-medium">Create a key</h3>
        <p className="mt-0.5 mb-3 text-[12px] text-muted">
          The key is shown once. Store it in your script's settings, never in code you share.
        </p>
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-56 flex-1">
            <Field label="Key name">
              {(id) => (
                <input
                  id={id}
                  name="name"
                  required
                  placeholder="e.g. Tally sync script"
                  className={inputCls}
                />
              )}
            </Field>
          </div>
          <button type="submit" className={btnPrimary}>
            <Plus size={14} aria-hidden /> Create API key
          </button>
        </div>
      </form>
      <ErrorText error={err} />
      {keys?.length === 0 ? (
        <div className="rounded-lg border border-line">
          <EmptyState icon={KeySquare} title="No API keys yet">
            Keys let scripts and other software use C Note as you. Agent keys are on the Agents page.
          </EmptyState>
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border border-line">
          <table className="w-full text-left text-[13px]">
            <thead className="border-b border-line bg-side text-[12px] text-muted">
              <tr>
                <th scope="col" className="px-4 py-2 font-medium">
                  Name
                </th>
                <th scope="col" className="px-4 py-2 font-medium">
                  Key
                </th>
                <th scope="col" className="px-4 py-2 font-medium">
                  Last used
                </th>
                <th scope="col" className="w-28 px-4 py-2">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {keys?.map((k) => (
                <tr key={k.id} className={k.revoked_at ? 'text-muted' : ''}>
                  <td className="px-4 py-2.5 font-medium">{k.name}</td>
                  <td className="px-4 py-2.5">
                    <code className="rounded bg-hover px-1.5 py-0.5 text-[12px]">cn_{k.prefix}_…</code>
                  </td>
                  <td className="px-4 py-2.5 text-muted">{k.last_used_at ? ago(k.last_used_at) : 'Never'}</td>
                  <td className="px-4 py-2.5 text-right">
                    {k.revoked_at ? (
                      <Pill tone="bad">Revoked</Pill>
                    ) : (
                      <button
                        type="button"
                        className={`${btn} text-bad`}
                        aria-label={`Revoke ${k.name}`}
                        onClick={async () => {
                          await cmd(ws, 'apikey.revoke', { id: k.id })
                          reload()
                        }}
                      >
                        Revoke
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
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
      <label className="flex cursor-pointer items-start gap-2 rounded px-2 py-1.5 hover:bg-hover">
        <input
          type="checkbox"
          checked={data.settings.chat_agents === true}
          className="mt-1"
          onChange={async (e) => {
            try {
              await cmd(ws, 'workspace.update', { settings: { chat_agents: e.target.checked } })
              setErr(undefined)
              reload()
            } catch (x) {
              setErr((x as Error).message)
            }
          }}
        />
        <span>
          <span className="font-medium">Agents in chat</span>
          <span className="block text-[12px] text-muted">
            Let agents be added to chat channels by their owner or an admin. They post as themselves, marked
            as agents, and only see the channels they were added to, never direct messages. Off by default.
          </span>
        </span>
      </label>
      <ErrorText error={err} />
    </fieldset>
  )
}

function ConfirmationsTab({ ws }: { ws: string }) {
  const { data, error, reload } = useCmd<Confirmation[]>(ws, 'confirmation.list')
  const decide = async (id: string, approve: boolean) => {
    await cmd(ws, 'confirmation.decide', { id, approve })
    reload()
  }
  if (error)
    return (
      <div className="space-y-2">
        <ErrorText error={error} />
        <button type="button" className={btn} onClick={reload}>
          Try again
        </button>
      </div>
    )
  if (!data) return <div aria-busy="true" className="h-24 animate-pulse rounded-lg bg-hover" />
  if (data.length === 0)
    return (
      <div className="rounded-lg border border-line">
        <EmptyState icon={ShieldCheck} title="No pending confirmations">
          When an agent asks to send, pay or delete something, the request waits here for you to approve or
          reject.
        </EmptyState>
      </div>
    )
  return (
    <ul className="space-y-3">
      {data.map((c) => (
        <li key={c.id} className="rounded-lg border border-line">
          <div className="flex items-start gap-3 p-4">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-line bg-hover text-muted">
              <Bot size={16} aria-hidden />
            </span>
            <div className="min-w-0 flex-1">
              <code className="text-[13px] font-medium">{c.command}</code>
              <p className="mt-0.5 text-[12px] text-muted">
                Requested by {c.requested_by} · {ago(c.created_at)}
              </p>
            </div>
            <div className="flex shrink-0 gap-2">
              <button type="button" className={btn} onClick={() => decide(c.id, false)}>
                Reject
              </button>
              <button type="button" className={btnPrimary} onClick={() => decide(c.id, true)}>
                Approve
              </button>
            </div>
          </div>
          <details className="border-t border-line px-4 py-2 text-[12px]">
            <summary className="cursor-pointer text-muted hover:text-fg">What it will do</summary>
            <pre className="mt-2 overflow-x-auto rounded bg-side p-2">{JSON.stringify(c.input, null, 2)}</pre>
          </details>
        </li>
      ))}
    </ul>
  )
}

type ConnectedApp = { id: string; name: string; created_at: string; last_used_at: string | null }

function AppsTab({ ws }: { ws: string }) {
  const { data, reload } = useCmd<ConnectedApp[]>(ws, 'oauth_app.list')
  if (data?.length === 0)
    return <p className="text-muted">No connected apps. MCP clients you approve through OAuth appear here.</p>
  return (
    <ul className="divide-y divide-line rounded-md border border-line">
      {data?.map((a) => (
        <li key={a.id} className="flex items-center gap-3 px-3 py-2">
          <span className="font-medium">{a.name}</span>
          <span className="text-[12px] text-muted">
            connected {ago(a.created_at)}, {a.last_used_at ? `used ${ago(a.last_used_at)}` : 'never used'}
          </span>
          <button
            type="button"
            className={`${btn} ml-auto`}
            aria-label={`Disconnect ${a.name}`}
            onClick={async () => {
              await cmd(ws, 'oauth_app.revoke', { id: a.id })
              reload()
            }}
          >
            Disconnect
          </button>
        </li>
      ))}
    </ul>
  )
}

// Friendly names for core.usage kinds; anything else is listed under "Other" as recorded.
const USAGE_CARDS = [
  {
    label: 'AI tokens',
    kinds: ['ai.tokens_in', 'ai.tokens_out'],
    icon: Sparkles,
    hint: 'Text the AI read and wrote for summaries, drafts and answers.',
  },
  {
    label: 'Agent & API calls',
    kinds: ['mcp.call'],
    icon: Bot,
    hint: 'Actions agents and connected apps ran through C Note.',
  },
  {
    label: 'Automation runs',
    kinds: ['automation.run'],
    icon: Zap,
    hint: 'Times a project automation fired.',
  },
  {
    label: 'Background jobs',
    kinds: ['worker.atomic'],
    icon: Cpu,
    hint: 'Imports, reminders and other work done in the background.',
  },
]

function UsageTab({ ws }: { ws: string }) {
  const { data } = useCmd<{ kind: string; quantity: number }[]>(ws, 'usage.summary')
  if (!data) return <div aria-busy="true" className="h-24 animate-pulse rounded-lg bg-hover" />
  if (data.length === 0)
    return (
      <div className="rounded-lg border border-line">
        <EmptyState icon={Gauge} title="No usage in the last 30 days">
          AI, agent calls and automations this workspace uses will be counted here.
        </EmptyState>
      </div>
    )
  const n = (k: string) => data.find((u) => u.kind === k)?.quantity ?? 0
  const fmt = (v: number) => v.toLocaleString('en-IN')
  const known = USAGE_CARDS.flatMap((c) => c.kinds)
  const other = data.filter((u) => !known.includes(u.kind))
  return (
    <div className="space-y-6">
      <div className="grid gap-3 sm:grid-cols-2">
        {USAGE_CARDS.map((c) => (
          <div key={c.label} className="rounded-lg border border-line p-4">
            <div className="flex items-center gap-2 text-[13px] text-muted">
              <c.icon size={15} aria-hidden />
              {c.label}
            </div>
            <div className="mt-2 text-xl font-semibold tabular-nums">
              {fmt(c.kinds.reduce((t, k) => t + n(k), 0))}
            </div>
            {c.kinds.length === 2 ? (
              <p className="mt-1 text-[12px] text-muted">
                {fmt(n(c.kinds[0]))} read · {fmt(n(c.kinds[1]))} written
              </p>
            ) : null}
            <p className="mt-1 text-[12px] text-muted">{c.hint}</p>
          </div>
        ))}
      </div>
      {other.length > 0 && (
        <section>
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wide text-muted">Other</h3>
          <ul className="divide-y divide-line rounded-lg border border-line">
            {other.map((u) => (
              <li key={u.kind} className="flex justify-between px-4 py-2 text-[13px]">
                <span>{u.kind}</span>
                <span className="tabular-nums">{fmt(u.quantity)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

export default function Settings() {
  const { ws } = useShell()
  const router = useRouter()
  const want = useSearchParams().get('tab')
  // unknown or missing ?tab= falls back to General
  const tab = (TABS.find((x) => x === want) ?? 'Workspace') as TabName
  const cur = ALL.find((t) => t.key === tab) ?? ALL[0]
  const href = (k: string) => `/w/${ws}/settings?tab=${encodeURIComponent(k)}`
  return (
    <div className="mx-auto flex max-w-[1080px] gap-10 px-6 py-8">
      <nav
        aria-label="Settings"
        className="sticky top-6 hidden max-h-[calc(100vh-6rem)] w-52 shrink-0 self-start overflow-y-auto md:block"
      >
        <h1 className="mb-4 px-2 text-xl font-semibold">Settings</h1>
        {SECTIONS.map((s) => (
          <div key={s.group} className="mb-4">
            <div className="mb-1 px-2 text-[11px] font-medium uppercase tracking-wide text-muted">
              {s.group}
            </div>
            <ul>
              {s.tabs.map((t) => (
                <li key={t.key}>
                  <Link
                    href={href(t.key)}
                    aria-current={t.key === tab ? 'page' : undefined}
                    className="flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] text-muted hover:bg-hover hover:text-fg aria-[current=page]:bg-hover aria-[current=page]:font-medium aria-[current=page]:text-fg"
                  >
                    <t.icon size={15} aria-hidden className="shrink-0" />
                    {t.label}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </nav>
      <main className="min-w-0 flex-1">
        <label className="mb-6 block md:hidden">
          <span className="mb-1 block text-[12px] text-muted">Settings section</span>
          <select className={inputCls} value={tab} onChange={(e) => router.push(href(e.target.value))}>
            {SECTIONS.map((s) => (
              <optgroup key={s.group} label={s.group}>
                {s.tabs.map((t) => (
                  <option key={t.key} value={t.key}>
                    {t.label}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
        </label>
        <header className="mb-6 border-b border-line pb-4">
          <h2 className="flex items-center gap-2.5 text-lg font-semibold">
            <span className="flex h-8 w-8 items-center justify-center rounded-md border border-line text-muted">
              <cur.icon size={17} aria-hidden />
            </span>
            {cur.label}
          </h2>
          <p className="mt-1 text-[13px] text-muted">{cur.desc}</p>
        </header>
        <div className="max-w-3xl">
          {tab === 'Workspace' && <WorkspaceTab ws={ws} />}
          {tab === 'Company' && <CompanyTab ws={ws} />}
          {tab === 'Messages' && <CustomerMessages ws={ws} />}
          {tab === 'Members' && <MembersTab ws={ws} />}
          {tab === 'API keys' && <KeysTab ws={ws} />}
          {tab === 'Agents' && <AgentsTab ws={ws} />}
          {tab === 'Tool groups' && <GroupsTab ws={ws} />}
          {tab === 'Approvals' && <ApprovalRules ws={ws} />}
          {tab === 'Confirmations' && <ConfirmationsTab ws={ws} />}
          {tab === 'Connected apps' && <AppsTab ws={ws} />}
          {tab === 'Webhooks' && <HooksTab ws={ws} />}
          {tab === 'Usage' && <UsageTab ws={ws} />}
          {tab === 'Audit log' && <AuditLog ws={ws} />}
          {tab === 'Privacy' && <Privacy ws={ws} />}
          {tab === 'Security' && <SignInSecurity ws={ws} />}
        </div>
      </main>
    </div>
  )
}
