const inr = new Intl.NumberFormat('en-IN', { style: 'currency', currency: 'INR' })
/** Paise -> "₹1,18,000.00" (Indian digit grouping). */
export const money = (minor: number) => inr.format(minor / 100)

/** "1234.5" -> 123450 without float math; null when not a valid non-negative amount. */
export function parseMinor(s: string): number | null {
  const m = /^\s*(\d+)(?:\.(\d{1,2}))?\s*$/.exec(s)
  return m ? Number(m[1]) * 100 + Number((m[2] ?? '').padEnd(2, '0')) : null
}
export const toRupees = (minor: number) => (minor % 100 ? (minor / 100).toFixed(2) : String(minor / 100))

export const STATES: [string, string][] = [
  ['01', 'Jammu and Kashmir'],
  ['02', 'Himachal Pradesh'],
  ['03', 'Punjab'],
  ['04', 'Chandigarh'],
  ['05', 'Uttarakhand'],
  ['06', 'Haryana'],
  ['07', 'Delhi'],
  ['08', 'Rajasthan'],
  ['09', 'Uttar Pradesh'],
  ['10', 'Bihar'],
  ['11', 'Sikkim'],
  ['12', 'Arunachal Pradesh'],
  ['13', 'Nagaland'],
  ['14', 'Manipur'],
  ['15', 'Mizoram'],
  ['16', 'Tripura'],
  ['17', 'Meghalaya'],
  ['18', 'Assam'],
  ['19', 'West Bengal'],
  ['20', 'Jharkhand'],
  ['21', 'Odisha'],
  ['22', 'Chhattisgarh'],
  ['23', 'Madhya Pradesh'],
  ['24', 'Gujarat'],
  ['26', 'Dadra and Nagar Haveli and Daman and Diu'],
  ['27', 'Maharashtra'],
  ['29', 'Karnataka'],
  ['30', 'Goa'],
  ['31', 'Lakshadweep'],
  ['32', 'Kerala'],
  ['33', 'Tamil Nadu'],
  ['34', 'Puducherry'],
  ['35', 'Andaman and Nicobar Islands'],
  ['36', 'Telangana'],
  ['37', 'Andhra Pradesh'],
  ['38', 'Ladakh'],
  ['97', 'Other Territory'],
  ['99', 'Centre Jurisdiction'],
]
export const stateName = (c?: string | null) => STATES.find(([k]) => k === c)?.[1] ?? c ?? ''

export type Contact = {
  id: string
  space_id: string
  name: string
  kind: string
  email: string | null
  phone: string | null
  company: string | null
  gstin: string | null
  state_code: string | null
  billing_address: string | null
}

export type Deal = {
  id: string
  space_id: string
  title: string
  stage: string
  amount_minor: number
  contact_id: string | null
  expected_close: string | null
  probability: number
  closed_at: string | null
}

export type Invoice = {
  id: string
  kind: 'invoice' | 'credit_note'
  number: string | null
  status: string
  contact_id: string | null
  supplier_gstin: string | null
  buyer_gstin: string | null
  place_of_supply: string | null
  reverse_charge: boolean
  invoice_date: string | null
  due_date: string | null
  subtotal_minor: number
  cgst_minor: number
  sgst_minor: number
  igst_minor: number
  total_minor: number
  paid_minor: number
  credited_minor: number
  irn: string | null
  ack_no: string | null
  ack_date: string | null
  irn_cancelled_at: string | null
  issued_at: string | null
}

export type Line = {
  id: string
  position: number
  description: string
  hsn_sac: string | null
  quantity: string
  unit_price_minor: number
  gst_rate_bp: number
  taxable_minor: number
  item_note_id: string | null
  stock_item_id?: string
}

export type InvoiceDetail = Invoice & {
  contact_name: string | null
  outstanding_minor: number
  lines: Line[]
  payments: {
    id: string
    amount_minor: number
    method: string
    reference: string | null
    received_at: string
  }[]
}

export const STATUS_LABEL: Record<string, string> = {
  draft: 'Draft',
  issued: 'Issued',
  irn_generated: 'IRN generated',
  sent: 'Sent',
  partly_paid: 'Partly paid',
  paid: 'Paid',
  cancelled: 'Cancelled',
}
export const STATUS_TONE: Record<string, 'ok' | 'warn' | 'bad' | 'neutral'> = {
  paid: 'ok',
  partly_paid: 'warn',
  irn_generated: 'ok',
  cancelled: 'bad',
}
