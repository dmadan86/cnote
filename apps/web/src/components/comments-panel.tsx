'use client'
import { useEffect, useRef, useState } from 'react'
import { Avatar, btn, btnPrimary, ErrorText, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago } from '@/lib/hooks'

export type Comment = {
  id: string
  author_name: string | null
  body: string
  created_at: string
  anchor_text: string | null
  anchor_prefix: string | null
  parent_id: string | null
  resolved_at: string | null
  resolved_by_name: string | null
}
export type Anchor = { anchor_text: string; anchor_prefix: string }

/** Open inline threads, for the editor to highlight. */
export const openThreads = (cs: Comment[] = []) =>
  cs
    .filter((c) => !c.parent_id && !c.resolved_at && c.anchor_text)
    .map((c) => ({ id: c.id, anchor_text: c.anchor_text as string, anchor_prefix: c.anchor_prefix }))

/** Page comments: inline threads (quoting page text) and page-level ones, with replies and resolve. */
export function CommentsTab({
  ws,
  noteId,
  comments,
  reload,
  draft,
  clearDraft,
  focus,
  setFocus,
  onPage,
}: {
  ws: string
  noteId: string
  comments: Comment[]
  reload: () => void
  draft?: Anchor
  clearDraft: () => void
  focus?: string
  setFocus: (id?: string) => void
  /** Whether a thread's quoted text is still on the page. */
  onPage: (c: Comment) => boolean
}) {
  const [err, setErr] = useState<string>()
  const top = comments.filter((c) => !c.parent_id)
  const replies = (id: string) => comments.filter((c) => c.parent_id === id)
  const open = top.filter((c) => !c.resolved_at)
  const resolved = top.filter((c) => c.resolved_at)

  // Focus the box when someone starts a comment from a selection.
  const draftBox = useRef<HTMLTextAreaElement>(null)
  useEffect(() => {
    if (draft) draftBox.current?.focus()
  }, [draft])

  useEffect(() => {
    if (focus)
      document.getElementById(`comment-${focus}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [focus])

  async function add(body: string, extra: Record<string, unknown> = {}) {
    try {
      const c = await cmd<Comment>(ws, 'comment.add', { note_id: noteId, body, ...extra })
      setErr(undefined)
      reload()
      return c
    } catch (x) {
      setErr((x as Error).message)
    }
  }

  const thread = (c: Comment) => (
    <li
      key={c.id}
      id={`comment-${c.id}`}
      aria-label={`Comment by ${c.author_name ?? 'Agent'}`}
      onClick={() => setFocus(c.id)}
      onKeyDown={(e) => e.key === 'Enter' && e.target === e.currentTarget && setFocus(c.id)}
      className={`rounded-md border p-2.5 ${focus === c.id ? 'border-accent ring-1 ring-accent' : 'border-line'}`}
    >
      {c.anchor_text && (
        <blockquote className="mb-1.5 border-l-2 border-warn pl-2 text-[12px] text-muted">
          <span className="line-clamp-2">{c.anchor_text}</span>
          {!c.resolved_at && !onPage(c) && <span className="block italic">Text no longer on the page</span>}
        </blockquote>
      )}
      {[c, ...replies(c.id)].map((m) => (
        <div key={m.id} className={m === c ? '' : 'mt-2 border-t border-line pt-2'}>
          <div className="flex items-center gap-2 text-[12px] text-muted">
            <Avatar name={m.author_name} size={18} /> {m.author_name ?? 'Agent'} · {ago(m.created_at)}
          </div>
          <p className="mt-1 whitespace-pre-wrap text-[13px]">{m.body}</p>
        </div>
      ))}
      {c.resolved_at ? (
        <div className="mt-2 flex items-center justify-between text-[12px] text-muted">
          <span>Resolved by {c.resolved_by_name ?? 'someone'}</span>
          <button
            type="button"
            className={btn}
            onClick={async () => {
              await cmd(ws, 'comment.resolve', { id: c.id, resolved: false })
              reload()
            }}
          >
            Reopen
          </button>
        </div>
      ) : (
        <form
          className="mt-2 flex gap-1.5"
          onSubmit={async (e) => {
            e.preventDefault()
            const form = e.currentTarget
            const body = String(new FormData(form).get('body') ?? '').trim()
            if (body && (await add(body, { parent_id: c.id }))) form.reset()
          }}
        >
          <input name="body" aria-label="Reply" placeholder="Reply..." className={`${inputCls} flex-1`} />
          <button
            type="button"
            className={btn}
            onClick={async () => {
              await cmd(ws, 'comment.resolve', { id: c.id })
              reload()
            }}
          >
            Resolve
          </button>
        </form>
      )}
    </li>
  )

  return (
    <div className="space-y-3">
      {draft && (
        <form
          aria-label="New inline comment"
          className="space-y-2 rounded-md border border-accent p-2.5"
          onSubmit={async (e) => {
            e.preventDefault()
            const body = String(new FormData(e.currentTarget).get('body') ?? '').trim()
            const c = body && (await add(body, draft))
            if (c) {
              clearDraft()
              setFocus(c.id)
            }
          }}
        >
          <blockquote className="border-l-2 border-warn pl-2 text-[12px] text-muted">
            <span className="line-clamp-3">{draft.anchor_text}</span>
          </blockquote>
          <textarea
            name="body"
            required
            rows={3}
            ref={draftBox}
            aria-label="Comment on selected text"
            className={inputCls}
          />
          <div className="flex gap-2">
            <button type="submit" className={btnPrimary}>
              Comment
            </button>
            <button type="button" className={btn} onClick={clearDraft}>
              Cancel
            </button>
          </div>
        </form>
      )}
      {open.length === 0 && !draft && (
        <p className="text-[13px] text-muted">No comments yet. Select text on the page to comment on it.</p>
      )}
      <ul aria-label="Comments" className="space-y-2">
        {open.map(thread)}
      </ul>
      {resolved.length > 0 && (
        <details className="text-[13px]">
          <summary className="cursor-pointer text-muted">Resolved ({resolved.length})</summary>
          <ul aria-label="Resolved comments" className="mt-2 space-y-2">
            {resolved.map(thread)}
          </ul>
        </details>
      )}
      <form
        onSubmit={async (e) => {
          e.preventDefault()
          const form = e.currentTarget
          const body = String(new FormData(form).get('body') ?? '').trim()
          if (body && (await add(body))) form.reset()
        }}
        className="space-y-2 pt-2"
      >
        <textarea
          name="body"
          required
          rows={3}
          aria-label="Add a comment"
          placeholder="Comment on the whole page..."
          className={inputCls}
        />
        <ErrorText error={err} />
        <button type="submit" className={btn}>
          Comment
        </button>
      </form>
    </div>
  )
}
