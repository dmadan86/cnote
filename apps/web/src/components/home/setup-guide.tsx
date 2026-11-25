'use client'
import { Check, Circle, X } from 'lucide-react'
import Link from 'next/link'
import { useMemo, useState } from 'react'
import { gridParse } from '@/components/widgets/grid'
import type { Ctx, GridConfig } from '@/components/widgets/types'
import { useCmd } from '@/lib/hooks'
import { useScreenConfig } from '@/lib/screen-config'

type Row = { key: string; done: boolean; href: string }
const LABEL: Record<string, string> = {
  company: 'Add your business name and state',
  customer: 'Add your first customer',
  invoice: 'Send your first invoice',
  website: 'Publish your website',
  table: 'Create your first table',
  import: 'Import your data',
  team: 'Invite your team',
}
const SHOWN = 5
const Box = ({ children }: { children: React.ReactNode }) => (
  <section aria-label="Get set up" className="h-full rounded-xl border border-line bg-canvas p-4 shadow-sm">
    <h3 className="text-[14px] font-semibold">Get set up</h3>
    <div className="mt-2">{children}</div>
  </section>
)

/** Home widget `setup`: the "Get set up" guide. Ticks come from setup.status; nothing is ticked by hand. */
export function SetupGuide({ ctx }: { ctx: Ctx }) {
  const { data, error, reload } = useCmd<Row[]>(ctx.ws, 'setup.status')
  const [all, setAll] = useState(false)
  const parse = useMemo(() => gridParse([]), [])
  // Same layout store and save path as Customise > Done; this entry is already loaded by the grid around us.
  const layout = useScreenConfig<GridConfig>('home', { v: 1, widgets: [] }, parse, String(ctx.finance))
  const hide = () =>
    layout.save({ v: 1, widgets: layout.config.widgets.filter((w) => w.type !== 'setup') }, true)

  if (error)
    return (
      <Box>
        <p className="text-[13px] text-muted">
          Couldn't load.{' '}
          <button type="button" onClick={reload} className="underline">
            Try again
          </button>
        </p>
      </Box>
    )
  if (!data)
    return (
      <Box>
        <div aria-busy="true" className="h-16 animate-pulse rounded-lg bg-hover" />
      </Box>
    )

  const done = data.filter((r) => r.done).length
  const ordered = [...data.filter((r) => !r.done), ...data.filter((r) => r.done)]
  const rows = all ? ordered : ordered.slice(0, SHOWN)
  const complete = data.length > 0 && done === data.length
  return (
    <section aria-label="Get set up" className="h-full rounded-xl border border-line bg-canvas p-4 shadow-sm">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h3 className="text-[14px] font-semibold">{complete ? 'All set' : 'Get set up'}</h3>
          <p className="text-[12px] text-muted">
            {done} of {data.length} done
          </p>
        </div>
        {!complete && (
          <button
            type="button"
            onClick={hide}
            aria-label="Hide setup guide"
            className="-m-2 flex size-11 items-center justify-center rounded-md text-muted hover:bg-hover"
          >
            <X size={16} aria-hidden />
          </button>
        )}
      </div>
      <div aria-hidden="true" className="mt-2 h-1.5 overflow-hidden rounded-full bg-line">
        <div
          className="h-full bg-accent"
          style={{ width: `${data.length ? (done / data.length) * 100 : 0}%` }}
        />
      </div>
      {complete ? (
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <p className="text-[13px] text-muted">Everything on the list is done.</p>
          <button
            type="button"
            onClick={hide}
            className="rounded-md border border-line px-3 py-1.5 text-[13px] hover:bg-hover"
          >
            Remove from Home
          </button>
        </div>
      ) : (
        <ul className="mt-2">
          {rows.map((r) => (
            <li key={r.key}>
              <Link
                href={r.href}
                className="flex min-h-11 items-center gap-2.5 rounded-md px-1 text-[13px] hover:bg-hover"
              >
                {r.done ? (
                  <Check size={16} aria-hidden className="shrink-0 text-ok" />
                ) : (
                  <Circle size={16} aria-hidden className="shrink-0 text-muted" />
                )}
                <span className={r.done ? 'text-muted' : ''}>
                  <span className="sr-only">{r.done ? 'Done: ' : 'To do: '}</span>
                  {LABEL[r.key] ?? r.key}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px]">
        {!complete && data.length > SHOWN && (
          <button
            type="button"
            onClick={() => setAll(!all)}
            aria-expanded={all}
            className="text-muted underline"
          >
            {all ? 'Show fewer' : `Show all (${data.length})`}
          </button>
        )}
        <Link href={`${ctx.base}/welcome`} className="text-muted underline">
          Finish setup
        </Link>
      </div>
    </section>
  )
}
