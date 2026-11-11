'use client'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { newDraft } from '@/components/billing/bits'
import { type Contact, type Deal, type Invoice, money, stateName } from '@/components/billing/fmt'
import { StatusPill } from '@/components/billing/invoice-list'
import { useShell } from '@/components/shell'
import { UseTemplateButton } from '@/components/templates/use-template'
import { btn, btnPrimary, ErrorText } from '@/components/ui'
import { ago, useCmd } from '@/lib/hooks'
import { ContactForm } from './contacts'

const Row = ({ k, v, mono }: { k: string; v?: string | null; mono?: boolean }) => (
  <div className="flex justify-between gap-4 py-1.5 text-[14px]">
    <dt className="text-muted">{k}</dt>
    <dd className={`text-right ${mono ? 'font-mono text-[13px]' : ''}`}>{v || '-'}</dd>
  </div>
)

export function ContactDetail({ id }: { id: string }) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const c = useCmd<Contact>(ws, 'contact.get', { id })
  const deals = useCmd<Deal[]>(ws, 'deal.list', { contact_id: id, limit: 100 })
  const invoices = useCmd<Invoice[]>(ws, 'invoice.list', { contact_id: id, limit: 100 })
  const [editing, setEditing] = useState(false)
  const [err, setErr] = useState<string>()
  if (c.error) return <p className="p-6 text-bad">{c.error.message}</p>
  if (!c.data) return <p className="p-6 text-muted">Loading...</p>
  const k = c.data

  // The API has no activity feed yet, so the timeline is built from dated deals and invoices.
  const timeline = [
    ...(deals.data ?? []).flatMap((d) =>
      d.closed_at ? [{ at: d.closed_at, text: `Deal "${d.title}" ${d.stage}` }] : [],
    ),
    ...(invoices.data ?? []).flatMap((i) =>
      i.issued_at
        ? [
            {
              at: i.issued_at,
              text: `${i.kind === 'credit_note' ? 'Credit note' : 'Invoice'} ${i.number} issued`,
            },
          ]
        : [],
    ),
  ].sort((a, b) => b.at.localeCompare(a.at))

  return (
    <div className="mx-auto grid max-w-5xl gap-4 px-6 py-8 md:grid-cols-[320px_1fr]">
      <section aria-label="Contact details" className="h-fit rounded-lg border border-line p-4">
        <h1 className="text-lg font-semibold">{k.name}</h1>
        <p className="mb-2 text-[13px] capitalize text-muted">{k.kind}</p>
        <dl className="divide-y divide-line">
          <Row k="Email" v={k.email} />
          <Row k="Phone" v={k.phone} />
          <Row k="Company" v={k.company} />
          <Row k="GSTIN" v={k.gstin} mono />
          <Row k="State" v={k.state_code ? `${k.state_code} ${stateName(k.state_code)}` : null} />
          <Row k="Address" v={k.billing_address} />
        </dl>
        <div className="mt-3 flex flex-wrap gap-2">
          <button type="button" className={btn} onClick={() => setEditing(true)}>
            Edit contact
          </button>
          <UseTemplateButton sourceKind="contact" sourceId={id} />
        </div>
      </section>
      <div className="space-y-4">
        <ErrorText error={err} />
        <section aria-label="Deals" className="rounded-lg border border-line p-4">
          <h2 className="mb-2 text-[14px] font-semibold">Deals</h2>
          {deals.data?.length === 0 && <p className="text-[13px] text-muted">No deals yet.</p>}
          <ul className="divide-y divide-line">
            {deals.data?.map((d) => (
              <li key={d.id} className="flex items-center gap-3 py-1.5 text-[14px]">
                <Link href={`/w/${ws}/crm/deals/${d.id}`} className="font-medium hover:underline">
                  {d.title}
                </Link>
                <span className="capitalize text-muted">{d.stage}</span>
                <span className="ml-auto tabular-nums">{money(d.amount_minor)}</span>
              </li>
            ))}
          </ul>
        </section>
        <section aria-label="Invoices" className="rounded-lg border border-line p-4">
          <div className="mb-2 flex items-center">
            <h2 className="text-[14px] font-semibold">Invoices</h2>
            <button
              type="button"
              className={`${btnPrimary} ml-auto`}
              disabled={!spaces.length}
              onClick={async () => {
                try {
                  router.push(
                    `/w/${ws}/billing/${await newDraft(ws, spaces[0].id, id, { description: 'Services', unit_price_minor: 0 })}`,
                  )
                } catch (x) {
                  setErr((x as Error).message)
                }
              }}
            >
              New invoice
            </button>
          </div>
          {invoices.data?.length === 0 && <p className="text-[13px] text-muted">No invoices yet.</p>}
          <ul className="divide-y divide-line">
            {invoices.data?.map((i) => (
              <li key={i.id} className="flex items-center gap-3 py-1.5 text-[14px]">
                <Link href={`/w/${ws}/billing/${i.id}`} className="font-medium hover:underline">
                  {i.number ?? 'Draft'}
                </Link>
                <StatusPill status={i.status} />
                <span className="ml-auto tabular-nums">{money(i.total_minor)}</span>
              </li>
            ))}
          </ul>
        </section>
        <section aria-label="Timeline" className="rounded-lg border border-line p-4">
          <h2 className="mb-2 text-[14px] font-semibold">Timeline</h2>
          {timeline.length === 0 && <p className="text-[13px] text-muted">Nothing has happened yet.</p>}
          <ol className="space-y-1.5">
            {timeline.map((t) => (
              <li key={t.text} className="flex justify-between text-[14px]">
                {t.text}
                <span className="text-[12px] text-muted">{ago(t.at)}</span>
              </li>
            ))}
          </ol>
        </section>
      </div>
      {editing && (
        <ContactForm
          contact={k}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false)
            c.reload()
          }}
        />
      )}
    </div>
  )
}
