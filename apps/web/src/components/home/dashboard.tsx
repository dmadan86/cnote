'use client'
import { AlertTriangle } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { money } from '@/components/billing/fmt'
import { BranchSelect } from '@/components/company/branch-filter'
import { CheckInCard } from '@/components/hr/checkin'
import { useShell } from '@/components/shell'
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

function TileLink({
  href,
  label,
  value,
  detail,
  alert,
}: {
  href: string
  label: string
  value: string
  detail: string
  /** Overdue text: shown in red with an icon, and always spelled out in words (never colour alone). */
  alert?: string
}) {
  return (
    <Link
      href={href}
      aria-label={`${label}: ${value}. ${detail}${alert ? `. ${alert}` : ''}`}
      className="block rounded-lg border border-line p-3 hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent"
    >
      <span className="block text-[12px] font-medium text-muted">{label}</span>
      <span className="mt-1 block text-xl font-semibold tabular-nums">{value}</span>
      <span className="mt-0.5 block text-[12px] text-muted">{detail}</span>
      {alert && (
        <span className="mt-1 flex items-center gap-1 text-[12px] font-medium text-bad">
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
  if (!data) return <div aria-busy="true" className="h-32 animate-pulse rounded-lg bg-hover" />
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
        href={`${base}/billing?filter=outstanding`}
        label="Payments pending"
        value={money(t.payments_pending.total_minor)}
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
        href={`${base}/billing/bills?due=soon`}
        label="Bills due"
        value={money(t.bills_due.total_minor)}
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
        href={`${base}/crm/inbox`}
        label="New inquiries"
        value={String(t.new_inquiries.count)}
        detail="New leads in the last 7 days"
      />
    ),
    t.follow_ups && (
      <TileLink
        key="fu"
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
        href={`${base}/crm/tickets?filter=breached`}
        label="Tickets breaching SLA"
        value={String(t.tickets_breaching.count)}
        detail={t.tickets_breaching.count ? 'Past their due time' : 'All within SLA'}
        alert={t.tickets_breaching.count ? 'Needs attention' : undefined}
      />
    ),
    t.people_present && (
      <TileLink
        key="people"
        href={`${base}/people/attendance`}
        label="People present"
        value={`${t.people_present.present} of ${t.people_present.expected}`}
        detail={`${t.people_present.on_leave} on leave, ${t.people_present.not_marked} not marked yet`}
      />
    ),
    t.urgent && (
      <TileLink
        key="urg"
        href={t.urgent.overdue_invoices ? `${base}/billing?filter=overdue` : `${base}/projects?due=me`}
        label="Urgent"
        value={String(t.urgent.count)}
        detail={`${plural(t.urgent.overdue_tasks, 'overdue task')}, ${plural(t.urgent.overdue_invoices, 'overdue invoice')}`}
      />
    ),
  ].filter(Boolean)

  return (
    <div className="space-y-6">
      <CheckInCard />
      <BranchSelect ws={ws} value={branch} onChange={setBranch} allLabel="All branches (invoices)" />
      {tiles.length > 0 && (
        <section aria-label="Today at a glance">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">{tiles}</div>
        </section>
      )}
      <section aria-labelledby="attention">
        <h2 id="attention" className="mb-2 text-[13px] font-semibold text-muted">
          Needs your attention
        </h2>
        {data.attention.length === 0 ? (
          <p className="text-muted">You are caught up. Nothing needs action today.</p>
        ) : (
          <ul className="divide-y divide-line rounded-lg border border-line">
            {rows.map((a) => (
              <li key={`${a.kind}-${a.id}`}>
                <Link href={href(a)} className="flex items-start gap-3 p-3 hover:bg-hover">
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
            ))}
          </ul>
        )}
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
    </div>
  )
}
