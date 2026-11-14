import { cmd } from '@/lib/api'

export const STAGE_KEYS = ['lead', 'qualified', 'proposal', 'negotiation', 'won', 'lost'] as const
type Labels = Record<(typeof STAGE_KEYS)[number], string>

export type BusinessType = { id: string; name: string; blurb: string; modules: string[]; labels: Labels }

const L = (a: string, b: string, c: string, d: string, e: string): Labels => ({
  lead: a,
  qualified: b,
  proposal: c,
  negotiation: d,
  won: e,
  lost: 'Lost',
})

/** Flow: inquiry -> quotation -> approval -> project/order -> invoice -> payment -> follow-up. Keys stay fixed (DB enum); only labels change. */
export const BUSINESS_TYPES: BusinessType[] = [
  {
    id: 'service',
    name: 'Service company',
    blurb: 'Installation, repair, agencies: quote, get approval, deliver, invoice.',
    modules: ['docs', 'projects', 'crm', 'billing'],
    labels: L('Inquiry', 'Qualified', 'Quotation sent', 'Approval', 'Won'),
  },
  {
    id: 'project',
    name: 'Project company',
    blurb: 'Contractors and EPC firms running site or milestone-based jobs.',
    modules: ['docs', 'projects', 'crm', 'billing'],
    labels: L('Tender / inquiry', 'Site visit done', 'Quotation sent', 'Approval', 'Project awarded'),
  },
  {
    id: 'trading',
    name: 'Trading company',
    blurb: 'Buy and sell goods: enquiries, quotes, orders, invoices.',
    modules: ['docs', 'crm', 'billing'],
    labels: L('Enquiry', 'Qualified', 'Quoted', 'Negotiating', 'Ordered'),
  },
  {
    id: 'manufacturing',
    name: 'Manufacturing',
    blurb: 'Make to order: quote, confirm the order, produce, invoice.',
    modules: ['docs', 'crm', 'billing'],
    labels: L('RFQ received', 'Specs confirmed', 'Quoted', 'Negotiating', 'Order confirmed'),
  },
  {
    id: 'recurring',
    name: 'Recurring services',
    blurb: 'Retainers, AMCs and subscriptions billed on a cycle.',
    modules: ['docs', 'crm', 'billing'],
    labels: L('Prospect', 'Qualified', 'Proposal sent', 'Negotiation', 'Subscribed'),
  },
  {
    id: 'professional',
    name: 'Professional firm',
    blurb: 'CA, legal, consulting: engagements, documents, client billing.',
    modules: ['docs', 'crm', 'billing', 'projects'],
    labels: L('Inquiry', 'Consultation held', 'Engagement proposed', 'Terms agreed', 'Engaged'),
  },
]

export const applyBusinessType = (ws: string, id: string) => {
  const t = BUSINESS_TYPES.find((b) => b.id === id)
  // "other" (skip) only records the choice; modules and labels stay as they are.
  return cmd(ws, 'workspace.update', {
    settings: t
      ? { business_type: id, modules: t.modules, deal_stage_labels: t.labels }
      : { business_type: id },
  })
}
