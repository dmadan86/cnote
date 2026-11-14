'use client'
import { ArrowLeft, FileText, Hash, Lock, Plus, Receipt, SquareCheck } from 'lucide-react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useCallback, useEffect, useRef, useState } from 'react'
import { MentionField } from '@/components/mention-field'
import { useShell } from '@/components/shell'
import { Avatar, btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { cmd } from '@/lib/api'

type Chan = {
  id: string
  kind: 'public' | 'private' | 'dm'
  name: string
  user_id: string | null
  unread: number
}
type Link_ = { note_id: string; title: string | null; types: string[] }
type Msg = {
  id: string
  user_id: string
  author: string
  body: string
  created_at: string
  edited_at: string | null
  deleted: boolean
  mine: boolean
  links: Link_[]
}
type Thread = { messages: Msg[]; has_more: boolean; last_read_at: string | null }

// shortcut: polling, not a socket. The Go server has no realtime channel and the collab socket is per page.
const POLL_MS = 4000

const TOKEN =
  /\[([^\]]+)\]\((\/w\/[0-9a-f-]{36}\/(?:people|n)\/[0-9a-f-]{36})\)|(\/w\/[0-9a-f-]{36}\/n\/[0-9a-f-]{36})/g

const timeOf = (iso: string) =>
  new Date(iso).toLocaleTimeString('en-IN', { hour: 'numeric', minute: '2-digit' })
const dayOf = (iso: string) => new Date(iso).toLocaleDateString('en-IN', { day: 'numeric', month: 'short' })

function Chip({ href, l }: { href: string; l?: Link_ }) {
  const Icon = l?.types.includes('invoice')
    ? Receipt
    : l?.types.some((t) => /task|item|bug|story/.test(t))
      ? SquareCheck
      : FileText
  return (
    <Link
      href={href}
      className="mx-0.5 inline-flex max-w-full items-center gap-1 rounded-md border border-line bg-hover px-1.5 py-0.5 align-bottom text-[12px] font-medium hover:underline"
    >
      <Icon size={12} aria-hidden />
      <span className="truncate">{l ? (l.title ?? 'Private page') : 'Page'}</span>
    </Link>
  )
}

/** Message text: @mentions as accent links, page links (notes, tasks, invoices) as chips. */
function Body({ m }: { m: Msg }) {
  const out: React.ReactNode[] = []
  let last = 0
  for (const t of m.body.matchAll(TOKEN)) {
    out.push(m.body.slice(last, t.index))
    const href = t[2] ?? t[3]
    const id = href.split('/').pop()
    if (href.includes('/people/'))
      out.push(
        <Link key={t.index} href={href} className="font-medium text-accent hover:underline">
          {t[1]}
        </Link>,
      )
    else out.push(<Chip key={t.index} href={href} l={m.links.find((x) => x.note_id === id)} />)
    last = (t.index ?? 0) + t[0].length
  }
  out.push(m.body.slice(last))
  return <p className="whitespace-pre-wrap break-words text-[14px]">{out}</p>
}

