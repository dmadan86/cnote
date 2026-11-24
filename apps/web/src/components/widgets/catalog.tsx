'use client'
import { AlertTriangle, ArrowDown, ArrowUp, CircleCheck } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { dayIST, money } from '@/components/billing/fmt'
import { AskCard, GROUPS, KIND, plural, type Summary, summaryTiles } from '@/components/home/dashboard'
import { CheckInCard } from '@/components/hr/checkin'
import type { Spec } from '@/components/reports/lib'
import { useShell } from '@/components/shell'
import { EmptyState } from '@/components/ui'
import { cmd } from '@/lib/api'
import { moduleOn } from '@/lib/modules'
import type { SiteStats } from '@/lib/sites'
import { full, HBars, type Point, Sparkline, short, TrendChart } from './charts'
import { useFetched } from './data'
import type { Ctx, RunState, WidgetDef, WidgetProps } from './types'

// ---- shared pieces ----

export function Card({ title, note, children }: { title: string; note?: string; children: React.ReactNode }) {
  return (
    <section aria-label={title} className="h-full rounded-xl border border-line bg-canvas p-4 shadow-sm">
      <h3 className="text-[12px] font-medium text-muted">{title}</h3>
      {note && <p className="text-[12px] text-muted">{note}</p>}
      <div className="mt-2">{children}</div>
    </section>
  )
}

/** Same-size box for loading, denied and failed states of widgets that fetch for themselves. */
export function Status({
  state,
  title,
  retry,
}: {
  state: RunState['state']
  title: string
  retry?: () => void
}) {
  if (state === 'ok') return null
  return (
    <Card title={title}>
      {state === 'loading' ? (
        <div aria-busy="true" className="h-16 animate-pulse rounded-lg bg-hover" />
      ) : state === 'denied' ? (
        <p className="text-[13px] text-muted">Not available to you.</p>
      ) : (
        <p className="text-[13px] text-muted">
          Could not load.{' '}
          {retry && (
            <button type="button" onClick={retry} className="underline">
              Try again
            </button>
          )}
        </p>
      )}
    </Card>
  )
}

const monthLabel = new Intl.DateTimeFormat('en-IN', { month: 'short', year: '2-digit', timeZone: 'UTC' })
const dayMonth = new Intl.DateTimeFormat('en-IN', { day: 'numeric', month: 'short', timeZone: 'UTC' })
const cellLabel = (v: unknown) =>
  v && typeof v === 'object' && 'label' in v
    ? String((v as { label: unknown }).label)
    : v == null
      ? '(none)'
      : String(v)
const num = (v: unknown) => (typeof v === 'number' ? v : 0)

/** Rows of a one-group result as chart points; dates become month labels. */
const points = (res: RunState, date = false, col = 1): Point[] =>
  (res.result?.rows ?? []).map((r) => ({
    label: date ? monthLabel.format(new Date(`${String(r[0]).slice(0, 10)}T00:00:00Z`)) : cellLabel(r[0]),
    value: num(r[col]),
  }))

// ---- report specs (web-side constants, like report templates) ----

const SOLD = ['issued', 'irn_generated', 'sent', 'partly_paid', 'paid']
const soldFilters = [
  { field: 'kind', op: 'in', value: ['invoice'] },
  { field: 'status', op: 'in', value: SOLD },
]
const salesTrend: Spec = {
  dataset: 'invoices',
  filters: soldFilters,
  period: { field: 'invoice_date', preset: 'last_12_months' },
  group_by: [{ field: 'invoice_date', bucket: 'month' }],
  measures: [{ field: 'total', agg: 'sum' }],
  sort: [{ key: 'g0', dir: 'asc' }],
  viz: 'bar',
}
/** Same days of last month, so month-to-date is compared like with like. */
function lastMonthSameDays(): { spec: Spec; label: string } {
  const [y, m, d] = dayIST().split('-').map(Number)
  const start = new Date(Date.UTC(y, m - 2, 1))
  const last = Math.min(d, new Date(Date.UTC(y, m - 1, 0)).getUTCDate())
  const end = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth(), last))
  const iso = (x: Date) => x.toISOString().slice(0, 10)
  return {
    label: `${dayMonth.format(start)} to ${dayMonth.format(end)}`,
    spec: {
      ...salesTrend,
      period: { field: 'invoice_date', preset: 'custom', from: iso(start), to: iso(end) },
    },
  }
}
const dealSpec = (finance: boolean): Spec => ({
  dataset: 'deals',
  filters: [{ field: 'stage', op: 'not_in', value: ['won', 'lost'] }],
  group_by: [{ field: 'stage' }],
  measures: finance ? [{ agg: 'count' }, { field: 'amount', agg: 'sum' }] : [{ agg: 'count' }],
  viz: 'bar',
})
const expenseSpec: Spec = {
  dataset: 'expenses',
  period: { field: 'spent_on', preset: 'this_month' },
  group_by: [{ field: 'category' }],
  measures: [{ field: 'amount', agg: 'sum' }],
  sort: [{ key: 'm0', dir: 'desc' }],
  limit: 6,
  viz: 'bar',
}
const customerSpec: Spec = {
  dataset: 'invoices',
  filters: soldFilters,
  period: { field: 'invoice_date', preset: 'this_fy' },
  group_by: [{ field: 'contact' }],
  measures: [{ field: 'total', agg: 'sum' }],
  sort: [{ key: 'm0', dir: 'desc' }],
  limit: 5,
  viz: 'bar',
}

