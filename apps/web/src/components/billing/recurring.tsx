'use client'
import Link from 'next/link'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { run } from './bits'
import { type Contact, parseMinor } from './fmt'

type Schedule = {
  id: string
  contact_id: string
  frequency: string
  interval: number
  next_run: string
  status: string
  last_invoice_id: string | null
}
type Row = { key: number; description: string; quantity: string; price: string; rate: string }

const today = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Kolkata' }).format(new Date())
const cadence = (f: string, n: number) =>
  `Every ${n === 1 ? '' : `${n} `}${f === 'monthly' ? 'month' : f === 'quarterly' ? 'quarter' : 'year'}${n === 1 ? '' : 's'}`

export function RecurringList() {
  const { ws, spaces } = useShell()
  const { data, error, reload } = useCmd<Schedule[]>(ws, 'recurring.list')
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const [adding, setAdding] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  const [rows, setRows] = useState<Row[]>([{ key: 0, description: '', quantity: '1', price: '', rate: '18' }])
  const act = async (id: string, action: string) => {
    setBusy(true)
    try {
      await run(ws, `recurring.${action}`, { id })
      setErr(undefined)
      reload()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-4">
      <button
        type="button"
        className={btnPrimary}
        disabled={!spaces.length}
        onClick={() => {
          setErr(undefined)
          setRows([{ key: 0, description: '', quantity: '1', price: '', rate: '18' }])
          setAdding(true)
        }}
      >
        New recurring invoice
      </button>
      <ErrorText error={error ?? err} />
      {data?.length === 0 && (
        <p className="text-muted">No recurring invoices yet. Create one to bill a customer on a schedule.</p>
      )}
      {!!data?.length && (
        <table className="w-full text-left text-[14px]">
          <thead className="text-[12px] text-muted">
            <tr className="border-b border-line">
              <th className="py-2 font-medium">Customer</th>
              <th className="font-medium">Repeats</th>
              <th className="font-medium">Next invoice</th>
              <th className="font-medium">Status</th>
              <th className="text-right font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {data.map((s) => {
              const due = s.status === 'active' && s.next_run <= today
              return (
                <tr key={s.id} className="border-b border-line align-top">
                  <td className="py-2 font-medium">
                    {contacts.data?.find((c) => c.id === s.contact_id)?.name ?? 'Customer'}
                  </td>
                  <td>{cadence(s.frequency, s.interval)}</td>
                  <td>{s.status === 'ended' ? '' : s.next_run}</td>
                  <td>
                    <Pill>{s.status}</Pill>
                  </td>
                  <td className="space-x-2 py-2 text-right">
                    {s.last_invoice_id && (
                      <Link
                        className="text-muted hover:underline"
                        href={`/w/${ws}/billing/${s.last_invoice_id}`}
                      >
                        Open latest invoice
                      </Link>
                    )}
                    {s.status === 'active' && (
                      <>
                        <button
                          type="button"
                          className={btn}
                          disabled={busy}
                          onClick={() => act(s.id, 'pause')}
                        >
                          Pause
                        </button>
                        <button
                          type="button"
                          className={btn}
                          disabled={busy || !due}
                          title={due ? 'Creates the invoice for the due period' : `Available ${s.next_run}`}
                          onClick={() => act(s.id, 'run_now')}
                        >
                          Run now
                        </button>
                        {!due && (
                          <span className="block text-[12px] text-muted">Run now available {s.next_run}</span>
                        )}
                      </>
                    )}
                    {s.status === 'paused' && (
                      <button
                        type="button"
                        className={btn}
                        disabled={busy}
                        onClick={() => act(s.id, 'resume')}
                      >
                        Resume
                      </button>
                    )}
                    {s.status !== 'ended' && (
                      <button
                        type="button"
                        className={btn}
                        disabled={busy}
                        onClick={() => {
                          if (
                            window.confirm(
                              'End this schedule? No more invoices will be generated; existing invoices stay.',
                            )
                          )
                            act(s.id, 'end')
                        }}
                      >
                        End
                      </button>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {adding && (
        <Modal open title="New recurring invoice" onClose={() => setAdding(false)}>
          <form
            className="space-y-3"
            onSubmit={async (e) => {
              e.preventDefault()
              const f = new FormData(e.currentTarget)
              setBusy(true)
              try {
                const lines = rows.map((r) => ({
                  description: r.description,
                  quantity: Number(r.quantity),
                  unit_price_minor: parseMinor(r.price),
                  gst_rate_bp: Number(r.rate) * 100,
                }))
                if (
                  lines.some((l) => !l.description.trim() || !(l.quantity > 0) || l.unit_price_minor === null)
                )
                  throw new Error('Complete every line.')
                await run(ws, 'recurring.create', {
                  space_id: spaces[0].id,
                  contact_id: f.get('contact'),
                  frequency: f.get('frequency'),
                  interval: Number(f.get('interval')),
                  start_date: f.get('start'),
                  end_date: f.get('end') || undefined,
                  auto_issue: f.get('auto') === 'on',
                  lines,
                })
                setAdding(false)
                setErr(undefined)
                reload()
              } catch (x) {
                setErr((x as Error).message)
              } finally {
                setBusy(false)
              }
            }}
          >
            <Field label="Customer">
              {(id) => (
                <select id={id} name="contact" required className={inputCls}>
                  <option value="">Choose a contact</option>
                  {contacts.data?.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Frequency">
              {(id) => (
                <select id={id} name="frequency" className={inputCls}>
                  <option value="monthly">Monthly</option>
                  <option value="quarterly">Quarterly</option>
                  <option value="yearly">Yearly</option>
                </select>
              )}
            </Field>
            <Field label="Interval">
              {(id) => (
                <input
                  id={id}
                  name="interval"
                  type="number"
                  min="1"
                  max="120"
                  defaultValue="1"
                  required
                  className={inputCls}
                />
              )}
            </Field>
            <Field label="Start date">
              {(id) => <input id={id} name="start" type="date" required className={inputCls} />}
            </Field>
            <p className="text-[12px] text-muted">
              Runs on this day each period; a 31st start runs on the last day of shorter months.
            </p>
            <Field label="End date">
              {(id) => <input id={id} name="end" type="date" className={inputCls} />}
            </Field>
            {rows.map((r, i) => (
              <div key={r.key} className="grid grid-cols-2 gap-2">
                {(['description', 'quantity', 'price', 'rate'] as const).map((key) => (
                  <label key={key}>
                    {`Line ${i + 1} ${key}`}
                    <input
                      aria-label={`Line ${i + 1} ${key}`}
                      required
                      value={r[key]}
                      className={inputCls}
                      onChange={(e) =>
                        setRows((rs) => rs.map((v, n) => (n === i ? { ...v, [key]: e.target.value } : v)))
                      }
                    />
                  </label>
                ))}
                {rows.length > 1 && (
                  <button
                    type="button"
                    className={btn}
                    onClick={() => setRows((rs) => rs.filter((_, n) => n !== i))}
                  >
                    Remove line {i + 1}
                  </button>
                )}
              </div>
            ))}
            <button
              type="button"
              className={btn}
              onClick={() =>
                setRows((rs) => [
                  ...rs,
                  {
                    key: Math.max(...rs.map((r) => r.key)) + 1,
                    description: '',
                    quantity: '1',
                    price: '',
                    rate: '18',
                  },
                ])
              }
            >
              Add line
            </button>
            <label className="block">
              <input type="checkbox" name="auto" /> Automatically issue each invoice (posts to the ledger;
              otherwise drafts are created for review)
            </label>
            <ErrorText error={err} />
            <div className="flex gap-2">
              <button type="submit" disabled={busy} className={btnPrimary}>
                Create schedule
              </button>
              <button type="button" className={btn} onClick={() => setAdding(false)}>
                Cancel
              </button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  )
}