export function ChatPage() {
  const { ws, me, members } = useShell()
  const router = useRouter()
  const sel = useSearchParams().get('c')
  const [chans, setChans] = useState<Chan[]>()
  const [thread, setThread] = useState<Thread>()
  const [err, setErr] = useState<string>()
  const [dialog, setDialog] = useState<'channel' | 'dm' | null>(null)
  const [editing, setEditing] = useState<string>()
  const [confirmDel, setConfirmDel] = useState<string>()
  const [announce, setAnnounce] = useState('')
  const scroller = useRef<HTMLDivElement>(null)
  const stick = useRef(true) // follow the bottom while the reader is at it
  const divider = useRef<{ id: string; at: string | null }>({ id: '', at: null })
  const seen = useRef(0)
  const heading = useRef<HTMLHeadingElement>(null)
  const current = chans?.find((c) => c.id === sel)

  const loadChans = useCallback(
    () =>
      cmd<Chan[]>(ws, 'chat.channel_list')
        .then(setChans)
        .catch((e) => setErr(e.message)),
    [ws],
  )
  useEffect(() => {
    loadChans()
    const t = setInterval(() => document.visibilityState === 'visible' && loadChans(), POLL_MS)
    return () => clearInterval(t)
  }, [loadChans])

  // The open conversation: load, poll, and mark read when the tab is visible and something new arrived.
  useEffect(() => {
    if (!sel) return
    let live = true
    setThread(undefined)
    seen.current = 0
    stick.current = true
    const load = async () => {
      if (document.visibilityState !== 'visible' && seen.current) return
      try {
        const t = await cmd<Thread>(ws, 'chat.messages', { channel_id: sel })
        if (!live) return
        if (divider.current.id !== sel) divider.current = { id: sel, at: t.last_read_at }
        const fresh = t.messages.filter((m) => !m.mine).length
        if (seen.current && fresh > seen.current)
          setAnnounce(`${fresh - seen.current} new message${fresh - seen.current > 1 ? 's' : ''}`)
        if (fresh !== seen.current || !seen.current) {
          seen.current = fresh
          if (document.visibilityState === 'visible')
            cmd(ws, 'chat.read', { channel_id: sel }).then(
              () => {
                if (live) loadChans()
              },
              () => {},
            )
        }
        setThread(t)
        setErr(undefined)
      } catch (e) {
        if (live) setErr((e as Error).message)
      }
    }
    load()
    const t = setInterval(load, POLL_MS)
    return () => {
      live = false
      clearInterval(t)
    }
  }, [ws, sel, loadChans])

  const count = thread?.messages.length
  // biome-ignore lint/correctness/useExhaustiveDependencies: scroll when the message count changes
  useEffect(() => {
    const el = scroller.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [count, sel])

  const open = (id: string | null) => router.replace(id ? `/w/${ws}/chat?c=${id}` : `/w/${ws}/chat`)
  useEffect(() => {
    if (sel) heading.current?.focus()
  }, [sel])

  async function send(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const body = String(new FormData(form).get('body') ?? '').trim()
    if (!body || !sel) return
    setErr(undefined)
    try {
      await cmd(ws, 'chat.send', { channel_id: sel, body })
      form.reset()
      stick.current = true
      const t = await cmd<Thread>(ws, 'chat.messages', { channel_id: sel })
      setThread(t)
      loadChans()
    } catch (x) {
      setErr((x as Error).message) // the text stays in the box to retry
    }
  }

  async function act(name: string, input: object) {
    setErr(undefined)
    try {
      await cmd(ws, name, input)
      setEditing(undefined)
      setConfirmDel(undefined)
      setThread(await cmd<Thread>(ws, 'chat.messages', { channel_id: sel }))
    } catch (x) {
      setErr((x as Error).message)
    }
  }

  const row = (c: Chan) => (
    <li key={c.id}>
      <button
        type="button"
        onClick={() => open(c.id)}
        aria-current={c.id === sel ? 'true' : undefined}
        className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[14px] hover:bg-hover aria-[current=true]:bg-hover aria-[current=true]:font-semibold"
      >
        {c.kind === 'dm' ? (
          <Avatar name={c.name} size={20} />
        ) : c.kind === 'private' ? (
          <Lock size={14} aria-hidden />
        ) : (
          <Hash size={14} aria-hidden />
        )}
        <span className={`min-w-0 flex-1 truncate ${c.unread ? 'font-semibold' : ''}`}>{c.name}</span>
        {c.unread > 0 && (
          <span
            role="img"
            aria-label={`${c.unread} unread`}
            className="min-w-5 rounded-full bg-accent px-1.5 text-center text-[11px] font-bold leading-5 text-accent-fg"
          >
            {c.unread}
          </span>
        )}
      </button>
    </li>
  )

  let lastDay = ''
  let dividerShown = false
  return (
    <div className="mx-auto flex h-[calc(100dvh-3rem)] max-w-5xl md:gap-4 md:px-4 md:py-4">
      <aside
        aria-label="Conversations"
        className={`${sel ? 'hidden md:block' : 'block'} w-full shrink-0 overflow-y-auto p-3 md:w-64 md:p-0`}
      >
        <div className="flex items-center justify-between pb-1">
          <h1 className="text-[15px] font-semibold">Chat</h1>
        </div>
        <ErrorText error={sel ? undefined : err} />
        <div className="flex items-center justify-between pt-3 pb-1">
          <h2 className="text-[12px] font-semibold uppercase tracking-wide text-muted">Channels</h2>
          <button
            type="button"
            aria-label="New channel"
            onClick={() => setDialog('channel')}
            className="rounded p-1 text-muted hover:bg-hover"
          >
            <Plus size={14} />
          </button>
        </div>
        <ul aria-label="Channels">{(chans ?? []).filter((c) => c.kind !== 'dm').map(row)}</ul>
        {chans && !chans.some((c) => c.kind !== 'dm') && (
          <p className="px-2 text-[13px] text-muted">No channels yet. Create one to start.</p>
        )}
        <div className="flex items-center justify-between pt-4 pb-1">
          <h2 className="text-[12px] font-semibold uppercase tracking-wide text-muted">Direct messages</h2>
          <button
            type="button"
            aria-label="New direct message"
            onClick={() => setDialog('dm')}
            className="rounded p-1 text-muted hover:bg-hover"
          >
            <Plus size={14} />
          </button>
        </div>
        <ul aria-label="Direct messages">{(chans ?? []).filter((c) => c.kind === 'dm').map(row)}</ul>
      </aside>

      <section
        aria-label="Conversation"
        className={`${sel ? 'flex' : 'hidden md:flex'} min-w-0 flex-1 flex-col rounded-lg md:border md:border-line`}
      >
        {!sel || (chans && !current) ? (
          <p className="m-auto px-6 text-center text-[14px] text-muted">
            {sel ? 'This conversation is not available.' : 'Pick a channel or a person to start talking.'}
          </p>
        ) : (
          <>
            <header className="flex items-center gap-2 border-b border-line px-3 py-2">
              <button
                type="button"
                aria-label="Back to conversations"
                onClick={() => open(null)}
                className="rounded p-1 hover:bg-hover md:hidden"
              >
                <ArrowLeft size={18} />
              </button>
              <h2 ref={heading} tabIndex={-1} className="truncate text-[15px] font-semibold outline-none">
                {current ? (current.kind === 'public' ? `# ${current.name}` : current.name) : ''}
              </h2>
              {current?.kind === 'private' && <span className="text-[12px] text-muted">Private</span>}
              {current?.kind === 'private' && (
                <AddPerson ws={ws} channel={current.id} onDone={() => setErr(undefined)} />
              )}
            </header>
            <div
              ref={scroller}
              // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable log must be keyboard scrollable
              tabIndex={0}
              role="log"
              aria-label="Messages"
              onScroll={(e) => {
                const el = e.currentTarget
                stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80
              }}
              className="flex-1 space-y-1 overflow-y-auto px-3 py-3"
            >
              {thread?.has_more && (
                <button
                  type="button"
                  className="mx-auto block text-[12px] text-muted hover:text-fg"
                  onClick={async () => {
                    stick.current = false
                    const older = await cmd<Thread>(ws, 'chat.messages', {
                      channel_id: sel,
                      before: thread.messages[0].id,
                    })
                    setThread({
                      ...thread,
                      messages: [...older.messages, ...thread.messages],
                      has_more: older.has_more,
                    })
                  }}
                >
                  Load earlier messages
                </button>
              )}
              {thread && thread.messages.length === 0 && (
                <p className="py-8 text-center text-[13px] text-muted">No messages yet. Say hello.</p>
              )}
              {thread?.messages.map((m) => {
                const d = dayOf(m.created_at)
                const showDay = d !== lastDay
                lastDay = d
                const showNew =
                  !dividerShown && !m.mine && divider.current.at !== null && m.created_at > divider.current.at
                if (showNew) dividerShown = true
                return (
                  <div key={m.id}>
                    {showDay && <p className="py-2 text-center text-[11px] text-muted">{d}</p>}
                    {showNew && (
                      <div className="my-2 flex items-center gap-2 text-[11px] font-semibold text-accent">
                        <span className="h-px flex-1 bg-accent" />
                        New messages
                        <span className="h-px flex-1 bg-accent" />
                      </div>
                    )}
                    <article className="group flex gap-2 rounded px-1 py-1 focus-within:bg-hover hover:bg-hover">
                      <Avatar name={m.author} size={28} />
                      <div className="min-w-0 flex-1">
                        <p className="flex flex-wrap items-baseline gap-2 text-[13px]">
                          <span className="font-semibold">{m.author}</span>
                          <time dateTime={m.created_at} className="text-[11px] text-muted">
                            {timeOf(m.created_at)}
                          </time>
                          {m.edited_at && !m.deleted && (
                            <span className="text-[11px] text-muted">(edited)</span>
                          )}
                          {m.mine && !m.deleted && editing !== m.id && (
                            <span className="ml-auto flex gap-2 text-[12px] text-muted">
                              <button
                                type="button"
                                onClick={() => setEditing(m.id)}
                                className="hover:text-fg"
                                aria-label={`Edit message from ${timeOf(m.created_at)}`}
                              >
                                Edit
                              </button>
                              {confirmDel === m.id ? (
                                <button
                                  type="button"
                                  onClick={() => act('chat.delete', { id: m.id })}
                                  className="font-semibold text-red-600"
                                >
                                  Confirm delete
                                </button>
                              ) : (
                                <button
                                  type="button"
                                  onClick={() => setConfirmDel(m.id)}
                                  className="hover:text-fg"
                                  aria-label={`Delete message from ${timeOf(m.created_at)}`}
                                >
                                  Delete
                                </button>
                              )}
                            </span>
                          )}
                        </p>
                        {m.deleted ? (
                          <p className="text-[13px] italic text-muted">Message deleted</p>
                        ) : editing === m.id ? (
                          <form
                            onSubmit={(e) => {
                              e.preventDefault()
                              act('chat.edit', {
                                id: m.id,
                                body: String(new FormData(e.currentTarget).get('text') ?? ''),
                              })
                            }}
                            className="space-y-1"
                          >
                            <textarea
                              name="text"
                              defaultValue={m.body}
                              aria-label="Edit message"
                              rows={2}
                              className={inputCls}
                            />
                            <div className="flex gap-2">
                              <button type="submit" className={btnPrimary}>
                                Save
                              </button>
                              <button type="button" className={btn} onClick={() => setEditing(undefined)}>
                                Cancel
                              </button>
                            </div>
                          </form>
                        ) : (
                          <Body m={m} />
                        )}
                      </div>
                    </article>
                  </div>
                )
              })}
            </div>
            <div aria-live="polite" className="sr-only">
              {announce}
            </div>
            <div className="px-3 pt-1">
              <ErrorText error={err} />
            </div>
            <form
              onSubmit={send}
              onKeyDown={(e) => {
                // Enter sends, Shift+Enter is a new line; the @ picker (which handles Enter itself) and IME input are left alone.
                if (e.key === 'Enter' && !e.shiftKey && !e.defaultPrevented && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  e.currentTarget.requestSubmit()
                }
              }}
              className="flex items-end gap-2 border-t border-line p-3"
            >
              <div className="min-w-0 flex-1">
                <MentionField
                  key={sel}
                  name="body"
                  multiline
                  rows={2}
                  aria-label="Message"
                  placeholder={`Message ${current?.kind === 'public' ? `#${current.name}` : (current?.name ?? '')}. Enter to send, Shift+Enter for a new line.`}
                  className={inputCls}
                />
              </div>
              <button type="submit" className={btnPrimary}>
                Send
              </button>
            </form>
          </>
        )}
      </section>

      <NewChannel
        open={dialog === 'channel'}
        onClose={() => setDialog(null)}
        onMade={(id) => {
          setDialog(null)
          loadChans()
          open(id)
        }}
      />
      <NewDm
        open={dialog === 'dm'}
        onClose={() => setDialog(null)}
        meId={me.user.id}
        people={members}
        onMade={(id) => {
          setDialog(null)
          loadChans()
          open(id)
        }}
      />
    </div>
  )
}

function AddPerson({ ws, channel, onDone }: { ws: string; channel: string; onDone: () => void }) {
  const { members, me } = useShell()
  const [msg, setMsg] = useState<string>()
  return (
    <label className="ml-auto flex items-center gap-1 text-[12px] text-muted">
      <span className="sr-only">Add a person to this channel</span>
      <select
        aria-label="Add a person"
        value=""
        onChange={async (e) => {
          try {
            await cmd(ws, 'chat.member_add', { channel_id: channel, user_id: e.target.value })
            setMsg('Added')
            onDone()
          } catch (x) {
            setMsg((x as Error).message)
          }
        }}
        className="rounded border border-line bg-canvas px-1 py-0.5"
      >
        <option value="">Add person</option>
        {members
          .filter((m) => m.user_id !== me.user.id && m.role !== 'guest')
          .map((m) => (
            <option key={m.user_id} value={m.user_id}>
              {m.name}
            </option>
          ))}
      </select>
      <span role="status">{msg}</span>
    </label>
  )
}

function NewChannel({
  open,
  onClose,
  onMade,
}: {
  open: boolean
  onClose: () => void
  onMade: (id: string) => void
}) {
  const { ws, members, me } = useShell()
  const [err, setErr] = useState<string>()
  const [priv, setPriv] = useState(false)
  return (
    <Modal open={open} onClose={onClose} title="New channel">
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          try {
            const r = await cmd<{ id: string }>(ws, 'chat.channel_create', {
              name: String(f.get('name')),
              private: priv,
              member_ids: priv ? f.getAll('member') : [],
            })
            setErr(undefined)
            onMade(r.id)
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <h2 className="text-[15px] font-semibold">New channel</h2>
        <Field label="Name">
          {(id) => <input id={id} name="name" required maxLength={60} className={inputCls} />}
        </Field>
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={priv} onChange={(e) => setPriv(e.target.checked)} />
          Private: only the people you pick can see it
        </label>
        {priv && (
          <fieldset className="max-h-40 space-y-1 overflow-y-auto">
            <legend className="text-[12px] text-muted">Members</legend>
            {members
              .filter((m) => m.user_id !== me.user.id && m.role !== 'guest')
              .map((m) => (
                <label key={m.user_id} className="flex items-center gap-2 text-[13px]">
                  <input type="checkbox" name="member" value={m.user_id} />
                  {m.name}
                </label>
              ))}
          </fieldset>
        )}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Create channel
          </button>
        </div>
      </form>
    </Modal>
  )
}

function NewDm({
  open,
  onClose,
  onMade,
  people,
  meId,
}: {
  open: boolean
  onClose: () => void
  onMade: (id: string) => void
  people: { user_id: string; name: string; role: string }[]
  meId: string
}) {
  const { ws } = useShell()
  const [err, setErr] = useState<string>()
  return (
    <Modal open={open} onClose={onClose} title="New direct message">
      <h2 className="mb-2 text-[15px] font-semibold">New direct message</h2>
      <ul className="max-h-64 overflow-y-auto">
        {people
          .filter((p) => p.user_id !== meId && p.role !== 'guest')
          .map((p) => (
            <li key={p.user_id}>
              <button
                type="button"
                className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[14px] hover:bg-hover"
                onClick={async () => {
                  try {
                    onMade((await cmd<{ id: string }>(ws, 'chat.dm_open', { user_id: p.user_id })).id)
                  } catch (x) {
                    setErr((x as Error).message)
                  }
                }}
              >
                <Avatar name={p.name} size={22} />
                {p.name}
              </button>
            </li>
          ))}
      </ul>
      <ErrorText error={err} />
    </Modal>
  )
}
