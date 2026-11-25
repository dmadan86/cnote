'use client'
import { useEffect, useState } from 'react'
import { Avatar, btn, btnPrimary, ErrorText, inputCls } from '@/components/ui'
import { auth, cmd } from '@/lib/api'
import {
  type CommentB1,
  commentDelete,
  commentEdit,
  commentReact,
  REACTIONS,
  type Reaction,
} from '@/lib/docs'
import { ago } from '@/lib/hooks'
import { CommentBody, MentionField } from './mention-field'
import { personProps } from './person'

export type Comment = {
  id: string
  author_id: string | null
  author_name: string | null
  body: string
  created_at: string
  anchor_text: string | null
  anchor_prefix: string | null
  parent_id: string | null
  resolved_at: string | null
  resolved_by_name: string | null
} & Partial<CommentB1>
export type Anchor = { anchor_text: string; anchor_prefix: string }

/** Open inline threads, for the editor to highlight. */
export const openThreads = (cs: Comment[] = []) =>
  cs
    .filter((c) => !c.parent_id && !c.resolved_at && !c.deleted && c.anchor_text)
    .map((c) => ({ id: c.id, anchor_text: c.anchor_text as string, anchor_prefix: c.anchor_prefix }))

const MENTION = /\[@([^\]]+)\]\((\/w\/[0-9a-f-]{36}\/(?:people|agents)\/[0-9a-f-]{36})\)/g
const smallBtn = 'rounded px-1.5 py-0.5 text-[12px] text-muted hover:bg-hover hover:text-fg'

/** Reaction chips with counts and a "+" for the six; the tooltip says who, by count (names are not shared). */
function Reactions({
  ws,
  c,
  mutate,
}: {
  ws: string
  c: Comment
  mutate: (fn: () => Promise<unknown>) => void
}) {
  const [picking, setPicking] = useState(false)
  const rs = c.reactions ?? []
  const react = (emoji: Reaction, on: boolean) => mutate(() => commentReact(ws, c.id, emoji, on))
  return (
    <fieldset className="m-0 mt-1.5 flex min-w-0 flex-wrap items-center gap-1 border-0 p-0">
      <legend className="sr-only">Reactions</legend>
      {rs.map((r) => {
        const tipId = `tip-${c.id}-${r.emoji}`
        const others = r.count - (r.mine ? 1 : 0)
        const who = r.mine
          ? others > 0
            ? `You and ${others} ${others === 1 ? 'other' : 'others'}`
            : 'You'
          : `${r.count} ${r.count === 1 ? 'person' : 'people'}`
        return (
          <span key={r.emoji} className="group relative">
            <button
              type="button"
              aria-pressed={r.mine}
              aria-describedby={tipId}
              aria-label={`${r.emoji} ${r.count}${r.mine ? ', you reacted' : ''}`}
              onClick={() => react(r.emoji, !r.mine)}
              className={`rounded-full border px-2 py-0.5 text-[12px] ${r.mine ? 'border-accent bg-accent/10' : 'border-line'}`}
            >
              {r.emoji} {r.count}
            </button>
            <span
              id={tipId}
              role="tooltip"
              className="pointer-events-none absolute bottom-full left-0 z-10 mb-1 hidden whitespace-nowrap rounded bg-fg px-2 py-1 text-[11px] text-bg group-focus-within:block group-hover:block"
            >
              {who} reacted with {r.emoji}
            </span>
          </span>
        )
      })}
      <span className="relative">
        <button
          type="button"
          aria-label="Add reaction"
          aria-expanded={picking}
          onClick={() => setPicking(!picking)}
          className="rounded-full border border-line px-2 py-0.5 text-[12px] text-muted"
        >
          +
        </button>
        {picking && (
          <fieldset
            onKeyDown={(e) => e.key === 'Escape' && setPicking(false)}
            className="absolute left-0 top-full z-10 m-0 mt-1 flex min-w-0 gap-0.5 rounded-md border border-line bg-bg p-1 shadow"
          >
            <legend className="sr-only">Pick a reaction</legend>
            {REACTIONS.map((e) => (
              <button
                key={e}
                type="button"
                aria-label={`React with ${e}`}
                aria-pressed={rs.some((r) => r.emoji === e && r.mine)}
                className="rounded px-1 text-[15px] hover:bg-hover"
                onClick={() => {
                  setPicking(false)
                  react(e, !rs.some((r) => r.emoji === e && r.mine))
                }}
              >
                {e}
              </button>
            ))}
          </fieldset>
        )}
      </span>
    </fieldset>
  )
}

