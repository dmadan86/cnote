'use client'
import { Pill } from '@/components/ui'
import { ago } from '@/lib/hooks'
import type { Job } from './types'

const tone = { queued: 'neutral', running: 'warn', done: 'ok', failed: 'bad' } as const

export function JobList({ jobs, error }: { jobs?: Job[]; error?: Error }) {
  if (error) return <p className="text-muted">Imports are available to workspace admins.</p>
  return (
    <section aria-label="Import jobs" className="space-y-3">
      <h2 className="text-[15px] font-semibold">Recent imports</h2>
      {jobs?.length === 0 && <p className="text-muted">No imports yet.</p>}
      <ul className="space-y-2">
        {jobs?.map((j) => {
          const r = j.report
          const warnings = Object.entries(r.warnings ?? {})
          return (
            <li key={j.id} className="rounded-md border border-line p-3">
              <div className="flex items-center gap-2">
                <span className="font-medium capitalize">{j.source}</span>
                <Pill tone={tone[j.status]}>{j.status}</Pill>
                {(j.status === 'running' || j.status === 'queued') && (
                  <span className="text-[12px] text-muted">
                    {j.cursor.phase ?? 'starting'}
                    {j.cursor.offset ? ` · ${j.cursor.offset} done` : ''}
                  </span>
                )}
                <span className="ml-auto text-[12px] text-muted">{ago(j.created_at)}</span>
              </div>
              <dl className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-[13px]">
                {(
                  [
                    ['Imported', r.imported],
                    ['Updated', r.updated],
                    ['Skipped', r.skipped],
                    ['Comments', r.comments],
                  ] as const
                ).map(([k, v]) => (
                  <div key={k} className="flex gap-1">
                    <dt className="text-muted">{k}</dt>
                    <dd className="font-medium">{v ?? 0}</dd>
                  </div>
                ))}
              </dl>
              {warnings.length > 0 && (
                <details className="mt-2 text-[13px]">
                  <summary className="cursor-pointer text-warn">{warnings.length} warning type(s)</summary>
                  <ul className="mt-1 list-disc pl-5 text-muted">
                    {warnings.map(([m, n]) => (
                      <li key={m}>
                        {m} {n > 1 && `(x${n})`}
                      </li>
                    ))}
                  </ul>
                </details>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}
