'use client'
import {
  type Col,
  cellText,
  compactVal,
  type DatasetInfo,
  type FieldInfo,
  isNum,
  type RunResult,
  type Spec,
} from './lib'

/** Cell text for any column of a result: g0 uses the grouped field, row-mode columns use their own key. */
export function useCells(spec: Spec, ds: DatasetInfo) {
  const g = spec.group_by?.[0]
  const fieldOf = (c: Col): FieldInfo | undefined =>
    ds.fields.find((f) => f.key === (c.key === 'g0' ? g?.field : c.key))
  return {
    grouped: !!g,
    text: (c: Col, v: unknown, compact = false) =>
      cellText(c, v, fieldOf(c), c.key === 'g0' ? g?.bucket : undefined, compact),
  }
}

const th = 'py-1.5 font-medium'

export function DataTable({
  res,
  spec,
  ds,
  onDrill,
  max,
}: {
  res: RunResult
  spec: Spec
  ds: DatasetInfo
  onDrill?: (row: number) => void
  max?: number
}) {
  const { grouped, text } = useCells(spec, ds)
  const rows = max ? res.rows.slice(0, max) : res.rows
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-[13px]">
        <thead className="text-[12px] text-muted">
          <tr>
            {res.columns.map((c) => (
              <th key={c.key} scope="col" className={`${th} ${isNum(c.type) ? 'text-right' : ''}`}>
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: result rows have no id
            <tr key={i} className="border-t border-line hover:bg-hover">
              {res.columns.map((c, j) => (
                <td key={c.key} className={`py-1.5 ${isNum(c.type) ? 'text-right tabular-nums' : ''}`}>
                  {j === 0 && grouped && onDrill ? (
                    <button
                      type="button"
                      className="text-left text-accent underline-offset-2 hover:underline"
                      aria-label={`Show rows for ${text(c, r[j])}`}
                      onClick={() => onDrill(i)}
                    >
                      {text(c, r[j])}
                    </button>
                  ) : (
                    text(c, r[j])
                  )}
                </td>
              ))}
            </tr>
          ))}
          {res.totals && !max && (
            <tr className="border-t border-line font-semibold">
              {res.columns.map((c, j) => (
                <td key={c.key} className={`py-1.5 ${isNum(c.type) ? 'text-right tabular-nums' : ''}`}>
                  {j === 0 && res.totals?.[0] == null ? 'Total' : text(c, res.totals?.[j])}
                </td>
              ))}
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

/** Bars are CSS widths; every bar carries its value as text, so nothing depends on colour. */
export function Bars({
  res,
  spec,
  ds,
  onDrill,
  max = 12,
}: {
  res: RunResult
  spec: Spec
  ds: DatasetInfo
  onDrill?: (row: number) => void
  max?: number
}) {
  const { text } = useCells(spec, ds)
  const [g, m] = res.columns
  const rows = res.rows.slice(0, max)
  const top = Math.max(1, ...rows.map((r) => Math.abs(Number(r[1]) || 0)))
  if (!g || !m) return null
  return (
    <ul aria-label={`${m.label} by ${g.label}`} className="space-y-1.5">
      {rows.map((r, i) => {
        const label = text(g, r[0])
        const w = Math.max(1, (Math.abs(Number(r[1]) || 0) / top) * 100)
        const body = (
          <>
            <span className="w-32 shrink-0 truncate text-left sm:w-40">{label}</span>
            <span className="h-3 flex-1 rounded-sm bg-hover" aria-hidden>
              <span className="block h-3 rounded-sm bg-accent" style={{ width: `${w}%` }} />
            </span>
            <span className="w-20 shrink-0 text-right tabular-nums">
              {compactVal(m.type, r[1]) || text(m, r[1])}
            </span>
          </>
        )
        return (
          // biome-ignore lint/suspicious/noArrayIndexKey: result rows have no id
          <li key={i} className="text-[13px]">
            {onDrill ? (
              <button
                type="button"
                aria-label={`${label}: ${text(m, r[1])}. Show rows`}
                onClick={() => onDrill(i)}
                className="flex w-full items-center gap-2 rounded hover:bg-hover"
              >
                {body}
              </button>
            ) : (
              <div className="flex items-center gap-2">{body}</div>
            )}
          </li>
        )
      })}
      {res.rows.length > max && (
        <li className="text-[12px] text-muted">+ {res.rows.length - max} more in the table</li>
      )}
    </ul>
  )
}

/** The one number a report answers with: the only cell when ungrouped, else the first measure's total. */
export function headline(res: RunResult): { col: Col; value: unknown } | null {
  const grouped = res.columns[0]?.key === 'g0'
  const i = grouped ? 1 : 0
  const col = res.columns[i]
  if (!col) return null
  return { col, value: grouped ? res.totals?.[i] : res.rows[0]?.[i] }
}

export function BigNumber({ res }: { res: RunResult }) {
  const h = headline(res)
  if (!h) return null
  return (
    <div>
      <span className="block text-[12px] font-medium text-muted">{h.col.label}</span>
      <span className="mt-1 block text-xl font-semibold tabular-nums" title={String(h.value ?? '')}>
        {h.value == null ? '—' : compactVal(h.col.type, h.value) || cellText(h.col, h.value)}
      </span>
    </div>
  )
}
