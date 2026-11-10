'use client'
import { PanelRight, Plus } from 'lucide-react'
import Link from 'next/link'
import { use, useEffect, useState } from 'react'
import { DocActions, Proposals } from '@/components/docs/note-tools'
import { NoteEditor } from '@/components/note-editor'
import { useShell } from '@/components/shell'
import { Avatar, btn, ErrorText, FreshnessPill, inputCls } from '@/components/ui'
import { cmd, type NoteDetail } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

type Tab = 'relations' | 'history' | 'comments'
type Evt = { id: number; kind: string; actor_type: string; created_at: string }
type Comment = { id: string; author_name: string | null; body: string; created_at: string }

function Panel({ ws, note }: { ws: string; note: NoteDetail }) {
  const [tab, setTab] = useState<Tab>('comments')
  const history = useCmd<Evt[]>(ws, 'note.history', { id: note.id }, tab === 'history')
  const comments = useCmd<Comment[]>(ws, 'comment.list', { note_id: note.id }, tab === 'comments')
  const [err, setErr] = useState<string>()

  async function add(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const body = String(new FormData(form).get('body') ?? '')
    try {
      await cmd(ws, 'comment.add', { note_id: note.id, body })
      form.reset()
      setErr(undefined)
      comments.reload()
    } catch (x) {
      setErr((x as Error).message)
    }
  }

  return (
    <aside aria-label="Page details" className="w-80 shrink-0 border-l border-line bg-side">
      <div role="tablist" className="flex border-b border-line">
        {(['relations', 'history', 'comments'] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            onClick={() => setTab(t)}
            className="flex-1 px-3 py-2 text-[13px] capitalize text-muted hover:bg-hover aria-selected:border-b-2 aria-selected:border-accent aria-selected:font-medium aria-selected:text-fg"
          >
            {t}
          </button>
        ))}
      </div>
      <div role="tabpanel" className="space-y-2 p-3">
        {tab === 'relations' && (
          <>
            {note.relations.length === 0 && <p className="text-muted">No relations yet.</p>}
            {note.relations.map((r) => {
              const other = r.from_id === note.id ? r.to_id : r.from_id
              return (
                <Link
                  key={r.id}
                  href={`/w/${ws}/n/${other}`}
                  className="flex items-center gap-2 rounded px-2 py-1 hover:bg-hover"
                >
                  <span className="rounded bg-hover px-1 text-[11px] text-muted">{r.kind}</span>
                  <span className="truncate">{r.other_title ?? 'Untitled'}</span>
                </Link>
              )
            })}
          </>
        )}
        {tab === 'history' && (
          <>
            {history.data?.length === 0 && <p className="text-muted">No history yet.</p>}
            <ul className="space-y-1.5">
              {history.data?.map((h) => (
                <li key={h.id} className="flex justify-between text-[13px]">
                  <span>{h.kind}</span>
                  <span className="text-muted">
                    {h.actor_type} · {ago(h.created_at)}
                  </span>
                </li>
              ))}
            </ul>
          </>
        )}
        {tab === 'comments' && (
          <>
            <ul className="space-y-3">
              {comments.data?.map((c) => (
                <li key={c.id}>
                  <div className="flex items-center gap-2 text-[12px] text-muted">
                    <Avatar name={c.author_name} size={18} /> {c.author_name ?? 'Agent'} · {ago(c.created_at)}
                  </div>
                  <p className="mt-1 whitespace-pre-wrap">{c.body}</p>
                </li>
              ))}
            </ul>
            <form onSubmit={add} className="space-y-2 pt-2">
              <textarea
                name="body"
                required
                rows={3}
                aria-label="Add a comment"
                placeholder="Add a comment..."
                className={inputCls}
              />
              <ErrorText error={err} />
              <button type="submit" className={btn}>
                Comment
              </button>
            </form>
          </>
        )}
      </div>
    </aside>
  )
}

export default function NotePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws, members, reload, newPage } = useShell()
  const { data: note, error, reload: reloadNote } = useCmd<NoteDetail>(ws, 'note.get', { id })
  const [panel, setPanel] = useState(true)
  const [rev, setRev] = useState(0) // bumps when an approved proposal rewrites the content
  const [title, setTitle] = useState('')

  // Only on page change: later reloads must not clobber what the user is typing.
  // biome-ignore lint/correctness/useExhaustiveDependencies: keyed by id on purpose
  useEffect(() => {
    if (note) setTitle(note.title)
  }, [note?.id])

  if (error)
    return <p className="p-6 text-muted">This page does not exist or you do not have access to it.</p>
  if (!note) return null

  async function saveTitle() {
    if (!note || title.trim() === note.title) return
    await cmd(ws, 'note.update', { id, title })
    reload()
  }

  return (
    <div className="flex min-h-full">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 border-b border-line px-6 py-2">
          <FreshnessPill freshness={note.freshness} verifyBy={note.verify_by} />
          <span className="text-[12px] text-muted">Updated {ago(note.updated_at)}</span>
          <div className="ml-auto flex items-center gap-2">
            <DocActions
              ws={ws}
              note={note}
              members={members}
              onChange={() => {
                reloadNote()
                reload()
              }}
            />
            <button
              type="button"
              onClick={() => newPage({ spaceId: note.space_id, parentId: note.id })}
              className={btn}
            >
              <Plus size={14} /> Sub-page
            </button>
            <button
              type="button"
              aria-label="Toggle details panel"
              aria-pressed={panel}
              onClick={() => setPanel(!panel)}
              className={btn}
            >
              <PanelRight size={14} />
            </button>
          </div>
        </div>
        <Proposals
          ws={ws}
          note={note}
          onApplied={() => {
            setRev((r) => r + 1)
            reloadNote()
          }}
        />
        <article className="mx-auto max-w-[720px] px-6 py-10">
          <input
            aria-label="Page title"
            value={title}
            placeholder="Untitled"
            onChange={(e) => setTitle(e.target.value)}
            onBlur={saveTitle}
            onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
            className="mb-6 w-full bg-transparent text-3xl font-bold placeholder:text-muted"
          />
          <NoteEditor key={`${id}-${rev}`} ws={ws} id={id} initialMd={note.content_md} onSaved={reloadNote} />
        </article>
      </div>
      {panel && <Panel ws={ws} note={note} />}
    </div>
  )
}
