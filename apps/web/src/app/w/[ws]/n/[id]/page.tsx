'use client'
import type { Editor } from '@tiptap/react'
import { PanelRight, Plus } from 'lucide-react'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import { Suspense, use, useEffect, useMemo, useRef, useState } from 'react'
import { type Anchor, type Comment, CommentsTab, openThreads } from '@/components/comments-panel'
import { AttachmentsPanel } from '@/components/docs/attachments-panel'
import { Cover } from '@/components/docs/cover'
import { LabelChips } from '@/components/docs/labels'
import { DocActions, Proposals } from '@/components/docs/note-tools'
import { PageMenuButton } from '@/components/docs/page-actions'
import {
  ArchiveButton,
  EmojiPicker,
  ExportMenu,
  StarButton,
  StatusMenu,
  useRecordViewed,
  WideToggle,
} from '@/components/docs/page-header'
import { RequestAccess } from '@/components/docs/request-access'
import { SubPages } from '@/components/docs/sub-pages'
import { countSubtree } from '@/components/docs/tree-loader'
import { Viewers } from '@/components/docs/viewers'
import { locate } from '@/components/editor/inline-comments'
import { NoteEditor } from '@/components/note-editor'
import { personProps } from '@/components/person'
import { ShareButton } from '@/components/share/share-dialog'
import { useShell } from '@/components/shell'
import { InsertField } from '@/components/templates/fields'
import { SaveAsTemplate } from '@/components/templates/save-template'
import { btn, FreshnessPill } from '@/components/ui'
import { VersionsTab } from '@/components/versions-panel'
import { WatchButton } from '@/components/watch-button'
import { activity, activityRest, type Evt } from '@/lib/activity'
import { cmd, type NoteDetail } from '@/lib/api'
import { type NoteChrome, type SpaceB1, STATUS_LABEL, treeLevel } from '@/lib/docs'
import { ago, useCmd } from '@/lib/hooks'

type Tab = 'relations' | 'history' | 'versions' | 'comments'

function Panel({
  ws,
  note,
  tab,
  setTab,
  comments,
  canEdit,
  onRestored,
}: {
  ws: string
  note: NoteDetail
  tab: Tab
  setTab: (t: Tab) => void
  canEdit: boolean
  onRestored: () => void
  comments: Omit<React.ComponentProps<typeof CommentsTab>, 'ws' | 'noteId'>
}) {
  const history = useCmd<Evt[]>(ws, 'note.history', { id: note.id }, tab === 'history')

  return (
    <aside aria-label="Page details" className="w-80 shrink-0 border-l border-line bg-side">
      <div role="tablist" className="flex border-b border-line">
        {(['relations', 'history', 'versions', 'comments'] as Tab[]).map((t) => (
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
                  <span>
                    <span {...personProps(a.userId)}>{a.who}</span> {activityRest(a)}
                  </span>
                  <span className="shrink-0 text-muted">{ago(a.at)}</span>
                </li>
              ))}
            </ul>
          </>
        )}
        {tab === 'versions' && <VersionsTab ws={ws} note={note} canEdit={canEdit} onRestored={onRestored} />}
        {tab === 'comments' && <CommentsTab ws={ws} noteId={note.id} {...comments} />}
      </div>
    </aside>
  )
}

/** Live sub-pages under a page, for the Archive confirmation: the tree knows whether there are any, then they are counted. */
function useSubPageCount(ws: string, id: string, spaceId: string | undefined, known: boolean | undefined) {
  const [count, setCount] = useState(0)
  useEffect(() => {
    if (!spaceId || known === false) return setCount(0)
    let live = true
    treeLevel(ws, { space_id: spaceId, parent_id: id, limit: 1 })
      .then((lv) => (lv.rows.length ? countSubtree(ws, spaceId, id) : { total: 1 }))
      .then((r) => live && setCount(r.total - 1))
      .catch(() => live && setCount(0))
    return () => {
      live = false
    }
  }, [ws, id, spaceId, known])
  return count
}

