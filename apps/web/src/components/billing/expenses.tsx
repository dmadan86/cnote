'use client'
import { Plus } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { ApprovalBanner } from '@/components/approvals/banner'
import { BranchSelect } from '@/components/company/branch-filter'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { run } from './bits'
import { money, parseMinor, toRupees } from './fmt'

export type Expense = {
  id: string
  title: string
  kind: 'claim' | 'direct'
  status: 'draft' | 'submitted' | 'approved' | 'rejected' | 'reimbursed' | 'posted'
  spent_on: string
  category: string
  category_label: string
  amount_minor: number
  gst_minor: number
  vendor_name: string | null
  description: string
  paid_by_user_id: string | null
  paid_by_name: string | null
  reference: string | null
  decision_reason: string | null
}
type Category = { key: string; label: string }

const LABEL: Record<Expense['status'], string> = {
  draft: 'Draft',
  submitted: 'Waiting for approval',
  approved: 'To reimburse',
  rejected: 'Rejected',
  reimbursed: 'Reimbursed',
  posted: 'Paid by company',
}
const TONE: Record<Expense['status'], 'ok' | 'warn' | 'bad' | 'neutral'> = {
  draft: 'neutral',
  submitted: 'warn',
  approved: 'warn',
  rejected: 'bad',
  reimbursed: 'ok',
  posted: 'ok',
}
const StatusPill = ({ s }: { s: Expense['status'] }) => <Pill tone={TONE[s]}>{LABEL[s]}</Pill>
const today = () => new Date().toISOString().slice(0, 10)

