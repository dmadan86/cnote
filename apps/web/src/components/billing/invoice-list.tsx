'use client'
import { FileText, Plus } from 'lucide-react'
import Link from 'next/link'
import { usePathname, useRouter, useSearchParams } from 'next/navigation'
import { useMemo, useState } from 'react'
import { BranchSelect, useBranches } from '@/components/company/branch-filter'
import { NeedSpace } from '@/components/crm/contacts'
import { type Column, ListTable } from '@/components/list-table'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, EmptyState, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { useCmd, useOpenOnNew } from '@/lib/hooks'
import { hits, newDraft, SearchBox } from './bits'
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

const OPEN = ['issued', 'irn_generated', 'sent', 'partly_paid']
/** Amount still owed; null where it does not apply (drafts, cancelled, credit notes). */
const outstanding = (i: Invoice) =>
  i.kind === 'invoice' && i.status !== 'draft' && i.status !== 'cancelled'
    ? i.total_minor - i.paid_minor - i.credited_minor
    : null
const LIMIT = 500

export function InvoiceList() {
  const { ws, spaces, me } = useShell()
  const role = me.workspaces.find((w) => w.id === ws)?.role
  const canAdd = spaces.length > 0 && role !== 'guest'
  const [adding, setAdding] = useState(false)
  useOpenOnNew(canAdd, () => setAdding(true))
  const [branch, setBranch] = useState('')
  const [q, setQ] = useState('')
  const { data: all, error } = useCmd<Invoice[]>(ws, 'invoice.list', {
    limit: LIMIT,
    ...(branch && { branch_id: branch }),
  })
  // home tiles link here with ?filter=outstanding|overdue
  const filter = useSearchParams().get('filter')
  const router = useRouter()
  const pathname = usePathname()
  const base =
    filter === 'outstanding' || filter === 'overdue'
      ? all?.filter(
          (i) =>
            i.kind === 'invoice' &&
            OPEN.includes(i.status) &&
            i.total_minor - i.paid_minor - i.credited_minor > 0 &&
            (filter === 'outstanding' || (!!i.due_date && i.due_date < today())),
        )
      : all
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const names = useMemo(() => new Map(contacts.data?.map((c) => [c.id, c.name])), [contacts.data])
  const columns = useMemo<Column<Invoice>[]>(
    () => [
      {
        key: 'number',
        label: 'Number',
        locked: true,
        sort: (i) => i.number,
        cell: (i) => (
          <>
            <Link href={`/w/${ws}/billing/${i.id}`} className="hover:underline">
              {i.number ?? 'Draft'}
            </Link>
            {i.kind === 'credit_note' && <span className="ml-1.5 text-[12px] text-muted">credit note</span>}
          </>
        ),
      },
      {
        key: 'customer',
        label: 'Customer',
        sort: (i) => (i.contact_id && names.get(i.contact_id)) || null,
        cell: (i) => (i.contact_id ? names.get(i.contact_id) : ''),
      },
      {
        key: 'date',
        label: 'Date',
        sort: (i) => i.invoice_date,
        cell: (i) => <span className="text-muted">{i.invoice_date}</span>,
      },
      {
        key: 'due',
        label: 'Due',
        sort: (i) => i.due_date,
        cell: (i) => <span className="text-muted">{i.due_date}</span>,
      },
      {
        key: 'status',
        label: 'Status',
        sort: (i) => STATUS_LABEL[i.status] ?? i.status,
        cell: (i) => <StatusPill status={i.status} />,
      },
      {
        key: 'total',
        label: 'Total',
        align: 'right',
        sort: (i) => i.total_minor,
        cell: (i) => money(i.total_minor),
      },
      {
        key: 'outstanding',
        label: 'Outstanding',
        align: 'right',
        sort: outstanding,
        cell: (i) => {
          const o = outstanding(i)
          return o == null ? '' : money(o)
        },
      },
      {
        key: 'paid',
        label: 'Paid',
        align: 'right',
        hidden: true,
        sort: (i) => i.paid_minor,
        cell: (i) => money(i.paid_minor),
      },
      {
        key: 'gstin',
        label: 'Buyer GSTIN',
        hidden: true,
        sort: (i) => i.buyer_gstin,
        cell: (i) => <span className="font-mono text-[13px]">{i.buyer_gstin}</span>,
      },
    ],
    [ws, names],
  )
  const data = q.trim()
    ? base?.filter((i) =>
        hits(
          q,
          i.number,
          i.contact_id && names.get(i.contact_id),
          STATUS_LABEL[i.status],
          i.total_minor / 100,
        ),
      )
    : base
  // really empty only when no search, status filter or branch narrowed the list
  const narrowed = data !== all || !!branch
  return (
    <div>
      <div className="mb-3 flex items-center gap-2">
        <SearchBox label="Search invoices" value={q} onChange={setQ} loaded={all?.length} />
        <BranchSelect ws={ws} value={branch} onChange={setBranch} />
        <button
          type="button"
          className={`${btnPrimary} ml-auto`}
          disabled={!canAdd}
          onClick={() => setAdding(true)}
        >
          <Plus size={14} /> New invoice
        </button>
      </div>
      {base !== all && (
        <FilterBanner
          label={filter === 'overdue' ? 'overdue invoices' : 'unpaid invoices'}
          href={`/w/${ws}/billing`}
        />
      )}
      <ErrorText error={error} />
      {!spaces.length && <NeedSpace />}
      <ListTable
        screen="billing.invoices"
        label="Invoices"
        columns={columns}
        rows={data}
        rowKey={(i) => i.id}
        filters={{ q, branch, filter: filter === 'outstanding' || filter === 'overdue' ? filter : '' }}
        onFilters={(f) => {
          setQ(f.q ?? '')
          setBranch(f.branch ?? '')
          const next = f.filter === 'outstanding' || f.filter === 'overdue' ? `?filter=${f.filter}` : ''
          router.replace(`${pathname}${next}`)
        }}
        partial={all && all.length >= LIMIT ? `newest ${LIMIT} invoices` : undefined}
        empty={
          spaces.length > 0 && data?.length === 0 ? (
            <EmptyState
              icon={FileText}
              title={narrowed ? 'No invoices match' : 'No invoices yet'}
              action={
                narrowed || !canAdd ? undefined : (
                  <>
                    <button type="button" className={btnPrimary} onClick={() => setAdding(true)}>
                      <Plus size={14} /> New invoice
                    </button>
                    {(role === 'owner' || role === 'admin') && (
                      <Link href={`/w/${ws}/import/tally`} className="text-[13px] text-muted underline">
                        Import from Tally
                      </Link>
                    )}
                  </>
                )
              }
            >
              {narrowed
                ? 'Nothing fits this filter.'
                : 'Make a GST invoice in under a minute and send it by WhatsApp or email.'}
            </EmptyState>
          ) : undefined
        }
      />
      {adding && <NewInvoice onClose={() => setAdding(false)} />}
    </div>
  )
}
