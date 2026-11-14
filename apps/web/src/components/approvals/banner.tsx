'use client'
import { useEffect } from 'react'
import { ago, useCmd } from '@/lib/hooks'
import { ACTION, type Approval } from './labels'

/** "Waiting for approval" or "Rejected: reason" on the document the request is about. */
export function ApprovalBanner({ ws, noteId }: { ws: string; noteId: string }) {
  const { data, reload } = useCmd<Approval[]>(ws, 'approval.list', { note_id: noteId })
  useEffect(() => {
    window.addEventListener('approval-changed', reload)
    return () => window.removeEventListener('approval-changed', reload)
  }, [reload])
  const a = data?.[0] // pending first, then newest
  if (!a || (a.status !== 'pending' && a.status !== 'rejected')) return null
  const waiting = a.status === 'pending'
  return (
    <div
      role="status"
      className={`rounded-md border px-3 py-2 text-[13px] ${waiting ? 'border-warn text-warn' : 'border-bad text-bad'}`}
    >
      {waiting
        ? `${ACTION[a.command] ?? a.command}: waiting for approval from ${a.approver_name ?? 'an admin'}, asked ${ago(a.created_at)} by ${a.requester_name ?? 'someone'}.`
        : `${ACTION[a.command] ?? a.command} was rejected${a.reason ? `: ${a.reason}` : ''}. Edit the draft and send it again.`}
    </div>
  )
}
