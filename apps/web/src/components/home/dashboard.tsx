'use client'
import {
  AlertTriangle,
  ArrowUpRight,
  Banknote,
  CircleCheck,
  ClipboardCheck,
  FileText,
  Handshake,
  Inbox,
  ListChecks,
  type LucideIcon,
  Receipt,
  ShieldCheck,
  Sparkles,
  Users,
} from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { money } from '@/components/billing/fmt'
import { BranchSelect } from '@/components/company/branch-filter'
import { CheckInCard } from '@/components/hr/checkin'
import { useShell } from '@/components/shell'
import { EmptyState } from '@/components/ui'
import { useCmd } from '@/lib/hooks'

type Tile<T> = T | undefined
type Summary = {
  view: 'owner' | 'personal'
  tiles: {
    tasks_due?: { count: number; overdue: number }
    payments_pending?: { count: number; total_minor: number; overdue_count: number; overdue_minor: number }
    new_inquiries?: { count: number }
    follow_ups?: { count: number; overdue: number }
    approvals?: { count: number; confirmations: number; quotes_awaiting: number; to_approve: number }
    bills_due?: { count: number; total_minor: number; overdue_count: number }
    tickets_breaching?: { count: number }
    expiring_stock?: {
      days: number
      soon: number
      expired: number
      sample: { item_name: string; number: string }[]
    }
    low_stock?: { count: number; items: number; sample: { id: string; name: string }[] }
    people_present?: { present: number; expected: number; on_leave: number; not_marked: number }
    urgent?: { count: number; overdue_tasks: number; overdue_invoices: number }
  }
  attention: {
    kind: 'task' | 'invoice' | 'bill' | 'quote' | 'deal' | 'confirmation' | 'approval'
    id: string
    key: string | null
    title: string
    reason: string
    amount_minor: number | null
    date: string | null
    severity: 'overdue' | 'due' | 'info'
  }[]
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`

const compact = new Intl.NumberFormat('en-IN', {
  style: 'currency',
  currency: 'INR',
  notation: 'compact',
  maximumFractionDigits: 1,
})
/** ₹ short form for the big number (₹1.2L, ₹3.4Cr); the full amount stays in the link's accessible name. */
const shortMoney = (minor: number) =>
  Math.abs(minor) >= 10_000_000 ? compact.format(minor / 100) : money(minor).replace(/\.00$/, '')

const KIND: Record<Summary['attention'][number]['kind'], { icon: LucideIcon; label: string }> = {
  task: { icon: ListChecks, label: 'Task' },
  invoice: { icon: Receipt, label: 'Invoice' },
  bill: { icon: Banknote, label: 'Bill' },
  quote: { icon: FileText, label: 'Quote' },
  deal: { icon: Handshake, label: 'Lead' },
  confirmation: { icon: ShieldCheck, label: 'Confirm' },
  approval: { icon: ClipboardCheck, label: 'Approval' },
}
const GROUPS = [
  { sev: 'overdue', title: 'Overdue', tone: 'bg-bad-bg text-bad' },
  { sev: 'due', title: 'Due now', tone: 'bg-warn-bg text-warn' },
  { sev: 'info', title: 'For your information', tone: 'bg-hover text-muted' },
] as const

function TileLink({
  href,
  label,
  value,
  detail,
  alert,
  icon: Icon,
  shown,
  big,
}: {
  href: string
  label: string
  value: string
  detail: string
  icon: LucideIcon
  /** Short form of the value for display (defaults to value). */
  shown?: string
  big?: boolean
  /** Overdue text: shown in red with an icon, and always spelled out in words (never colour alone). */
  alert?: string
}) {
  return (
    <Link
      href={href}
      aria-label={`${label}: ${value}. ${detail}${alert ? `. ${alert}` : ''}`}
      className={`group block rounded-xl border border-line bg-canvas shadow-sm transition hover:border-accent hover:shadow-md focus-visible:outline-2 focus-visible:outline-accent ${big ? 'p-4' : 'p-3'}`}
    >
      <span className="flex items-center gap-2 text-[12px] font-medium text-muted">
        <span aria-hidden className="flex h-6 w-6 items-center justify-center rounded-md bg-hover text-fg">
          <Icon size={14} />
        </span>
        <span className="min-w-0 flex-1 leading-tight">{label}</span>
        <ArrowUpRight size={14} aria-hidden className="opacity-0 transition group-hover:opacity-100" />
      </span>
      <span className={`mt-2 block font-semibold tabular-nums ${big ? 'text-2xl sm:text-3xl' : 'text-xl'}`}>
        {shown ?? value}
      </span>
      <span className="mt-0.5 line-clamp-2 block text-[12px] text-muted">{detail}</span>
      {alert && (
        <span className="mt-2 inline-flex items-start gap-1 rounded-lg bg-bad-bg px-2 py-0.5 text-[12px] font-medium text-bad">
          <AlertTriangle size={12} aria-hidden /> {alert}
        </span>
      )}
    </Link>
  )
}

export function HomeDashboard() {
  const { ws } = useShell()
  const [branch, setBranch] = useState('')
  const { data, error } = useCmd<Summary>(ws, 'home.summary', branch ? { branch_id: branch } : {})
  const [all, setAll] = useState(false)
  const base = `/w/${ws}`
  if (error)
    return (
      <p role="alert" className="text-muted">
        Could not load your summary.
      </p>
    )
  if (!data) return <Skeleton />
  const t = data.tiles
  const href = (a: Summary['attention'][number]) =>
    ({
      task: `${base}/projects/${a.key}`,
      invoice: `${base}/billing/${a.id}`,
      bill: `${base}/billing/bills/${a.id}`,
      quote: `${base}/billing/quotes/${a.id}`,
      deal: `${base}/crm/deals/${a.id}`,
      confirmation: `${base}/settings?tab=Confirmations`,
      approval: `${base}/approvals`,
    })[a.kind]
  const rows = all ? data.attention : data.attention.slice(0, 5)
  const tiles: Tile<React.ReactNode>[] = [
    t.payments_pending && (
      <TileLink
        key="pay"
        icon={Banknote}
        big
        href={`${base}/billing?filter=outstanding`}
        label="Payments pending"
        value={money(t.payments_pending.total_minor)}
        shown={shortMoney(t.payments_pending.total_minor)}
        detail={
          t.payments_pending.count ? plural(t.payments_pending.count, 'unpaid invoice') : 'No unpaid invoices'
        }
        alert={
          t.payments_pending.overdue_count
            ? `${plural(t.payments_pending.overdue_count, 'invoice')} overdue, ${money(t.payments_pending.overdue_minor)}`
            : undefined
        }
      />
    ),
    t.bills_due && (
      <TileLink
        key="bills"
        icon={Receipt}
        href={`${base}/billing/bills?due=soon`}
        label="Bills due"
        value={money(t.bills_due.total_minor)}
        shown={shortMoney(t.bills_due.total_minor)}
        detail={
          t.bills_due.count
            ? `${plural(t.bills_due.count, 'bill')} due within 7 days`
            : 'No bills due this week'
        }
        alert={t.bills_due.overdue_count ? `${t.bills_due.overdue_count} overdue` : undefined}
      />
    ),
    t.approvals && (
      <TileLink
        key="appr"
        icon={ClipboardCheck}
        big
        href={
          t.approvals.to_approve
            ? `${base}/approvals`
            : t.approvals.confirmations
              ? `${base}/settings?tab=Confirmations`
              : `${base}/billing/quotes?status=sent`
        }
        label="Approvals waiting"
        value={String(t.approvals.count)}
        detail={`${t.approvals.to_approve} to approve, ${t.approvals.confirmations} to confirm, ${t.approvals.quotes_awaiting} quotes awaiting customer`}
      />
    ),
    t.tasks_due && (
      <TileLink
        key="tasks"
        icon={ListChecks}
        big
        href={`${base}/projects?due=me`}
        label="Tasks due"
        value={String(t.tasks_due.count)}
        detail={t.tasks_due.count ? 'Assigned to you, due today or earlier' : 'Nothing due'}
        alert={t.tasks_due.overdue ? `${t.tasks_due.overdue} overdue` : undefined}
      />
    ),
    t.new_inquiries && (
      <TileLink
        key="inq"
        icon={Inbox}
        big
        href={`${base}/crm/inbox`}
        label="New inquiries"
        value={String(t.new_inquiries.count)}
        detail="New leads in the last 7 days"
      />
    ),
    t.follow_ups && (
      <TileLink
        key="fu"
        icon={Handshake}
        href={`${base}/crm/follow-ups`}
        label="Follow-ups"
        value={String(t.follow_ups.count)}
        detail={t.follow_ups.count ? 'Due today or earlier' : 'Nothing due'}
        alert={t.follow_ups.overdue ? `${t.follow_ups.overdue} overdue` : undefined}
      />
    ),
    t.tickets_breaching && (
      <TileLink
        key="tkt"
        icon={AlertTriangle}
        href={`${base}/crm/tickets?filter=breached`}
        label="Tickets breaching SLA"
        value={String(t.tickets_breaching.count)}
        detail={t.tickets_breaching.count ? 'Past their due time' : 'All within SLA'}
        alert={t.tickets_breaching.count ? 'Needs attention' : undefined}
      />
    ),
    t.low_stock && (
      <TileLink
        key="stock"
        icon={Inbox}
        href={`${base}/billing/stock`}
        label="Low stock"
        value={String(t.low_stock.count)}
        detail={
          t.low_stock.count
            ? t.low_stock.sample.map((s) => s.name).join(', ')
            : `${plural(t.low_stock.items, 'item')}, all above reorder level`
        }
        alert={t.low_stock.count ? 'Needs reordering' : undefined}
      />
    ),
    t.expiring_stock && (
      <TileLink
        key="expiring"
        icon={AlertTriangle}
        href={`${base}/billing/stock/expiry`}
        label="Expiring in 30 days"
        value={String(t.expiring_stock.soon)}
        detail={
          t.expiring_stock.expired
            ? `${t.expiring_stock.expired} batch${t.expiring_stock.expired === 1 ? '' : 'es'} already expired`
            : t.expiring_stock.sample.map((s) => `${s.item_name} ${s.number}`).join(', ') ||
              'No batch expires soon'
        }
        alert={t.expiring_stock.soon || t.expiring_stock.expired ? 'Needs attention' : undefined}
      />
    ),
    t.people_present && (
      <TileLink
        key="people"
        icon={Users}
        href={`${base}/people/attendance`}
        label="People present"
        value={`${t.people_present.present} of ${t.people_present.expected}`}
        detail={`${t.people_present.on_leave} on leave, ${t.people_present.not_marked} not marked yet`}
      />
    ),
    t.urgent && (
      <TileLink
        key="urg"
        icon={AlertTriangle}
        href={t.urgent.overdue_invoices ? `${base}/billing?filter=overdue` : `${base}/projects?due=me`}
        label="Urgent"
        value={String(t.urgent.count)}
        detail={`${plural(t.urgent.overdue_tasks, 'overdue task')}, ${plural(t.urgent.overdue_invoices, 'overdue invoice')}`}
      />
    ),
  ].filter(Boolean)

  const bigTiles = tiles.filter((x) => (x as React.ReactElement<{ big?: boolean }>).props.big)
  const smallTiles = tiles.filter((x) => !(x as React.ReactElement<{ big?: boolean }>).props.big)
  const pp = t.payments_pending
  const present = t.people_present

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="-mt-6 text-[13px] text-muted">
          {new Intl.DateTimeFormat('en-IN', {
            weekday: 'long',
            day: 'numeric',
            month: 'long',
            timeZone: 'Asia/Kolkata',
          }).format(new Date())}
        </p>
        <BranchSelect ws={ws} value={branch} onChange={setBranch} allLabel="All branches (invoices)" />
      </div>
      <CheckInCard />
      <AskCard />
      {tiles.length > 0 && (
        <section aria-label="Today at a glance" className="space-y-3">
          {bigTiles.length > 0 && <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">{bigTiles}</div>}
          {smallTiles.length > 0 && (
            <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-5">{smallTiles}</div>
          )}
        </section>
      )}
      <div className="grid gap-6 lg:grid-cols-3">
        <section aria-labelledby="attention" className="lg:col-span-2">
          <div className="mb-2 flex items-baseline justify-between">
            <h2 id="attention" className="text-[15px] font-semibold">
              Needs your attention
            </h2>
            {data.attention.length > 0 && (
              <span className="text-[12px] text-muted">{plural(data.attention.length, 'item')}</span>
            )}
          </div>
          <div className="rounded-xl border border-line bg-canvas shadow-sm">
            {data.attention.length === 0 ? (
              <EmptyState compact icon={CircleCheck} title="You are caught up">
                Nothing needs action today.
              </EmptyState>
            ) : (
              GROUPS.map((g) => {
                const items = rows.filter((a) => a.severity === g.sev)
                if (!items.length) return null
                return (
                  <div key={g.sev} className="border-b border-line last:border-b-0">
                    <h3 className="px-4 pt-3 text-[12px] font-semibold uppercase tracking-wide text-muted">
                      {g.title} ({items.length})
                    </h3>
                    <ul className="divide-y divide-line">
                      {items.map((a) => {
                        const K = KIND[a.kind]
                        return (
                          <li key={`${a.kind}-${a.id}`}>
                            <Link
                              href={href(a)}
                              className="flex items-center gap-3 px-4 py-2.5 hover:bg-hover"
                            >
                              <span
                                aria-hidden
                                className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${g.tone}`}
                              >
                                <K.icon size={16} />
                              </span>
                              <span className="min-w-0 flex-1">
                                <span className="block truncate font-medium">{a.title}</span>
                                <span
                                  className={`block text-[13px] ${a.severity === 'overdue' ? 'font-medium text-bad' : 'text-muted'}`}
                                >
                                  {a.severity === 'overdue' && (
                                    <AlertTriangle size={12} aria-hidden className="mr-1 inline" />
                                  )}
                                  {a.reason}
                                </span>
                              </span>
                              <span className="shrink-0 text-right text-[13px] tabular-nums">
                                {a.amount_minor != null && (
                                  <span className="block font-medium">{money(a.amount_minor)}</span>
                                )}
                                {a.date && <span className="block text-muted">{a.date}</span>}
                              </span>
                            </Link>
                          </li>
                        )
                      })}
                    </ul>
                  </div>
                )
              })
            )}
          </div>
          {data.attention.length > 5 && (
            <button
              type="button"
              onClick={() => setAll(!all)}
              aria-expanded={all}
              className="mt-2 text-[13px] text-muted underline"
            >
              {all ? 'Show fewer' : `Show all ${data.attention.length}`}
            </button>
          )}
        </section>
        {(pp || present) && (
          <section aria-labelledby="snapshot" className="space-y-3">
            <h2 id="snapshot" className="mb-2 text-[15px] font-semibold">
              Snapshot
            </h2>
            {pp && (
              <div className="rounded-xl border border-line bg-canvas p-4 shadow-sm">
                <p className="text-[12px] font-medium text-muted">Receivables overdue</p>
                <p className="mt-1 text-xl font-semibold tabular-nums">
                  {money(pp.overdue_minor)}
                  <span className="text-[13px] font-normal text-muted"> of {money(pp.total_minor)}</span>
                </p>
                <Bar
                  part={pp.overdue_minor}
                  whole={pp.total_minor}
                  tone="bg-bad"
                  label="Overdue share of receivables"
                />
              </div>
            )}
            {present && (
              <div className="rounded-xl border border-line bg-canvas p-4 shadow-sm">
                <p className="text-[12px] font-medium text-muted">Team present today</p>
                <p className="mt-1 text-xl font-semibold tabular-nums">
                  {present.present}
                  <span className="text-[13px] font-normal text-muted"> of {present.expected} expected</span>
                </p>
                <Bar
                  part={present.present}
                  whole={present.expected}
                  tone="bg-ok"
                  label="Team present share"
                />
              </div>
            )}
          </section>
        )}
      </div>
    </div>
  )
}

