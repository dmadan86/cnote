'use client'
import dynamic from 'next/dynamic'
import { useEffect, useMemo, useRef, useState } from 'react'
import { btn } from '@/components/ui'
import { useScreenConfig } from '@/lib/screen-config'
import { useRuns } from './data'
import {
  type Ctx,
  type GridConfig,
  MAX_WIDGETS,
  type RunState,
  type Size,
  type WidgetCfg,
  type WidgetDef,
} from './types'

// Edit mode (drag, gallery) loads only when someone customises.
const WidgetEditor = dynamic(() => import('./edit').then((m) => m.WidgetEditor), { ssr: false })

const SIZES: Size[] = ['s', 'm', 'l']
const SPAN: Record<Size, string> = { s: '', m: 'sm:col-span-2', l: 'sm:col-span-2 lg:col-span-4' }
const HEIGHT = { kpi: 'h-28', chart: 'h-64', list: 'h-48' } as const

/**
 * Stored JSON is untrusted: keeps well-formed entries, clamps a size the widget does not allow, drops duplicate ids and
 * caps the list. Unknown or currently unavailable types are kept (never pruned for being unavailable; only the person's own
 * Remove deletes one). Schema v1 only; later versions add new default widgets here for configs older than that version.
 */
export const gridParse =
  (catalog: WidgetDef[]) =>
  (raw: unknown, defaults: GridConfig): GridConfig => {
    const list = (raw as { widgets?: unknown } | null)?.widgets
    if (!Array.isArray(list)) return defaults
    const seen = new Set<string>()
    const widgets: WidgetCfg[] = []
    for (const w of list) {
      if (widgets.length >= MAX_WIDGETS) break
      const { id, type, size } = (w ?? {}) as Record<string, unknown>
      if (
        typeof id !== 'string' ||
        typeof type !== 'string' ||
        id.length > 64 ||
        type.length > 64 ||
        seen.has(id)
      )
        continue
      const def = catalog.find((d) => d.type === type)
      const ok = SIZES.includes(size as Size) ? (size as Size) : (def?.sizes[0] ?? 'm')
      seen.add(id)
      widgets.push({ id, type, size: def && !def.sizes.includes(ok) ? def.sizes[0] : ok })
    }
    return { v: 1, widgets }
  }

export function defaultGrid(list: [string, Size][], catalog: WidgetDef[], ctx: Ctx): GridConfig {
  return {
    v: 1,
    widgets: list
      .filter(([t]) => catalog.find((d) => d.type === t)?.available(ctx))
      .map(([type, size]) => ({ id: type, type, size })),
  }
}

function Cell({
  def,
  size,
  ctx,
  runs,
  nonce,
  retry,
}: {
  def: WidgetDef
  size: Size
  ctx: Ctx
  runs: RunState[]
  nonce: number
  retry: () => void
}) {
  const box = HEIGHT[def.height ?? 'kpi']
  const frame = (body: React.ReactNode) => (
    <div
      className={`flex ${box} items-center justify-center rounded-xl border border-line bg-canvas p-3 text-center text-[13px] text-muted`}
    >
      {body}
    </div>
  )
  if (runs.some((r) => r.state === 'denied')) return frame(<span>{def.title}: not available to you.</span>)
  if (runs.some((r) => r.state === 'failed'))
    return frame(
      <span>
        {def.title} could not load.{' '}
        <button type="button" onClick={retry} className="underline">
          Try again
        </button>
      </span>,
    )
  if (runs.some((r) => r.state === 'loading'))
    return (
      <div
        role="status"
        aria-busy="true"
        aria-label={`${def.title}, loading`}
        className={`${box} animate-pulse rounded-xl bg-hover`}
      />
    )
  return <>{def.render({ size, ctx, runs, nonce })}</>
}

/** Configurable grid: an ordered list of widgets with sizes, one column on phones, per-widget loading and error states. */
export function WidgetGrid({
  screen,
  title,
  catalog,
  defaults,
  ctx,
  nonce,
  toolbar,
  label,
}: {
  screen: string
  title: string
  catalog: WidgetDef[]
  defaults: GridConfig
  ctx: Ctx
  nonce: number
  toolbar?: React.ReactNode
  label?: string
}) {
  const parse = useMemo(() => gridParse(catalog), [catalog])
  const cfg = useScreenConfig<GridConfig>(screen, defaults, parse, String(ctx.finance))
  const [editing, setEditing] = useState(false)
  const [retries, setRetries] = useState(0)
  const wasEditing = useRef(false)
  useEffect(() => {
    // leaving edit mode returns focus to the button that opened it
    if (wasEditing.current && !editing)
      document.querySelector<HTMLElement>('[data-focus="customise"]')?.focus()
    wasEditing.current = editing
  }, [editing])

  // the renderable projection: stored widgets we know and the person may use
  const shown = cfg.config.widgets.flatMap((w) => {
    const def = catalog.find((d) => d.type === w.type)
    return def?.available(ctx) ? [{ w, def }] : []
  })
  const results = useRuns(
    ctx.ws,
    shown.map(({ def }) => def.runs?.(ctx) ?? []),
    nonce + retries,
  )
  const cells = shown.map(({ w, def }, i) => ({ w, def, runs: results[i] }))

  if (editing)
    return (
      <WidgetEditor
        title={title}
        catalog={catalog}
        ctx={ctx}
        initial={cfg.config.widgets}
        onCancel={() => setEditing(false)}
        onDone={async (widgets) => {
          await cfg.save({ v: 1, widgets }, true)
          setEditing(false)
        }}
        onReset={async () => {
          await cfg.reset()
          setEditing(false)
        }}
      />
    )

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-3">{toolbar}</div>
        {!cfg.locked && (
          <button
            type="button"
            data-focus="customise"
            disabled={!cfg.ready}
            onClick={() => setEditing(true)}
            className={btn}
          >
            Customise
          </button>
        )}
      </div>
      {cfg.error && (
        <p role="alert" className="text-[13px] text-bad">
          Could not save your layout: {cfg.error.message}
        </p>
      )}
      {!cfg.ready ? (
        <div aria-busy="true" className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className="h-28 animate-pulse rounded-xl bg-hover" />
          ))}
        </div>
      ) : cells.length === 0 ? (
        <div className="rounded-xl border border-dashed border-line p-6 text-center">
          <p className="mb-3 text-muted">Your {title.toLowerCase()} is empty.</p>
          <button type="button" onClick={() => setEditing(true)} className={btn}>
            Add a widget
          </button>
        </div>
      ) : (
        <section aria-label={label ?? title} className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {cells.map(({ w, def, runs }) => (
            <div key={w.id} className={`min-w-0 ${SPAN[w.size]}`}>
              <Cell
                def={def}
                size={w.size}
                ctx={ctx}
                runs={runs}
                nonce={nonce + retries}
                retry={() => setRetries((n) => n + 1)}
              />
            </div>
          ))}
        </section>
      )}
    </div>
  )
}
