'use client'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { money, parseMinor, toRupees } from '@/components/billing/fmt'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

type Entry = {
  id: string
  item_id: string
  item_key: string
  item_title: string
  user_id: string
  user_name: string
  worked_on: string
  minutes: number
  note: string | null
  billable: boolean
  invoice_id: string | null
}
type Listing = {
  entries: Entry[]
  totals: { minutes: number; billable_minutes: number; unbilled_minutes: number }
}

/** "1h30", "1:30", "90m", "1.5" (hours) or "45" (minutes) -> minutes, null when unreadable. */
export function parseDuration(s: string): number | null {
  const t = s.trim().toLowerCase()
  let m =
    /^(\d+):(\d{1,2})$/.exec(t) ?? /^(\d+)\s*h(?:ours?|rs?)?\s*(?:(\d+)\s*(?:m(?:in(?:utes?)?)?)?)?$/.exec(t)
  if (m) return Number(m[1]) * 60 + Number(m[2] ?? 0)
  m = /^(\d+)\s*m(?:in(?:utes?)?)?$/.exec(t)
  if (m) return Number(m[1])
  m = /^(\d+)\.(\d+)$/.exec(t)
  if (m) return Math.round(Number(t) * 60)
  return /^\d+$/.test(t) ? Number(t) : null
}
export const hm = (min: number) => `${Math.floor(min / 60)}h ${String(min % 60).padStart(2, '0')}m`
const iso = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const today = () => iso(new Date())

