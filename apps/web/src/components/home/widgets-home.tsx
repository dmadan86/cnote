'use client'
import { RefreshCw } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { BranchSelect } from '@/components/company/branch-filter'
import { useShell } from '@/components/shell'
import { homeDefaults, homeWidgets } from '@/components/widgets/catalog'
import { defaultGrid, WidgetGrid } from '@/components/widgets/grid'
import type { Ctx } from '@/components/widgets/types'
import { useCmd } from '@/lib/hooks'
import type { Summary } from './dashboard'

const minutesAgo = (at: number) => {
  const m = Math.floor((Date.now() - at) / 60000)
  return m < 1 ? 'Updated just now' : `Updated ${m} min ago`
}

/** The configurable Home (HOME_WIDGETS=1): a WidgetGrid over home.summary and report.run_many. */
export function HomeWidgets() {
  const { ws, me, modules } = useShell()
  const [branch, setBranch] = useState('')
  const { data, error, reload } = useCmd<Summary>(ws, 'home.summary', branch ? { branch_id: branch } : {})
  const [nonce, setNonce] = useState(0)
  const [at, setAt] = useState(() => Date.now())
  const [, tick] = useState(0)
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new summary means fresh data
  useEffect(() => setAt(Date.now()), [data])
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 30_000)
    return () => clearInterval(t)
  }, [])
  const role = me.workspaces.find((w) => w.id === ws)?.role ?? 'member'
  // the summary only carries money tiles when billing is on and the person has finance access
  const finance = !!data?.tiles.payments_pending
  const ctx: Ctx | undefined = useMemo(
    () => data && { ws, base: `/w/${ws}`, role, finance, modules, summary: data, branch },
    [data, ws, role, finance, modules, branch],
  )
  // the starting layout depends on who is looking, not on the numbers
  const defaults = ctx
    ? defaultGrid(homeDefaults({ role, finance }), homeWidgets, ctx)
    : { v: 1 as const, widgets: [] }
  if (error)
    return (
      <p role="alert" className="text-muted">
        Could not load your summary.
      </p>
    )
  if (!ctx) return <div aria-busy="true" className="h-64 animate-pulse rounded-xl bg-hover" />
  return (
    <div className="space-y-3">
      <p className="-mt-6 text-[13px] text-muted">
        {new Intl.DateTimeFormat('en-IN', {
          weekday: 'long',
          day: 'numeric',
          month: 'long',
          timeZone: 'Asia/Kolkata',
        }).format(new Date())}
      </p>
      <WidgetGrid
        screen="home"
        title="Home"
        label="Today at a glance"
        catalog={homeWidgets}
        defaults={defaults}
        ctx={ctx}
        nonce={nonce}
        toolbar={
          <>
            <BranchSelect ws={ws} value={branch} onChange={setBranch} allLabel="All branches (invoices)" />
            <span className="text-[13px] text-muted" aria-live="off">
              {minutesAgo(at)}
            </span>
            <button
              type="button"
              onClick={() => {
                setNonce((n) => n + 1)
                reload()
              }}
              className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[13px] text-muted hover:bg-hover"
            >
              <RefreshCw size={13} aria-hidden /> Refresh
            </button>
          </>
        }
      />
    </div>
  )
}