// ---- access hints (the server is the real check) ----

const member = (c: Ctx) => c.role !== 'guest'
const need = (c: Ctx, mod: string, finance = false) =>
  member(c) && moduleOn(c.modules, mod) && (!finance || c.finance)

// ---- widgets ----

function KpiCard({
  href,
  label,
  value,
  full: exact,
  detail,
  spark,
}: {
  href: string
  label: string
  value: string
  full: string
  detail: React.ReactNode
  spark?: number[]
}) {
  return (
    <Link
      href={href}
      aria-label={`${label}: ${exact}`}
      className="block h-full rounded-xl border border-line bg-canvas p-3 shadow-sm transition hover:border-accent hover:shadow-md focus-visible:outline-2 focus-visible:outline-accent"
    >
      <span className="block text-[12px] font-medium text-muted">{label}</span>
      <span className="mt-2 flex items-end justify-between gap-2">
        <span className="text-xl font-semibold tabular-nums">{value}</span>
        {spark && <Sparkline values={spark} />}
      </span>
      <span className="mt-0.5 block text-[12px] text-muted">{detail}</span>
    </Link>
  )
}

function SalesKpi({ ctx, runs }: WidgetProps) {
  const [trend, prev] = runs
  const pts = points(trend, true)
  const key = dayIST().slice(0, 7)
  const mtd = num((trend.result?.rows ?? []).find((r) => String(r[0]).startsWith(key))?.[1])
  const before = num(prev.result?.rows?.[0]?.[1])
  const pct = before > 0 ? Math.round(((mtd - before) / before) * 100) : null
  const { label } = lastMonthSameDays()
  return (
    <KpiCard
      href={`${ctx.base}/billing`}
      label="Sales this month, all branches"
      value={short(mtd, 'money')}
      full={full(mtd, 'money')}
      spark={pts.map((p) => p.value)}
      detail={
        pct === null ? (
          `No sales in ${label} last month`
        ) : (
          <span className="inline-flex items-center gap-1">
            {pct >= 0 ? <ArrowUp size={12} aria-hidden /> : <ArrowDown size={12} aria-hidden />}
            {pct >= 0 ? 'Up' : 'Down'} {Math.abs(pct)}% vs {label}
          </span>
        )
      }
    />
  )
}

function SalesChart({ runs }: WidgetProps) {
  const pts = points(runs[0], true)
  return (
    <Card title="Sales trend, last 12 months" note="Invoices issued, before credit notes. All branches">
      <TrendChart title="Sales, last 12 months" points={pts} />
    </Card>
  )
}

function DealsKpi({ ctx, runs }: WidgetProps) {
  const rows = runs[0].result?.rows ?? []
  const count = rows.reduce((n, r) => n + num(r[1]), 0)
  const value = rows.reduce((n, r) => n + num(r[2]), 0)
  return (
    <KpiCard
      href={`${ctx.base}/crm/deals`}
      label="Open deals"
      value={String(count)}
      full={`${count}${ctx.finance ? `, worth ${full(value, 'money')}` : ''}`}
      detail={ctx.finance ? `Worth ${short(value, 'money')}` : 'Not won or lost yet'}
    />
  )
}