/** Thin share bar; the numbers beside it carry the meaning, so it is decorative. */
function AskCard() {
  const { openAsk } = useShell()
  const [q, setQ] = useState('')
  return (
    <form
      aria-label="Ask your business"
      className="flex items-center gap-2 rounded-lg border border-line px-3 py-2"
      onSubmit={(e) => {
        e.preventDefault()
        openAsk(q.trim() || undefined)
        setQ('')
      }}
    >
      <Sparkles size={15} className="text-muted" aria-hidden="true" />
      <input
        value={q}
        onChange={(e) => setQ(e.target.value)}
        aria-label="Ask your business"
        placeholder="Ask your business: Who owes me money?"
        className="min-w-0 flex-1 bg-transparent text-[14px] placeholder:text-muted focus:outline-none"
      />
      <kbd className="hidden rounded border border-line px-1 text-[11px] text-muted sm:block">⌘J</kbd>
    </form>
  )
}

function Bar({ part, whole, tone, label }: { part: number; whole: number; tone: string; label: string }) {
  const pct = whole > 0 ? Math.min(100, Math.round((part / whole) * 100)) : 0
  return (
    <div
      role="img"
      aria-label={`${label}: ${pct}%`}
      className="mt-3 h-2 overflow-hidden rounded-full bg-hover"
    >
      <div className={`h-full rounded-full ${tone}`} style={{ width: `${pct}%` }} />
    </div>
  )
}

function Skeleton() {
  const box = 'animate-pulse rounded-xl bg-hover'
  return (
    <div aria-busy="true" className="space-y-6">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className={`${box} h-28`} />
        ))}
      </div>
      <div className="grid gap-6 lg:grid-cols-3">
        <div className={`${box} h-64 lg:col-span-2`} />
        <div className={`${box} h-64`} />
      </div>
    </div>
  )
}
