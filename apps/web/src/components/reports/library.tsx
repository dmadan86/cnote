'use client'
import { Plus, Search } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { SubNav } from '@/components/billing/bits'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, inputCls, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { type DatasetInfo, fmtTs, type Report } from './lib'
import { TEMPLATES } from './templates'

const CATS = ['All', 'Money', 'Sales', 'Projects', 'Support'] as const
const CAT_OF: Record<string, string> = {
  invoices: 'Money',
  payments: 'Money',
  purchase_bills: 'Money',
  expenses: 'Money',
  deals: 'Sales',
  tasks: 'Projects',
  time_entries: 'Projects',
  tickets: 'Support',
}
const STANDARD = ['Receivables ageing', 'GST summary', 'Trial balance', 'Profit and loss']

export function ReportsNav() {
  const { ws } = useShell()
  return (
    <SubNav
      label="Reports sections"
      items={[
        [`/w/${ws}/reports`, 'Reports'],
        [`/w/${ws}/reports/dashboards`, 'Dashboards'],
      ]}
    />
  )
}

export function Library() {
  const { ws } = useShell()
  const base = `/w/${ws}/reports`
  const list = useCmd<{ reports: Report[] }>(ws, 'report.list')
  const sets = useCmd<{ datasets: DatasetInfo[] }>(ws, 'report.datasets')
  const [cat, setCat] = useState<(typeof CATS)[number]>('All')
  const [q, setQ] = useState('')
  const labelOf = (k: string) => sets.data?.datasets.find((d) => d.key === k)?.label ?? k
  const has = (k: string) => !sets.data || sets.data.datasets.some((d) => d.key === k)
  const needle = q.trim().toLowerCase()
  const rows = (list.data?.reports ?? []).filter(
    (r) => (cat === 'All' || CAT_OF[r.dataset] === cat) && (!needle || r.name.toLowerCase().includes(needle)),
  )
  const std = STANDARD.filter(
    (n) => (cat === 'All' || cat === 'Money') && (!needle || n.toLowerCase().includes(needle)),
  )
  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-[15px] font-semibold">Start from a question</h2>
        <Link href={`${base}/new`} className={btnPrimary}>
          <Plus size={14} aria-hidden /> New report
        </Link>
      </div>
      <ul aria-label="Templates" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {TEMPLATES.filter((t) => has(t.spec.dataset)).map((t) => (
          <li key={t.id}>
            <Link
              href={`${base}/new?template=${t.id}`}
              className="block h-full rounded-lg border border-line p-3 hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent"
            >
              <span className="block text-[14px] font-medium">{t.title}</span>
              <span className="mt-0.5 block text-[12px] text-muted">{t.blurb}</span>
            </Link>
          </li>
        ))}
      </ul>

      <div className="flex flex-wrap items-center gap-2">
        <fieldset className="flex flex-wrap gap-1">
          <legend className="sr-only">Category</legend>
          {CATS.map((c) => (
            <button
              key={c}
              type="button"
              aria-pressed={cat === c}
              onClick={() => setCat(c)}
              className="rounded-full border border-line px-3 py-1 text-[12px] hover:bg-hover aria-pressed:border-accent aria-pressed:bg-hover aria-pressed:font-medium"
            >
              {c}
            </button>
          ))}
        </fieldset>
        <label className="relative ml-auto">
          <span className="sr-only">Search reports</span>
          <Search size={14} aria-hidden className="absolute left-2.5 top-2.5 text-muted" />
          <input
            className={`${inputCls} w-56 pl-8`}
            placeholder="Search reports"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </label>
      </div>
      <ErrorText error={list.error} />
      {list.data && !list.data.reports.length && (
        <div className="rounded-lg border border-dashed border-line p-6 text-center text-[13px] text-muted">
          <p>No saved reports yet. Pick a question above, or</p>
          <Link href={`${base}/new`} className={`${btn} mt-2`}>
            Start from scratch
          </Link>
        </div>
      )}
      {(rows.length > 0 || std.length > 0) && (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-[13px]">
            <thead className="text-[12px] text-muted">
              <tr>
                <th className="py-1.5 font-medium">Name</th>
                <th className="font-medium">Report on</th>
                <th className="font-medium">Shared</th>
                <th className="font-medium">Updated</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id} className="border-t border-line hover:bg-hover">
                  <td className="py-1.5">
                    <Link href={`${base}/${r.id}`} className="font-medium hover:underline">
                      {r.name}
                    </Link>
                  </td>
                  <td>{labelOf(r.dataset)}</td>
                  <td>{r.visibility === 'workspace' ? 'Workspace' : 'Only me'}</td>
                  <td>{fmtTs(r.updated_at)}</td>
                </tr>
              ))}
              {std.map((n) => (
                <tr key={n} className="border-t border-line hover:bg-hover">
                  <td className="py-1.5">
                    <Link href={`/w/${ws}/billing/reports`} className="font-medium hover:underline">
                      {n}
                    </Link>{' '}
                    <Pill>Standard</Pill>
                  </td>
                  <td>Billing</td>
                  <td>Workspace</td>
                  <td />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {list.data && list.data.reports.length > 0 && !rows.length && !std.length && (
        <p className="text-[13px] text-muted">No reports match.</p>
      )}
    </div>
  )
}
