'use client'
import { useId, useState } from 'react'

/** Colour-blind-safe categorical palette (docs/design/dashboard-research.md); the CSS variables switch for dark mode. */
export const PALETTE = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)']

const inrCompact = new Intl.NumberFormat('en-IN', {
  style: 'currency',
  currency: 'INR',
  notation: 'compact',
  maximumFractionDigits: 1,
})
const inrFull = new Intl.NumberFormat('en-IN', { style: 'currency', currency: 'INR' })
const numCompact = new Intl.NumberFormat('en-IN', { notation: 'compact', maximumFractionDigits: 1 })
const numFull = new Intl.NumberFormat('en-IN')

export type Fmt = 'money' | 'number'
/** Short form for tiles and axes: Rs 1.2L, Rs 3.4Cr (money values are paise). */
export const short = (v: number, f: Fmt) =>
  f === 'money' ? inrCompact.format(v / 100) : numCompact.format(v)
/** Exact form for tables and text alternatives. */
export const full = (v: number, f: Fmt) => (f === 'money' ? inrFull.format(v / 100) : numFull.format(v))

/** Pure path maths for the line and column charts. y grows downward; the baseline is zero, or the data if negative. */
export function scale(values: number[], w: number, h: number) {
  const lo = Math.min(0, ...values)
  const hi = Math.max(0, ...values)
  const span = hi - lo || 1
  const y = (v: number) => h - ((v - lo) / span) * h
  const x = (i: number) => (values.length < 2 ? w / 2 : (i / (values.length - 1)) * w)
  return { x, y, zero: y(0) }
}

/** Line path for a series; an empty series gives '' and one point a short flat stroke. */
export function linePath(values: number[], w: number, h: number) {
  if (!values.length) return ''
  const { x, y } = scale(values, w, h)
  if (values.length === 1)
    return `M${x(0) - 4},${y(values[0]).toFixed(1)}L${x(0) + 4},${y(values[0]).toFixed(1)}`
  return values.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join('')
}

/** Tiny trend line next to a number. Decorative: the tile's text carries the value. */
export function Sparkline({ values, className = 'h-8 w-24' }: { values: number[]; className?: string }) {
  if (values.length < 2) return null
  return (
    <svg aria-hidden viewBox="0 0 100 32" preserveAspectRatio="none" className={className}>
      <path
        d={linePath(values, 100, 30)}
        transform="translate(0 1)"
        fill="none"
        stroke={PALETTE[0]}
        strokeWidth="2"
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
      />
    </svg>
  )
}

export type Point = { label: string; value: number }

/** Sentence that states the result, used as the chart's text alternative. */
export function summarise(title: string, pts: Point[], f: Fmt) {
  if (!pts.length) return `${title}. No data.`
  const top = pts.reduce((a, b) => (b.value > a.value ? b : a))
  const last = pts[pts.length - 1]
  return `${title}. Highest ${full(top.value, f)} in ${top.label}, latest ${full(last.value, f)} in ${last.label}.`
}

/** Line or column chart over time with a light axis, a text alternative and a "View as table" toggle. */
export function TrendChart({
  title,
  points,
  format = 'money',
  kind = 'line',
}: {
  title: string
  points: Point[]
  format?: Fmt
  kind?: 'line' | 'columns'
}) {
  const [table, setTable] = useState(false)
  const id = useId()
  const W = 300
  const H = 100
  const vals = points.map((p) => p.value)
  const { x, y, zero } = scale(vals, W, H)
  const max = Math.max(0, ...vals)
  const step = points.length > 1 ? W / (points.length - 1) : W
  const bw = Math.max(2, Math.min(24, step * 0.6))
  return (
    <div>
      {table ? (
        <table id={id} className="w-full text-left text-[13px]">
          <caption className="sr-only">{title}</caption>
          <thead className="text-[12px] text-muted">
            <tr>
              <th scope="col" className="py-1 font-medium">
                Period
              </th>
              <th scope="col" className="py-1 text-right font-medium">
                Amount
              </th>
            </tr>
          </thead>
          <tbody>
            {points.map((p) => (
              <tr key={p.label} className="border-t border-line">
                <td className="py-1">{p.label}</td>
                <td className="py-1 text-right tabular-nums">{full(p.value, format)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : points.length === 0 ? (
        <p className="py-6 text-center text-[13px] text-muted">No data for this period yet.</p>
      ) : (
        <div>
          <div className="flex items-start gap-2">
            <span aria-hidden className="w-12 shrink-0 text-right text-[11px] tabular-nums text-muted">
              {short(max, format)}
            </span>
            <svg
              role="img"
              aria-label={summarise(title, points, format)}
              viewBox={`-6 -6 ${W + 12} ${H + 12}`}
              preserveAspectRatio="none"
              className="h-28 min-w-0 flex-1"
            >
              <line
                x1="0"
                x2={W}
                y1={zero}
                y2={zero}
                stroke="var(--line)"
                strokeWidth="1"
                vectorEffect="non-scaling-stroke"
              />
              {kind === 'columns' ? (
                points.map((p, i) => (
                  <rect
                    key={p.label}
                    x={x(i) - bw / 2}
                    width={bw}
                    y={Math.min(y(p.value), zero)}
                    height={Math.abs(zero - y(p.value))}
                    fill={PALETTE[0]}
                  />
                ))
              ) : (
                <path
                  d={linePath(vals, W, H)}
                  fill="none"
                  stroke={PALETTE[0]}
                  strokeWidth="2"
                  vectorEffect="non-scaling-stroke"
                  strokeLinejoin="round"
                />
              )}
              {points.length > 1 &&
                kind === 'line' &&
                points.map((p, i) => (
                  <circle key={p.label} cx={x(i)} cy={y(p.value)} r="2.5" fill={PALETTE[0]} />
                ))}
            </svg>
          </div>
          <div aria-hidden className="ml-14 flex justify-between text-[11px] text-muted">
            <span>{points[0].label}</span>
            <span>{points[points.length - 1].label}</span>
          </div>
        </div>
      )}
      {points.length > 0 && (
        <button
          type="button"
          aria-pressed={table}
          onClick={() => setTable(!table)}
          className="mt-2 text-[12px] text-muted underline"
        >
          {table ? 'View as chart' : 'View as table'}
        </button>
      )}
    </div>
  )
}

/** Horizontal bars for a ranking or ageing buckets. Every bar carries its value as text. */
export function HBars({ title, rows, format = 'money' }: { title: string; rows: Point[]; format?: Fmt }) {
  const max = Math.max(1, ...rows.map((r) => r.value))
  if (!rows.length) return <p className="py-6 text-center text-[13px] text-muted">No data yet.</p>
  return (
    <ul aria-label={title} className="space-y-2">
      {rows.map((r) => (
        <li key={r.label} className="grid grid-cols-[minmax(0,7rem)_1fr_auto] items-center gap-2 text-[13px]">
          <span className="truncate" title={r.label}>
            {r.label}
          </span>
          <span aria-hidden className="h-2.5 overflow-hidden rounded-full bg-hover">
            <span
              className="block h-full rounded-full"
              style={{ width: `${Math.max(2, (Math.max(r.value, 0) / max) * 100)}%`, background: PALETTE[0] }}
            />
          </span>
          <span className="tabular-nums" title={full(r.value, format)}>
            {short(r.value, format)}
          </span>
        </li>
      ))}
    </ul>
  )
}
