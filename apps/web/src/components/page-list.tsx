'use client'
import Link from 'next/link'
import type { Note } from '@/lib/api'
import { ago } from '@/lib/hooks'
import { useShell } from './shell'
import { FreshnessPill } from './ui'

export function PageList({ pages, empty }: { pages: Note[]; empty: string }) {
  const { ws, spaces } = useShell()
  if (!pages.length) return <p className="py-3 text-muted">{empty}</p>
  return (
    <ul className="divide-y divide-line rounded-md border border-line">
      {pages.map((n) => (
        <li key={n.id}>
          <Link href={`/w/${ws}/n/${n.id}`} className="flex items-center gap-3 px-3 py-2 hover:bg-hover">
            <span className="rounded bg-hover px-1 text-[10px] font-bold text-muted">
              {spaces.find((s) => s.id === n.space_id)?.key}
            </span>
            <span className="min-w-0 flex-1 truncate font-medium">{n.title || 'Untitled'}</span>
            <FreshnessPill freshness={n.freshness} verifyBy={n.verify_by} />
            <span className="w-20 text-right text-[12px] text-muted">{ago(n.updated_at)}</span>
          </Link>
        </li>
      ))}
    </ul>
  )
}
