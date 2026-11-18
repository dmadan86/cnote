'use client'
import { Monitor, Scale } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useMemo, useState } from 'react'
import { money, parseMinor, toRupees } from '@/components/billing/fmt'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, EmptyState, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

type Line = { account_code: string; dr_minor: number; cr_minor: number }
type Opening = {
  go_live: string
  status: 'draft' | 'posted' | 'reversed'
  lines: Line[]
  invoices: { count: number; total_minor: number }
  bills: { count: number; total_minor: number }
}
type Account = { code: string; name: string; kind: string }
const SECTIONS = [
  ['asset', 'What you have', 'Cash, bank, stock, equipment and money others owe you.'],
  ['liability', 'What you owe', 'Loans, taxes payable and money you owe suppliers.'],
  ['equity', "Owner's money", 'Capital and reserves put in so far.'],
] as const
const ADJ = '3900'
const DERIVED = ['1100', '2100']

/** Amount text -> paise; blank is 0, invalid is null. */
const amt = (s: string) => (s.trim() === '' ? 0 : parseMinor(s.replace(/,/g, '')))

export function OpeningBalances() {
  const { ws } = useShell()
  const op = useCmd<Opening>(ws, 'opening.get')
  const tb = useCmd<Account[]>(ws, 'ledger.account_list')
  const [goLive, setGoLive] = useState('')
  const [vals, setVals] = useState<Record<string, { dr: string; cr: string }>>({})
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const o = op.data
  const editable = o?.status !== 'posted'

  useEffect(() => {
    if (!o) return
    setGoLive(o.go_live)
    setVals(
      Object.fromEntries(
        o.lines.map((l) => [
          l.account_code,
          { dr: l.dr_minor ? toRupees(l.dr_minor) : '', cr: l.cr_minor ? toRupees(l.cr_minor) : '' },
        ]),
      ),
    )
  }, [o])

  const accounts = tb.data ?? []
  const derived: Record<string, Line> = {
    '1100': { account_code: '1100', dr_minor: o?.invoices.total_minor ?? 0, cr_minor: 0 },
    '2100': { account_code: '2100', dr_minor: 0, cr_minor: o?.bills.total_minor ?? 0 },
  }
  const parsed = useMemo(() => {
    let bad = false
    const lines: Line[] = []
    for (const [code, v] of Object.entries(vals)) {
      if (DERIVED.includes(code) || code === ADJ) continue
      const dr = amt(v.dr)
      const cr = amt(v.cr)
      if (dr === null || cr === null) bad = true
      else if (dr || cr) lines.push({ account_code: code, dr_minor: dr, cr_minor: cr })
    }
    return { bad, lines }
  }, [vals])
  const all = [...parsed.lines, derived['1100'], derived['2100']]
  const dr = all.reduce((t, l) => t + l.dr_minor, 0)
  const cr = all.reduce((t, l) => t + l.cr_minor, 0)
  const diff = dr - cr // Dr minus Cr; posted to 3900 on the other side

  async function run(name: string, input: unknown, ok: string) {
    setBusy(true)
    setErr(undefined)
    setMsg(undefined)
    try {
      await cmd(ws, name, input)
      setMsg(ok)
      op.reload()
      return true
    } catch (x) {
      setErr((x as Error).message)
      return false
    } finally {
      setBusy(false)
      setConfirm(false)
    }
  }
  const save = () =>
    run('opening.save', { go_live: goLive, lines: parsed.lines }, 'Draft saved. Nothing is posted yet.')

  return (
    <div className="mx-auto max-w-3xl px-6 py-8">
      <Link href={`/w/${ws}/import`} className="text-[13px] text-muted hover:underline">
        Import data
      </Link>
      <h1 className="mt-1 text-xl font-semibold">Opening balances</h1>
      <p className="mt-1 mb-6 text-[13px] text-muted">
        What your books held the day before you started using C Note. Posted once as a single entry.
      </p>
      <div className="md:hidden">
        <EmptyState icon={Monitor} title="Open this on a computer">
          The balance grid needs a bigger screen.
        </EmptyState>
      </div>
      <div className="hidden space-y-6 md:block">
        <ErrorText error={op.error ?? tb.error ?? err} />
        {o && (
          <>
            <div className="flex flex-wrap items-end gap-4">
              <Field label="Go-live date">
                {(id) => (
                  <input
                    id={id}
                    type="date"
                    value={goLive}
                    disabled={!editable}
                    onChange={(e) => setGoLive(e.target.value)}
                    className={`${inputCls} w-44`}
                  />
                )}
              </Field>
              <p className="pb-2 text-[12px] text-muted">Balances are booked on the day before this date.</p>
              <span className="ml-auto pb-1">
                <Pill tone={o.status === 'posted' ? 'ok' : 'neutral'}>
                  {o.status === 'posted' ? 'Posted' : o.status === 'reversed' ? 'Reversed' : 'Draft'}
                </Pill>
              </span>
            </div>

            {SECTIONS.map(([kind, title, hint]) => (
              <section key={kind} className="rounded-lg border border-line">
                <header className="border-b border-line px-4 py-2.5">
                  <h2 className="text-[14px] font-semibold">{title}</h2>
                  <p className="text-[12px] text-muted">{hint}</p>
                </header>
                <table className="w-full text-[13px]">
                  <thead className="text-left text-[12px] text-muted">
                    <tr>
                      <th className="px-4 py-1.5 font-medium">Account</th>
                      <th className="w-36 px-2 py-1.5 font-medium">Debit (Dr)</th>
                      <th className="w-36 px-2 py-1.5 font-medium">Credit (Cr)</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {accounts
                      .filter((a) => a.kind === kind && a.code !== ADJ)
                      .map((a) => {
                        const d = DERIVED.includes(a.code) ? derived[a.code] : undefined
                        return (
                          <tr key={a.code}>
                            <td className="px-4 py-1.5">
                              <span className="tabular-nums text-muted">{a.code}</span> {a.name}
                              {d && (
                                <span className="block text-[12px] text-muted">
                                  From your unpaid {a.code === '1100' ? 'invoices' : 'bills'} below
                                </span>
                              )}
                            </td>
                            {(['dr', 'cr'] as const).map((side) => (
                              <td key={side} className="px-2 py-1">
                                {d ? (
                                  <span className="block px-2.5 py-1.5 text-right tabular-nums text-muted">
                                    {(side === 'dr' ? d.dr_minor : d.cr_minor)
                                      ? money(side === 'dr' ? d.dr_minor : d.cr_minor)
                                      : '-'}
                                  </span>
                                ) : (
                                  <input
                                    aria-label={`${a.name} ${side === 'dr' ? 'debit' : 'credit'}`}
                                    inputMode="decimal"
                                    disabled={!editable}
                                    value={vals[a.code]?.[side] ?? ''}
                                    onChange={(e) =>
                                      setVals({
                                        ...vals,
                                        [a.code]: {
                                          ...(vals[a.code] ?? { dr: '', cr: '' }),
                                          [side]: e.target.value,
                                        },
                                      })
                                    }
                                    className={`${inputCls} text-right tabular-nums`}
                                  />
                                )}
                              </td>
                            ))}
                          </tr>
                        )
                      })}
                  </tbody>
                </table>
              </section>
            ))}

            <section className="rounded-lg border border-line p-4 text-[13px]">
              <dl className="space-y-1.5 tabular-nums">
                <div className="flex justify-between">
                  <dt className="text-muted">Total debit</dt>
                  <dd>{money(dr)}</dd>
                </div>
                <div className="flex justify-between">
                  <dt className="text-muted">Total credit</dt>
                  <dd>{money(cr)}</dd>
                </div>
                <div className="flex justify-between border-t border-line pt-1.5 font-semibold">
                  <dt>Difference</dt>
                  <dd data-testid="difference">{money(Math.abs(diff))}</dd>
                </div>
              </dl>
              {diff !== 0 && (
                <p className="mt-2 text-muted">
                  The difference of {money(Math.abs(diff))} is booked as a {diff > 0 ? 'credit' : 'debit'} to
                  Opening balance adjustments (an owner's money account), so your books balance from day one.
                </p>
              )}
            </section>

            <section className="rounded-lg border border-line p-4">
              <h2 className="text-[14px] font-semibold">Unpaid invoices and bills</h2>
              <p className="mt-0.5 mb-3 text-[12px] text-muted">
                Customers and suppliers balances come only from these, so nothing is counted twice. They stay
                open for payments and show in ageing, but never in GST returns.
              </p>
              <dl className="mb-3 grid grid-cols-2 gap-3 text-[13px]">
                <div>
                  <dt className="text-muted">Customers owe you</dt>
                  <dd className="tabular-nums font-medium">
                    {money(o.invoices.total_minor)}{' '}
                    <span className="text-muted">({o.invoices.count} invoices)</span>
                  </dd>
                </div>
                <div>
                  <dt className="text-muted">You owe suppliers</dt>
                  <dd className="tabular-nums font-medium">
                    {money(o.bills.total_minor)} <span className="text-muted">({o.bills.count} bills)</span>
                  </dd>
                </div>
              </dl>
              <Link href={`/w/${ws}/import/opening-docs`} className={btn}>
                Import from CSV
              </Link>
            </section>

            {msg && (
              <p role="status" className="text-[13px] text-ok">
                {msg}
              </p>
            )}
            <div className="flex flex-wrap gap-2">
              {editable ? (
                <>
                  <button type="button" className={btn} disabled={busy || parsed.bad} onClick={save}>
                    Save draft
                  </button>
                  <button
                    type="button"
                    className={btnPrimary}
                    disabled={busy || parsed.bad}
                    onClick={() => setConfirm(true)}
                  >
                    <Scale size={14} /> Post opening balances
                  </button>
                </>
              ) : (
                <button
                  type="button"
                  className={btn}
                  disabled={busy}
                  onClick={() =>
                    run('opening.reverse', {}, 'Opening balances reversed. You can edit and post again.')
                  }
                >
                  Reverse
                </button>
              )}
            </div>
          </>
        )}
      </div>

      <Modal open={confirm} onClose={() => setConfirm(false)} title="Post opening balances?">
        <div className="space-y-3 text-[13px]">
          <p>
            This posts one entry dated the day before <strong>{goLive}</strong>. You can reverse it later.
          </p>
          <dl className="space-y-1 tabular-nums">
            <div className="flex justify-between">
              <dt>Total debit</dt>
              <dd>{money(dr)}</dd>
            </div>
            <div className="flex justify-between">
              <dt>Total credit</dt>
              <dd>{money(cr)}</dd>
            </div>
            <div className="flex justify-between font-semibold">
              <dt>Difference to Opening balance adjustments</dt>
              <dd>{money(Math.abs(diff))}</dd>
            </div>
          </dl>
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setConfirm(false)}>
              Cancel
            </button>
            <button
              type="button"
              className={btnPrimary}
              disabled={busy}
              onClick={async () => {
                if (await run('opening.save', { go_live: goLive, lines: parsed.lines }, ''))
                  await run('opening.post', {}, 'Opening balances posted.')
              }}
            >
              Confirm and post
            </button>
          </div>
        </div>
      </Modal>
    </div>
  )
}
