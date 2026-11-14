'use client'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { newDraft } from '@/components/billing/bits'
import { type Contact, type Deal, type Invoice, money, stateName } from '@/components/billing/fmt'
import { StatusPill } from '@/components/billing/invoice-list'
import { useShell } from '@/components/shell'
import { UseTemplateButton } from '@/components/templates/use-template'
import { btn, btnPrimary, ErrorText, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { ContactForm } from './contacts'
import { DealForm } from './deals'

const Row = ({ k, v, mono }: { k: string; v?: string | null; mono?: boolean }) => (
  <div className="flex justify-between gap-4 py-1.5 text-[14px]">
    <dt className="text-muted">{k}</dt>
    <dd className={`text-right ${mono ? 'font-mono text-[13px]' : ''}`}>{v || '-'}</dd>
  </div>
)

type Quote = {
  id: string
  number: string | null
  status: string
  total_minor: number
  sent_at: string | null
  decided_at: string | null
  created_at: string
}
type Project = {
  id: string
  space_id: string
  key: string
  title: string
  status: string
  created_at: string
}
type Payment = {
  id: string
  invoice_id: string
  invoice_number: string | null
  amount_minor: number
  method: string
  received_at: string
}
/** contact.lifecycle: a section is null when the caller may not read it (so money is never shown to them). */
type Lifecycle = {
  deals: (Deal & { created_at: string })[] | null
  quotes: Quote[] | null
  invoices: Invoice[] | null
  projects: Project[] | null
  payments: Payment[] | null
  outstanding_minor: number | null
}
type Event = { at: string; kind: string; text: string; href?: string }

const Section = ({
  title,
  empty,
  n,
  children,
}: {
  title: string
  empty: string
  n: number
  children: React.ReactNode
}) => (
  <section aria-label={title} className="rounded-lg border border-line p-4">
    <h2 className="mb-2 text-[14px] font-semibold">{title}</h2>
    {n === 0 ? (
      <p className="text-[13px] text-muted">{empty}</p>
    ) : (
      <ul className="divide-y divide-line">{children}</ul>
    )}
  </section>
)
const li = 'flex flex-wrap items-center gap-x-3 gap-y-1 py-1.5 text-[14px]'

export function ContactDetail({ id }: { id: string }) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const c = useCmd<Contact>(ws, 'contact.get', { id })
  const life = useCmd<Lifecycle>(ws, 'contact.lifecycle', { id })
  const [editing, setEditing] = useState(false)
  const [dealing, setDealing] = useState(false)
  const [err, setErr] = useState<string>()
  if (c.error) return <p className="p-6 text-bad">{c.error.message}</p>
  if (!c.data) return <p className="p-6 text-muted">Loading...</p>
  const k = c.data
  const L = life.data
  const base = `/w/${ws}`
  const go = (f: () => Promise<string>) =>
    f().then(
      (to) => router.push(to),
      (x) => setErr((x as Error).message),
    )
  const newQuote = () =>
    go(async () => {
      const q = await cmd<{ id: string }>(ws, 'quote.create_draft', {
        space_id: spaces[0].id,
        contact_id: id,
        lines: [{ description: 'Services', unit_price_minor: 0, gst_rate_bp: 1800 }],
      })
      return `${base}/billing/quotes/${q.id}`
    })
  const newInvoice = () =>
    go(
      async () =>
        `${base}/billing/${await newDraft(ws, spaces[0].id, id, { description: 'Services', unit_price_minor: 0 })}`,
    )

  const deals = L?.deals ?? []
  const quotes = L?.quotes ?? []
  const invoices = L?.invoices ?? []
  const projects = L?.projects ?? []
  const payments = L?.payments ?? []
  const iNo = (i: Invoice) => `${i.kind === 'credit_note' ? 'Credit note' : 'Invoice'} ${i.number}`
  const timeline: Event[] = [
    ...deals.map((d) => ({
      at: d.created_at,
      kind: 'Deal',
      text: `Inquiry "${d.title}" created`,
      href: `${base}/crm/deals/${d.id}`,
    })),
    ...deals.flatMap((d) =>
      d.closed_at
        ? [
            {
              at: d.closed_at,
              kind: 'Deal',
              text: `Deal "${d.title}" ${d.stage}`,
              href: `${base}/crm/deals/${d.id}`,
            },
          ]
        : [],
    ),
    ...quotes.flatMap((q) => [
      ...(q.sent_at
        ? [
            {
              at: q.sent_at,
              kind: 'Quotation',
              text: `Quotation ${q.number} sent`,
              href: `${base}/billing/quotes/${q.id}`,
            },
          ]
        : []),
      ...(q.decided_at
        ? [
            {
              at: q.decided_at,
              kind: 'Quotation',
              text: `Quotation ${q.number} ${q.status === 'rejected' ? 'rejected' : 'accepted'}`,
              href: `${base}/billing/quotes/${q.id}`,
            },
          ]
        : []),
    ]),
    ...projects.map((p) => ({
      at: p.created_at,
      kind: 'Project',
      text: `Project ${p.key} "${p.title}" started`,
      href: `${base}/n/${p.id}`,
    })),
    ...invoices.flatMap((i) =>
      i.issued_at
        ? [{ at: i.issued_at, kind: 'Invoice', text: `${iNo(i)} issued`, href: `${base}/billing/${i.id}` }]
        : [],
    ),
    ...payments.map((p) => ({
      at: p.received_at,
      kind: 'Payment',
      text: `Payment ${money(p.amount_minor)} received for ${p.invoice_number ?? 'invoice'}`,
      href: `${base}/billing/${p.invoice_id}`,
    })),
  ].sort((a, b) => b.at.localeCompare(a.at))
  const money_ = L?.outstanding_minor != null
  const billed = invoices
    .filter((i) => i.kind === 'invoice' && i.status !== 'draft' && i.status !== 'cancelled')
    .reduce((n, i) => n + i.total_minor, 0)
  const received = payments.reduce((n, p) => n + p.amount_minor, 0)

  return (
    <div className="mx-auto grid max-w-5xl gap-4 px-4 py-6 sm:px-6 sm:py-8 md:grid-cols-[320px_1fr]">
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
      <div className="min-w-0 space-y-4">
        <fieldset className="flex flex-wrap gap-2">
          <legend className="sr-only">Quick actions</legend>
          <button
            type="button"
            className={btnPrimary}
            onClick={() => setDealing(true)}
            disabled={!spaces.length}
          >
            New deal
          </button>
          <button type="button" className={btn} onClick={newQuote} disabled={!spaces.length}>
            New quotation
          </button>
          <button type="button" className={btn} onClick={newInvoice} disabled={!spaces.length}>
            New invoice
          </button>
        </fieldset>
        <ErrorText error={err ?? life.error} />
        {money_ && (
          <section aria-label="Balance" className="grid grid-cols-3 gap-2">
            {[
              ['Outstanding', L?.outstanding_minor ?? 0],
              ['Invoiced (all time)', billed],
              ['Received (all time)', received],
            ].map(([t, v]) => (
              <div key={t} className="rounded-lg border border-line p-3">
                <div className="text-[12px] text-muted">{t}</div>
                <div
                  className={`text-[15px] font-semibold tabular-nums sm:text-lg ${t === 'Outstanding' && v ? 'text-bad' : ''}`}
                >
                  {money(Number(v))}
                </div>
              </div>
            ))}
          </section>
        )}
        <Section title="Deals" empty="No deals yet." n={deals.length}>
          {deals.map((d) => (
            <li key={d.id} className={li}>
              <Link href={`${base}/crm/deals/${d.id}`} className="font-medium hover:underline">
                {d.title}
              </Link>
              <span className="capitalize text-muted">{d.stage}</span>
              {money_ && <span className="ml-auto tabular-nums">{money(d.amount_minor)}</span>}
            </li>
          ))}
        </Section>
        {L?.quotes && (
          <Section title="Quotations" empty="No quotations yet." n={quotes.length}>
            {quotes.map((q) => (
              <li key={q.id} className={li}>
                <Link href={`${base}/billing/quotes/${q.id}`} className="font-medium hover:underline">
                  {q.number ?? 'Draft'}
                </Link>
                <Pill>{q.status}</Pill>
                {money_ && <span className="ml-auto tabular-nums">{money(q.total_minor)}</span>}
              </li>
            ))}
          </Section>
        )}
        {L?.projects && (
          <Section
            title="Projects"
            empty="No projects yet. Convert an accepted quotation to start one."
            n={projects.length}
          >
            {projects.map((p) => (
              <li key={p.id} className={li}>
                <Link href={`${base}/n/${p.id}`} className="font-medium hover:underline">
                  {p.key} {p.title}
                </Link>
                <span className="text-muted">{p.status}</span>
              </li>
            ))}
          </Section>
        )}
        {L?.invoices && (
          <Section title="Invoices" empty="No invoices yet." n={invoices.length}>
            {invoices.map((i) => (
              <li key={i.id} className={li}>
                <Link href={`${base}/billing/${i.id}`} className="font-medium hover:underline">
                  {i.number ?? 'Draft'}
                </Link>
                <StatusPill status={i.status} />
                <span className="ml-auto tabular-nums">{money(i.total_minor)}</span>
              </li>
            ))}
          </Section>
        )}
        {L?.payments && (
          <Section title="Payments received" empty="No payments yet." n={payments.length}>
            {payments.map((p) => (
              <li key={p.id} className={li}>
                <Link href={`${base}/billing/${p.invoice_id}`} className="font-medium hover:underline">
                  {p.invoice_number ?? 'Invoice'}
                </Link>
                <span className="text-muted">{p.method}</span>
                <span className="ml-auto tabular-nums">{money(p.amount_minor)}</span>
              </li>
            ))}
          </Section>
        )}
        <section aria-label="Timeline" className="rounded-lg border border-line p-4">
          <h2 className="mb-2 text-[14px] font-semibold">Timeline</h2>
          {timeline.length === 0 && (
            <p className="text-[13px] text-muted">
              Nothing has happened yet. Start with a deal or a quotation.
            </p>
          )}
          <ol className="space-y-1.5">
            {timeline.map((t) => (
              <li
                key={`${t.kind}${t.at}${t.text}`}
                className="flex flex-wrap justify-between gap-x-3 text-[14px]"
              >
                <span>
                  <span className="mr-2 text-[12px] text-muted">{t.kind}</span>
                  {t.href ? (
                    <Link href={t.href} className="hover:underline">
                      {t.text}
                    </Link>
                  ) : (
                    t.text
                  )}
                </span>
                <time dateTime={t.at} className="text-[12px] text-muted">
                  {ago(t.at)}
                </time>
              </li>
            ))}
          </ol>
        </section>
      </div>
      {dealing && (
        <DealForm
          stage="lead"
          contactId={id}
          onClose={() => setDealing(false)}
          onSaved={() => {
            setDealing(false)
            life.reload()
          }}
        />
      )}
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
