'use client'
import { useState } from 'react'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { isAdminRole, PeopleTabs, useRole } from './shared'

type Balance = {
  leave_type_id: string
  name: string
  yearly_quota: number | null
  used: number
  pending: number
  remaining: number | null
}
type LeaveType = { id: string; name: string; yearly_quota: number | null; paid: boolean; active: boolean }
type Req = {
  id: string
  employee: string
  leave_type: string
  from_day: string
  to_day: string
  days: number
  half_day: boolean
  status: string
  reason: string | null
  decision_note: string | null
  can_decide: boolean
  can_cancel: boolean
}
type Holiday = { id: string; day: string; name: string; kind: string }

const tone = (s: string) =>
  s === 'approved' ? 'ok' : s === 'pending' ? 'warn' : s === 'rejected' ? 'bad' : 'neutral'
const span = (r: Req) => (r.from_day === r.to_day ? r.from_day : `${r.from_day} to ${r.to_day}`)

export function LeavePage() {
  const { ws, role } = useRole()
  const admin = isAdminRole(role)
  const bal = useCmd<Balance[]>(ws, 'leave.balance')
  const types = useCmd<LeaveType[]>(ws, 'leave_type.list')
  const reqs = useCmd<Req[]>(ws, 'leave.list')
  const [open, setOpen] = useState(false)
  const [err, setErr] = useState<string>()
  const [rejecting, setRejecting] = useState<string>()
  const reload = () => {
    bal.reload()
    reqs.reload()
  }
  const act = async (name: string, input: object) => {
    setErr(undefined)
    try {
      await cmd(ws, name, input)
      setRejecting(undefined)
      reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const decisions = (reqs.data ?? []).filter((r) => r.can_decide)
  return (
    <div className="mx-auto max-w-4xl space-y-8 px-4 py-8 sm:px-6">
      <h1 className="text-xl font-semibold">People</h1>
      <PeopleTabs current="leave" />
      <ErrorText error={err ?? reqs.error ?? bal.error ?? types.error} />
      <section aria-labelledby="balances" className="space-y-3">
        <div className="flex items-center justify-between gap-3">
          <h2 id="balances" className="text-[15px] font-semibold">
            My leave this year
          </h2>
          <button type="button" className={btnPrimary} onClick={() => setOpen(true)}>
            Request leave
          </button>
        </div>
        <ul className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          {(bal.data ?? []).map((b) => (
            <li key={b.leave_type_id} className="rounded-lg border border-line p-3">
              <p className="text-[13px] font-medium">{b.name}</p>
              <p className="text-lg font-semibold tabular-nums">
                {b.remaining == null ? 'No limit' : `${b.remaining} left`}
              </p>
              <p className="text-[12px] text-muted">
                {b.yearly_quota != null && `${b.yearly_quota} a year, `}
                {b.used} used, {b.pending} pending
              </p>
            </li>
          ))}
        </ul>
        <p className="text-[12px] text-muted">
          Pending requests already reduce what is left. Weekly offs and holidays are not counted as leave
          days.
        </p>
      </section>

      {decisions.length > 0 && (
        <section aria-labelledby="decide" className="space-y-2">
          <h2 id="decide" className="text-[15px] font-semibold">
            Needs your decision
          </h2>
          <ul className="divide-y divide-line rounded-md border border-line">
            {decisions.map((r) => (
              <li key={r.id} className="space-y-2 px-3 py-2">
                <p>
                  <span className="font-medium">{r.employee}</span>, {r.leave_type}, {span(r)} ({r.days}{' '}
                  {r.days === 1 ? 'day' : 'days'})
                </p>
                {r.reason && <p className="text-[12px] text-muted">{r.reason}</p>}
                {rejecting === r.id ? (
                  <form
                    className="flex flex-wrap items-end gap-2"
                    onSubmit={(e) => {
                      e.preventDefault()
                      act('leave.decide', {
                        id: r.id,
                        decision: 'reject',
                        reason: new FormData(e.currentTarget).get('reason'),
                      })
                    }}
                  >
                    <div className="min-w-48 flex-1">
                      <Field label="Reason for rejecting">
                        {(id) => (
                          <input id={id} name="reason" required maxLength={500} className={inputCls} />
                        )}
                      </Field>
                    </div>
                    <button type="submit" className={btn}>
                      Confirm rejection
                    </button>
                    <button type="button" className={btn} onClick={() => setRejecting(undefined)}>
                      Back
                    </button>
                  </form>
                ) : (
                  <div className="flex gap-2">
                    <button
                      type="button"
                      className={btnPrimary}
                      aria-label={`Approve ${r.employee}`}
                      onClick={() => act('leave.decide', { id: r.id, decision: 'approve' })}
                    >
                      Approve
                    </button>
                    <button
                      type="button"
                      className={btn}
                      aria-label={`Reject ${r.employee}`}
                      onClick={() => setRejecting(r.id)}
                    >
                      Reject
                    </button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      <section aria-labelledby="requests" className="space-y-2">
        <h2 id="requests" className="text-[15px] font-semibold">
          Requests
        </h2>
        <ul aria-label="Leave requests" className="divide-y divide-line rounded-md border border-line">
          {(reqs.data ?? []).map((r) => (
            <li key={r.id} className="flex flex-wrap items-center gap-3 px-3 py-2">
              <div className="min-w-0 flex-1">
                <p>
                  <span className="font-medium">{r.employee}</span>, {r.leave_type}
                </p>
                <p className="text-[12px] text-muted">
                  {span(r)} · {r.days} {r.days === 1 ? 'day' : 'days'}
                  {r.half_day ? ' (half day)' : ''}
                  {r.decision_note ? ` · ${r.decision_note}` : ''}
                </p>
              </div>
              <Pill tone={tone(r.status)}>{r.status}</Pill>
              {r.can_cancel && (
                <button
                  type="button"
                  className={btn}
                  aria-label={`Cancel leave ${span(r)}`}
                  onClick={() => act('leave.cancel', { id: r.id })}
                >
                  Cancel
                </button>
              )}
            </li>
          ))}
          {reqs.data && reqs.data.length === 0 && (
            <li className="px-3 py-4 text-[13px] text-muted">No leave requests.</li>
          )}
        </ul>
      </section>

      {admin && <Setup ws={ws} types={types.data ?? []} onChange={types.reload} />}
      {open && (
        <RequestSheet
          ws={ws}
          types={(types.data ?? []).filter((t) => t.active)}
          onClose={() => setOpen(false)}
          onSaved={() => {
            setOpen(false)
            reload()
          }}
        />
      )}
    </div>
  )
}

function RequestSheet({
  ws,
  types,
  onClose,
  onSaved,
}: {
  ws: string
  types: LeaveType[]
  onClose: () => void
  onSaved: () => void
}) {
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  return (
    <Modal open onClose={onClose} title="Request leave">
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          const t = (k: string) => String(f.get(k) ?? '').trim()
          setBusy(true)
          setErr(undefined)
          try {
            await cmd(ws, 'leave.request', {
              leave_type_id: t('type'),
              from_day: t('from'),
              to_day: t('to') || t('from'),
              half_day: f.get('half') === 'on',
              reason: t('reason') || undefined,
            })
            onSaved()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <Field label="Leave type">
          {(id) => (
            <select id={id} name="type" required className={inputCls}>
              {types.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="From">
            {(id) => <input id={id} name="from" type="date" required className={inputCls} />}
          </Field>
          <Field label="To (blank for one day)">
            {(id) => <input id={id} name="to" type="date" className={inputCls} />}
          </Field>
        </div>
        <label className="flex items-center gap-2 text-[13px]">
          <input name="half" type="checkbox" /> Half day (one day only)
        </label>
        <Field label="Reason">
          {(id) => <input id={id} name="reason" maxLength={500} className={inputCls} />}
        </Field>
        <p className="text-[12px] text-muted">
          Working days are counted for you: weekly offs and holidays are left out.
        </p>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy}>
            Send request
          </button>
        </div>
      </form>
    </Modal>
  )
}

function Setup({ ws, types, onChange }: { ws: string; types: LeaveType[]; onChange: () => void }) {
  const year = new Date().getFullYear()
  const hol = useCmd<Holiday[]>(ws, 'holiday.list', { year })
  const [err, setErr] = useState<string>()
  const run = async (name: string, input: object, after: () => void) => {
    setErr(undefined)
    try {
      await cmd(ws, name, input)
      after()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <section aria-labelledby="setup" className="space-y-6">
      <h2 id="setup" className="text-[15px] font-semibold">
        Leave types and holidays
      </h2>
      <p className="text-[12px] text-muted">
        Starting values only, not legal advice: state Shops and Establishments rules differ, so check yours
        and edit freely.
      </p>
      <ErrorText error={err} />
      <div className="space-y-2">
        <h3 className="text-[13px] font-semibold text-muted">Leave types</h3>
        <ul aria-label="Leave types" className="divide-y divide-line rounded-md border border-line">
          {types.map((t) => (
            <li key={t.id} className="flex flex-wrap items-center gap-3 px-3 py-2">
              <span className="min-w-0 flex-1 font-medium">{t.name}</span>
              <span className="text-[13px] text-muted">
                {t.yearly_quota == null ? 'No limit' : `${t.yearly_quota} a year`}
              </span>
              {!t.active && <Pill>Off</Pill>}
              <button
                type="button"
                className={btn}
                onClick={() => run('leave_type.set', { id: t.id, active: !t.active }, onChange)}
              >
                {t.active ? 'Switch off' : 'Switch on'}
              </button>
            </li>
          ))}
        </ul>
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            const form = e.currentTarget
            const f = new FormData(form)
            const q = String(f.get('quota') ?? '').trim()
            run('leave_type.set', { name: f.get('name'), yearly_quota: q === '' ? null : Number(q) }, () => {
              form.reset()
              onChange()
            })
          }}
        >
          <Field label="New leave type">
            {(id) => <input id={id} name="name" required maxLength={60} className={inputCls} />}
          </Field>
          <Field label="Days a year (blank = no limit)">
            {(id) => (
              <input id={id} name="quota" type="number" min="0" max="366" step="0.5" className={inputCls} />
            )}
          </Field>
          <button type="submit" className={btn}>
            Add
          </button>
        </form>
      </div>
      <div className="space-y-2">
        <h3 className="text-[13px] font-semibold text-muted">Holidays {year}</h3>
        <ul aria-label="Holidays" className="divide-y divide-line rounded-md border border-line">
          {(hol.data ?? []).map((h) => (
            <li key={h.id} className="flex flex-wrap items-center gap-3 px-3 py-2">
              <span className="w-28 tabular-nums text-[13px]">{h.day}</span>
              <span className="min-w-0 flex-1">{h.name}</span>
              <Pill>{h.kind}</Pill>
              <button
                type="button"
                className={btn}
                aria-label={`Remove ${h.name}`}
                onClick={() => run('holiday.delete', { id: h.id }, hol.reload)}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
        <p className="text-[12px] text-muted">
          The three national holidays are added for you; add the festival holidays your state and business
          use.
        </p>
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            const form = e.currentTarget
            const f = new FormData(form)
            run('holiday.set', { day: f.get('day'), name: f.get('name'), kind: 'festival' }, () => {
              form.reset()
              hol.reload()
            })
          }}
        >
          <Field label="Date">
            {(id) => <input id={id} name="day" type="date" required className={inputCls} />}
          </Field>
          <Field label="Holiday name">
            {(id) => <input id={id} name="name" required maxLength={80} className={inputCls} />}
          </Field>
          <button type="submit" className={btn}>
            Add holiday
          </button>
        </form>
      </div>
    </section>
  )
}
