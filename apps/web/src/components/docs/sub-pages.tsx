'use client'
import { FileText } from 'lucide-react'
import Link from 'next/link'
import { useCallback, useEffect, useState } from 'react'
import { TREE_EVENT, type TreeRow, treeLevel } from '@/lib/docs'

// docs-parity-b section 14: a page that has children shows them under its body. Page chrome, not a block: nothing is
// stored in the page. note.tree filters by access in its query, so a restricted child never reaches this list. Long lists
// load a page at a time, like the sidebar.

export function SubPages({ ws, noteId, spaceId }: { ws: string; noteId: string; spaceId: string }) {
  const [kids, setKids] = useState<TreeRow[]>([])
  const [next, setNext] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const load = useCallback(
    (after?: string) =>
      treeLevel(ws, { space_id: spaceId, parent_id: noteId, after, limit: 100 }).then(
        (lv) => {
          setKids((k) => (after ? [...k, ...lv.rows] : lv.rows))
          setNext(lv.next)
        },
        () => {},
      ),
    [ws, spaceId, noteId],
  )
  useEffect(() => {
    setKids([])
    setNext(null)
    load()
    const again = () => load()
    window.addEventListener(TREE_EVENT, again)
    return () => window.removeEventListener(TREE_EVENT, again)
  }, [load])
  if (!kids.length) return null
  return (
    <nav aria-label="Sub-pages" className="mt-8 border-t border-line pt-4">
      <h2 className="mb-2 text-[13px] font-semibold">Sub-pages</h2>
      <ul className="text-[14px]">
        {kids.map((k) => (
          <li key={k.id}>
            <Link
              href={`/w/${ws}/n/${k.id}`}
              className="flex items-center gap-2 rounded px-1.5 py-1 hover:bg-hover"
            >
              <span className="w-4 text-center">
                {k.icon ?? <FileText size={14} className="inline text-muted" />}
              </span>
              {k.title || 'Untitled'}
            </Link>
          </li>
        ))}
      </ul>
      {next && (
        <button
          type="button"
          disabled={busy}
          className="mt-1 rounded px-1.5 py-1 text-[13px] text-muted hover:bg-hover hover:text-fg"
          onClick={async () => {
            setBusy(true)
            await load(next)
            setBusy(false)
          }}
        >
          Show more
        </button>
      )}
    </nav>
  )
}
