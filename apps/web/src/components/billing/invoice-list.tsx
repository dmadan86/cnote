'use client'
import { Plus } from 'lucide-react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useState } from 'react'
import { BranchSelect, useBranches } from '@/components/company/branch-filter'
import { NeedSpace } from '@/components/crm/contacts'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { newDraft } from './bits'
import { FilterBanner, today } from './filter'
import { type Contact, type Invoice, money, STATUS_LABEL, STATUS_TONE } from './fmt'

export const StatusPill = ({ status }: { status: string }) => (
  <Pill tone={STATUS_TONE[status]}>{STATUS_LABEL[status] ?? status}</Pill>
)

/** Which branch raises the invoice; its GSTIN and state apply. Hidden until a branch exists. */
function BranchPick({ ws }: { ws: string }) {
  const { branches } = useBranches(ws)
  if (!branches.length) return null
  return (
    <Field label="Branch">
      {(id) => (
        <select id={id} name="branch" className={inputCls}>
          <option value="">My branch or head office</option>
          {branches.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
              {b.gstin ? ` (${b.gstin})` : ''}
            </option>
          ))}
        </select>
      )}
    </Field>
  )
}

function NewInvoice({ onClose }: { onClose: () => void }) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const [err, setErr] = useState<string>()
  return (
    <Modal open title="New invoice" onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          try {
            const id = await newDraft(
              ws,
              spaces[0].id,
              String(new FormData(e.currentTarget).get('contact')),
              {
                description: 'Services',
                unit_price_minor: 0,
              },
              String(new FormData(e.currentTarget).get('branch') ?? '') || undefined,
            )
            router.push(`/w/${ws}/billing/${id}`)
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <BranchPick ws={ws} />
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
        {contacts.data?.length === 0 && (
          <p className="text-[13px] text-muted">
            No contacts yet.{' '}
            <Link href={`/w/${ws}/crm`} className="underline">
              Create one in CRM
            </Link>
            .
          </p>
        )}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Create draft
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function InvoiceList() {
  const { ws, spaces } = useShell()
  const [adding, setAdding] = useState(false)
  const [branch, setBranch] = useState('')
  const { data: all, error } = useCmd<Invoice[]>(ws, 'invoice.list', {
    limit: 500,
    ...(branch && { branch_id: branch }),
  })
  // home tiles link here with ?filter=outstanding|overdue
  const filter = useSearchParams().get('filter')
  const data =
    filter === 'outstanding' || filter === 'overdue'
      ? all?.filter(
          (i) =>
            i.kind === 'invoice' &&
            ['issued', 'irn_generated', 'sent', 'partly_paid'].includes(i.status) &&
            i.total_minor - i.paid_minor - i.credited_minor > 0 &&
            (filter === 'outstanding' || (!!i.due_date && i.due_date < today())),
        )
      : all
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const names = new Map(contacts.data?.map((c) => [c.id, c.name]))
  return (
    <div>
      <div className="mb-3 flex items-center gap-2">
        <BranchSelect ws={ws} value={branch} onChange={setBranch} />
        <button
          type="button"
          className={`${btnPrimary} ml-auto`}
          disabled={!spaces.length}
          onClick={() => setAdding(true)}
        >
          <Plus size={14} /> New invoice
        </button>
      </div>
      {data !== all && (
        <FilterBanner
          label={filter === 'overdue' ? 'overdue invoices' : 'unpaid invoices'}
          href={`/w/${ws}/billing`}
        />
      )}
      <ErrorText error={error} />
      {!spaces.length && <NeedSpace />}
      {data?.length === 0 && spaces.length > 0 && (
        <p className="py-8 text-center text-muted">
          {data !== all ? 'Nothing matches.' : 'No invoices yet.'}
        </p>
      )}
      {!!data?.length && (
        <table className="w-full text-left text-[14px]">
          <thead className="text-[12px] text-muted">
            <tr className="border-b border-line">
              <th className="py-2 font-medium">Number</th>
              <th className="font-medium">Customer</th>
              <th className="font-medium">Date</th>
              <th className="font-medium">Due</th>
              <th className="font-medium">Status</th>
              <th className="text-right font-medium">Total</th>
              <th className="text-right font-medium">Outstanding</th>
            </tr>
          </thead>
          <tbody>
            {data.map((i) => (
              <tr key={i.id} className="border-b border-line hover:bg-hover">
                <td className="py-2 font-medium">
                  <Link href={`/w/${ws}/billing/${i.id}`} className="hover:underline">
                    {i.number ?? 'Draft'}
                  </Link>
                  {i.kind === 'credit_note' && (
                    <span className="ml-1.5 text-[12px] text-muted">credit note</span>
                  )}
                </td>
                <td>{i.contact_id ? names.get(i.contact_id) : ''}</td>
                <td className="text-muted">{i.invoice_date}</td>
                <td className="text-muted">{i.due_date}</td>
                <td>
                  <StatusPill status={i.status} />
                </td>
                <td className="text-right tabular-nums">{money(i.total_minor)}</td>
                <td className="text-right tabular-nums">
                  {i.kind === 'invoice' && i.status !== 'draft' && i.status !== 'cancelled'
                    ? money(i.total_minor - i.paid_minor - i.credited_minor)
                    : ''}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && <NewInvoice onClose={() => setAdding(false)} />}
    </div>
  )
}
