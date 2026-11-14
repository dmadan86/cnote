'use client'
import { useState } from 'react'
import { parseMinor, toRupees } from '@/components/billing/fmt'
import { btnPrimary, ErrorText, inputCls } from '@/components/ui'
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
}
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
  const [approver, setApprover] = useState(rule?.approver_user_id ?? rule?.approver_role ?? 'admin')
  const [fallback, setFallback] = useState(rule?.fallback_user_id ?? '')
  const [hours, setHours] = useState(String(rule?.escalate_after_hours ?? 24))
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState('')
  const decider =
    approver === 'admin'
      ? 'any admin or the owner'
      : approver === 'owner'
        ? 'the owner'
        : people.find((p) => p.user_id === approver)?.name
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
          try {
            await cmd(ws, 'approval.rule_set', {
              command,
              enabled: on,
              min_amount_minor: min,
              ...(approver === 'admin' || approver === 'owner'
                ? { approver_role: approver }
                : { approver_user_id: approver }),
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
              ? `Needs approval from ${decider ?? 'the approver'} at or above ₹${amount || '0'}. Below it goes straight through. The owner is never held.`
              : 'Off: anyone allowed can do this directly.'}
          </span>
        </div>
        {on && (
          <div className="grid gap-3 sm:grid-cols-4">
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
              <span className="text-[12px] font-medium text-muted">Approver</span>
              <select className={inputCls} value={approver} onChange={(e) => setApprover(e.target.value)}>
                <option value="admin">Any admin</option>
                <option value="owner">Owner</option>
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