function NotePageInner({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws, me, spaces, members, tree, reload, newPage } = useShell()
  const {
    data: note,
    error,
    reload: reloadNote,
  } = useCmd<NoteDetail & NoteChrome & { labels: string[] }>(ws, 'note.get', { id })
  useRecordViewed(ws, id)
  const [panel, setPanel] = useState(true)
  const [tab, setTab] = useState<Tab>('comments')
  const comments = useCmd<Comment[]>(ws, 'comment.list', { note_id: id })
  const [focus, setFocus] = useState<string>()
  // A notification links here with ?thread=<comment id>: open the comments and focus that thread.
  const thread = useSearchParams().get('thread')
  // biome-ignore lint/correctness/useExhaustiveDependencies: showComments only sets state
  useEffect(() => {
    if (!thread) return
    setPanel(true)
    setTab('comments')
    setFocus(thread)
  }, [thread, id])
  const [draft, setDraft] = useState<Anchor>()
  // Same rule as the server: the page owner, a space admin or a workspace admin may delete any comment.
  const wsRole = me.workspaces.find((w) => w.id === ws || w.slug === ws)?.role
  const canModerate =
    !!note &&
    (note.owner_id === me.user.id ||
      (spaces.find((s) => s.id === note.space_id) as Partial<SpaceB1> | undefined)?.my_role === 'admin' ||
      wsRole === 'owner' ||
      wsRole === 'admin')
  const threads = useMemo(() => openThreads(comments.data), [comments.data])
  const subPages = useSubPageCount(ws, id, note?.space_id, tree.byId.get(id)?.has_children)
  const showComments = () => {
    setPanel(true)
    setTab('comments')
  }
  const [rev, setRev] = useState(0) // bumps when an approved proposal rewrites the content
  const [restored, setRestored] = useState(false)
  const [deleted, setDeleted] = useState(false) // closed by the gateway with CLOSE_DELETED
  const [title, setTitle] = useState('')
  const editorRef = useRef<Editor | null>(null)

  // Only on page change: later reloads must not clobber what the user is typing.
  // biome-ignore lint/correctness/useExhaustiveDependencies: keyed by id on purpose
  useEffect(() => {
    if (note) setTitle(note.title)
  }, [note?.id])

  if (deleted)
    return (
      <div role="alert" className="mx-auto max-w-xl space-y-2 px-6 py-16 text-center">
        <h1 className="text-lg font-semibold">This page was deleted</h1>
        <p className="text-muted">It is in Trash. Anyone who could edit it can restore it from there.</p>
        <Link href={`/w/${ws}/trash`} className={btn}>
          Open Trash
        </Link>
      </div>
    )
  if (error)
    return (
      <>
        <p className="p-6 text-muted">This page does not exist or you do not have access to it.</p>
        <RequestAccess ws={ws} id={id} />
      </>
    )
  if (!note) return null

  const archived = note.freshness === 'archived'
  const canEdit = note.level === 'edit' && !archived // an archived page is read-only until it is restored

  async function saveTitle() {
    if (!note || !canEdit || title.trim() === note.title) return
    await cmd(ws, 'note.update', { id, title })
    reload()
  }

  return (
    <div className="flex min-h-full">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-2">
          <StatusMenu
            ws={ws}
            id={id}
            status={note.status}
            canEdit={canEdit}
            onChange={() => {
              reloadNote()
              reload()
            }}
          />
          <FreshnessPill freshness={note.freshness} verifyBy={note.verify_by} />
          <span className="text-[12px] text-muted">Updated {ago(note.updated_at)}</span>
          <Viewers ws={ws} id={id} />
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
            <ExportMenu
              editorRef={editorRef}
              meta={{
                title,
                status: note.status ? STATUS_LABEL[note.status] : null,
                labels: note.labels ?? [],
                cover: note.cover && {
                  src: `/api/w/${ws}/files/${note.cover.file_id}`,
                  y: note.cover.y ?? 50,
                },
                updatedAt: new Date(note.updated_at).toLocaleDateString('en-GB', {
                  day: 'numeric',
                  month: 'short',
                  year: 'numeric',
                }),
              }}
            />
            <StarButton ws={ws} id={id} />
            <WatchButton ws={ws} noteId={id} />
            {canEdit && <WideToggle ws={ws} id={id} wide={note.wide} onChange={reloadNote} />}
            <button
              type="button"
              onClick={() => newPage({ spaceId: note.space_id, parentId: note.id })}
              className={btn}
            >
              <Plus size={14} /> Sub-page
            </button>
            {canEdit && <PageMenuButton note={note} />}
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
        {restored && (
          <p role="status" className="border-b border-line bg-hover px-6 py-1.5 text-[13px]">
            This page was restored to an earlier version. Edits you made in the last few seconds may not have
            been kept.
          </p>
        )}
        {archived && (
          <p role="status" className="border-b border-line bg-hover px-6 py-1.5 text-[13px]">
            Archived. This page is read-only until it is restored from the Docs dashboard.
          </p>
        )}
        {!canEdit && !archived && (
          <p role="status" className="border-b border-line bg-hover px-6 py-1.5 text-[13px] text-muted">
            You can view this page but not edit it.
          </p>
        )}
        <article className={`mx-auto px-6 py-10 ${note.wide ? 'max-w-[1120px]' : 'max-w-[720px]'}`}>
          <Cover
            ws={ws}
            noteId={id}
            title={title}
            cover={note.cover}
            wide={note.wide}
            canEdit={canEdit}
            onChange={reloadNote}
          />
          <div className="mb-6 flex items-center">
            <EmojiPicker
              ws={ws}
              id={id}
              icon={note.icon}
              canEdit={canEdit}
              onChange={() => {
                reloadNote()
                reload()
              }}
            />
            <input
              aria-label="Page title"
              value={title}
              placeholder="Untitled"
              onChange={(e) => setTitle(e.target.value)}
              readOnly={!canEdit}
              onBlur={saveTitle}
              onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
              className="min-w-0 flex-1 bg-transparent text-3xl font-bold placeholder:text-muted"
            />
          </div>
          <LabelChips
            ws={ws}
            noteId={id}
            labels={note.labels ?? []}
            canEdit={canEdit}
            onChange={reloadNote}
          />
          <NoteEditor
            key={`${id}-${rev}-${note.collab_epoch}`}
            ws={ws}
            id={id}
            epoch={note.collab_epoch}
            onDeleted={() => {
              setDeleted(true)
              reload()
            }}
            onStale={() => {
              setRestored(true)
              reloadNote()
            }}
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
          <SubPages ws={ws} noteId={id} spaceId={note.space_id} />
          <AttachmentsPanel ws={ws} noteId={id} canEdit={canEdit} editorRef={editorRef} />
          {canEdit && note.types.length === 0 && (
            <div className="mt-8 border-t border-line pt-3">
              <ArchiveButton
                ws={ws}
                id={id}
                subPages={subPages}
                onDone={() => {
                  reloadNote()
                  reload()
                }}
              />
            </div>
          )}
        </article>
      </div>
      {panel && (
        <Panel
          ws={ws}
          note={note}
          tab={tab}
          setTab={setTab}
          canEdit={canEdit}
          onRestored={reloadNote}
          comments={{
            comments: comments.data ?? [],
            reload: comments.reload,
            draft,
            clearDraft: () => setDraft(undefined),
            canModerate,
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

// useSearchParams needs a Suspense boundary for the production build.
export default function NotePage(props: { params: Promise<{ id: string }> }) {
  return (
    <Suspense>
      <NotePageInner {...props} />
    </Suspense>
  )
}
