'use client'
import Link from 'next/link'
import { usePathname } from 'next/navigation'
import { useState } from 'react'
import { btn, btnPrimary, ErrorText, Modal } from '@/components/ui'
import { cmd } from '@/lib/api'

/** Section tabs shared by the CRM and Billing pages. */
export function SubNav({ label, items }: { label: string; items: [href: string, text: string][] }) {
  const path = usePathname()
  return (
    <nav aria-label={label} className="mb-4 flex gap-1 border-b border-line">
      {items.map(([href, text], i) => {
        const on = i === 0 ? path === href : path.startsWith(href)
        return (
          <Link
            key={href}
            href={href}
            aria-current={on ? 'page' : undefined}
            className="px-3 py-2 text-[13px] text-muted hover:text-fg aria-[current=page]:border-b-2 aria-[current=page]:border-accent aria-[current=page]:font-medium aria-[current=page]:text-fg"
          >
            {text}
          </Link>
        )
      })}
    </nav>
  )
}

/** Run a command. Agents get `confirmation_required`, a failure to surface. A held human action (`approval_required`)
 * is not a failure: it returns, and the document's banner shows who is asked. */
export async function run<T>(ws: string, name: string, input: unknown): Promise<T> {
  const r = await cmd<T & { confirmation_required?: boolean }>(ws, name, input)
  if (r && typeof r === 'object' && 'confirmation_required' in r && r.confirmation_required)
    throw new Error('This action needs approval in Settings > Confirmations.')
  if (r && typeof r === 'object' && 'approval_required' in r)
    window.dispatchEvent(new Event('approval-changed'))
  return r
}

/** Create a draft invoice with one starter line and return its id; the editor takes over from there. */
export async function newDraft(
  ws: string,
  spaceId: string,
  contactId: string,
  line: { description: string; unit_price_minor: number },
  branchId?: string,
) {
  const d = await run<{ id: string }>(ws, 'invoice.create_draft', {
    space_id: spaceId,
    contact_id: contactId,
    ...(branchId && { branch_id: branchId }),
    lines: [{ ...line, gst_rate_bp: 1800 }],
  })
  return d.id
}

/** Confirm-before-act dialog for money-moving commands. */
export function Confirm({
  title,
  body,
  action,
  onClose,
  onDone,
  children,
}: {
  title: string
  body: React.ReactNode
  action: string
  onClose: () => void
  onDone: () => Promise<void>
  children?: React.ReactNode
}) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  return (
    <Modal open title={title} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          setBusy(true)
          try {
            await onDone()
            onClose()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <div className="text-[14px]">{body}</div>
        {children}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy}>
            {action}
          </button>
        </div>
      </form>
    </Modal>
  )
}
