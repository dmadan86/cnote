'use client'
import { ChevronRight, Info, Pencil, Plus, Trash2, X } from 'lucide-react'
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
  const [open, setOpen] = useState(false)
  const people2 = people.filter((p) => p.role !== 'guest')
  const approverOptions = (
    <>
      <option value="admin">Any admin</option>
      <option value="owner">Owner</option>
      <option value="dept_head">Requester's department head</option>
      {people2.map((p) => (
        <option key={p.user_id} value={p.user_id}>
          {p.name}
        </option>
      ))}
    </>
  )
  const sized = inputCls.replace('w-full ', '')
  const short = (c: string) =>
    c === 'admin' ? 'Any admin' : c === 'owner' ? 'Owner' : c === 'dept_head' ? 'Department head' : who(c)
  const rupees = (v: string) => (Number(v) ? `₹${Number(v).toLocaleString('en-IN')}` : '₹0')
  const chip = 'inline-flex items-center rounded-full border border-line bg-hover px-2 py-0.5 text-[12px]'
  return (
    <li className="rounded-lg border border-line">
      <form
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
            setOpen(false)
            saved()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <div className="flex items-start gap-3 p-4">
          <input
            type="checkbox"
            role="switch"
            aria-checked={on}
            checked={on}
            onChange={(e) => {
              setOn(e.target.checked)
              setOpen(true)
              setMsg('')
            }}
            aria-label={`Require approval to ${label.toLowerCase()}`}
            className="relative mt-0.5 h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full bg-line transition-colors checked:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent after:absolute after:top-0.5 after:left-0.5 after:h-4 after:w-4 after:rounded-full after:bg-canvas after:shadow-sm after:transition-transform checked:after:translate-x-4"
          />
          <div className="min-w-0 flex-1">
            <div className="text-[14px] font-medium">{label}</div>
            {on ? (
              <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-[12px] text-muted">
                <span>When amount ≥</span>
                <span className={chip}>{rupees(amount)}</span>
                {steps.map((st, i) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: steps are positional
                  <span key={i} className="inline-flex items-center gap-1.5">
                    <ChevronRight size={13} aria-hidden />
                    <span className={chip}>
                      {short(st.approvers[0])}
                      {st.approvers.length > 1
                        ? ` ${st.mode === 'all' ? 'and' : 'or'} ${st.approvers.length - 1} more`
                        : ''}
                    </span>
                  </span>
                ))}
              </div>
            ) : (
              <p className="mt-1 text-[12px] text-muted">Off · anyone allowed can do this directly.</p>
            )}
          </div>
          {!open && (
            <button type="button" className={btn} onClick={() => setOpen(true)}>
              <Pencil size={13} aria-hidden /> Edit
            </button>
          )}
          {msg && !open && (
            <span role="status" className="self-center text-[12px] text-muted">
              {msg}
            </span>
          )}
        </div>

        {open && (
          <div className="space-y-5 border-t border-line bg-side/60 p-4">
            {on && (
              <>
                <section className="space-y-2">
                  <h4 className="text-[11px] font-medium uppercase tracking-wide text-muted">When</h4>
                  <label className="flex flex-wrap items-center gap-2 text-[13px]">
                    <span>The amount is at least</span>
                    <span className="relative">
                      <span className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted">
                        ₹
                      </span>
                      <input
                        aria-label="From amount (₹, 0 = always)"
                        className={`${sized} w-40 pl-6`}
                        inputMode="decimal"
                        value={amount}
                        onChange={(e) => setAmount(e.target.value)}
                        placeholder="0"
                      />
                    </span>
                    <span className="text-[12px] text-muted">
                      0 means always. Below it goes straight through.
                    </span>
                  </label>
                </section>

                <section className="space-y-2">
                  <h4 className="text-[11px] font-medium uppercase tracking-wide text-muted">
                    Then ask, in order
                  </h4>
                  <ol className="space-y-0">
                    {steps.map((st, i) => (
                      // biome-ignore lint/suspicious/noArrayIndexKey: steps are positional
                      <li key={i} className="relative flex gap-3 pb-3">
                        {i < steps.length - 1 && (
                          <span aria-hidden className="absolute top-7 bottom-0 left-3 w-px bg-line" />
                        )}
                        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent text-[12px] font-medium text-accent-fg">
                          {i + 1}
                        </span>
                        <div className="min-w-0 flex-1 space-y-2 rounded-md border border-line bg-canvas p-3">
                          <div className="flex items-center justify-between">
                            <span className="text-[13px] font-medium">Step {i + 1}</span>
                            {steps.length > 1 && (
                              <button
                                type="button"
                                aria-label={`Remove step ${i + 1}`}
                                title="Remove step"
                                className="rounded p-1 text-muted hover:bg-hover hover:text-fg"
                                onClick={() => setSteps((all) => all.filter((_, j) => j !== i))}
                              >
                                <Trash2 size={14} aria-hidden />
                              </button>
                            )}
                          </div>
                          {st.approvers.map((c, k) => (
                            // biome-ignore lint/suspicious/noArrayIndexKey: approvers are positional
                            <div key={k} className="flex items-center gap-2">
                              {k > 0 && (
                                <span className="w-10 shrink-0 text-center text-[12px] text-muted">
                                  {st.mode === 'all' ? 'and' : 'or'}
                                </span>
                              )}
                              <select
                                aria-label={`Step ${i + 1} approver ${k + 1}`}
                                className={`${sized} w-full max-w-xs`}
                                value={c}
                                onChange={(e) =>
                                  setStep(i, (x) => ({
                                    ...x,
                                    approvers: x.approvers.map((y, j) => (j === k ? e.target.value : y)),
                                  }))
                                }
                              >
                                {approverOptions}
                              </select>
                              {st.approvers.length > 1 && (
                                <button
                                  type="button"
                                  aria-label={`Remove approver ${k + 1} from step ${i + 1}`}
                                  className="rounded p-1 text-muted hover:bg-hover hover:text-fg"
                                  onClick={() =>
                                    setStep(i, (x) => ({
                                      ...x,
                                      approvers: x.approvers.filter((_, j) => j !== k),
                                    }))
                                  }
                                >
                                  <X size={14} aria-hidden />
                                </button>
                              )}
                            </div>
                          ))}
                          <div className="flex flex-wrap items-center gap-3 pt-1">
                            {st.approvers.length < 5 && (
                              <button
                                type="button"
                                className="inline-flex items-center gap-1 text-[12px] font-medium text-accent hover:underline"
                                onClick={() =>
                                  setStep(i, (x) => ({ ...x, approvers: [...x.approvers, 'admin'] }))
                                }
                              >
                                <Plus size={13} aria-hidden /> Add approver
                              </button>
                            )}
                            {st.approvers.length > 1 && (
                              <fieldset
                                aria-label={`Step ${i + 1} needs`}
                                className="inline-flex rounded-md border border-line text-[12px]"
                              >
                                {(['any', 'all'] as const).map((m) => (
                                  <button
                                    key={m}
                                    type="button"
                                    aria-pressed={st.mode === m}
                                    className="px-2 py-1 first:rounded-l-md last:rounded-r-md aria-pressed:bg-hover aria-pressed:font-medium"
                                    onClick={() => setStep(i, (x) => ({ ...x, mode: m }))}
                                  >
                                    {m === 'any' ? 'Any one approves' : 'Everyone approves'}
                                  </button>
                                ))}
                              </fieldset>
                            )}
                            <label className="ml-auto flex items-center gap-2 whitespace-nowrap text-[12px] text-muted">
                              Only from ₹
                              <input
                                aria-label={`Step ${i + 1} required from (₹, 0 = always)`}
                                className={`${sized} w-28`}
                                inputMode="decimal"
                                value={st.min}
                                onChange={(e) => setStep(i, (x) => ({ ...x, min: e.target.value }))}
                                placeholder="0"
                              />
                            </label>
                          </div>
                        </div>
                      </li>
                    ))}
                  </ol>
                  {steps.length < 5 && (
                    <button
                      type="button"
                      className={`${btn} ml-9`}
                      onClick={() =>
                        setSteps((all) => [...all, { mode: 'any', min: '', approvers: ['owner'] }])
                      }
                    >
                      <Plus size={13} aria-hidden /> Add step
                    </button>
                  )}
                </section>

                <section className="space-y-2">
                  <h4 className="text-[11px] font-medium uppercase tracking-wide text-muted">
                    If no one answers
                  </h4>
                  <div className="flex flex-wrap items-center gap-2 text-[13px]">
                    <span>Ask</span>
                    <select
                      aria-label="If no answer, ask"
                      className={`${sized} w-auto`}
                      value={fallback}
                      onChange={(e) => setFallback(e.target.value)}
                    >
                      <option value="">nobody else</option>
                      {people2.map((p) => (
                        <option key={p.user_id} value={p.user_id}>
                          {p.name}
                        </option>
                      ))}
                    </select>
                    <span>after</span>
                    <input
                      aria-label="after (hours)"
                      className={`${sized} w-20`}
                      inputMode="numeric"
                      value={hours}
                      disabled={!fallback}
                      onChange={(e) => setHours(e.target.value)}
                    />
                    <span>hours</span>
                  </div>
                </section>

                <p className="flex gap-2 text-[12px] text-muted">
                  <Info size={14} aria-hidden className="mt-px shrink-0" />
                  Nobody approves their own request, and a person fills a step once. A missing department head
                  falls back to the admins. The owner is never held.
                </p>
              </>
            )}
            <div className="flex items-center gap-2">
              <button type="submit" className={btnPrimary}>
                Save
              </button>
              <button
                type="button"
                className={btn}
                onClick={() => {
                  setOpen(false)
                  setErr(undefined)
                }}
              >
                Close
              </button>
              <span role="status" className="text-[13px] text-muted">
                {msg}
              </span>
            </div>
            <ErrorText error={err} />
          </div>
        )}
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
    <div className="space-y-4">
      <div className="flex gap-2 rounded-md border border-line bg-hover/50 p-3 text-[13px] text-muted">
        <Info size={15} aria-hidden className="mt-0.5 shrink-0" />
        <p>
          Hold an action until the right person approves it. Amounts are compared before tax for drafts. Tip:
          GSTR-1 corrections are painful, so consider approval on issuing invoices above an amount you choose.
        </p>
      </div>
      <ul className="space-y-3">
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
