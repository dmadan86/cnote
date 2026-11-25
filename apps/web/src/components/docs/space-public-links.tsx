'use client'
import { Globe } from 'lucide-react'
import { useState } from 'react'
import { EmptyState, ErrorText } from '@/components/ui'
import { type PageLinkRow, pageLinkRevoke } from '@/lib/docs'
import { useCmd } from '@/lib/hooks'

/**
 * Space settings: the pages of this space with a public link that the person can read (13.2.3: a page they cannot read is
 * not listed or counted). The only bulk action is the space switch itself, which the settings page owns.
 */
export function SpacePublicLinks({ ws, spaceId }: { ws: string; spaceId: string }) {
  const { data, error, reload } = useCmd<PageLinkRow[]>(ws, 'page_link.list', { space_id: spaceId })
  const [err, setErr] = useState<string>()
  async function off(noteId: string) {
    setErr(undefined)
    try {
      await pageLinkRevoke(ws, noteId)
      reload()
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  if (error) return <ErrorText error={error} />
  if (!data) return <p className="text-[13px] text-muted">Loading...</p>
  if (data.length === 0)
    return (
      <EmptyState icon={Globe} title="No public pages" compact>
        Pages shared with a public link show up here.
      </EmptyState>
    )
  return (
    <section aria-label="Pages with a public link">
      <ul className="divide-y divide-line">
        {data.map((r) => (
          <li key={r.note_id} className="flex items-center gap-2 py-1.5 text-[13px]">
            <span className="min-w-0 flex-1 truncate">
              {r.title || 'Untitled'}{' '}
              <span className="text-muted">
                by {r.created_by_name ?? 'someone'}, {r.view_count} {r.view_count === 1 ? 'view' : 'views'}
              </span>
            </span>
            <button
              type="button"
              className="rounded px-2 py-0.5 text-[12px] hover:bg-hover"
              aria-label={`Turn off the public link of ${r.title || 'Untitled'}`}
              onClick={() => off(r.note_id)}
            >
              Turn off
            </button>
          </li>
        ))}
      </ul>
      <ErrorText error={err} />
    </section>
  )
}
