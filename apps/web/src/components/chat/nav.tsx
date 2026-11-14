'use client'
import { MessageSquare } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { cmd } from '@/lib/api'

/** Sidebar link with the total unread count. shortcut: polled every 20s, not pushed; move to a socket if it must be instant. */
export function ChatNavLink({ ws, active }: { ws: string; active: boolean }) {
  const [unread, setUnread] = useState(0)
  useEffect(() => {
    const load = () =>
      cmd<{ unread: number }[]>(ws, 'chat.channel_list')
        .then((l) => setUnread(l.reduce((n, c) => n + c.unread, 0)))
        .catch(() => {})
    load()
    const t = setInterval(load, 20_000)
    window.addEventListener('focus', load)
    return () => {
      clearInterval(t)
      window.removeEventListener('focus', load)
    }
  }, [ws])
  return (
    <Link
      href={`/w/${ws}/chat`}
      aria-label={unread ? `Chat, ${unread} unread` : 'Chat'}
      title="Chat"
      aria-current={active ? 'page' : undefined}
      className="relative rounded-md p-2 text-muted hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:text-fg"
    >
      <MessageSquare size={18} />
      {unread > 0 && (
        <span
          aria-hidden
          className="absolute top-0 right-0 min-w-4 rounded-full bg-accent px-1 text-center text-[10px] font-bold leading-4 text-accent-fg"
        >
          {unread > 99 ? '99+' : unread}
        </span>
      )}
    </Link>
  )
}
