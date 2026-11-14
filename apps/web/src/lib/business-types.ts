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

export const COMPANY_SIZES = ['1-10', '11-50', '51-200', '200+'] as const

/** Why each module is worth switching on; shown in step 2 of company setup. */
export const MODULE_WHY: Record<string, { label: string; why: string }> = {
  crm: { label: 'CRM', why: 'Track inquiries, deals and every customer in one place.' },
  billing: {
    label: 'Billing',
    why: 'Send quotations and GST invoices, record payments, see what is outstanding.',
  },
  projects: { label: 'Projects', why: 'Turn an approved quotation into tasks with owners and due dates.' },
  docs: { label: 'Docs', why: 'Write SOPs, scopes of work and meeting notes next to the work.' },
  whiteboard: { label: 'Whiteboard', why: 'Sketch layouts and plans together on a shared canvas.' },
}

/** "Other" has no preset: recommend the core four and keep the default deal stage names. */
export const defaultModules = (id?: string) =>
  BUSINESS_TYPES.find((b) => b.id === id)?.modules ?? ['docs', 'projects', 'crm', 'billing']

export const applyBusinessType = (
  ws: string,
  id: string,
  extra: { company_size?: string; modules?: string[] } = {},
) => {
  const t = BUSINESS_TYPES.find((b) => b.id === id)
  // "other" only records the choice (and any modules picked); deal stage labels stay as they are.
  return cmd(ws, 'workspace.update', {
    settings: {
      business_type: id,
      ...(extra.company_size ? { company_size: extra.company_size } : {}),
      ...(extra.modules || t ? { modules: extra.modules ?? t?.modules } : {}),
      ...(t ? { deal_stage_labels: t.labels } : {}),
    },
  })
}

export const saveCompanySize = (ws: string, company_size: string) =>
  cmd(ws, 'workspace.update', { settings: { company_size } })