/** Time card on an item: entries, a quick add form and a browser-only timer that fills the field when stopped. */
export function ItemTimeCard({ itemId, push }: { itemId: string; push?: (m: string) => void }) {
  const { ws, me } = useShell()
  const list = useCmd<Listing>(ws, 'time.list', { item_id: itemId })
  const [err, setErr] = useState<string>()
  const [text, setText] = useState('')
  const [since, setSince] = useState<number>()
  const [, tick] = useState(0)
  const [live, setLive] = useState('')
  useEffect(() => {
    if (since === undefined) return
    const t = setInterval(() => tick((n) => n + 1), 30000)
    return () => clearInterval(t)
  }, [since])
  const parsed = text ? parseDuration(text) : null

  function stop() {
    if (since === undefined) return
    const min = Math.max(1, Math.round((Date.now() - since) / 60000))
    setSince(undefined)
    setText(`${min}m`)
    setLive('Timer stopped')
  }
  async function add(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    if (!parsed) return setErr('Enter the time like 1h30, 90m or 1.5')
    try {
      await cmd(ws, 'time.log', {
        item_id: itemId,
        minutes: parsed,
        worked_on: String(f.get('day')) || today(),
        note: String(f.get('note')),
        billable: f.get('billable') === 'on',
      })
      setText('')
      setErr(undefined)
      list.reload()
      push?.('Time logged')
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const del = (id: string) =>
    cmd(ws, 'time.delete', { id }).then(list.reload, (x) => setErr((x as Error).message))
  const d = list.data
  return (
    <section className="rounded-lg border border-line bg-side p-3" aria-label="Time">
      <h2 className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-muted">
        Time {d ? `· ${hm(d.totals.minutes)}` : ''}
      </h2>
      <ul className="mb-2 space-y-1.5">
        {d?.entries.length === 0 && <li className="text-[13px] text-muted">No time logged yet.</li>}
        {d?.entries.map((e) => (
          <li key={e.id} className="flex flex-wrap items-center gap-1.5 text-[13px]">
            <span className="text-muted">{e.worked_on}</span>
            <span>{e.user_name}</span>
            <strong>{hm(e.minutes)}</strong>
            {e.invoice_id ? (
              <Pill tone="ok">Invoiced</Pill>
            ) : e.billable ? (
              <Pill>Billable</Pill>
            ) : (
              <Pill>Not billable</Pill>
            )}
            {!e.invoice_id &&
              (e.user_id === me.user.id ||
                ['owner', 'admin'].includes(me.workspaces.find((w) => w.id === ws)?.role ?? '')) && (
                <button
                  type="button"
                  onClick={() => del(e.id)}
                  className="ml-auto min-h-8 px-1 text-[12px] text-muted hover:text-fg"
                  aria-label={`Delete ${hm(e.minutes)} on ${e.worked_on}`}
                >
                  Delete
                </button>
              )}
            {e.note && <span className="basis-full text-muted">{e.note}</span>}
          </li>
        ))}
      </ul>
      <form onSubmit={add} className="space-y-2">
        <Field label="Time spent">
          {(id) => (
            <input
              id={id}
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder="1h30, 90m or 1.5"
              className={inputCls}
              aria-describedby="time-hint"
            />
          )}
        </Field>
        <p id="time-hint" className="text-[12px] text-muted">
          {parsed ? `Will log ${hm(parsed)}` : 'Examples: 1h30, 90m, 1.5'}
        </p>
        <Field label="Date">
          {(id) => <input id={id} name="day" type="date" defaultValue={today()} className={inputCls} />}
        </Field>
        <Field label="Note">{(id) => <input id={id} name="note" className={inputCls} />}</Field>
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" name="billable" defaultChecked /> Billable
        </label>
        <ErrorText error={err} />
        <div className="flex flex-wrap items-center gap-2">
          <button type="submit" className={btnPrimary}>
            Log time
          </button>
          {since === undefined ? (
            <button
              type="button"
              className={btn}
              onClick={() => {
                setSince(Date.now())
                setLive('Timer started')
              }}
            >
              Start timer
            </button>
          ) : (
            <button type="button" className={btn} onClick={stop}>
              Stop timer ({hm(Math.max(1, Math.round((Date.now() - since) / 60000)))})
            </button>
          )}
          <span className="text-[12px] text-muted">
            {since === undefined ? 'Timer lives in this browser only' : 'Running (this browser only)'}
          </span>
        </div>
        <p className="sr-only" role="status">
          {live}
        </p>
      </form>
    </section>
  )
}

const DAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
function monday(d: Date) {
  const x = new Date(d)
  x.setDate(x.getDate() - ((x.getDay() + 6) % 7))
  return x
}

type Profile = { contact_id: string; hourly_rate_minor: number; hsn_sac: string; gst_rate_bp: number }

/** Time tab of a project: my week, then the unbilled hours with "Create invoice". */
export function TimeTab({ space }: { space: { id: string } }) {
  const { ws, me } = useShell()
  const [start, setStart] = useState(() => monday(new Date()))
  const [through, setThrough] = useState(today())
  const [group, setGroup] = useState('none')
  const [err, setErr] = useState<string>()
  const [made, setMade] = useState<string>()
  const days = Array.from({ length: 7 }, (_, i) =>
    iso(new Date(start.getFullYear(), start.getMonth(), start.getDate() + i)),
  )
  const mine = useCmd<Listing>(ws, 'time.list', {
    space_id: space.id,
    user_id: me.user.id,
    from: days[0],
    to: days[6],
  })
  const unbilled = useCmd<Listing>(ws, 'time.list', { space_id: space.id, unbilled: true, to: through })
  const profile = useCmd<Profile>(ws, 'project.billing_get', { space_id: space.id })
  const rows = new Map<string, { title: string; cells: number[] }>()
  for (const e of mine.data?.entries ?? []) {
    const r = rows.get(e.item_id) ?? { title: `${e.item_key} ${e.item_title}`, cells: Array(7).fill(0) }
    r.cells[days.indexOf(e.worked_on)] += e.minutes
    rows.set(e.item_id, r)
  }
  const rate = profile.data?.hourly_rate_minor ?? 0
  const mins = unbilled.data?.totals.unbilled_minutes ?? 0
  const value = Math.floor((rate * mins + 30) / 60)
  const ready = !!profile.data?.contact_id && rate > 0 && !!profile.data?.hsn_sac
  async function create() {
    try {
      const r = await cmd<{ invoice: { id: string } }>(ws, 'time.invoice', {
        space_id: space.id,
        to_date: through,
        group_by: group,
      })
      setMade(r.invoice.id)
      setErr(undefined)
      unbilled.reload()
      mine.reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <div className="mx-auto w-full max-w-4xl space-y-8 p-4 sm:p-6">
      <section aria-label="My week" className="space-y-2">
        <div className="flex items-center gap-2">
          <h2 className="text-[14px] font-semibold">My week</h2>
          <button
            type="button"
            className={btn}
            onClick={() => setStart(new Date(start.getFullYear(), start.getMonth(), start.getDate() - 7))}
          >
            Previous
          </button>
          <span className="text-[13px] text-muted">
            {days[0]} to {days[6]}
          </span>
          <button
            type="button"
            className={btn}
            onClick={() => setStart(new Date(start.getFullYear(), start.getMonth(), start.getDate() + 7))}
          >
            Next
          </button>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[560px] text-[13px]">
            <thead>
              <tr className="text-left text-muted">
                <th scope="col" className="py-1 pr-2 font-medium">
                  Item
                </th>
                {days.map((d, i) => (
                  <th key={d} scope="col" className="px-1 text-right font-medium">
                    {DAYS[i]}
                    <span className="block text-[11px]">{d.slice(5)}</span>
                  </th>
                ))}
                <th scope="col" className="pl-2 text-right font-medium">
                  Total
                </th>
              </tr>
            </thead>
            <tbody>
              {[...rows.values()].map((r) => (
                <tr key={r.title} className="border-t border-line">
                  <th scope="row" className="py-1 pr-2 text-left font-normal">
                    {r.title}
                  </th>
                  {r.cells.map((m, i) => (
                    <td key={days[i]} className="px-1 text-right tabular-nums">
                      {m ? hm(m) : ''}
                    </td>
                  ))}
                  <td className="pl-2 text-right font-medium tabular-nums">
                    {hm(r.cells.reduce((a, b) => a + b, 0))}
                  </td>
                </tr>
              ))}
              {rows.size === 0 && (
                <tr>
                  <td colSpan={9} className="py-3 text-muted">
                    Nothing logged this week. Log time from an item.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <p className="text-[13px] text-muted">Week total: {hm(mine.data?.totals.minutes ?? 0)}</p>
      </section>

      <section aria-label="Unbilled" className="space-y-3">
        <h2 className="text-[14px] font-semibold">Unbilled time</h2>
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Include time through">
            {(id) => (
              <input
                id={id}
                type="date"
                value={through}
                onChange={(e) => setThrough(e.target.value)}
                className={inputCls}
              />
            )}
          </Field>
          <Field label="Group lines by">
            {(id) => (
              <select id={id} value={group} onChange={(e) => setGroup(e.target.value)} className={inputCls}>
                <option value="none">One line</option>
                <option value="item">Item</option>
                <option value="person">Person</option>
              </select>
            )}
          </Field>
        </div>
        <p className="text-[14px]" role="status">
          {hm(mins)} billable and not yet invoiced
          {rate > 0 ? ` at ${money(rate)} per hour = ${money(value)} before GST` : ''}
        </p>
        {!ready && (
          <p className="text-[13px] text-warn">
            Set the customer, hourly rate and SAC on the Billing tab before invoicing.
          </p>
        )}
        <ErrorText error={err} />
        {made && (
          <p className="text-[13px]">
            Draft invoice created.{' '}
            <Link className="underline" href={`/w/${ws}/billing/${made}`}>
              Open it
            </Link>
          </p>
        )}
        <button type="button" className={btnPrimary} disabled={!ready || mins === 0} onClick={create}>
          Create invoice
        </button>
        <ul className="divide-y divide-line text-[13px]">
          {unbilled.data?.entries.map((e) => (
            <li key={e.id} className="flex flex-wrap gap-x-3 py-1.5">
              <span className="text-muted">{e.worked_on}</span>
              <span>
                {e.item_key} {e.item_title}
              </span>
              <span className="text-muted">{e.user_name}</span>
              <strong className="ml-auto tabular-nums">{hm(e.minutes)}</strong>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}

type Milestone = {
  id: string
  name: string
  released: boolean
  billable: boolean
  bill_amount_minor: number | null
  bill_percent_bp: number | null
  quotation_number: string | null
  invoice_id: string | null
  invoice_number: string | null
  status: 'planned' | 'due' | 'invoiced' | 'paid'
}
type Money = Record<string, number>

const TONE = { planned: 'neutral', due: 'warn', invoiced: 'neutral', paid: 'ok' } as const

/** Billing tab: the project's money, its billing profile and milestone table. */
export function BillingTab({ space }: { space: { id: string } }) {
  const { ws } = useShell()
  const money_ = useCmd<Money>(ws, 'report.project_money', { space_id: space.id })
  const ms = useCmd<Milestone[]>(ws, 'milestone.list', { space_id: space.id })
  const profile = useCmd<Profile>(ws, 'project.billing_get', { space_id: space.id })
  const contacts = useCmd<{ id: string; name: string }[]>(ws, 'contact.list', { limit: 500 })
  const quotes = useCmd<{ id: string; number: string | null; status: string; space_id: string }[]>(
    ws,
    'quote.list',
    { limit: 200 },
  )
  const [err, setErr] = useState<string>()
  const [dates, setDates] = useState<Record<string, string>>({})
  const m = money_.data
  const reload = () => (money_.reload(), ms.reload(), profile.reload())
  const fail = (x: unknown) => setErr((x as Error).message)
  async function saveProfile(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    const rate = parseMinor(String(f.get('rate')))
    if (rate === null) return setErr('Hourly rate must be an amount like 1500 or 1500.50')
    cmd(ws, 'project.billing_set', {
      space_id: space.id,
      contact_id: String(f.get('contact')) || undefined,
      hourly_rate_minor: rate,
      hsn_sac: String(f.get('sac')),
    }).then(() => (setErr(undefined), reload()), fail)
  }
  async function addMilestone(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const f = new FormData(form)
    const quote = String(f.get('quote')) || undefined
    const pct = Number(f.get('percent'))
    try {
      const v = await cmd<{ id: string }>(ws, 'version.create', {
        space_id: space.id,
        name: String(f.get('name')),
      })
      const amt = parseMinor(String(f.get('amount')))
      await cmd(ws, 'milestone.set_billing', {
        version_id: v.id,
        quotation_id: quote,
        ...(pct ? { bill_percent_bp: Math.round(pct * 100) } : { bill_amount_minor: amt }),
      })
      setErr(undefined)
      form.reset()
      reload()
    } catch (x) {
      fail(x)
    }
  }
  const raise = (id: string) =>
    cmd(ws, 'milestone.invoice', { version_id: id, ...(dates[id] ? { invoice_date: dates[id] } : {}) }).then(
      () => (setErr(undefined), reload()),
      fail,
    )
  const tile = (label: string, v: number | undefined, hint?: string) => (
    <div className="rounded-lg border border-line p-3">
      <dt className="text-[12px] text-muted">{label}</dt>
      <dd className="text-[16px] font-semibold tabular-nums">{v === undefined ? '...' : money(v)}</dd>
      {hint && <p className="text-[11px] text-muted">{hint}</p>}
    </div>
  )
  if (money_.error?.message.includes('finance access'))
    return <p className="p-6 text-muted">Finance access is turned off for you. Ask an admin.</p>
  return (
    <div className="mx-auto w-full max-w-4xl space-y-8 p-4 sm:p-6">
      <section aria-label="Project money" className="space-y-2">
        <h2 className="text-[14px] font-semibold">Project money</h2>
        <dl className="grid grid-cols-2 gap-2 sm:grid-cols-3">
          {tile('Quoted (before GST)', m?.quoted_minor)}
          {tile('Invoiced (before GST)', m?.invoiced_minor)}
          {tile(
            'Received',
            m?.received_minor,
            'Bank and cash payments; advances applied are counted separately',
          )}
          {tile('Advance held', m?.advance_unapplied_minor, 'Received, not yet applied to an invoice')}
          {tile('Advance applied', m?.advance_applied_minor)}
          {tile(
            'Unbilled hours value',
            m?.unbilled_value_minor,
            m ? `${hm(m.unbilled_minutes)} unbilled of ${hm(m.minutes)} logged` : undefined,
          )}
        </dl>
      </section>

      <section aria-label="Milestones" className="space-y-2">
        <h2 className="text-[14px] font-semibold">Milestones</h2>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[560px] text-[13px]">
            <thead>
              <tr className="text-left text-muted">
                <th scope="col" className="py-1 font-medium">
                  Milestone
                </th>
                <th scope="col" className="font-medium">
                  Share
                </th>
                <th scope="col" className="text-right font-medium">
                  Amount before GST
                </th>
                <th scope="col" className="pl-3 font-medium">
                  Status
                </th>
                <th scope="col" className="font-medium">
                  Invoice
                </th>
              </tr>
            </thead>
            <tbody>
              {ms.data
                ?.filter((x) => x.billable)
                .map((x) => (
                  <tr key={x.id} className="border-t border-line">
                    <th scope="row" className="py-1.5 text-left font-normal">
                      {x.name}
                      {x.quotation_number && (
                        <span className="block text-[11px] text-muted">of {x.quotation_number}</span>
                      )}
                    </th>
                    <td>{x.bill_percent_bp ? `${x.bill_percent_bp / 100}%` : 'Fixed'}</td>
                    <td className="text-right tabular-nums">{money(x.bill_amount_minor ?? 0)}</td>
                    <td className="pl-3">
                      <Pill tone={TONE[x.status]}>{x.status[0].toUpperCase() + x.status.slice(1)}</Pill>
                    </td>
                    <td>
                      {x.invoice_id ? (
                        <Link className="underline" href={`/w/${ws}/billing/${x.invoice_id}`}>
                          {x.invoice_number ?? 'Draft'}
                        </Link>
                      ) : (
                        <span className="flex flex-wrap items-center gap-1.5">
                          <input
                            type="date"
                            aria-label={`Invoice date for ${x.name}`}
                            value={dates[x.id] ?? ''}
                            onChange={(e) => setDates({ ...dates, [x.id]: e.target.value })}
                            className="rounded-md border border-line bg-canvas px-1.5 py-1 text-[12px]"
                          />
                          <button
                            type="button"
                            className={btn}
                            onClick={() => raise(x.id)}
                            aria-label={`Raise invoice for ${x.name}`}
                          >
                            Raise invoice
                          </button>
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              {ms.data?.every((x) => !x.billable) && (
                <tr>
                  <td colSpan={5} className="py-3 text-muted">
                    No billing milestones yet.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <p className="text-[12px] text-muted">
          Invoice date defaults to the milestone's release date, else today. Leave it empty to accept that.
        </p>
        <form onSubmit={addMilestone} className="grid gap-2 sm:grid-cols-5" aria-label="Add milestone">
          <Field label="Name">{(id) => <input id={id} name="name" required className={inputCls} />}</Field>
          <Field label="Percent of quotation">
            {(id) => (
              <input id={id} name="percent" inputMode="decimal" placeholder="30" className={inputCls} />
            )}
          </Field>
          <Field label="or fixed amount">
            {(id) => (
              <input id={id} name="amount" inputMode="decimal" placeholder="50000" className={inputCls} />
            )}
          </Field>
          <Field label="Quotation">
            {(id) => (
              <select id={id} name="quote" className={inputCls}>
                <option value="">None</option>
                {quotes.data
                  ?.filter((q) => q.space_id === space.id && ['accepted', 'converted'].includes(q.status))
                  .map((q) => (
                    <option key={q.id} value={q.id}>
                      {q.number}
                    </option>
                  ))}
              </select>
            )}
          </Field>
          <button type="submit" className={`${btn} self-end`}>
            Add milestone
          </button>
        </form>
      </section>

      <section aria-label="Billing profile" className="space-y-2">
        <h2 className="text-[14px] font-semibold">Billing profile</h2>
        <form
          key={profile.data?.contact_id ?? 'new'}
          onSubmit={saveProfile}
          className="grid gap-2 sm:grid-cols-4"
        >
          <Field label="Customer">
            {(id) => (
              <select
                id={id}
                name="contact"
                defaultValue={profile.data?.contact_id ?? ''}
                className={inputCls}
              >
                <option value="">Choose</option>
                {contacts.data?.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            )}
          </Field>
          <Field label="Hourly rate (INR)">
            {(id) => (
              <input
                id={id}
                name="rate"
                defaultValue={profile.data ? toRupees(profile.data.hourly_rate_minor) : ''}
                inputMode="decimal"
                className={inputCls}
              />
            )}
          </Field>
          <Field label="SAC">
            {(id) => (
              <input
                id={id}
                name="sac"
                defaultValue={profile.data?.hsn_sac ?? ''}
                inputMode="numeric"
                placeholder="998314"
                className={inputCls}
              />
            )}
          </Field>
          <button type="submit" className={`${btn} self-end`}>
            Save profile
          </button>
        </form>
      </section>
      <ErrorText error={err} />
    </div>
  )
}
