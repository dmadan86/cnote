'use client'
import { useShell } from '@/components/shell'
import { Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import type { StockItem } from './stock'

/** Batch or serial details typed on a bill or invoice line. */
export type Trk = { batch: string; mfg: string; expiry: string; serials: string; override: string }
export const blankTrk: Trk = { batch: '', mfg: '', expiry: '', serials: '', override: '' }
type Saved = {
  batch?: string
  mfg_date?: string
  expiry_date?: string
  serials?: string[]
  override_expired_reason?: string
}

export const trkFrom = (t?: Saved | null): Trk => ({
  batch: t?.batch ?? '',
  mfg: t?.mfg_date ?? '',
  expiry: t?.expiry_date ?? '',
  serials: (t?.serials ?? []).join('\n'),
  override: t?.override_expired_reason ?? '',
})

const serialsOf = (s: string) =>
  s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean)

/** What the API takes: bills name the batch or serials, invoices only serials or an expired-stock override (batches are first-expiry-first-out). */
export function trkPayload(kind: 'bill' | 'invoice', t: Trk) {
  const o: Saved = {}
  if (t.serials.trim()) o.serials = serialsOf(t.serials)
  if (kind === 'bill') {
    if (t.batch.trim()) o.batch = t.batch.trim()
    if (t.mfg) o.mfg_date = t.mfg
    if (t.expiry) o.expiry_date = t.expiry
  } else if (t.override.trim()) o.override_expired_reason = t.override.trim()
  return Object.keys(o).length ? o : undefined
}

type Batch = {
  id: string
  number: string
  expiry_date: string | null
  on_hand: string
  days_left: number | null
  expired: boolean
}

const cellCls = 'min-h-9 w-full rounded-md border border-line bg-bg px-2 text-[13px]'

export function BatchPill({ b }: { b: Pick<Batch, 'expired' | 'days_left'> }) {
  if (b.expired) return <Pill tone="warn">Expired</Pill>
  if (b.days_left !== null && b.days_left <= 30) return <Pill tone="warn">Expires in {b.days_left} d</Pill>
  return null
}

/** Tracking inputs under a line whose stock item is tracked; renders nothing for untracked items. */
export function TrackingFields({
  label,
  stockId,
  qty,
  kind,
  value,
  onChange,
}: {
  label: string
  stockId: string
  qty: string
  kind: 'bill' | 'invoice'
  value: Trk
  onChange: (p: Partial<Trk>) => void
}) {
  const { ws } = useShell()
  const items = useCmd<StockItem[]>(ws, 'stock_item.list', { limit: 500 })
  const mode = items.data?.find((i) => i.id === stockId)?.tracking ?? 'none'
  const batches = useCmd<Batch[]>(
    ws,
    'stock_batch.list',
    { item_id: stockId },
    kind === 'invoice' && mode === 'batch',
  )
  if (mode === 'none') return null
  const n = serialsOf(value.serials).length
  const serialField = (
    <label className="block text-[12px] text-muted">
      Serial numbers, one per line
      <textarea
        aria-label={`${label} serial numbers`}
        value={value.serials}
        rows={3}
        onChange={(e) => onChange({ serials: e.target.value })}
        className={`${cellCls} mt-0.5 py-1`}
      />
      <span aria-live="polite" className={n === Number(qty) ? '' : 'text-warn'}>
        {n} of {Number(qty) || 0} serials entered
      </span>
    </label>
  )
  if (mode === 'serial') return <div className="mt-1 rounded-md bg-hover/40 p-2">{serialField}</div>
  if (kind === 'bill')
    return (
      <div className="mt-1 grid grid-cols-1 gap-2 rounded-md bg-hover/40 p-2 sm:grid-cols-3">
        <label className="text-[12px] text-muted">
          Batch number
          <input
            aria-label={`${label} batch number`}
            value={value.batch}
            onChange={(e) => onChange({ batch: e.target.value })}
            className={`${cellCls} mt-0.5`}
          />
        </label>
        <label className="text-[12px] text-muted">
          Manufactured (optional)
          <input
            type="date"
            aria-label={`${label} manufacturing date`}
            value={value.mfg}
            onChange={(e) => onChange({ mfg: e.target.value })}
            className={`${cellCls} mt-0.5`}
          />
        </label>
        <label className="text-[12px] text-muted">
          Expires (optional)
          <input
            type="date"
            aria-label={`${label} expiry date`}
            value={value.expiry}
            onChange={(e) => onChange({ expiry: e.target.value })}
            className={`${cellCls} mt-0.5`}
          />
        </label>
        <p className="text-[12px] text-muted sm:col-span-3">
          One batch per line: add another line for a second batch of the same item.
        </p>
      </div>
    )
  const stock = batches.data?.filter((b) => !b.expired)
  const gone = batches.data?.filter((b) => b.expired)
  return (
    <div className="mt-1 space-y-1 rounded-md bg-hover/40 p-2 text-[12px] text-muted">
      <p>Batches are taken first-expiry-first-out when the invoice is issued.</p>
      {!!stock?.length && (
        <ul aria-label={`${label} available batches`}>
          {stock.map((b) => (
            <li key={b.id} className="flex flex-wrap gap-x-2">
              <span>{b.number}</span>
              <span className="tabular-nums">{Number(b.on_hand)} on hand</span>
              <span>{b.expiry_date ? `expires ${b.expiry_date}` : 'no expiry'}</span>
              <BatchPill b={b} />
            </li>
          ))}
        </ul>
      )}
      {!!gone?.length && (
        <div>
          <p className="text-warn">
            {gone.map((b) => `${b.number} (${Number(b.on_hand)})`).join(', ')} expired and cannot be issued.
          </p>
          <label className="block">
            Admin only: issue expired stock, reason
            <input
              aria-label={`${label} expired stock reason`}
              value={value.override}
              onChange={(e) => onChange({ override: e.target.value })}
              className={`${cellCls} mt-0.5`}
            />
          </label>
        </div>
      )}
    </div>
  )
}
