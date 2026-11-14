'use client'
import { useState } from 'react'
import { btn, ErrorText, Field, inputCls, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { isAdminRole, PeopleTabs, timeOf, useRole } from './shared'

type Person = {
  employee_id: string
  name: string
  code: string | null
  status: string
  in_at: string | null
  out_at: string | null
}
type Board = {
  day: string
  counts: { expected: number; present: number; on_leave: number; absent: number; not_marked: number }
  people: Person[]
}
type Register = {
  days_in_month: number
  legend: string
  csv: string
  filename: string
  rows: { employee_id: string; name: string; code: string; days: string[]; totals: Record<string, number> }[]
}

const label: Record<string, string> = {
  present: 'Present',
  half_day: 'Half day',
  leave: 'On leave',
  absent: 'Absent',
  not_marked: 'Not marked',
  holiday: 'Holiday',
  week_off: 'Weekly off',
}
const tone = (s: string) =>
  s === 'present'
    ? 'ok'
    : s === 'absent'
      ? 'bad'
      : s === 'not_marked' || s === 'half_day'
        ? 'warn'
        : 'neutral'
const filters = [
  ['all', 'Everyone'],
  ['present', 'Present'],
  ['leave', 'On leave'],
  ['absent', 'Absent'],
  ['not_marked', 'Not marked'],
] as const

export function Attendance() {
  const { ws, role } = useRole()
  const admin = isAdminRole(role)
  const board = useCmd<Board>(ws, 'attendance.today')
  const [f, setF] = useState<string>('all')
  const [err, setErr] = useState<string>()
  const c = board.data?.counts
  const count = (k: string) =>
    k === 'all'
      ? (c?.expected ?? 0)
      : k === 'present'
        ? (c?.present ?? 0)
        : k === 'leave'
          ? (c?.on_leave ?? 0)
          : k === 'absent'
            ? (c?.absent ?? 0)
            : (c?.not_marked ?? 0)
  const rows = (board.data?.people ?? []).filter(
    (p) => f === 'all' || p.status === f || (f === 'present' && p.status === 'half_day'),
  )
  const mark = async (employee_id: string, status: string) => {
    setErr(undefined)
    try {
      await cmd(ws, 'attendance.mark', { employee_id, day: board.data?.day, status })
      board.reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <div className="mx-auto max-w-4xl space-y-6 px-4 py-8 sm:px-6">
      <h1 className="text-xl font-semibold">People</h1>
      <PeopleTabs current="attendance" />
      <section aria-labelledby="today" className="space-y-3">
        <h2 id="today" className="text-[15px] font-semibold">
          Today{board.data ? `, ${board.data.day}` : ''}
        </h2>
        <p className="text-[12px] text-muted">
          Not marked means nobody has recorded the day yet; Absent is set by an admin. People on a holiday or
          weekly off are not counted.
        </p>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
          {filters.map(([k, name]) => (
            <button
              key={k}
              type="button"
              aria-pressed={f === k}
              onClick={() => setF(k)}
              className="min-h-11 rounded-md border border-line px-3 py-2 text-left hover:bg-hover aria-pressed:border-accent aria-pressed:bg-hover"
            >
              <span className="block text-[12px] text-muted">{name}</span>
              <span className="block text-lg font-semibold tabular-nums">{count(k)}</span>
            </button>
          ))}
        </div>
        <ErrorText error={err ?? board.error} />
        <ul aria-label="Attendance today" className="divide-y divide-line rounded-md border border-line">
          {rows.map((p) => (
            <li key={p.employee_id} className="flex flex-wrap items-center gap-3 px-3 py-2">
              <div className="min-w-0 flex-1">
                <span className="font-medium">{p.name}</span>
                {p.code && <span className="ml-2 text-[12px] text-muted">{p.code}</span>}
                {p.in_at && (
                  <p className="text-[12px] text-muted">
                    In {timeOf(p.in_at)}
                    {p.out_at ? `, out ${timeOf(p.out_at)}` : ''}
                  </p>
                )}
              </div>
              <Pill tone={tone(p.status)}>{label[p.status] ?? p.status}</Pill>
              {admin && p.status !== 'leave' && (
                <select
                  aria-label={`Mark ${p.name}`}
                  value=""
                  onChange={(e) => e.target.value && mark(p.employee_id, e.target.value)}
                  className={`${inputCls} !w-auto`}
                >
                  <option value="">Mark...</option>
                  <option value="present">Present</option>
                  <option value="half_day">Half day</option>
                  <option value="absent">Absent</option>
                </select>
              )}
            </li>
          ))}
          {board.data && rows.length === 0 && (
            <li className="px-3 py-4 text-[13px] text-muted">No one here.</li>
          )}
        </ul>
      </section>
      {admin && <MonthRegister ws={ws} />}
    </div>
  )
}

function MonthRegister({ ws }: { ws: string }) {
  const [month, setMonth] = useState(new Date().toISOString().slice(0, 7))
  const reg = useCmd<Register>(ws, 'attendance.register', { month })
  const download = () => {
    if (!reg.data) return
    const url = URL.createObjectURL(new Blob([reg.data.csv], { type: 'text/csv' }))
    const a = document.createElement('a')
    a.href = url
    a.download = reg.data.filename
    a.click()
    URL.revokeObjectURL(url)
  }
  return (
    <section aria-labelledby="register" className="space-y-3">
      <h2 id="register" className="text-[15px] font-semibold">
        Monthly register
      </h2>
      <div className="flex flex-wrap items-end gap-3">
        <Field label="Month">
          {(id) => (
            <input
              id={id}
              type="month"
              value={month}
              onChange={(e) => e.target.value && setMonth(e.target.value)}
              className={inputCls}
            />
          )}
        </Field>
        <button type="button" className={btn} disabled={!reg.data} onClick={download}>
          Download {month} CSV
        </button>
      </div>
      <ErrorText error={reg.error} />
      {reg.data && (
        <>
          <p className="text-[12px] text-muted">
            {reg.data.legend}. For your payroll tool or CA; this is not a payslip.
          </p>
          {/* biome-ignore lint/a11y/useSemanticElements: a focusable scroll region lets keyboard users scroll the wide grid */}
          <div
            role="region"
            aria-label="Attendance register"
            // biome-ignore lint/a11y/noNoninteractiveTabindex: same scroll region
            tabIndex={0}
            className="overflow-x-auto rounded-md border border-line"
          >
            <table className="min-w-full text-[12px]">
              <thead>
                <tr className="border-b border-line text-left text-muted">
                  <th className="sticky left-0 bg-canvas px-2 py-1">Name</th>
                  {reg.data.rows[0]?.days.map((_, i) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: fixed grid of days
                    <th key={i} className="px-1 py-1 text-center font-normal">
                      {i + 1}
                    </th>
                  ))}
                  <th className="px-2 py-1">Present</th>
                  <th className="px-2 py-1">Leave</th>
                  <th className="px-2 py-1">Absent</th>
                </tr>
              </thead>
              <tbody>
                {reg.data.rows.map((r) => (
                  <tr key={r.employee_id} className="border-b border-line last:border-0">
                    <th
                      scope="row"
                      className="sticky left-0 whitespace-nowrap bg-canvas px-2 py-1 text-left font-medium"
                    >
                      {r.name}
                    </th>
                    {r.days.map((d, i) => (
                      // biome-ignore lint/suspicious/noArrayIndexKey: fixed grid of days
                      <td key={i} className="px-1 py-1 text-center">
                        {d}
                      </td>
                    ))}
                    <td className="px-2 py-1 tabular-nums">{r.totals.present + r.totals.half_day / 2}</td>
                    <td className="px-2 py-1 tabular-nums">{r.totals.leave}</td>
                    <td className="px-2 py-1 tabular-nums">{r.totals.absent}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  )
}
