import { cmd } from '@/lib/api'

export const STAGE_KEYS = ['lead', 'qualified', 'proposal', 'negotiation', 'won', 'lost'] as const
type Labels = Record<(typeof STAGE_KEYS)[number], string>

export type BusinessType = { id: string; name: string; blurb: string; labels: Labels }

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
    labels: L('Inquiry', 'Qualified', 'Quotation sent', 'Approval', 'Won'),
  },
  {
    id: 'project',
    name: 'Project company',
    blurb: 'Contractors and EPC firms running site or milestone-based jobs.',
    labels: L('Tender / inquiry', 'Site visit done', 'Quotation sent', 'Approval', 'Project awarded'),
  },
  {
    id: 'trading',
    name: 'Trading company',
    blurb: 'Buy and sell goods: enquiries, quotes, orders, invoices.',
    labels: L('Enquiry', 'Qualified', 'Quoted', 'Negotiating', 'Ordered'),
  },
  {
    id: 'manufacturing',
    name: 'Manufacturing',
    blurb: 'Make to order: quote, confirm the order, produce, invoice.',
    labels: L('RFQ received', 'Specs confirmed', 'Quoted', 'Negotiating', 'Order confirmed'),
  },
  {
    id: 'recurring',
    name: 'Recurring services',
    blurb: 'Retainers, AMCs and subscriptions billed on a cycle.',
    labels: L('Prospect', 'Qualified', 'Proposal sent', 'Negotiation', 'Subscribed'),
  },
  {
    id: 'professional',
    name: 'Professional firm',
    blurb: 'CA, legal, consulting: engagements, documents, client billing.',
    labels: L('Inquiry', 'Consultation held', 'Engagement proposed', 'Terms agreed', 'Engaged'),
  },
]

export const COMPANY_SIZES = ['1-10', '11-50', '51-200', '200+'] as const

export const applyBusinessType = (
  ws: string,
  id: string,
  extra: { company_size?: string } = {},
  signal?: AbortSignal,
) => {
  const t = BUSINESS_TYPES.find((b) => b.id === id)
  // A business type sets deal stage labels only; it never writes or hides modules. "other" only records the choice.
  return cmd(
    ws,
    'workspace.update',
    {
      settings: {
        business_type: id,
        ...(extra.company_size ? { company_size: extra.company_size } : {}),
        ...(t ? { deal_stage_labels: t.labels } : {}),
      },
    },
    signal,
  )
}

export const saveCompanySize = (ws: string, company_size: string) =>
  cmd(ws, 'workspace.update', { settings: { company_size } })
