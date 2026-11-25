'use client'
import Collaboration, { isChangeOrigin } from '@tiptap/extension-collaboration'
import CollaborationCaret from '@tiptap/extension-collaboration-caret'
import { TaskItem, TaskList } from '@tiptap/extension-list'
import { TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import TextAlign from '@tiptap/extension-text-align'
import { Color } from '@tiptap/extension-text-style'
import { Placeholder } from '@tiptap/extensions'
import { Markdown } from '@tiptap/markdown'
import { type Editor, EditorContent, useEditor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { useEffect, useRef, useState } from 'react'
import { WebsocketProvider } from 'y-websocket'
import * as Y from 'yjs'
import { cmd } from '@/lib/api'
import { CLOSE_ARCHIVED, CLOSE_RELOAD, DOC_SCHEMA } from '@/lib/doc-schema'
import { CLOSE_DELETED } from '@/lib/docs'
import { blockExtensions, ImageUpload, setGatewayMinSchema } from './editor/blocks'
import { FileChips, FileUpload } from './editor/file-link'
import { AlignHeading, AlignParagraph, ColorText, HtmlTable, SafeImage } from './editor/formats'
import { InlineComments, setThreads, type Thread } from './editor/inline-comments'
import { MentionPicker } from './editor/mention'
import { CommentBubble, EditorMenus } from './editor/menus'
import { PageLinkPicker } from './editor/page-link'
import { SlashCommand } from './editor/slash'
import { TaskControls } from './editor/task-controls'
import { FixedToolbar } from './editor/toolbar'
import { Presence, userColor } from './presence'
import { useShell } from './shell'

const COLLAB_URL = process.env.NEXT_PUBLIC_COLLAB_URL ?? 'ws://localhost:4000'
const SYNC_TIMEOUT_MS = 2000
/** Browsers cap keepalive requests at 64 KiB; a bigger close update is not sent (the live socket normally has it). */
const MAX_CLOSE_UPDATE = 48 * 1024
/** An edit older than this reached the gateway long ago; the close beacon is for the last few seconds only. */
const IN_FLIGHT_MS = 5000

const toBase64 = (b: Uint8Array) => {
  let s = ''
  for (let i = 0; i < b.length; i += 8192) s += String.fromCharCode(...b.subarray(i, i + 8192))
  return btoa(s)
}
/** Server close code: the page was restored, this document's epoch is over. */
const STALE_EPOCH = 4409

type Mode = 'connecting' | 'collab' | 'solo' | 'reload'

/**
 * Tries the Yjs gateway first; if it does not sync within a short window we fall back to a plain editor.
 * Either way the Markdown is saved through note.update (debounced), so the page works without the gateway.
 */
export function NoteEditor({
  ws,
  id,
  initialMd,
  onSaved,
  editorRef,
  editable = true,
  comments,
  epoch,
  onStale,
  onDeleted,
}: {
  ws: string
  id: string
  initialMd: string
  onSaved: () => void
  editorRef?: React.RefObject<Editor | null>
  editable?: boolean
  /** Inline comments: open threads to highlight, and what to do on click / on a new selection comment. */
  comments?: InlineCommentProps
  /** note.collab_epoch this editor's document belongs to. */
  epoch?: number
  /** The page was restored elsewhere: this document is obsolete (server closed it with 4409). */
  onStale?: () => void
  /** The page was moved to the trash while open (close code 4411): final, never reconnect. */
  onDeleted?: () => void
}) {
  const [mode, setMode] = useState<Mode>('connecting')
  const doc = useRef<Y.Doc>(null)
  const onStaleRef = useRef(onStale)
  onStaleRef.current = onStale
  const onDeletedRef = useRef(onDeleted)
  onDeletedRef.current = onDeleted
  const provider = useRef<WebsocketProvider>(null)

  useEffect(() => {
    const d = new Y.Doc()
    let decided = false
    const p = new WebsocketProvider(`${COLLAB_URL}/collab`, `${ws}/${id}`, d, {
      params: { schema: String(DOC_SCHEMA), ...(epoch === undefined ? {} : { epoch: String(epoch) }) },
      // No tab-to-tab BroadcastChannel: it is keyed by room name only, so a tab still holding a restored-away
      // document would hand it to a fresh one past the server's epoch fence. The gateway relays everything.
      disableBc: true,
    })
    // Never reconnect with an obsolete document: merging it would bring the replaced content back.
    p.on('connection-close', (e) => {
      if (e?.code === STALE_EPOCH) {
        p.destroy()
        onStaleRef.current?.()
      }
      // 4412 (an edit was refused during a delete fence) is deliberately not handled: the provider reconnects and the resync resends it.
      if (e?.code === CLOSE_DELETED) {
        p.destroy()
        onDeletedRef.current?.()
      }
      if (e?.code === CLOSE_ARCHIVED) {
        // Archived while open: read-only until restored. Reloading shows the archived page.
        p.destroy()
        window.location.reload()
      }
      if (e?.code === CLOSE_RELOAD) {
        // The gateway needs a newer editor than this tab runs: stop rather than save over newer content.
        p.destroy()
        decided = true
        setMode('reload')
      }
    })
    doc.current = d
    provider.current = p
    // Blocks stay off until the gateway says no connected tab can predate them (docs-parity 7.3).
    fetch(`${COLLAB_URL.replace(/^ws/, 'http')}/collab/min-schema`)
      .then((r) => r.json())
      .then((j: { min?: number }) => setGatewayMinSchema(Number(j.min) || 0))
      .catch(() => {})
    const decide = (m: Mode) => {
      if (decided) return
      decided = true
      if (m === 'solo') p.destroy()
      setMode(m)
    }
    p.once('sync', () => decide('collab'))
    const t = setTimeout(() => decide('solo'), SYNC_TIMEOUT_MS)
    return () => {
      clearTimeout(t)
      p.destroy()
      d.destroy()
    }
  }, [ws, id, epoch])

  if (mode === 'connecting') return <p className="text-muted">Loading editor...</p>
  if (mode === 'reload')
    return (
      <p role="alert" className="text-[14px]">
        C Note was updated. Reload to keep editing.{' '}
        <button type="button" className="underline" onClick={() => window.location.reload()}>
          Reload
        </button>
      </p>
    )
  return (
    <Surface
      ws={ws}
      id={id}
      initialMd={initialMd}
      onSaved={onSaved}
      editorRef={editorRef}
      editable={editable}
      comments={comments}
      epoch={epoch}
      ydoc={mode === 'collab' ? doc.current : null}
      provider={mode === 'collab' ? provider.current : null}
    />
  )
}

function Surface({
  ws,
  id,
  initialMd,
  onSaved,
  editorRef,
  editable,
  ydoc,
  provider,
  comments,
  epoch,
}: {
  ws: string
  id: string
  initialMd: string
  onSaved: () => void
  editorRef?: React.RefObject<Editor | null>
  editable: boolean
  comments?: InlineCommentProps
  ydoc: Y.Doc | null
  provider: WebsocketProvider | null
  epoch?: number
}) {
  const { me } = useShell()
  // Extensions are created once; read the latest callbacks through a ref.
  const commentsRef = useRef(comments)
  commentsRef.current = comments
  const [status, setStatus] = useState<'saved' | 'saving' | 'error' | 'denied' | 'offline' | 'upgrade'>(
    'saved',
  )
  const last = useRef(initialMd)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const pending = useRef<(() => string) | null>(null) // set until the latest edit is saved
  // True while the gateway connection is up: its log already holds every edit.
  const live = useRef(!!provider?.wsconnected)
  // What the gateway had when this editor opened: the close beacon sends only what came after.
  const base = useRef<Uint8Array | null>(null)
  if (ydoc && !base.current) base.current = Y.encodeStateVector(ydoc)
  // When the person last typed (focused, not a remote change or the editor normalising loaded content): only a recent edit can be in flight.
  const lastEdit = useRef(0)
  const ydocRef = useRef(ydoc)
  ydocRef.current = ydoc

  const editor = useEditor({
    immediatelyRender: false,
    editable,
    extensions: [
      StarterKit.configure({
        paragraph: false,
        heading: false,
        codeBlock: false,
        ...(ydoc ? { undoRedo: false } : {}),
      }),
      AlignParagraph,
      AlignHeading,
      TextAlign.configure({ types: ['heading', 'paragraph'] }),
      ColorText,
      Color,
      SafeImage.configure({ inline: false, allowBase64: false }),
      Markdown,
      HtmlTable.configure({ resizable: true }),
      TableRow,
      TableHeader,
      TableCell,
      TaskList,
      TaskItem.configure({ nested: true }),
      ...blockExtensions,
      ImageUpload.configure({ ws, noteId: id }),
      FileUpload.configure({ ws, noteId: id }),
      FileChips,
      PageLinkPicker.configure({ ws }),
      Placeholder.configure({ placeholder: "Type '/' for commands" }),
      SlashCommand,
      MentionPicker.configure({ ws }),
      ...(comments ? [InlineComments.configure({ onOpen: (cid) => commentsRef.current?.onOpen(cid) })] : []),
      ...(ydoc ? [Collaboration.configure({ document: ydoc })] : []),
      ...(provider
        ? [
            CollaborationCaret.configure({
              provider,
              user: { id: me.user.id, name: me.user.name, color: userColor(me.user.id), canEdit: editable },
            }),
          ]
        : []),
    ],
    ...(ydoc ? {} : { content: initialMd, contentType: 'markdown' as const }),
    editorProps: { attributes: { 'aria-label': 'Page content', role: 'textbox', 'aria-multiline': 'true' } },
    onCreate: ({ editor }) => {
      if (editorRef) editorRef.current = editor
      // Never seed here: the gateway seeds a room from content_md once, so seeding again would duplicate it.
    },
    onUpdate: ({ editor, transaction }) => {
      // A person typing has focus; the editor normalising loaded content (also a local change) does not.
      if (!isChangeOrigin(transaction) && editor.isFocused) lastEdit.current = Date.now()
      clearTimeout(timer.current)
      const mine = () => editor.getMarkdown()
      pending.current = mine
      timer.current = setTimeout(async () => {
        const md = editor.getMarkdown()
        if (md === last.current) {
          if (pending.current === mine) pending.current = null
          return setStatus('saved')
        }
        setStatus('saving')
        try {
          // expected_epoch: after a restore the server refuses this tab's stale copy.
          await cmd(ws, 'note.update', { id, content_md: md, expected_epoch: epoch, schema: DOC_SCHEMA })
          // Keep an edit typed while this save was in flight: navigating away must still flush it.
          if (pending.current === mine) pending.current = null
          last.current = md
          setStatus('saved')
          onSaved()
        } catch (e) {
          // 403: access changed while editing; stop retrying and say why.
          const code = (e as { status?: number }).status
          setStatus(code === 403 ? 'denied' : code === 426 ? 'upgrade' : 'error')
        }
      }, 700)
      setStatus('saving')
    },
  })

  // A debounced save still pending on navigation or tab close would be lost: send it now.
  // keepalive lets the request outlive the page; sendBeacon cannot (the API needs JSON + cookie).
  useEffect(() => {
    const flush = () => {
      clearTimeout(timer.current)
      const get = pending.current
      pending.current = null
      // Connected: never write content_md here (the gateway's snapshot does, and a second write makes a reload before
      // that snapshot apply the edit twice). The socket may not have delivered the last edit yet, so send the Yjs
      // update the gateway may lack; updates are idempotent, a resend changes nothing. Solo or offline: this save
      // is the only copy.
      const doc = ydocRef.current
      if (live.current && doc) {
        if (Date.now() - lastEdit.current > IN_FLIGHT_MS) return
        try {
          const u = Y.encodeStateAsUpdate(doc, base.current ?? undefined)
          if (u.length <= 2) return // nothing new
          if (u.length > MAX_CLOSE_UPDATE) return console.warn('Close update too large to send:', u.length)
          const q = new URLSearchParams({ schema: String(DOC_SCHEMA) })
          if (epoch !== undefined) q.set('epoch', String(epoch))
          fetch(`${COLLAB_URL.replace(/^ws/, 'http')}/collab/${ws}/${id}/update?${q}`, {
            method: 'POST',
            keepalive: true,
            credentials: 'include',
            headers: { 'content-type': 'text/plain' },
            body: toBase64(u),
          }).catch(() => {})
        } catch {} // doc already destroyed
        return
      }
      if (!get) return
      try {
        const md = get()
        if (md === last.current) return
        last.current = md
        fetch(`/api/w/${ws}/commands/note.update`, {
          method: 'POST',
          keepalive: true,
          credentials: 'same-origin',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ id, content_md: md, expected_epoch: epoch, schema: DOC_SCHEMA }),
        }).catch(() => {})
      } catch {} // editor already destroyed: nothing left to read
    }
    window.addEventListener('pagehide', flush)
    window.addEventListener('beforeunload', flush)
    return () => {
      window.removeEventListener('pagehide', flush)
      window.removeEventListener('beforeunload', flush)
      flush()
    }
  }, [ws, id, epoch])

  // Live: an edit is saved once the gateway has it; while disconnected, edits wait in this tab.
  useEffect(() => {
    if (!provider) return
    const on = ({ status }: { status: string }) => {
      live.current = status === 'connected'
      setStatus(status === 'connected' ? 'saved' : 'offline')
    }
    provider.on('status', on)
    return () => provider.off('status', on)
  }, [provider])

  const threads = comments?.threads
  useEffect(() => {
    if (editor && threads) setThreads(editor, threads)
  }, [editor, threads])

  return (
    <div>
      {provider && <Presence provider={provider} selfId={me.user.id} />}
      <div className="prose-note">
        {editor && editable && <FixedToolbar editor={editor} />}
        {editor && editable && <EditorMenus editor={editor} onComment={comments?.onNew} />}
        {editor && !editable && comments && <CommentBubble editor={editor} onComment={comments.onNew} />}
        <EditorContent editor={editor} />
        {editor && <TaskControls editor={editor} editable={editable} />}
      </div>
      <p aria-live="polite" className="mt-6 text-[12px] text-muted">
        {status === 'saved'
          ? editable
            ? 'Saved'
            : 'Read only'
          : status === 'saving'
            ? 'Saving...'
            : status === 'offline'
              ? 'Offline: your edits are kept here and sync when the connection returns'
              : status === 'denied'
                ? 'You no longer have permission to edit this page; your last changes were not saved'
                : status === 'upgrade'
                  ? 'C Note was updated. Reload to keep editing.'
                  : 'Could not save, retrying on next edit'}
        {ydoc ? ' · live' : ''}
      </p>
    </div>
  )
}

type InlineCommentProps = {
  threads: Thread[]
  onOpen: (threadId: string) => void
  onNew: (anchor: { anchor_text: string; anchor_prefix: string }) => void
}
