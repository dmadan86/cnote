'use client'
import { useEffect, useState } from 'react'
import { noteViewers, type PageViewers } from '@/lib/docs'
import { ago } from '@/lib/hooks'

/** "Seen by N" in the page byline. Editors can open the list of names; everyone else sees the number. Nothing shows for a page nobody opened. */
export function Viewers({ ws, id }: { ws: string; id: string }) {
  const [v, setV] = useState<PageViewers>()
  const [open, setOpen] = useState(false)
  useEffect(() => {
    setV(undefined)
    setOpen(false)
    noteViewers(ws, id)
      .then(setV)
      .catch(() => {})
  }, [ws, id])
  if (!v || v.count === 0) return null
  const label = `Seen by ${v.count}`
  if (!v.viewers) return <span className="text-[12px] text-muted">{label}</span>
  return (
    <span className="relative">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="text-[12px] text-muted hover:text-fg"
      >
        {label}
      </button>
      {open && (
        <ul
          aria-label="Viewers"
          className="absolute left-0 z-20 mt-1 max-h-72 w-64 overflow-y-auto rounded-md border border-line bg-canvas p-1 shadow-lg"
        >
          {v.viewers.map((p) => (
            <li key={p.user_id} className="flex items-center justify-between gap-2 px-2 py-1 text-[13px]">
              <span className="truncate">{p.name}</span>
              <span className="shrink-0 text-[12px] text-muted">{ago(p.last_viewed_at)}</span>
            </li>
          ))}
        </ul>
      )}
    </span>
  )
}
