'use client'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import { Suspense, useEffect, useState } from 'react'
import { useShell } from '@/components/shell'
import { FreshnessPill } from '@/components/ui'
import { api, cmd } from '@/lib/api'

type Hit = { id: string; title: string; freshness: string; snippet: string }

function Results() {
  const { ws } = useShell()
  const q = useSearchParams().get('q') ?? ''
  const [data, setData] = useState<Hit[]>()
  const loading = !data && q.length > 0
  useEffect(() => {
    if (!q) return
    let live = true
    setData(undefined)
    // search.hybrid ships with the AI module; fall back to plain full-text where it is missing or fails.
    api<{ name: string }[]>('/api/commands')
      .then((l) => l.some((c) => c.name === 'search.hybrid'))
      .catch(() => false)
      .then((hy) =>
        (hy ? cmd<Hit[]>(ws, 'search.hybrid', { q, limit: 50 }) : Promise.reject()).catch(() =>
          cmd<Hit[]>(ws, 'search.notes', { q, limit: 50 }),
        ),
      )
      .then((d) => live && setData(d))
      .catch(() => live && setData([]))
    return () => {
      live = false
    }
  }, [ws, q])
  return (
    <div className="mx-auto max-w-3xl space-y-4 px-6 py-8">
      <h1 className="text-xl font-semibold">Results for "{q}"</h1>
      {loading && q && <p className="text-muted">Searching...</p>}
      {data?.length === 0 && <p className="text-muted">No pages match.</p>}
      <ul className="space-y-2">
        {data?.map((h) => (
          <li key={h.id} className="rounded-md border border-line p-3">
            <div className="flex items-center gap-2">
              <Link href={`/w/${ws}/n/${h.id}`} className="font-medium hover:underline">
                {h.title || 'Untitled'}
              </Link>
              <FreshnessPill freshness={h.freshness} verifyBy={null} />
            </div>
            <p className="mt-1 text-[13px] text-muted">{h.snippet.replace(/<[^>]+>/g, '')}</p>
          </li>
        ))}
      </ul>
    </div>
  )
}

export default function SearchPage() {
  return (
    <Suspense>
      <Results />
    </Suspense>
  )
}
