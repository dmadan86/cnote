'use client'
import { Bell } from 'lucide-react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useCallback, useEffect, useRef, useState } from 'react'
import { EmptyState } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago } from '@/lib/hooks'

type Item = {
  id: string
  kind: string
  note_id: string | null
  thread_id?: string
  channel_id?: string
  note_title: string
  actor_name: string | null
  created_at: string
  read_at: string | null
}

const text = (n: Item) => {
  const who = n.actor_name ?? 'Someone'
  if (n.kind === 'approval_requested') return `${who} asked for your approval: ${n.note_title || 'Untitled'}`
  if (
    n.kind === 'approval_decided' ||
    n.kind === 'reminder' ||
    n.kind === 'quote_decided' ||
    n.kind === 'lead'
  )
    return n.note_title // the server wrote the sentence
  if (n.kind === 'comment') return `${who} commented on ${n.note_title || 'Untitled'}`
  if (n.kind === 'page_updated') return `${who} updated ${n.note_title || 'Untitled'}`
  if (n.kind === 'shared') return `${who} shared ${n.note_title || 'Untitled'} with you`
  if (n.kind === 'assigned') return `${who} assigned you a task in ${n.note_title || 'Untitled'}`
  if (n.kind === 'access_request') return `${who} asked for access to ${n.note_title || 'Untitled'}`
  if (n.kind === 'chat_mention') return `${who} mentioned you in ${n.note_title}`
  return `${who} mentioned you in ${n.note_title || 'Untitled'}`
}
const link = (ws: string, n: Item) =>
  n.kind === 'chat_mention'
    ? `/w/${ws}/chat?c=${n.channel_id}`
    : n.kind === 'reminder'
      ? `/w/${ws}`
      : n.kind.startsWith('approval')
        ? `/w/${ws}/approvals`
        : `/w/${ws}/n/${n.note_id}${n.thread_id ? `?thread=${n.thread_id}` : ''}` // the page focuses that comment thread

/** Bell with an unread badge (polled every 30s and on focus) and a panel of the latest notifications. */
export function NotificationBell({ ws }: { ws: string }) {
  const router = useRouter()
  const [count, setCount] = useState(0)
  const [items, setItems] = useState<Item[]>([])
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  const refresh = useCallback(async () => {
    try {
      setCount((await cmd<{ count: number }>(ws, 'notification.unread_count')).count)
      if (open) setItems(await cmd<Item[]>(ws, 'notification.list'))
    } catch {}
  }, [ws, open])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 30_000)
    window.addEventListener('focus', refresh)
    return () => {
      clearInterval(t)
      window.removeEventListener('focus', refresh)
    }
  }, [refresh])

  useEffect(() => {
    if (!open) return
    const out = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false)
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', out)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', out)
      document.removeEventListener('keydown', esc)
    }
  }, [open])

  const markRead = async (id?: string) => {
    await cmd(ws, 'notification.read', { id })
    setItems((l) =>
      l.map((n) => (id && n.id !== id ? n : { ...n, read_at: n.read_at ?? new Date().toISOString() })),
    )
    refresh()
  }

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-label="Notifications"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="relative rounded-md p-1.5 text-muted hover:bg-hover hover:text-fg"
      >
        <Bell size={16} />
        {count > 0 && (
          <span
            data-testid="notification-badge"
            className="absolute -top-0.5 -right-0.5 min-w-4 rounded-full bg-accent px-1 text-center text-[10px] font-bold leading-4 text-accent-fg"
          >
            {count}
          </span>
        )}
      </button>
      {open && (
        <div className="absolute right-0 z-30 mt-1 w-80 rounded-md border border-line bg-canvas shadow-lg">
          <div className="flex items-center justify-between border-b border-line px-3 py-2">
            <span className="text-[13px] font-semibold">Notifications</span>
            <Link
              href={`/w/${ws}/settings?tab=Notifications`}
              onClick={() => setOpen(false)}
              className="text-[12px] text-muted hover:text-fg"
            >
              Email settings
            </Link>
            <button
              type="button"
              disabled={count === 0}
              onClick={() => markRead()}
              className="text-[12px] text-muted hover:text-fg disabled:opacity-50"
            >
              Mark all read
            </button>
          </div>
          <ul aria-label="Notifications" className="max-h-96 overflow-y-auto p-1">
            {items.length === 0 && (
              <li>
                <EmptyState compact icon={Bell} title="Nothing yet">
                  Mentions, comments and approvals will show up here.
                </EmptyState>
              </li>
            )}
            {items.map((n) => (
              <li key={n.id} className="flex items-start">
                <button
                  type="button"
                  onClick={async () => {
                    if (!n.read_at) await markRead(n.id)
                    setOpen(false)
                    router.push(link(ws, n))
                  }}
                  className="flex w-full items-start gap-2 rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover"
                >
                  <span
                    aria-hidden
                    className={`mt-1.5 size-2 shrink-0 rounded-full ${n.read_at ? 'bg-transparent' : 'bg-accent'}`}
                  />
                  <span className="min-w-0">
                    <span className="block">{text(n)}</span>
                    <span className="block text-[12px] text-muted">{ago(n.created_at)}</span>
                  </span>
                </button>
                {n.kind === 'access_request' && n.note_id && (
                  <Link
                    href={`/w/${ws}/n/${n.note_id}?share=${encodeURIComponent(n.actor_name ?? '')}`}
                    onClick={() => setOpen(false)}
                    className="mt-1 mr-1 shrink-0 rounded-md border border-line px-2 py-0.5 text-[12px] hover:bg-hover"
                  >
                    Share
                  </Link>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