/** Body, edited marker, the "..." actions and an inline editor for one comment or reply. */
function Message({
  ws,
  c,
  me,
  admin,
  mutate,
}: {
  ws: string
  c: Comment
  me?: string
  admin: boolean
  mutate: (fn: () => Promise<unknown>) => void
}) {
  const [menu, setMenu] = useState(false)
  const [editing, setEditing] = useState(false)
  // The editor shows "@Name"; the stored text has links, kept here so they survive the edit.
  const links = [...c.body.matchAll(MENTION)].map((m) => [m[1], m[2]] as const)
  const plain = c.body.replace(MENTION, '@$1')
  if (c.deleted) return <p className="mt-1 text-[13px] italic text-muted">Comment deleted</p>
  const mine = !!me && c.author_id === me
  const canDelete = mine || admin
  return (
    <>
      {editing ? (
        <form
          className="mt-1 space-y-1.5"
          aria-label="Edit comment"
          onKeyDown={(e) => e.key === 'Escape' && setEditing(false)}
          onSubmit={(e) => {
            e.preventDefault()
            const typed = String(new FormData(e.currentTarget).get('body') ?? '').trim()
            // Put the stored mention links back for the names that are still in the text.
            const body = links.reduce(
              (t, [name, href]) => t.split(`@${name}`).join(`[@${name}](${href})`),
              typed,
            )
            if (body) mutate(() => commentEdit(ws, c.id, body))
            setEditing(false)
          }}
        >
          <textarea
            name="body"
            required
            rows={3}
            aria-label="Edit comment text"
            defaultValue={plain}
            className={inputCls}
          />
          <div className="flex gap-2">
            <button type="submit" className={btnPrimary}>
              Save
            </button>
            <button type="button" className={btn} onClick={() => setEditing(false)}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <p className="mt-1 whitespace-pre-wrap text-[13px]">
          <CommentBody body={c.body} />
        </p>
      )}
      {!editing && <Reactions ws={ws} c={c} mutate={mutate} />}
      {(mine || canDelete) && !editing && (
        <div className="relative mt-1">
          <button
            type="button"
            aria-label="Comment actions"
            aria-expanded={menu}
            className={smallBtn}
            onClick={() => setMenu(!menu)}
          >
            ...
          </button>
          {menu && (
            <span className="ml-1 inline-flex gap-1">
              {mine && (
                <button
                  type="button"
                  className={smallBtn}
                  onClick={() => {
                    setMenu(false)
                    setEditing(true)
                  }}
                >
                  Edit
                </button>
              )}
              {canDelete && (
                <button
                  type="button"
                  className={smallBtn}
                  onClick={() => {
                    setMenu(false)
                    if (window.confirm('Delete this comment? This cannot be undone.'))
                      mutate(() => commentDelete(ws, c.id))
                  }}
                >
                  Delete
                </button>
              )}
            </span>
          )}
        </div>
      )}
    </>
  )
}

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
  canModerate,
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
  /** The page owner or a space admin may delete anyone's comment (a workspace admin is found from the session). */
  canModerate?: boolean
}) {
  const [err, setErr] = useState<string>()
  const [me, setMe] = useState<{ id: string; admin: boolean }>()
  useEffect(() => {
    auth.me().then(
      (m) => {
        const role = m.workspaces.find((w) => w.id === ws || w.slug === ws)?.role
        setMe({ id: m.user.id, admin: role === 'owner' || role === 'admin' })
      },
      () => undefined,
    )
  }, [ws])
  // Edit, delete and react: run, show a refusal in the panel, reload.
  async function mutate(fn: () => Promise<unknown>) {
    try {
      await fn()
      setErr(undefined)
      reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const top = comments.filter((c) => !c.parent_id)
  const replies = (id: string) => comments.filter((c) => c.parent_id === id)
  const open = top.filter((c) => !c.resolved_at)
  const resolved = top.filter((c) => c.resolved_at)

  // Focus the box when someone starts a comment from a selection.
  useEffect(() => {
    if (draft) document.querySelector<HTMLElement>('[aria-label="Comment on selected text"]')?.focus()
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
            {!m.deleted && (
              <>
                <span {...personProps(m.author_id)} className="inline-flex items-center gap-2">
                  <Avatar name={m.author_name} size={18} /> {m.author_name ?? 'Agent'}
                </span>{' '}
                · {ago(m.created_at)}
                {m.edited_at && (
                  <span title={new Date(m.edited_at).toLocaleString()}> · edited {ago(m.edited_at)}</span>
                )}
              </>
            )}
          </div>
          <Message ws={ws} c={m} me={me?.id} admin={!!me?.admin || !!canModerate} mutate={mutate} />
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
          <div className="flex-1">
            <MentionField
              name="body"
              aria-label="Reply"
              placeholder="Reply... (@ to mention)"
              className={inputCls}
            />
          </div>
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
          <MentionField
            name="body"
            multiline
            required
            rows={3}
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
        <MentionField
          name="body"
          multiline
          required
          rows={3}
          aria-label="Add a comment"
          placeholder="Comment on the whole page... (@ to mention)"
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
