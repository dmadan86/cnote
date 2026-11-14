'use client'
import { MessageSquare } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { cmd } from '@/lib/api'
import { subscribeChat } from '@/lib/chat-stream'

/** Sidebar link with the total unread count, kept live by the chat stream (polling while it is down). */
export function ChatNavLink({ ws, active }: { ws: string; active: boolean }) {
  const [unread, setUnread] = useState(0)
  useEffect(() => {
    const load = () =>
      cmd<{ unread: number }[]>(ws, 'chat.channel_list')
        .then((l) => setUnread(l.reduce((n, c) => n + c.unread, 0)))
        .catch(() => {})
    load()
    let t: ReturnType<typeof setTimeout> | undefined
    const stop = subscribeChat(ws, (e) => {
      if (e.k === 'status' || e.k === 'reaction') return
      clearTimeout(t)
      t = setTimeout(load, 150)
    })
    window.addEventListener('focus', load)
    return () => {
      clearTimeout(t)
      stop()
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
