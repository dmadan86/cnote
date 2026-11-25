'use client'
import { Eye, EyeOff } from 'lucide-react'
import { useEffect, useState } from 'react'
import { MINE_EVENT, noteMine, noteWatch } from '@/lib/docs'
import { btn } from './ui'

/** Watch or stop watching a page: watchers get a notification for comments and changes. A toggle button, not a link. */
export function WatchButton({ ws, noteId }: { ws: string; noteId: string }) {
  const [on, setOn] = useState<boolean>()
  const [err, setErr] = useState(false)
  useEffect(() => {
    let live = true
    // shortcut: initial state comes from the watching list (at most 100 pages); note.get has no watching flag yet
    noteMine(ws, 'watching', 100)
      .then((l) => live && setOn(l.some((p) => p.id === noteId)))
      .catch(() => live && setOn(false))
    return () => {
      live = false
    }
  }, [ws, noteId])
  async function toggle() {
    setErr(false)
    try {
      setOn((await noteWatch(ws, noteId, !on)).watching)
      window.dispatchEvent(new Event(MINE_EVENT))
    } catch {
      setErr(true)
    }
  }
  return (
    <>
      <button
        type="button"
        aria-pressed={on === true}
        disabled={on === undefined}
        onClick={toggle}
        title={on ? 'Stop watching this page' : 'Get notified about comments and changes'}
        className={`${btn} inline-flex items-center gap-1.5`}
      >
        {on ? <EyeOff size={14} aria-hidden /> : <Eye size={14} aria-hidden />}
        {on ? 'Watching' : 'Watch'}
      </button>
      {err && (
        <span role="alert" className="text-[12px] text-bad">
          Could not change watching
        </span>
      )}
    </>
  )
}
