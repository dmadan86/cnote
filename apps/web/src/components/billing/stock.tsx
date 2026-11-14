'use client'
import { ArrowLeft, Plus } from 'lucide-react'
import Link from 'next/link'
import { use, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { run } from './bits'
import { money, parseMinor } from './fmt'

export type StockItem = {
  id: string
  sku: string
  name: string
  hsn_sac: string | null
  unit: string
  gst_rate_bp: number
  price_minor: number
  reorder_level: string
  active: boolean
  on_hand: string
  low: boolean
}
type Loc = { id: string; name: string; is_default: boolean; active: boolean }
type Move = {
  id: string
  kind: string
  qty: string
  location: string
  reason: string | null
  source_type: 'invoice' | 'purchase_bill' | null
  source_id: string | null
  created_at: string
}
type Detail = {
  item: StockItem
  locations: { location_id: string; name: string; on_hand: string }[]
  movements: Move[]
}

const KIND: Record<string, string> = {
  receipt: 'Received (bill)',
  issue: 'Sold (invoice)',
  adjustment: 'Adjustment',
  transfer_in: 'Transfer in',
  transfer_out: 'Transfer out',
  reversal: 'Reversal (cancelled)',
}
const REASONS = ['Opening stock', 'Count correction', 'Damaged', 'Theft or loss', 'Other']
const num = (s: string) => String(Number(s))

/** Item picker for invoice and bill lines: choosing one fills the line from the item. */
export function StockSelect({
  label,
  value,
  onPick,
  className,
}: {
  label: string
  value: string
  onPick: (it: StockItem | null) => void
  className?: string
}) {
  const { ws } = useShell()
  const items = useCmd<StockItem[]>(ws, 'stock_item.list', { limit: 500 })
  if (items.error || !items.data?.length) return null // no items (or no access): the line stays free text
  return (
    <select
      aria-label={label}
      value={value}
      onChange={(e) => onPick(items.data?.find((i) => i.id === e.target.value) ?? null)}
      className={className}
    >
      <option value="">No stock item</option>
      {items.data.map((i) => (
        <option key={i.id} value={i.id}>
          {i.sku} {i.name} ({num(i.on_hand)} {i.unit})
        </option>
      ))}
    </select>
  )
}

function ItemForm({
  initial,
  onClose,
  onSaved,
}: {
  initial?: StockItem
  onClose: () => void
  onSaved: () => void
}) {
  const { ws } = useShell()
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  return (
    <Modal open title={initial ? 'Edit item' : 'New item'} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          const price = parseMinor(String(f.get('price') || '0'))
          if (price === null) return setErr('Selling price is in rupees, for example 250 or 250.50.')
          setBusy(true)
          try {
            await run(ws, initial ? 'stock_item.update' : 'stock_item.create', {
              ...(initial ? { id: initial.id } : {}),
              sku: String(f.get('sku')),
              name: String(f.get('name')),
              hsn_sac: String(f.get('hsn') ?? ''),
              unit: String(f.get('unit') || 'pcs'),
              gst_rate_bp: Number(f.get('rate')) * 100,
              price_minor: price,
              reorder_level: String(f.get('reorder') || '0'),
            })
            onSaved()
            onClose()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="SKU">
            {(id) => <input id={id} name="sku" required defaultValue={initial?.sku} className={inputCls} />}
          </Field>
          <Field label="Unit">
            {(id) => <input id={id} name="unit" defaultValue={initial?.unit ?? 'pcs'} className={inputCls} />}
          </Field>
        </div>
        <Field label="Name">
          {(id) => <input id={id} name="name" required defaultValue={initial?.name} className={inputCls} />}
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="HSN code (4, 6 or 8 digits)">
            {(id) => (
              <input
                id={id}
                name="hsn"
                inputMode="numeric"
                defaultValue={initial?.hsn_sac ?? ''}
                className={inputCls}
              />
            )}
          </Field>
          <Field label="GST rate">
            {(id) => (
              <select
                id={id}
                name="rate"
                defaultValue={(initial?.gst_rate_bp ?? 1800) / 100}
                className={inputCls}
              >
                {[0, 5, 12, 18, 28].map((r) => (
                  <option key={r} value={r}>
                    {r}%
                  </option>
                ))}
              </select>
            )}
          </Field>
          <Field label="Selling price per unit (INR)">
            {(id) => (
              <input
                id={id}
                name="price"
                inputMode="decimal"
                defaultValue={initial ? String(initial.price_minor / 100) : ''}
                className={inputCls}
              />
            )}
          </Field>
          <Field label="Reorder level (0 = no alert)">
            {(id) => (
              <input
                id={id}
                name="reorder"
                inputMode="decimal"
                defaultValue={initial ? num(initial.reorder_level) : '0'}
                className={inputCls}
              />
            )}
          </Field>
        </div>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy}>
            Save
          </button>
        </div>
      </form>
    </Modal>
  )
}

function AdjustForm({
  item,
  locs,
  onClose,
  onSaved,
}: {
  item: StockItem
  locs: Loc[]
  onClose: () => void
  onSaved: () => void
}) {
  const { ws } = useShell()
  const [dir, setDir] = useState<'add' | 'remove'>('add')
  const [qty, setQty] = useState('')
  const [loc, setLoc] = useState(locs.find((l) => l.is_default)?.id ?? '')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const q = Number(qty)
  const after = Number(item.on_hand) + (dir === 'add' ? q : -q)
  return (
    <Modal open title={`Adjust stock: ${item.name}`} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          const reason = [String(f.get('reason')), String(f.get('note') ?? '').trim()]
            .filter(Boolean)
            .join(': ')
          const cost = String(f.get('cost') ?? '').trim()
          const unitCost = cost ? parseMinor(cost) : undefined
          if (unitCost === null) return setErr('Cost per unit is in rupees, for example 120 or 120.50.')
          if (!(q > 0)) return setErr('Enter a quantity greater than zero.')
          setBusy(true)
          try {
            await run(ws, 'stock.adjust', {
              item_id: item.id,
              location_id: loc || undefined,
              qty: dir === 'add' ? qty : `-${qty}`,
              reason,
              unit_cost_minor: dir === 'add' ? unitCost : undefined,
            })
            onSaved()
            onClose()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <fieldset className="flex gap-2">
          <legend className="sr-only">Direction</legend>
          {(['add', 'remove'] as const).map((d) => (
            <label key={d} className="flex items-center gap-1.5 text-[14px]">
              <input type="radio" name="dir" checked={dir === d} onChange={() => setDir(d)} />
              {d === 'add' ? 'Add stock' : 'Remove stock'}
            </label>
          ))}
        </fieldset>
        <div className="grid grid-cols-2 gap-3">
          <Field label={`Quantity (${item.unit})`}>
            {(id) => (
              <input
                id={id}
                inputMode="decimal"
                required
                value={qty}
                onChange={(e) => setQty(e.target.value)}
                className={inputCls}
              />
            )}
          </Field>
          <Field label="Location">
            {(id) => (
              <select id={id} value={loc} onChange={(e) => setLoc(e.target.value)} className={inputCls}>
                {locs
                  .filter((l) => l.active)
                  .map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.name}
                    </option>
                  ))}
              </select>
            )}
          </Field>
        </div>
        {q > 0 && (
          <p className="text-[13px] text-muted" aria-live="polite">
            On hand {num(item.on_hand)} to{' '}
            {Number.isFinite(after) ? String(Math.round(after * 1000) / 1000) : '?'} {item.unit} (all
            locations)
          </p>
        )}
        <Field label="Reason">
          {(id) => (
            <select id={id} name="reason" required className={inputCls}>
              {REASONS.map((r) => (
                <option key={r}>{r}</option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Note (optional)">{(id) => <input id={id} name="note" className={inputCls} />}</Field>
        {dir === 'add' && (
          <Field label="Cost per unit (INR), needed when the item has no stock yet">
            {(id) => <input id={id} name="cost" inputMode="decimal" className={inputCls} />}
          </Field>
        )}
        <p className="text-[12px] text-muted">
          Stock is counted from the ledger, so an adjustment adds a line; it never edits history.
        </p>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy}>
            Save adjustment
          </button>
        </div>
      </form>
    </Modal>
  )
}

function TransferForm({
  item,
  locs,
  onClose,
  onSaved,
}: {
  item: StockItem
  locs: Loc[]
  onClose: () => void
  onSaved: () => void
}) {
  const { ws } = useShell()
  const active = locs.filter((l) => l.active)
  const [from, setFrom] = useState(active[0]?.id ?? '')
  const [to, setTo] = useState(active[1]?.id ?? '')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  return (
    <Modal open title={`Transfer: ${item.name}`} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          if (from === to) return setErr('Choose two different locations.')
          setBusy(true)
          try {
            await run(ws, 'stock.transfer', {
              item_id: item.id,
              from_location_id: from,
              to_location_id: to,
              qty: String(new FormData(e.currentTarget).get('qty')),
            })
            onSaved()
            onClose()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <Field label="From">
          {(id) => (
            <select id={id} value={from} onChange={(e) => setFrom(e.target.value)} className={inputCls}>
              {active.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="To">
          {(id) => (
            <select id={id} value={to} onChange={(e) => setTo(e.target.value)} className={inputCls}>
              {active.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label={`Quantity (${item.unit})`}>
          {(id) => <input id={id} name="qty" required inputMode="decimal" className={inputCls} />}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy || active.length < 2}>
            Transfer
          </button>
        </div>
        {active.length < 2 && <p className="text-[12px] text-muted">Add a second location first.</p>}
      </form>
    </Modal>
  )
}

function LocationsModal({
  locs,
  onClose,
  onSaved,
}: {
  locs: Loc[]
  onClose: () => void
  onSaved: () => void
}) {
  const { ws, me } = useShell()
  const role = me.workspaces.find((w) => w.id === ws)?.role
  const admin = role === 'owner' || role === 'admin'
  const [err, setErr] = useState<string>()
  const act = async (name: string, body: unknown) => {
    try {
      await run(ws, name, body)
      setErr(undefined)
      onSaved()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <Modal open title="Locations" onClose={onClose}>
      <ul className="mb-3 divide-y divide-line rounded-md border border-line text-[14px]">
        {locs.map((l) => (
          <li key={l.id} className="flex flex-wrap items-center gap-2 px-3 py-2">
            <span className={l.active ? '' : 'text-muted line-through'}>{l.name}</span>
            {l.is_default && <Pill tone="ok">Default</Pill>}
            {admin && (
              <span className="ml-auto flex gap-1">
                {!l.is_default && l.active && (
                  <button
                    type="button"
                    className={btn}
                    onClick={() => act('stock_location.update', { id: l.id, is_default: true })}
                  >
                    Make default
                  </button>
                )}
                {!l.is_default && (
                  <button
                    type="button"
                    className={btn}
                    onClick={() => act('stock_location.update', { id: l.id, active: !l.active })}
                  >
                    {l.active ? 'Archive' : 'Restore'}
                  </button>
                )}
              </span>
            )}
          </li>
        ))}
      </ul>
      {admin ? (
        <form
          className="flex gap-2"
          onSubmit={async (e) => {
            e.preventDefault()
            const form = e.currentTarget
            await act('stock_location.create', { name: String(new FormData(form).get('name')) })
            form.reset()
          }}
        >
          <input
            name="name"
            required
            aria-label="New location name"
            placeholder="Warehouse, shop, van"
            className={inputCls}
          />
          <button type="submit" className={btnPrimary}>
            Add
          </button>
        </form>
      ) : (
        <p className="text-[13px] text-muted">Only admins add or change locations.</p>
      )}
      <p className="mt-2 text-[12px] text-muted">Bills and invoices post to the default location.</p>
      <ErrorText error={err} />
    </Modal>
  )
}

function Valuation() {
  const { ws } = useShell()
  const v = useCmd<{ total_minor: number }>(ws, 'report.stock_valuation', {})
  if (!v.data) return null // no finance access, or still loading
  return (
    <div className="rounded-lg border border-line p-3">
      <div className="text-[12px] text-muted">Stock value, weighted average cost, all locations</div>
      <div className="text-lg font-semibold tabular-nums">{money(v.data.total_minor)}</div>
    </div>
  )
}

export function StockList() {
  const { ws } = useShell()
  const [q, setQ] = useState('')
  const [low, setLow] = useState(false)
  const items = useCmd<StockItem[]>(ws, 'stock_item.list', { q, low, include_archived: false, limit: 500 })
  const locs = useCmd<Loc[]>(ws, 'stock_location.list', {})
  const [dlg, setDlg] = useState<{
    kind: 'new' | 'edit' | 'adjust' | 'transfer' | 'locations'
    item?: StockItem
  }>()
  const done = () => {
    items.reload()
    locs.reload()
  }
  const close = () => setDlg(undefined)
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <input
          aria-label="Search items"
          placeholder="Search SKU or name"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          className={`${inputCls} max-w-xs`}
        />
        <label className="flex items-center gap-1.5 text-[13px]">
          <input type="checkbox" checked={low} onChange={(e) => setLow(e.target.checked)} />
          Low stock only
        </label>
        <span className="ml-auto flex gap-2">
          <button type="button" className={btn} onClick={() => setDlg({ kind: 'locations' })}>
            Locations
          </button>
          <button type="button" className={btnPrimary} onClick={() => setDlg({ kind: 'new' })}>
            <Plus size={14} /> New item
          </button>
        </span>
      </div>
      <Valuation />
      <ErrorText error={items.error} />
      {items.data && items.data.length === 0 && (
        <p className="rounded-lg border border-dashed border-line p-6 text-center text-[14px] text-muted">
          {q || low ? 'No items match.' : 'No items yet. Create your first item, then add opening stock.'}
        </p>
      )}
      {!!items.data?.length && (
        <div className="overflow-x-auto rounded-lg border border-line">
          <table className="w-full min-w-[520px] text-left text-[13px]">
            <thead className="border-b border-line text-[12px] text-muted">
              <tr>
                <th className="p-2 font-medium">Item</th>
                <th className="font-medium">SKU</th>
                <th className="text-right font-medium">On hand (all locations)</th>
                <th className="text-right font-medium">Reorder at</th>
                <th className="w-40 p-2" />
              </tr>
            </thead>
            <tbody>
              {items.data.map((i) => (
                <tr key={i.id} className="border-b border-line last:border-0">
                  <td className="p-2">
                    <Link href={`/w/${ws}/billing/stock/${i.id}`} className="font-medium hover:underline">
                      {i.name}
                    </Link>
                  </td>
                  <td>{i.sku}</td>
                  <td className="text-right tabular-nums">
                    {num(i.on_hand)} {i.unit} {i.low && <Pill tone="warn">Low stock</Pill>}
                  </td>
                  <td className="text-right tabular-nums">
                    {Number(i.reorder_level) ? num(i.reorder_level) : 'none'}
                  </td>
                  <td className="p-2 text-right">
                    <button
                      type="button"
                      className={`${btn} !min-h-9`}
                      onClick={() => setDlg({ kind: 'adjust', item: i })}
                    >
                      Adjust
                    </button>{' '}
                    <button
                      type="button"
                      className={`${btn} !min-h-9`}
                      onClick={() => setDlg({ kind: 'transfer', item: i })}
                    >
                      Transfer
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {dlg?.kind === 'new' && <ItemForm onClose={close} onSaved={done} />}
      {dlg?.kind === 'edit' && <ItemForm initial={dlg.item} onClose={close} onSaved={done} />}
      {dlg?.kind === 'adjust' && dlg.item && locs.data && (
        <AdjustForm item={dlg.item} locs={locs.data} onClose={close} onSaved={done} />
      )}
      {dlg?.kind === 'transfer' && dlg.item && locs.data && (
        <TransferForm item={dlg.item} locs={locs.data} onClose={close} onSaved={done} />
      )}
      {dlg?.kind === 'locations' && locs.data && (
        <LocationsModal locs={locs.data} onClose={close} onSaved={done} />
      )}
    </div>
  )
}

export function StockDetail({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws } = useShell()
  const d = useCmd<Detail>(ws, 'stock_item.get', { id })
  const locs = useCmd<Loc[]>(ws, 'stock_location.list', {})
  const [dlg, setDlg] = useState<'edit' | 'adjust' | 'transfer'>()
  const it = d.data?.item
  const link = (m: Move) =>
    m.source_id &&
    (m.source_type === 'invoice'
      ? `/w/${ws}/billing/${m.source_id}`
      : `/w/${ws}/billing/bills/${m.source_id}`)
  return (
    <div className="space-y-4">
      <Link
        href={`/w/${ws}/billing/stock`}
        className="inline-flex items-center gap-1 text-[13px] text-muted hover:text-fg"
      >
        <ArrowLeft size={14} /> All items
      </Link>
      <ErrorText error={d.error} />
      {it && (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-lg font-semibold">{it.name}</h2>
            <span className="text-[13px] text-muted">{it.sku}</span>
            {!it.active && <Pill>Archived</Pill>}
            {it.low && <Pill tone="warn">Low stock</Pill>}
            <span className="ml-auto flex gap-2">
              <button type="button" className={btn} onClick={() => setDlg('edit')}>
                Edit
              </button>
              <button type="button" className={btn} onClick={() => setDlg('transfer')}>
                Transfer
              </button>
              <button type="button" className={btnPrimary} onClick={() => setDlg('adjust')}>
                Adjust
              </button>
            </span>
          </div>
          <dl className="grid grid-cols-2 gap-3 text-[13px] sm:grid-cols-4">
            <div>
              <dt className="text-muted">On hand</dt>
              <dd className="text-base font-semibold tabular-nums">
                {num(it.on_hand)} {it.unit}
              </dd>
            </div>
            {d.data?.locations.map((l) => (
              <div key={l.location_id}>
                <dt className="text-muted">{l.name}</dt>
                <dd className="tabular-nums">{num(l.on_hand)}</dd>
              </div>
            ))}
          </dl>
          <h3 className="text-[14px] font-semibold">Stock ledger</h3>
          {d.data?.movements.length === 0 ? (
            <p className="text-[14px] text-muted">No movements yet.</p>
          ) : (
            <div className="overflow-x-auto rounded-lg border border-line">
              <table className="w-full min-w-[520px] text-left text-[13px]">
                <thead className="border-b border-line text-[12px] text-muted">
                  <tr>
                    <th className="p-2 font-medium">Date</th>
                    <th className="font-medium">What</th>
                    <th className="font-medium">Location</th>
                    <th className="text-right font-medium">Qty</th>
                    <th className="p-2 font-medium">Reason or document</th>
                  </tr>
                </thead>
                <tbody>
                  {d.data?.movements.map((m) => (
                    <tr key={m.id} className="border-b border-line last:border-0">
                      <td className="p-2 whitespace-nowrap">{m.created_at.slice(0, 10)}</td>
                      <td>{KIND[m.kind] ?? m.kind}</td>
                      <td>{m.location}</td>
                      <td className="text-right tabular-nums">
                        {Number(m.qty) > 0 ? '+' : ''}
                        {num(m.qty)} {it.unit}
                      </td>
                      <td className="p-2">
                        {link(m) ? (
                          <Link href={link(m) as string} className="underline">
                            {m.source_type === 'invoice' ? 'Invoice' : 'Bill'}
                          </Link>
                        ) : (
                          (m.reason ?? '')
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {dlg === 'edit' && <ItemForm initial={it} onClose={() => setDlg(undefined)} onSaved={d.reload} />}
          {dlg === 'adjust' && locs.data && (
            <AdjustForm item={it} locs={locs.data} onClose={() => setDlg(undefined)} onSaved={d.reload} />
          )}
          {dlg === 'transfer' && locs.data && (
            <TransferForm item={it} locs={locs.data} onClose={() => setDlg(undefined)} onSaved={d.reload} />
          )}
        </>
      )}
    </div>
  )
}
