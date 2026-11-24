'use client'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { ErrorText } from '@/components/ui'
import { type SiteStats, siteApi } from '@/lib/sites'

const sum = (a: { visits: number }[]) => a.reduce((n, d) => n + d.visits, 0)

function Bars({ rows }: { rows: { label: string; n: number }[] }) {
  const max = Math.max(1, ...rows.map((r) => r.n))
  if (!rows.length) return <p className="text-muted">Nothing yet.</p>
  return (
    <ul className="space-y-1">
      {rows.map((r) => (
        <li key={r.label} className="flex items-center gap-2">
          <span className="w-28 shrink-0 truncate" title={r.label}>
            {r.label}
          </span>
          <span className="h-2 flex-1 rounded-full bg-hover">
            <span className="block h-2 rounded-full bg-accent" style={{ width: `${(r.n / max) * 100}%` }} />
          </span>
          <span className="w-10 text-right tabular-nums">{r.n}</span>
        </li>
      ))}
    </ul>
  )
}

/** Visits, a 30-day chart, top pages, where visitors came from and enquiries. "Visits", never "visitors" (design 16.1). */
export function StatsCard({ ws, siteId }: { ws: string; siteId: string }) {
  const [s, setS] = useState<SiteStats>()
  const [err, setErr] = useState<Error>()
  useEffect(() => {
    siteApi.stats(ws, siteId).then(setS, setErr)
  }, [ws, siteId])
  if (err) return <ErrorText error={err} />
  if (!s) return <p className="text-muted">Loading…</p>
  const days = s.days.slice(-30)
  const total = sum(days)
  const max = Math.max(1, ...days.map((d) => d.visits))
  const bar = 100 / Math.max(1, days.length)
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
        <p>
          <span className="text-2xl font-semibold tabular-nums">{total}</span> visits
        </p>
        <p>
          <span className="text-2xl font-semibold tabular-nums">{s.enquiries}</span>{' '}
          {s.enquiries === 1 ? 'enquiry' : 'enquiries'}{' '}
          <Link href={`/w/${ws}/crm/inbox`} className="text-[12px] underline">
            Leads
          </Link>
        </p>
      </div>
      {total === 0 ? (
        <p className="text-muted">No visits yet. They show up here within a few minutes of the first one.</p>
      ) : (
        <svg
          role="img"
          aria-label={`Visits per day for the last ${days.length} days, ${total} in total`}
          viewBox="0 0 100 30"
          preserveAspectRatio="none"
          className="h-20 w-full text-accent"
        >
          {days.map((d, i) => {
            const h = (d.visits / max) * 28
            return (
              <rect
                key={d.day}
                x={i * bar + bar * 0.1}
                y={30 - h}
                width={bar * 0.8}
                height={h}
                fill="currentColor"
              >
                <title>{`${d.day}: ${d.visits}`}</title>
              </rect>
            )
          })}
        </svg>
      )}
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1">
          <h4 className="text-[12px] font-medium text-muted">Most viewed pages</h4>
          <Bars
            rows={s.pages.slice(0, 5).map((p) => ({ label: p.slug ? `/${p.slug}` : 'Home', n: p.views }))}
          />
        </div>
        <div className="space-y-1">
          <h4 className="text-[12px] font-medium text-muted">Where visits came from</h4>
          <Bars rows={s.referrers.slice(0, 5).map((r) => ({ label: r.domain, n: r.views }))} />
        </div>
      </div>
      <p className="text-[12px] text-muted">
        We count visits without cookies and don't track individual people. Reloads and direct visits (typed
        address, QR code or an app that hides where it came from) count too, so a visit is not one person.
      </p>
    </div>
  )
}
