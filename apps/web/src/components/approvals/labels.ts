export type Approval = {
  id: string
  command: string
  status: 'pending' | 'approved' | 'rejected' | 'expired' | 'cancelled'
  amount_minor: number | null
  note_id: string | null
  note_title: string | null
  requester_id: string
  requester_name: string | null
  approver_user_id: string | null
  approver_name: string | null
  reason: string | null
  escalated: boolean
  created_at: string
  decided_at: string | null
  can_decide: boolean
  can_cancel: boolean
  step: number
  steps: {
    mode: string
    min_amount_minor: number
    approvers: { kind: string; role?: string; user_id?: string }[]
  }[]
  decisions: { step: number; slot: number; user_id: string; name: string }[]
}

export const ACTION: Record<string, string> = {
  'quote.send': 'Send quote',
  'invoice.issue': 'Issue invoice',
  'bill.record': 'Record bill',
  'bill.pay': 'Pay bill',
  'invoice.write_off': 'Write off invoice',
  'receipt.refund': 'Refund advance',
  'expense.approve': 'Approve expense claim',
  'expense.reimburse': 'Reimburse expense claim',
}

/** Where the document lives, by the command that was held. */
export const docPath = (ws: string, a: Pick<Approval, 'command' | 'note_id'>) => {
  if (!a.note_id) return `/w/${ws}/approvals`
  const base = `/w/${ws}/billing`
  if (a.command.startsWith('quote.')) return `${base}/quotes/${a.note_id}`
  if (a.command.startsWith('receipt.')) return `${base}/receipts/${a.note_id}`
  if (a.command.startsWith('bill.')) return `${base}/bills/${a.note_id}`
  if (a.command.startsWith('expense.')) return `${base}/expenses`
  return `${base}/${a.note_id}`
}