/** One form for a new claim, an edit of a draft or rejected claim, and a company-paid expense. */
function ExpenseForm({
  initial,
  direct,
  onClose,
  onSaved,
}: {
  initial?: Expense
  direct?: boolean
  onClose: () => void
  onSaved: (id: string) => void
}) {
  const { ws } = useShell()
  const cats = useCmd<Category[]>(ws, 'expense.categories', {})
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const title = initial ? 'Edit claim' : direct ? 'Company-paid expense' : 'New expense claim'
  return (
    <Modal open title={title} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          const amount = parseMinor(String(f.get('amount')))
          if (!amount) return setErr('Enter the amount in rupees, for example 1800 or 1800.50.')
          const body = {
            spent_on: String(f.get('date')),
            category: String(f.get('category')),
            amount_minor: amount,
            vendor_name: String(f.get('vendor') ?? ''),
            description: String(f.get('note') ?? ''),
          }
          setBusy(true)
          try {
            const d = initial
              ? await run<Expense>(ws, 'expense.update', { id: initial.id, ...body })
              : await run<Expense>(ws, direct ? 'expense.record_direct' : 'expense.create', body)
            onSaved(d.id)
            onClose()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <Field label="Amount paid (INR, GST included)">
          {(id) => (
            <input
              id={id}
              name="amount"
              required
              inputMode="decimal"
              defaultValue={initial ? toRupees(initial.amount_minor) : ''}
              className={inputCls}
            />
          )}
        </Field>
        <Field label="Category">
          {(id) => (
            <select
              id={id}
              name="category"
              required
              defaultValue={initial?.category ?? 'travel'}
              className={inputCls}
            >
              {cats.data?.map((c) => (
                <option key={c.key} value={c.key}>
                  {c.label}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Date spent">
          {(id) => (
            <input
              id={id}
              name="date"
              type="date"
              required
              max={today()}
              defaultValue={initial?.spent_on ?? today()}
              className={inputCls}
            />
          )}
        </Field>
        <Field label="Paid to (optional)">
          {(id) => (
            <input id={id} name="vendor" defaultValue={initial?.vendor_name ?? ''} className={inputCls} />
          )}
        </Field>
        <Field label="What was it for (optional)">
          {(id) => (
            <textarea
              id={id}
              name="note"
              rows={2}
              defaultValue={initial?.description ?? ''}
              className={inputCls}
            />
          )}
        </Field>
        {!direct && (
          <p className="text-[12px] text-muted">
            Save it as a draft first. Then open the claim page to add the receipt photo, and submit when it is
            ready.
          </p>
        )}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy}>
            {direct ? 'Record expense' : 'Save draft'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

function Detail({
  id,
  admin,
  onClose,
  onChange,
}: {
  id: string
  admin: boolean
  onClose: () => void
  onChange: () => void
}) {
  const { ws, me } = useShell()
  const { data: x, error, reload } = useCmd<Expense>(ws, 'expense.get', { id })
  const [err, setErr] = useState<string>()
  const [mode, setMode] = useState<'edit' | 'reject' | 'reimburse'>()
  const [busy, setBusy] = useState(false)
  const act = async (name: string, extra: object = {}) => {
    setBusy(true)
    setErr(undefined)
    try {
      await run(ws, name, { id, ...extra })
      setMode(undefined)
      reload()
      onChange()
    } catch (e) {
      setErr((e as Error).message)
    }
    setBusy(false)
  }
  const mine = x?.paid_by_user_id === me.user.id
  return (
    <Modal open title={x ? x.title : 'Expense'} onClose={onClose} className="max-w-lg">
      <ErrorText error={error} />
      {x && (
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <StatusPill s={x.status} />
            <span className="text-[20px] font-semibold tabular-nums">{money(x.amount_minor)}</span>
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
            <dt className="text-muted">Spent on</dt>
            <dd>{x.spent_on}</dd>
            <dt className="text-muted">Category</dt>
            <dd>{x.category_label}</dd>
            {x.paid_by_name && (
              <>
                <dt className="text-muted">Claimed by</dt>
                <dd>{x.paid_by_name}</dd>
              </>
            )}
            {x.vendor_name && (
              <>
                <dt className="text-muted">Paid to</dt>
                <dd>{x.vendor_name}</dd>
              </>
            )}
            {x.description && (
              <>
                <dt className="text-muted">For</dt>
                <dd>{x.description}</dd>
              </>
            )}
          </dl>
          {x.status === 'rejected' && x.decision_reason && (
            <p role="status" className="rounded-md bg-bad-bg p-2 text-[13px] text-bad">
              Rejected: {x.decision_reason}
            </p>
          )}
          <ApprovalBanner ws={ws} noteId={x.id} />
          <p className="text-[13px]">
            <Link href={`/w/${ws}/n/${x.id}`} className="underline">
              Open the claim page
            </Link>{' '}
            <span className="text-muted">for the receipt photo, comments and history.</span>
          </p>
          <ErrorText error={err} />
          {mode === 'reject' && (
            <form
              className="space-y-2"
              onSubmit={(e) => {
                e.preventDefault()
                act('expense.reject', { reason: String(new FormData(e.currentTarget).get('reason')) })
              }}
            >
              <Field label="Reason for rejecting">
                {(i) => <input id={i} name="reason" required className={inputCls} />}
              </Field>
              <div className="flex justify-end gap-2">
                <button type="button" className={btn} onClick={() => setMode(undefined)}>
                  Back
                </button>
                <button type="submit" className={btnPrimary} disabled={busy}>
                  Reject claim
                </button>
              </div>
            </form>
          )}
          {mode === 'reimburse' && (
            <form
              className="space-y-2"
              onSubmit={(e) => {
                e.preventDefault()
                const f = new FormData(e.currentTarget)
                act('expense.reimburse', {
                  method: String(f.get('method')),
                  reference: String(f.get('reference')),
                })
              }}
            >
              <p className="text-[13px]">
                This records that you paid {money(x.amount_minor)} to {x.paid_by_name ?? 'the claimant'}. It
                does not send money.
              </p>
              <Field label="How was it paid">
                {(i) => (
                  <select id={i} name="method" className={inputCls}>
                    <option value="bank">Bank transfer</option>
                    <option value="upi">UPI</option>
                    <option value="cash">Cash</option>
                  </select>
                )}
              </Field>
              <Field label="Reference (optional)">
                {(i) => <input id={i} name="reference" className={inputCls} />}
              </Field>
              <div className="flex justify-end gap-2">
                <button type="button" className={btn} onClick={() => setMode(undefined)}>
                  Back
                </button>
                <button type="submit" className={btnPrimary} disabled={busy}>
                  Record reimbursement
                </button>
              </div>
            </form>
          )}
          {!mode && (
            <div className="flex flex-wrap justify-end gap-2">
              {(x.status === 'draft' || x.status === 'rejected') && mine && (
                <>
                  <button type="button" className={btn} onClick={() => setMode('edit')}>
                    Edit
                  </button>
                  {x.status === 'draft' && (
                    <button
                      type="button"
                      className={btnPrimary}
                      disabled={busy}
                      onClick={() => act('expense.submit')}
                    >
                      Submit claim
                    </button>
                  )}
                </>
              )}
              {admin && x.status === 'submitted' && (
                <>
                  <button type="button" className={btn} onClick={() => setMode('reject')}>
                    Reject
                  </button>
                  <button
                    type="button"
                    className={btnPrimary}
                    disabled={busy}
                    onClick={() => act('expense.approve')}
                  >
                    Approve
                  </button>
                </>
              )}
              {admin && x.status === 'approved' && (
                <button type="button" className={btnPrimary} onClick={() => setMode('reimburse')}>
                  Reimburse
                </button>
              )}
            </div>
          )}
        </div>
      )}
      {mode === 'edit' && x && (
        <ExpenseForm
          initial={x}
          onClose={() => setMode(undefined)}
          onSaved={() => {
            reload()
            onChange()
          }}
        />
      )}
    </Modal>
  )
}

type Tab = 'mine' | 'approve' | 'reimburse'

export function ExpenseList() {
  const { ws, me, spaces } = useShell()
  const admin = ['owner', 'admin'].includes(me.workspaces.find((w) => w.id === ws)?.role ?? '')
  const [branch, setBranch] = useState('')
  const { data, error, reload } = useCmd<Expense[]>(ws, 'expense.list', {
    limit: 500,
    ...(branch && { branch_id: branch }),
  })
  const [tab, setTab] = useState<Tab>('mine')
  const [adding, setAdding] = useState<'claim' | 'direct'>()
  const [open, setOpen] = useState<string>()
  const mine = data?.filter((x) => x.paid_by_user_id === me.user.id || (!admin && x.kind === 'direct')) ?? []
  const lists: Record<Tab, Expense[]> = {
    mine: admin ? mine : (data ?? []),
    approve: data?.filter((x) => x.status === 'submitted') ?? [],
    reimburse: data?.filter((x) => x.status === 'approved') ?? [],
  }
  const tabs: [Tab, string][] = [['mine', admin ? 'My claims' : 'Expenses']]
  if (admin)
    tabs.push(
      ['approve', `To approve (${lists.approve.length})`],
      ['reimburse', `To reimburse (${lists.reimburse.length})`],
    )
  const rows = lists[tab]
  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div role="tablist" aria-label="Expense views" className="flex gap-1">
          {tabs.map(([k, t]) => (
            <button
              key={k}
              type="button"
              role="tab"
              aria-selected={tab === k}
              onClick={() => setTab(k)}
              className="rounded-md px-3 py-2 text-[13px] text-muted hover:bg-hover aria-selected:bg-hover aria-selected:font-medium aria-selected:text-fg"
            >
              {t}
            </button>
          ))}
        </div>
        <BranchSelect ws={ws} value={branch} onChange={setBranch} />
        <div className="ml-auto flex gap-2">
          {admin && (
            <button
              type="button"
              className={btn}
              disabled={!spaces.length}
              onClick={() => setAdding('direct')}
            >
              Company-paid expense
            </button>
          )}
          <button
            type="button"
            className={btnPrimary}
            disabled={!spaces.length}
            onClick={() => setAdding('claim')}
          >
            <Plus size={14} /> New claim
          </button>
        </div>
      </div>
      <ErrorText error={error} />
      {data && rows.length === 0 && (
        <p className="py-8 text-center text-muted">
          {tab === 'mine'
            ? 'No expenses yet. Add one when you pay for something for the business.'
            : 'Nothing waiting here.'}
        </p>
      )}
      <ul aria-label="Expenses" className="divide-y divide-line">
        {rows.map((x) => (
          <li key={x.id}>
            <button
              type="button"
              onClick={() => setOpen(x.id)}
              className="flex min-h-11 w-full flex-wrap items-center gap-x-3 gap-y-1 py-2 text-left hover:bg-hover"
            >
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">
                  {x.category_label}
                  {x.vendor_name ? ` at ${x.vendor_name}` : ''}
                </span>
                <span className="text-[12px] text-muted">
                  {x.spent_on}
                  {x.paid_by_name ? ` by ${x.paid_by_name}` : ''}
                </span>
              </span>
              <StatusPill s={x.status} />
              <span className="w-28 text-right tabular-nums">{money(x.amount_minor)}</span>
            </button>
          </li>
        ))}
      </ul>
      {adding && (
        <ExpenseForm
          direct={adding === 'direct'}
          onClose={() => setAdding(undefined)}
          onSaved={(id) => {
            reload()
            if (adding === 'claim') setOpen(id)
          }}
        />
      )}
      {open && <Detail id={open} admin={admin} onClose={() => setOpen(undefined)} onChange={reload} />}
    </div>
  )
}
