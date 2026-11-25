'use client'
import { SearchX } from 'lucide-react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { Suspense, useEffect, useState } from 'react'
import { useShell } from '@/components/shell'
import { EmptyState, FreshnessPill } from '@/components/ui'
import { api, cmd } from '@/lib/api'

type Hit = { id: string; title: string; freshness: string; snippet: string }

const selectCls = 'rounded-md border border-line bg-canvas px-2 py-1 text-[13px]'
const DAY = 86400_000

function Results() {
  const { ws, spaces, members } = useShell()
  const router = useRouter()
  const params = useSearchParams()
  const get = (k: string) => params.get(k) ?? ''
  const q = get('q')
  const [space, owner, label, status, days] = ['space_id', 'owner_id', 'label', 'status', 'days'].map(get)
  const titleOnly = get('title_only') === '1'
  const filtered = !!(space || owner || label || status || days || titleOnly)
  const [data, setData] = useState<Hit[]>()
  const loading = !data && (q.length > 0 || filtered)
  // Filters live in the URL, so a search can be shared and a label chip can link to one.
  const setParam = (k: string, v: string) => {
    const p = new URLSearchParams(params.toString())
    if (v) p.set(k, v)
    else p.delete(k)
    router.replace(`/w/${ws}/search?${p.toString()}`)
  }
  useEffect(() => {
    if (!q && !filtered) return
    let live = true
    setData(undefined)
    const filters = {
      ...(space && { space_id: space }),
      ...(owner && { owner_id: owner }),
      ...(label && { label }),
      ...(status && { status }),
      ...(days && { updated_after: new Date(Date.now() - Number(days) * DAY).toISOString() }),
      ...(titleOnly && { title_only: true }),
    }
    // search.hybrid ships with the AI module and takes no filters; fall back to full-text where it is missing or fails.
    const plain = () => cmd<Hit[]>(ws, 'search.notes', { q, limit: 50, ...filters })
    ;(filtered || !q
      ? plain()
      : api<{ name: string }[]>('/api/commands')
          .then((l) => l.some((c) => c.name === 'search.hybrid'))
          .catch(() => false)
          .then((hy) =>
            (hy ? cmd<Hit[]>(ws, 'search.hybrid', { q, limit: 50 }) : Promise.reject()).catch(plain),
          )
    )
      .then((d) => live && setData(d))
      .catch(() => live && setData([]))
    return () => {
      live = false
    }
  }, [ws, q, filtered, space, owner, label, status, days, titleOnly])
  return (
    <div className="mx-auto max-w-3xl space-y-4 px-6 py-8">
      <h1 className="text-xl font-semibold">
        {q ? `Results for "${q}"` : label ? `Pages labelled "${label}"` : 'Search'}
      </h1>
      <form
        aria-label="Search filters"
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => e.preventDefault()}
      >
        <select
          aria-label="Space"
          className={selectCls}
          value={space}
          onChange={(e) => setParam('space_id', e.target.value)}
        >
          <option value="">All spaces</option>
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <select
          aria-label="Contributor"
          className={selectCls}
          value={owner}
          onChange={(e) => setParam('owner_id', e.target.value)}
        >
          <option value="">Anyone</option>
          {members.map((m) => (
            <option key={m.user_id} value={m.user_id}>
              {m.name}
            </option>
          ))}
        </select>
        <select
          aria-label="Status"
          className={selectCls}
          value={status}
          onChange={(e) => setParam('status', e.target.value)}
        >
          <option value="">Any status</option>
          <option value="draft">Draft</option>
          <option value="in_review">In review</option>
          <option value="final">Final</option>
          <option value="verified">Verified</option>
        </select>
        <select
          aria-label="Updated"
          className={selectCls}
          value={days}
          onChange={(e) => setParam('days', e.target.value)}
        >
          <option value="">Any time</option>
          <option value="7">Last 7 days</option>
          <option value="30">Last 30 days</option>
          <option value="90">Last 90 days</option>
        </select>
        <input
          aria-label="Label"
          placeholder="Label"
          defaultValue={label}
          key={label}
          onBlur={(e) =>
            e.target.value.trim().toLowerCase() !== label &&
            setParam('label', e.target.value.trim().toLowerCase())
          }
          onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
          className={`${selectCls} w-28`}
        />
        <label className="flex items-center gap-1.5 text-[13px]">
          <input
            type="checkbox"
            checked={titleOnly}
            onChange={(e) => setParam('title_only', e.target.checked ? '1' : '')}
          />{' '}
          Title only
        </label>
      </form>
      {loading && <p className="text-muted">Searching...</p>}
      {data?.length === 0 && (
        <EmptyState icon={SearchX} title="No pages match">
          Check the spelling, try fewer words or clear a filter.
        </EmptyState>
      )}
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
