'use client'
import { FileText } from 'lucide-react'
import Link from 'next/link'
import { ago } from '@/lib/hooks'
import { useShell } from './shell'
import { EmptyState, FreshnessPill } from './ui'

/** What a list row needs: a note or a tree row. */
type Listed = {
  id: string
  space_id: string
  title: string
  freshness: string
  verify_by: string | null
  updated_at: string
}

export function PageList({ pages, empty }: { pages: Listed[]; empty: string }) {
  const { ws, spaces } = useShell()
  if (!pages.length) return <EmptyState compact icon={FileText} title={empty} />
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
