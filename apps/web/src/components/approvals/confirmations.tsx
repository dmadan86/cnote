'use client'
import { Bot, ShieldCheck } from 'lucide-react'
import { btn, btnPrimary, EmptyState, ErrorText } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

type Confirmation = { id: string; command: string; requested_by: string; created_at: string; input: unknown }

export function ConfirmationsTab({ ws }: { ws: string }) {
  const { data, error, reload } = useCmd<Confirmation[]>(ws, 'confirmation.list')
  const decide = async (id: string, approve: boolean) => {
    await cmd(ws, 'confirmation.decide', { id, approve })
    reload()
  }
  if (error)
    return (
      <div className="space-y-2">
        <ErrorText error={error} />
        <button type="button" className={btn} onClick={reload}>
          Try again
        </button>
      </div>
    )
  if (!data) return <div aria-busy="true" className="h-24 animate-pulse rounded-lg bg-hover" />
  if (data.length === 0)
    return (
      <div className="rounded-lg border border-line">
        <EmptyState icon={ShieldCheck} title="No pending confirmations">
          When an agent asks to send, pay or delete something, the request waits here for you to approve or
          reject.
        </EmptyState>
      </div>
    )
  return (
    <ul className="space-y-3">
      {data.map((c) => (
        <li key={c.id} className="rounded-lg border border-line">
          <div className="flex items-start gap-3 p-4">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-line bg-hover text-muted">
              <Bot size={16} aria-hidden />
            </span>
            <div className="min-w-0 flex-1">
              <code className="text-[13px] font-medium">{c.command}</code>
              <p className="mt-0.5 text-[12px] text-muted">
                Requested by {c.requested_by} · {ago(c.created_at)}
              </p>
            </div>
            <div className="flex shrink-0 gap-2">
              <button type="button" className={btn} onClick={() => decide(c.id, false)}>
                Reject
              </button>
              <button type="button" className={btnPrimary} onClick={() => decide(c.id, true)}>
                Approve
              </button>
            </div>
          </div>
          <details className="border-t border-line px-4 py-2 text-[12px]">
            <summary className="cursor-pointer text-muted hover:text-fg">What it will do</summary>
            <pre className="mt-2 overflow-x-auto rounded bg-side p-2">{JSON.stringify(c.input, null, 2)}</pre>
          </details>
        </li>
      ))}
    </ul>
  )
}
