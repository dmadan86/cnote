'use client'
import { useState } from 'react'
import { parseMinor, toRupees } from '@/components/billing/fmt'
import { btn, btnPrimary, ErrorText, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

type Rule = {
  command: string
  min_amount_minor: number
  approver_user_id: string | null
  approver_role: string | null
  fallback_user_id: string | null
  escalate_after_hours: number | null
  enabled: boolean
  steps: {
    mode: 'any' | 'all'
    min_amount_minor: number
    approvers: { kind: string; role?: string; user_id?: string }[]
  }[]
}
type StepForm = { mode: 'any' | 'all'; min: string; approvers: string[] }
const toCode = (a: { kind: string; role?: string; user_id?: string }) =>
  a.kind === 'user' ? (a.user_id ?? '') : a.kind === 'department_head' ? 'dept_head' : (a.role ?? 'admin')
const fromCode = (c: string) =>
  c === 'admin' || c === 'owner'
    ? { kind: 'role', role: c }
    : c === 'dept_head'
      ? { kind: 'department_head' }
      : { kind: 'user', user_id: c }
type Member = { user_id: string; name: string; role: string }

function RuleRow({
  ws,
  command,
  label,
  rule,
  people,
  saved,
}: {
  ws: string
  command: string
  label: string
  rule?: Rule
  people: Member[]
  saved: () => void
}) {
  const [on, setOn] = useState(rule?.enabled ?? false)
  const [amount, setAmount] = useState(rule ? toRupees(rule.min_amount_minor) : '')
  const [steps, setSteps] = useState<StepForm[]>(
    rule?.steps?.length
      ? rule.steps.map((st) => ({
          mode: st.mode,
          min: st.min_amount_minor ? toRupees(st.min_amount_minor) : '',
          approvers: st.approvers.map(toCode),
        }))
      : [{ mode: 'any', min: '', approvers: ['admin'] }],
  )
  const setStep = (i: number, f: (s: StepForm) => StepForm) =>
    setSteps((all) => all.map((x, j) => (j === i ? f(x) : x)))
  const [fallback, setFallback] = useState(rule?.fallback_user_id ?? '')
  const [hours, setHours] = useState(String(rule?.escalate_after_hours ?? 24))
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState('')
  const who = (c: string) =>
    c === 'admin'
      ? 'any admin or the owner'
      : c === 'owner'
        ? 'the owner'
        : c === 'dept_head'
          ? "the requester's department head"
          : (people.find((p) => p.user_id === c)?.name ?? 'someone')
  const summary = steps
    .map(
      (st) => who(st.approvers[0]) + (st.approvers.length > 1 ? ` and ${st.approvers.length - 1} more` : ''),
    )
    .join(', then ')
  return (
    <li className="space-y-3 py-4">
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          setErr(undefined)
          setMsg('')
          const min = amount.trim() === '' ? 0 : parseMinor(amount)
          const h = Number(hours)
          if (min === null) return setErr('Enter the amount in rupees, for example 100000.')
          if (fallback && !(h >= 1 && h <= 168)) return setErr('Escalate after 1 to 168 hours.')
          const stepsOut = steps.map((st) => ({
            mode: st.mode,
            min_amount_minor: st.min.trim() === '' ? 0 : parseMinor(st.min),
            approvers: st.approvers.map(fromCode),
          }))
          if (stepsOut.some((st) => st.min_amount_minor === null))
            return setErr('Enter each step amount in rupees, for example 100000.')
          try {
            await cmd(ws, 'approval.rule_set', {
              command,
              enabled: on,
              min_amount_minor: min,
              steps: stepsOut,
              ...(fallback ? { fallback_user_id: fallback, escalate_after_hours: h } : {}),
            })
            setMsg('Saved')
            saved()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <div className="flex items-center justify-between gap-3">
          <label className="flex items-center gap-2 text-[14px] font-medium">
            <input
              type="checkbox"
              checked={on}
              onChange={(e) => setOn(e.target.checked)}
              aria-label={`Require approval to ${label.toLowerCase()}`}
            />
            {label}
          </label>
          <span className="text-[12px] text-muted">
            {on
              ? `Needs approval from ${summary} at or above ₹${amount || '0'}. Below it goes straight through. The owner is never held.`
              : 'Off: anyone allowed can do this directly.'}
          </span>
        </div>
        {on && (
          <div className="grid gap-3 sm:grid-cols-3">
            <label className="block space-y-1">
              <span className="text-[12px] font-medium text-muted">From amount (₹, 0 = always)</span>
              <input
                className={inputCls}
                inputMode="decimal"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder="100000"
              />
            </label>
            <label className="block space-y-1">
              <span className="text-[12px] font-medium text-muted">If no answer, ask</span>
              <select className={inputCls} value={fallback} onChange={(e) => setFallback(e.target.value)}>
                <option value="">Nobody</option>
                {people
                  .filter((p) => p.role !== 'guest')
                  .map((p) => (
                    <option key={p.user_id} value={p.user_id}>
                      {p.name}
                    </option>
                  ))}
              </select>
            </label>
            <label className="block space-y-1">
              <span className="text-[12px] font-medium text-muted">after (hours)</span>
              <input
                className={inputCls}
                inputMode="numeric"
                value={hours}
                disabled={!fallback}
                onChange={(e) => setHours(e.target.value)}
              />
            </label>
          </div>
        )}
        {on && (
          <fieldset className="space-y-3">
            <legend className="text-[12px] font-medium text-muted">
              Approval steps, in order. A step is skipped when the amount is below its own limit.
            </legend>
            {steps.map((st, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: steps are positional
              <div key={i} className="space-y-2 rounded-md border border-line p-3">
                <div className="flex items-center justify-between">
                  <span className="text-[13px] font-medium">Step {i + 1}</span>
                  {steps.length > 1 && (
                    <button
                      type="button"
                      className="text-[12px] underline"
                      onClick={() => setSteps((all) => all.filter((_, j) => j !== i))}
                    >
                      Remove step {i + 1}
                    </button>
                  )}
                </div>
                {st.approvers.map((c, k) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: approvers are positional
                  <div key={k} className="flex items-center gap-2">
                    <select
                      aria-label={`Step ${i + 1} approver ${k + 1}`}
                      className={inputCls}
                      value={c}
                      onChange={(e) =>
                        setStep(i, (x) => ({
                          ...x,
                          approvers: x.approvers.map((y, j) => (j === k ? e.target.value : y)),
                        }))
                      }
                    >
                      <option value="admin">Any admin</option>
                      <option value="owner">Owner</option>
                      <option value="dept_head">Requester's department head</option>
                      {people
                        .filter((p) => p.role !== 'guest')
                        .map((p) => (
                          <option key={p.user_id} value={p.user_id}>
                            {p.name}
                          </option>
                        ))}
                    </select>
                    {st.approvers.length > 1 && (
                      <button
                        type="button"
                        className="text-[12px] underline"
                        aria-label={`Remove approver ${k + 1} from step ${i + 1}`}
                        onClick={() =>
                          setStep(i, (x) => ({ ...x, approvers: x.approvers.filter((_, j) => j !== k) }))
                        }
                      >
                        Remove
                      </button>
                    )}
                  </div>
                ))}
                <div className="flex flex-wrap items-end gap-3">
                  {st.approvers.length < 5 && (
                    <button
                      type="button"
                      className="text-[13px] underline"
                      onClick={() => setStep(i, (x) => ({ ...x, approvers: [...x.approvers, 'admin'] }))}
                    >
                      Add approver
                    </button>
                  )}
                  {st.approvers.length > 1 && (
                    <label className="block space-y-1">
                      <span className="text-[12px] font-medium text-muted">Needs</span>
                      <select
                        className={inputCls}
                        value={st.mode}
                        onChange={(e) => setStep(i, (x) => ({ ...x, mode: e.target.value as 'any' | 'all' }))}
                      >
                        <option value="any">Any one of them</option>
                        <option value="all">Every one of them</option>
                      </select>
                    </label>
                  )}
                  <label className="block space-y-1">
                    <span className="text-[12px] font-medium text-muted">Required from (₹, 0 = always)</span>
                    <input
                      className={inputCls}
                      inputMode="decimal"
                      value={st.min}
                      onChange={(e) => setStep(i, (x) => ({ ...x, min: e.target.value }))}
                      placeholder="0"
                    />
                  </label>
                </div>
              </div>
            ))}
            {steps.length < 5 && (
              <button
                type="button"
                className={btn}
                onClick={() => setSteps((all) => [...all, { mode: 'any', min: '', approvers: ['owner'] }])}
              >
                Add step
              </button>
            )}
            <p className="text-[12px] text-muted">
              Nobody approves their own request, and a person fills a step once. A missing department head
              falls back to the admins.
            </p>
          </fieldset>
        )}
        <div className="flex items-center gap-3">
          <button type="submit" className={btnPrimary}>
            Save
          </button>
          <span role="status" className="text-[13px] text-muted">
            {msg}
          </span>
        </div>
        <ErrorText error={err} />
      </form>
    </li>
  )
}

export function ApprovalRules({ ws }: { ws: string }) {
  const rules = useCmd<{ rules: Rule[]; commands: { command: string; label: string }[] }>(
    ws,
    'approval.rule_list',
  )
  const members = useCmd<Member[]>(ws, 'member.list')
  if (rules.error) return <ErrorText error={rules.error} />
  if (!rules.data || !members.data)
    return <div aria-busy="true" className="h-24 animate-pulse rounded-lg bg-hover" />
  return (
    <div className="space-y-3">
      <p className="text-[13px] text-muted">
        Hold an action until a named person approves it. Amounts are compared before tax for drafts. Tip:
        GSTR-1 corrections are painful, so consider approval on issuing invoices above an amount you choose.
      </p>
      <ul className="divide-y divide-line">
        {rules.data.commands.map((c) => (
          <RuleRow
            key={c.command}
            ws={ws}
            command={c.command}
            label={c.label}
            rule={rules.data?.rules.find((r) => r.command === c.command)}
            people={members.data ?? []}
            saved={rules.reload}
          />
        ))}
      </ul>
    </div>
  )
}
