'use client'
import { FileText } from 'lucide-react'
import Link from 'next/link'
import type { Note } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

// docs-parity-b section 14: a page that has children shows them under its body. Page chrome, not a block: nothing is
// stored in the page. note.list filters by access in its query, so a restricted child never reaches this list.

/** Sibling order everywhere: position, then created_at, then id. */
export const bySiblingOrder = (a: Note, b: Note) =>
  (a.position ?? 0) - (b.position ?? 0) ||
  (a.created_at ?? '').localeCompare(b.created_at ?? '') ||
  a.id.localeCompare(b.id)

export function SubPages({ ws, noteId, spaceId }: { ws: string; noteId: string; spaceId: string }) {
  const { data } = useCmd<Note[]>(ws, 'note.list', { space_id: spaceId, parent_id: noteId, limit: 500 })
  const kids = data?.slice().sort(bySiblingOrder)
  if (!kids?.length) return null
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
    </nav>
  )
}