const STAGE_ORDER = ['lead', 'qualified', 'proposal', 'negotiation']
function PipelineChart({ ctx, runs }: WidgetProps) {
  const { dealLabels } = useShell()
  const rows = [...(runs[0].result?.rows ?? [])].sort(
    (a, b) => STAGE_ORDER.indexOf(String(a[0])) - STAGE_ORDER.indexOf(String(b[0])),
  )
  const label = (s: string) => dealLabels?.[s] ?? s.charAt(0).toUpperCase() + s.slice(1)
  const pts = rows.map((r) => ({ label: label(String(r[0])), value: num(r[ctx.finance ? 2 : 1]) }))
  return (
    <Card title="Pipeline by stage" note={ctx.finance ? 'Open deals, by value' : 'Open deals, by number'}>
      <HBars title="Pipeline by stage" rows={pts} format={ctx.finance ? 'money' : 'number'} />
    </Card>
  )
}

function ExpensesChart({ runs }: WidgetProps) {
  return (
    <Card title="Expenses by category" note="This month">
      <HBars title="Expenses by category" rows={points(runs[0])} />
    </Card>
  )
}

function CustomersChart({ runs }: WidgetProps) {
  return (
    <Card title="Top customers" note="Invoiced this financial year. All branches">
      <HBars title="Top customers" rows={points(runs[0])} />
    </Card>
  )
}

type Ageing = { total_minor: number; buckets: { bucket: string; outstanding_minor: number; count: number }[] }
function AgeingChart({ ctx, nonce }: WidgetProps) {
  const r = useFetched<Ageing>(
    ctx.ws,
    'ageing',
    () => cmd<Ageing>(ctx.ws, 'report.receivables_ageing'),
    nonce,
  )
  if (r.state !== 'ok' || !r.data) return <Status state={r.state} title="Receivables ageing" />
  return (
    <Card title="Receivables ageing" note={`${money(r.data.total_minor)} outstanding. All branches`}>
      <HBars
        title="Receivables ageing"
        rows={r.data.buckets.map((b) => ({ label: b.bucket, value: b.outstanding_minor }))}
      />
    </Card>
  )
}

type SiteOut = SiteStats | null
function SiteKpi({ ctx, nonce }: WidgetProps) {
  const r = useFetched<SiteOut>(
    ctx.ws,
    'site',
    async () => {
      const sites = await cmd<{ id: string }[]>(ctx.ws, 'site.list')
      return sites[0] ? cmd<SiteStats>(ctx.ws, 'site.stats', { id: sites[0].id }) : null
    },
    nonce,
  )
  if (r.state !== 'ok') return <Status state={r.state} title="Website visits" />
  if (!r.data)
    return (
      <KpiCard
        href={`${ctx.base}/site`}
        label="Website visits"
        value="None yet"
        full="No website yet"
        detail="Create your website"
      />
    )
  const days = r.data.days.slice(-30)
  const visits = days.reduce((n, d) => n + d.visits, 0)
  return (
    <KpiCard
      href={`${ctx.base}/site`}
      label="Website visits"
      value={short(visits, 'number')}
      full={`${visits} visits in the last 30 days`}
      spark={days.map((d) => d.visits)}
      detail={`Last 30 days, ${plural(r.data.enquiries, 'enquiry', 'enquiries')}`}
    />
  )
}

const attentionHref = (a: Summary['attention'][number], base: string) =>
  ({
    task: `${base}/projects/${a.key}`,
    invoice: `${base}/billing/${a.id}`,
    bill: `${base}/billing/bills/${a.id}`,
    quote: `${base}/billing/quotes/${a.id}`,
    deal: `${base}/crm/deals/${a.id}`,
    confirmation: `${base}/settings?tab=Confirmations`,
    approval: `${base}/approvals`,
  })[a.kind]

