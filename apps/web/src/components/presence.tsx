'use client'
import { useEffect, useState } from 'react'
import type { WebsocketProvider } from 'y-websocket'

// Readable on light and dark backgrounds; also used for each person's caret and selection.
const COLORS = ['#dc2626', '#ea580c', '#ca8a04', '#16a34a', '#0891b2', '#2563eb', '#9333ea', '#db2777']

/** Stable colour per user, so someone keeps the same caret colour across pages and sessions. */
export function userColor(id: string) {
  let h = 0
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return COLORS[h % COLORS.length]
}

type Peer = { id: string; name: string; color: string }

/** Who else has this page open right now (from Yjs awareness; one entry per person, however many tabs). */
export function Presence({ provider, selfId }: { provider: WebsocketProvider; selfId: string }) {
  const [peers, setPeers] = useState<Peer[]>([])
  useEffect(() => {
    const aw = provider.awareness
    const update = () => {
      const seen = new Map<string, Peer>()
      for (const [client, st] of aw.getStates()) {
        const u = (st as { user?: Partial<Peer> }).user
        if (client === aw.clientID || !u?.id || !u.name || u.id === selfId) continue
        seen.set(u.id, { id: u.id, name: u.name, color: u.color ?? '#6b7280' })
      }
      setPeers([...seen.values()])
    }
    update()
    aw.on('change', update)
    return () => aw.off('change', update)
  }, [provider, selfId])
  if (!peers.length) return null
  return (
    <ul aria-label="Also editing" className="mb-3 flex items-center gap-1.5 text-[12px] text-muted">
      {peers.map((p) => (
        <li
          key={p.id}
          title={`${p.name} is editing`}
          className="inline-flex items-center gap-1 rounded-full border border-line py-0.5 pr-2 pl-0.5"
        >
          <span
            aria-hidden
            style={{ background: p.color }}
            className="grid size-5 place-items-center rounded-full text-[10px] font-semibold text-white"
          >
            {p.name.slice(0, 1).toUpperCase()}
          </span>
          {p.name}
        </li>
      ))}
      <li className="sr-only">{peers.length === 1 ? 'is' : 'are'} also on this page</li>
    </ul>
  )
}
