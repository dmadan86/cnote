'use client'
import { Plus } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { ApprovalBanner } from '@/components/approvals/banner'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { CmdFailure } from '@/lib/api'
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
            Save it as a draft first. Then add the receipt photo on the claim, and submit when it is ready.
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

type FileMeta = { id: string; filename: string; content_type: string; size_bytes: number }
const kb = (n: number) =>
  n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`

/** Receipt photos and PDFs on a claim: viewable by anyone who can see the claim, changed only by its owner while draft or rejected. */
function Receipts({ id, editable }: { id: string; editable: boolean }) {
  const { ws } = useShell()
  const { data, error, reload } = useCmd<FileMeta[]>(ws, 'file.list', { note_id: id })
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const upload = async (f: File) => {
    setErr(undefined)
    if (f.size > 10 << 20) return setErr('That file is larger than 10 MB.')
    setBusy(true)
    try {
      const res = await fetch(`/api/w/${ws}/files?note_id=${id}&filename=${encodeURIComponent(f.name)}`, {
        method: 'POST',
        body: f,
        credentials: 'same-origin',
      })
      if (!res.ok) {
        const e = (await res.json().catch(() => ({})))?.error ?? {}
        throw new CmdFailure(e.code ?? 'internal', e.message ?? res.statusText, res.status)
      }
      reload()
    } catch (x) {
      setErr((x as Error).message)
    }
    setBusy(false)
  }
  const remove = async (fid: string) => {
    setBusy(true)
    try {
      await run(ws, 'file.delete', { id: fid })
      reload()
    } catch (x) {
      setErr((x as Error).message)
    }
    setBusy(false)
  }
  return (
    <section aria-label="Receipts" className="space-y-2">
      <h3 className="text-[13px] font-medium">Receipts</h3>
      <ErrorText error={error?.message ?? err} />
      {data?.length === 0 && <p className="text-[13px] text-muted">No receipt attached yet.</p>}
      <ul className="space-y-2">
        {data?.map((f) => (
          <li key={f.id} className="flex min-h-11 items-center gap-3">
            <a
              href={`/api/w/${ws}/files/${f.id}`}
              target="_blank"
              rel="noreferrer"
              className="flex min-w-0 flex-1 items-center gap-3 underline-offset-2 hover:underline"
            >
              {f.content_type.startsWith('image/') ? (
                // biome-ignore lint/performance/noImgElement: authenticated same-origin bytes, not a static asset
                <img
                  src={`/api/w/${ws}/files/${f.id}`}
                  alt=""
                  className="size-16 rounded-md border border-line object-cover"
                />
              ) : (
                <span className="grid size-16 place-items-center rounded-md border border-line text-[12px] text-muted">
                  PDF
                </span>
              )}
              <span className="min-w-0 break-words text-[13px]">
                {f.filename} <span className="text-muted">({kb(f.size_bytes)}, opens in a new tab)</span>
              </span>
            </a>
            {editable && (
              <button
                type="button"
                className={btn}
                disabled={busy}
                onClick={() => remove(f.id)}
                aria-label={`Remove ${f.filename}`}
              >
                Remove
              </button>
            )}
          </li>
        ))}
      </ul>
      {editable && (
        <label className={`${btn} inline-flex min-h-11 cursor-pointer items-center`}>
          {busy ? 'Working...' : 'Add receipt photo'}
          <input
            type="file"
            accept="image/*,application/pdf"
            className="sr-only"
            disabled={busy}
            onChange={(e) => {
              const f = e.target.files?.[0]
              e.target.value = ''
              if (f) upload(f)
            }}
          />
        </label>
      )}
      {editable && <p className="text-[12px] text-muted">Images or PDF, up to 10 MB each.</p>}
    </section>
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
          <Receipts id={x.id} editable={mine && (x.status === 'draft' || x.status === 'rejected')} />
          <ApprovalBanner ws={ws} noteId={x.id} />
          <p className="text-[13px]">
            <Link href={`/w/${ws}/n/${x.id}`} className="underline">
              Open the claim page
            </Link>{' '}
            <span className="text-muted">for comments and history.</span>
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
  const { data, error, reload } = useCmd<Expense[]>(ws, 'expense.list', { limit: 500 })
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
