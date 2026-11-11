export type Space = { id: string; key: string; name: string; template: string; visibility: string }

export type Note = {
  id: string
  space_id: string
  title: string
  types: string[]
  content_md: string
  freshness: string
  updated_at: string
  version: number
}

export type Item = {
  id: string
  key: string
  title: string
  description: string
  status: string
  status_category: 'todo' | 'in_progress' | 'done'
  priority: string
  item_type: string
  space_id: string
  due_date: string | null
  updated_at: string
}
export type ItemDetail = Item & { transitions: { name: string; to: string }[] }

export type Comment = { id: string; author_name: string; body: string; created_at: string }

export type Contact = {
  id: string
  name: string
  kind: string
  email: string | null
  phone: string | null
  company: string | null
  gstin: string | null
  state_code: string | null
}

export type Deal = {
  id: string
  title: string
  stage: string
  amount_minor: number
  currency: string
  probability: number
  contact_id: string | null
  expected_close: string | null
}

export const DEAL_STAGES = ['lead', 'qualified', 'proposal', 'negotiation', 'won', 'lost']

export type Invoice = {
  id: string
  number: string | null
  kind: string
  status: string
  invoice_date: string
  due_date: string
  total_minor: number
  paid_minor: number
}
export type InvoiceDetail = Invoice & {
  contact_name: string
  subtotal_minor: number
  cgst_minor: number
  sgst_minor: number
  igst_minor: number
  outstanding_minor: number
  lines: {
    id: string
    description: string
    quantity: string
    unit_price_minor: number
    taxable_minor: number
  }[]
}

export type SearchHit = { id: string; title: string; snippet: string; types: string[]; freshness: string }

const ITEM_TYPES = ['task', 'bug', 'story', 'epic', 'subtask']
const RECORD_TYPES = [...ITEM_TYPES, 'deal', 'contact', 'invoice', 'credit_note']

/** Pages are notes that are not a typed record (item, deal, contact, invoice). */
export const isPage = (types: string[]) => !types.some((t) => RECORD_TYPES.includes(t))

/** Where a note id opens in the app, by its type. */
export function routeFor(types: string[], id: string): string {
  if (types.some((t) => ITEM_TYPES.includes(t))) return `/projects/${id}`
  if (types.includes('contact')) return `/crm/${id}`
  if (types.includes('invoice') || types.includes('credit_note')) return `/billing/${id}`
  return `/pages/${id}`
}
