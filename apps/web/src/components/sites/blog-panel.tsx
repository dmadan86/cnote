'use client'
import { Plus, Sparkles } from 'lucide-react'
import { useEffect, useState } from 'react'
import { btn, btnPrimary, EmptyState, ErrorText, Pill } from '@/components/ui'
import { blogApi, type PostRow, type SiteData } from '@/lib/sites'

const when = (iso: string) =>
  new Date(iso).toLocaleString('en-IN', {
    day: 'numeric',
    month: 'short',
    hour: 'numeric',
    minute: '2-digit',
    timeZone: 'Asia/Kolkata',
  })

/** The Blog page of the page switcher: the post list in the left column (design UX section 3). */
export function BlogPanel({
  ws,
  siteId,
  data,
  openId,
  tick,
  onOpen,
  onWrite,
}: {
  ws: string
  siteId: string
  data: SiteData
  /** The open post: its id, 'new', or null. */
  openId: string | null
  /** Bumps when the editor saved, published or deleted, so the list reloads. */
  tick: number
  onOpen: (id: string | 'new' | null) => void
  /** "Write a post with AI": the builder switches to Chat with the sentence typed. */
  onWrite: () => void
}) {
  const [posts, setPosts] = useState<PostRow[]>()
  const [tab, setTab] = useState<'published' | 'draft'>('published')
  const [err, setErr] = useState<Error>()
  // biome-ignore lint/correctness/useExhaustiveDependencies: tick is the reload signal
  useEffect(() => {
    blogApi.list(ws, siteId).then(
      (r) => {
        setPosts(r)
        setErr(undefined)
      },
      (e) => setErr(e as Error),
    )
  }, [ws, siteId, tick])
  const pub = (posts ?? []).filter((p) => p.status === 'published')
  const drafts = (posts ?? []).filter((p) => p.status !== 'published')
  const list = tab === 'published' ? pub : drafts
  const clash = data.pages?.some((p) => p.slug === 'blog')
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-3 text-[13px]">
      <div className="flex items-center justify-between">
        <h2 className="text-[14px] font-semibold">Posts</h2>
        <button type="button" className={btnPrimary} disabled={clash} onClick={() => onOpen('new')}>
          <Plus size={14} aria-hidden="true" /> New post
        </button>
      </div>
      {clash && (
        <p role="alert" className="rounded-md bg-warn-bg px-2.5 py-1.5 text-warn">
          You already have a page called "blog". Rename that page (Pages, Rename) before you use the blog.
        </p>
      )}
      <div role="tablist" aria-label="Post status" className="flex gap-1">
        {(
          [
            ['published', `Published ${pub.length}`],
            ['draft', `Drafts ${drafts.length}`],
          ] as const
        ).map(([k, l]) => (
          <button
            key={k}
            type="button"
            role="tab"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={`rounded-md px-3 py-1 font-medium ${tab === k ? 'bg-hover' : 'text-muted hover:bg-hover'}`}
          >
            {l}
          </button>
        ))}
      </div>
      <ErrorText error={err} />
      {posts && !posts.length && !err && (
        <EmptyState compact icon={Sparkles} title="No posts yet. Share news, tips or offers.">
          Visitors see the blog once one post is published.
        </EmptyState>
      )}
      <ul className="space-y-1.5">
        {list.map((p) => (
          <li key={p.id}>
            <button
              type="button"
              aria-current={openId === p.id}
              onClick={() => onOpen(p.id)}
              className={`flex w-full flex-col gap-1 rounded-lg border p-2.5 text-left hover:bg-hover ${openId === p.id ? 'border-accent' : 'border-line'}`}
            >
              <span className="line-clamp-2 font-medium">{p.title || 'Untitled'}</span>
              <span className="flex flex-wrap items-center gap-1.5 text-[12px] text-muted">
                {p.status === 'published' ? (
                  <Pill tone={p.pending ? 'warn' : 'ok'}>
                    {p.pending ? 'Published · changes not live' : 'Published'}
                  </Pill>
                ) : (
                  <Pill>Draft</Pill>
                )}
                {p.ai && <span>by C Note AI</span>}
                <span>edited {when(p.updated_at)}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      <button type="button" className={btn} onClick={onWrite}>
        <Sparkles size={14} aria-hidden="true" /> Write a post with AI
      </button>
    </div>
  )
}
