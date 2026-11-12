'use client'
import type { Editor } from '@tiptap/react'
import { PanelRight, Plus } from 'lucide-react'
import Link from 'next/link'
import { use, useEffect, useMemo, useRef, useState } from 'react'
import { type Anchor, type Comment, CommentsTab, openThreads } from '@/components/comments-panel'
import { DocActions, Proposals } from '@/components/docs/note-tools'
import { locate } from '@/components/editor/inline-comments'
import { NoteEditor } from '@/components/note-editor'
import { ShareButton } from '@/components/share/share-dialog'
import { useShell } from '@/components/shell'
import { InsertField } from '@/components/templates/fields'
import { SaveAsTemplate } from '@/components/templates/save-template'
import { btn, FreshnessPill } from '@/components/ui'
import { activity, activityText, type Evt } from '@/lib/activity'
import { cmd, type NoteDetail } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

type Tab = 'relations' | 'history' | 'comments'

function Panel({
  ws,
  note,
  tab,
  setTab,
  comments,
}: {
  ws: string
  note: NoteDetail
  tab: Tab
  setTab: (t: Tab) => void
  comments: Omit<React.ComponentProps<typeof CommentsTab>, 'ws' | 'noteId'>
}) {
  const history = useCmd<Evt[]>(ws, 'note.history', { id: note.id }, tab === 'history')

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
              {activity(history.data).map((a) => (
                <li key={a.id} className="flex justify-between gap-3 text-[13px]">
                  <span>{activityText(a)}</span>
                  <span className="shrink-0 text-muted">{ago(a.at)}</span>
                </li>
              ))}
            </ul>
          </>
        )}
        {tab === 'comments' && <CommentsTab ws={ws} noteId={note.id} {...comments} />}
      </div>
    </aside>
  )
}

export default function NotePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws, members, reload, newPage } = useShell()
  const { data: note, error, reload: reloadNote } = useCmd<NoteDetail>(ws, 'note.get', { id })
  const [panel, setPanel] = useState(true)
  const [tab, setTab] = useState<Tab>('comments')
  const comments = useCmd<Comment[]>(ws, 'comment.list', { note_id: id })
  const [focus, setFocus] = useState<string>()
  const [draft, setDraft] = useState<Anchor>()
  const threads = useMemo(() => openThreads(comments.data), [comments.data])
  const showComments = () => {
    setPanel(true)
    setTab('comments')
  }
  const [rev, setRev] = useState(0) // bumps when an approved proposal rewrites the content
  const [title, setTitle] = useState('')
  const editorRef = useRef<Editor | null>(null)

  // Only on page change: later reloads must not clobber what the user is typing.
  // biome-ignore lint/correctness/useExhaustiveDependencies: keyed by id on purpose
  useEffect(() => {
    if (note) setTitle(note.title)
  }, [note?.id])

  if (error)
    return <p className="p-6 text-muted">This page does not exist or you do not have access to it.</p>
  if (!note) return null

  const canEdit = note.level === 'edit'

  async function saveTitle() {
    if (!note || !canEdit || title.trim() === note.title) return
    await cmd(ws, 'note.update', { id, title })
    reload()
  }

  return (
    <div className="flex min-h-full">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-2">
          <FreshnessPill freshness={note.freshness} verifyBy={note.verify_by} />
          <span className="text-[12px] text-muted">Updated {ago(note.updated_at)}</span>
          {/* Wraps instead of clipping on narrow screens. */}
          <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
            <DocActions
              ws={ws}
              note={note}
              members={members}
              onChange={() => {
                reloadNote()
                reload()
              }}
            />
            {note.types.length === 0 && (
              <>
                <InsertField
                  ws={ws}
                  onInsert={(t) => editorRef.current?.chain().focus().insertContent(t).run()}
                />
                <SaveAsTemplate ws={ws} noteId={id} title={note.title} />
              </>
            )}
            <ShareButton
              ws={ws}
              noteId={id}
              restriction={note.restriction}
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
        {!canEdit && (
          <p role="status" className="border-b border-line bg-hover px-6 py-1.5 text-[13px] text-muted">
            You can view this page but not edit it.
          </p>
        )}
        <article className="mx-auto max-w-[720px] px-6 py-10">
          <input
            aria-label="Page title"
            value={title}
            placeholder="Untitled"
            onChange={(e) => setTitle(e.target.value)}
            readOnly={!canEdit}
            onBlur={saveTitle}
            onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
            className="mb-6 w-full bg-transparent text-3xl font-bold placeholder:text-muted"
          />
          <NoteEditor
            key={`${id}-${rev}`}
            ws={ws}
            id={id}
            initialMd={note.content_md}
            editable={canEdit}
            onSaved={reloadNote}
            editorRef={editorRef}
            comments={{
              threads,
              onOpen: (cid) => {
                showComments()
                setFocus(cid)
              },
              onNew: (a) => {
                showComments()
                setDraft(a)
              },
            }}
          />
        </article>
      </div>
      {panel && (
        <Panel
          ws={ws}
          note={note}
          tab={tab}
          setTab={setTab}
          comments={{
            comments: comments.data ?? [],
            reload: comments.reload,
            draft,
            clearDraft: () => setDraft(undefined),
            focus,
            setFocus,
            onPage: (c) => {
              const doc = editorRef.current?.state.doc
              return (
                !doc ||
                !c.anchor_text ||
                !!locate(doc, { id: c.id, anchor_text: c.anchor_text, anchor_prefix: c.anchor_prefix })
              )
            },
          }}
        />
      )}
    </div>
  )
}