function AttentionList({
  ctx,
  title,
  id,
  kinds,
  size,
}: {
  ctx: Ctx
  title: string
  id: string
  kinds?: Summary['attention'][number]['kind'][]
  size: WidgetProps['size']
}) {
  const [all, setAll] = useState(false)
  const items = ctx.summary.attention.filter((a) => !kinds || kinds.includes(a.kind))
  const limit = size === 'l' ? 8 : 5
  const rows = all ? items : items.slice(0, limit)
  return (
    <section aria-labelledby={id} className="h-full">
      <div className="mb-2 flex items-baseline justify-between">
        <h2 id={id} className="text-[15px] font-semibold">
          {title}
        </h2>
        {items.length > 0 && <span className="text-[12px] text-muted">{plural(items.length, 'item')}</span>}
      </div>
      <div className="rounded-xl border border-line bg-canvas shadow-sm">
        {items.length === 0 ? (
          <EmptyState compact icon={CircleCheck} title="You are caught up">
            Nothing needs action today.
          </EmptyState>
        ) : (
          GROUPS.map((g) => {
            const part = rows.filter((a) => a.severity === g.sev)
            if (!part.length) return null
            return (
              <div key={g.sev} className="border-b border-line last:border-b-0">
                <h3 className="px-4 pt-3 text-[12px] font-semibold uppercase tracking-wide text-muted">
                  {g.title} ({part.length})
                </h3>
                <ul className="divide-y divide-line">
                  {part.map((a) => {
                    const K = KIND[a.kind]
                    return (
                      <li key={`${a.kind}-${a.id}`}>
                        <Link
                          href={attentionHref(a, ctx.base)}
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
      {items.length > limit && (
        <button
          type="button"
          onClick={() => setAll(!all)}
          aria-expanded={all}
          className="mt-2 text-[13px] text-muted underline"
        >
          {all ? 'Show fewer' : `Show all ${items.length}`}
        </button>
      )}
    </section>
  )
}

const LINKS: [string, string, string][] = [
  ['Invoices', 'billing', '/billing'],
  ['Contacts and deals', 'crm', '/crm'],
  ['Projects', 'projects', '/projects'],
  ['Reports', 'reports', '/reports'],
  ['Website', 'site', '/site'],
]
function QuickLinks({ ctx }: WidgetProps) {
  return (
    <Card title="Quick links">
      <ul className="grid grid-cols-2 gap-2">
        {LINKS.filter(([, m]) => moduleOn(ctx.modules, m)).map(([label, , path]) => (
          <li key={path}>
            <Link
              href={`${ctx.base}${path}`}
              className="block rounded-lg border border-line px-3 py-2 text-[13px] hover:bg-hover"
            >
              {label}
            </Link>
          </li>
        ))}
      </ul>
    </Card>
  )
}

// one widget per existing Home tile, so nothing people see today disappears
const TILES: [string, string, string, WidgetDef['group']][] = [
  ['tile.payments', 'pay', 'Payments pending', 'Money'],
  ['tile.bills', 'bills', 'Bills due', 'Money'],
  ['tile.approvals', 'appr', 'Approvals waiting', 'Work'],
  ['tile.tasks', 'tasks', 'Tasks due', 'Work'],
  ['tile.inquiries', 'inq', 'New inquiries', 'Sales'],
  ['tile.follow_ups', 'fu', 'Follow-ups', 'Sales'],
  ['tile.tickets', 'tkt', 'Tickets breaching SLA', 'Sales'],
  ['tile.low_stock', 'stock', 'Low stock', 'Money'],
  ['tile.expiring', 'expiring', 'Expiring in 30 days', 'Money'],
  ['tile.people', 'people', 'People present', 'People'],
  ['tile.urgent', 'urg', 'Urgent', 'Work'],
]
const tileWidgets: WidgetDef[] = TILES.map(([type, key, title, group]) => ({
  type,
  title,
  group,
  sizes: ['s', 'm'],
  command: 'home.summary',
  height: 'kpi',
  available: (c) => summaryTiles(c.summary.tiles, c.base).some((t) => t.key === key),
  render: ({ ctx }) => summaryTiles(ctx.summary.tiles, ctx.base).find((t) => t.key === key) ?? null,
}))

const ATTN = (
  type: string,
  title: string,
  id: string,
  kinds?: Summary['attention'][number]['kind'][],
): WidgetDef => ({
  type,
  title,
  group: 'Work',
  sizes: ['m', 'l'],
  command: 'home.summary',
  height: 'list',
  available: () => true,
  render: ({ ctx, size }) => <AttentionList ctx={ctx} title={title} id={id} kinds={kinds} size={size} />,
})

export const homeWidgets: WidgetDef[] = [
  ...tileWidgets,
  {
    type: 'kpi.sales',
    title: 'Sales this month',
    group: 'Money',
    sizes: ['s', 'm'],
    command: 'report.run_many (invoices)',
    height: 'kpi',
    available: (c) => need(c, 'billing', true),
    runs: () => [salesTrend, lastMonthSameDays().spec],
    render: (p) => <SalesKpi {...p} />,
  },
  {
    type: 'chart.sales',
    title: 'Sales trend',
    group: 'Money',
    sizes: ['m', 'l'],
    command: 'report.run_many (invoices)',
    height: 'chart',
    available: (c) => need(c, 'billing', true),
    runs: () => [salesTrend],
    render: (p) => <SalesChart {...p} />,
  },
  {
    type: 'chart.ageing',
    title: 'Receivables ageing',
    group: 'Money',
    sizes: ['m', 'l'],
    command: 'report.receivables_ageing',
    height: 'chart',
    available: (c) => need(c, 'billing', true),
    render: (p) => <AgeingChart {...p} />,
  },
  {
    type: 'chart.customers',
    title: 'Top customers',
    group: 'Money',
    sizes: ['m', 'l'],
    command: 'report.run_many (invoices)',
    height: 'chart',
    available: (c) => need(c, 'billing', true),
    runs: () => [customerSpec],
    render: (p) => <CustomersChart {...p} />,
  },
  {
    type: 'chart.expenses',
    title: 'Expenses by category',
    group: 'Money',
    sizes: ['m', 'l'],
    command: 'report.run_many (expenses)',
    height: 'chart',
    available: (c) => need(c, 'billing', true),
    runs: () => [expenseSpec],
    render: (p) => <ExpensesChart {...p} />,
  },
  {
    type: 'kpi.deals',
    title: 'Open deals',
    group: 'Sales',
    sizes: ['s'],
    command: 'report.run_many (deals)',
    height: 'kpi',
    available: (c) => need(c, 'crm'),
    runs: (c) => [dealSpec(c.finance)],
    render: (p) => <DealsKpi {...p} />,
  },
  {
    type: 'chart.pipeline',
    title: 'Pipeline by stage',
    group: 'Sales',
    sizes: ['m', 'l'],
    command: 'report.run_many (deals)',
    height: 'chart',
    available: (c) => need(c, 'crm'),
    runs: (c) => [dealSpec(c.finance)],
    render: (p) => <PipelineChart {...p} />,
  },
  {
    type: 'kpi.site',
    title: 'Website visits',
    group: 'Website',
    sizes: ['s', 'm'],
    command: 'site.list, site.stats',
    height: 'kpi',
    available: (c) => need(c, 'site'),
    render: (p) => <SiteKpi {...p} />,
  },
  ATTN('list.attention', 'Needs your attention', 'attention'),
  ATTN('list.tasks', 'My tasks', 'attn-tasks', ['task']),
  ATTN('list.approvals', 'Approvals', 'attn-approvals', ['approval']),
  ATTN('list.confirmations', 'AI employee confirmations', 'attn-confirmations', ['confirmation']),
  {
    type: 'check_in',
    title: 'Check in',
    group: 'People',
    sizes: ['m', 'l'],
    command: 'attendance.today',
    available: member,
    render: () => <CheckInCard />,
  },
  {
    type: 'ask',
    title: 'Ask your business',
    group: 'Shortcuts',
    sizes: ['m', 'l'],
    command: '(opens the Ask panel)',
    available: () => true,
    render: () => <AskCard />,
  },
  {
    type: 'links.quick',
    title: 'Quick links',
    group: 'Shortcuts',
    sizes: ['m'],
    command: '(none)',
    available: () => true,
    render: (p) => <QuickLinks {...p} />,
  },
]

/** Starting layout by role; widgets whose module is off or that the person cannot use are dropped at render time. */
export function homeDefaults(c: Pick<Ctx, 'role' | 'finance'>): [string, WidgetDef['sizes'][number]][] {
  const head: [string, 's' | 'm' | 'l'][] = [
    ['ask', 'l'],
    ['check_in', 'l'],
  ]
  if (c.role === 'guest') return [...head, ['tile.tasks', 's'], ['list.attention', 'l']]
  if (c.role === 'owner' || c.role === 'admin')
    return [
      ...head,
      ['tile.payments', 's'],
      ['kpi.sales', 's'],
      ['tile.tasks', 's'],
      ['tile.inquiries', 's'],
      ['tile.approvals', 's'],
      ['tile.urgent', 's'],
      ['chart.sales', 'm'],
      ['chart.ageing', 'm'],
      ['list.attention', 'l'],
      ['chart.pipeline', 'm'],
    ]
  if (c.finance)
    return [
      ...head,
      ['tile.payments', 's'],
      ['kpi.sales', 's'],
      ['tile.tasks', 's'],
      ['tile.inquiries', 's'],
      ['list.attention', 'l'],
      ['list.tasks', 'm'],
      ['chart.pipeline', 'm'],
    ]
  return [
    ...head,
    ['tile.tasks', 's'],
    ['tile.inquiries', 's'],
    ['kpi.deals', 's'],
    ['tile.follow_ups', 's'],
    ['list.attention', 'l'],
    ['list.tasks', 'm'],
  ]
}
