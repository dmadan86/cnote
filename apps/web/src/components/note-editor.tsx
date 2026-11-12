'use client'
import Collaboration from '@tiptap/extension-collaboration'
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
import { AlignHeading, AlignParagraph, ColorText, HtmlTable, SafeImage } from './editor/formats'
import { MentionPicker } from './editor/mention'
import { EditorMenus } from './editor/menus'
import { SlashCommand } from './editor/slash'
import { FixedToolbar } from './editor/toolbar'
import { Presence, userColor } from './presence'
import { useShell } from './shell'

const COLLAB_URL = process.env.NEXT_PUBLIC_COLLAB_URL ?? 'ws://localhost:4000'
const SYNC_TIMEOUT_MS = 2000

type Mode = 'connecting' | 'collab' | 'solo'

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
}: {
  ws: string
  id: string
  initialMd: string
  onSaved: () => void
  editorRef?: React.RefObject<Editor | null>
  editable?: boolean
}) {
  const [mode, setMode] = useState<Mode>('connecting')
  const doc = useRef<Y.Doc>(null)
  const provider = useRef<WebsocketProvider>(null)

  useEffect(() => {
    const d = new Y.Doc()
    const p = new WebsocketProvider(`${COLLAB_URL}/collab`, `${ws}/${id}`, d)
    doc.current = d
    provider.current = p
    let decided = false
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
  }, [ws, id])

  if (mode === 'connecting') return <p className="text-muted">Loading editor...</p>
  return (
    <Surface
      ws={ws}
      id={id}
      initialMd={initialMd}
      onSaved={onSaved}
      editorRef={editorRef}
      editable={editable}
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
}: {
  ws: string
  id: string
  initialMd: string
  onSaved: () => void
  editorRef?: React.RefObject<Editor | null>
  editable: boolean
  ydoc: Y.Doc | null
  provider: WebsocketProvider | null
}) {
  const { me } = useShell()
  const [status, setStatus] = useState<'saved' | 'saving' | 'error' | 'denied'>('saved')
  const last = useRef(initialMd)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const pending = useRef<(() => string) | null>(null) // set until the latest edit is saved

  const editor = useEditor({
    immediatelyRender: false,
    editable,
    extensions: [
      StarterKit.configure({ paragraph: false, heading: false, ...(ydoc ? { undoRedo: false } : {}) }),
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
      Placeholder.configure({ placeholder: "Type '/' for commands" }),
      SlashCommand,
      MentionPicker.configure({ ws }),
      ...(ydoc ? [Collaboration.configure({ document: ydoc })] : []),
      ...(provider
        ? [
            CollaborationCaret.configure({
              provider,
              user: { id: me.user.id, name: me.user.name, color: userColor(me.user.id) },
            }),
          ]
        : []),
    ],
    ...(ydoc ? {} : { content: initialMd, contentType: 'markdown' as const }),
    editorProps: { attributes: { 'aria-label': 'Page content', role: 'textbox', 'aria-multiline': 'true' } },
    onCreate: ({ editor }) => {
      if (editorRef) editorRef.current = editor
      // A fresh Yjs room has no content yet: seed it from the stored Markdown once.
      if (ydoc && ydoc.getXmlFragment('default').length === 0 && initialMd)
        editor.commands.setContent(initialMd, { contentType: 'markdown' })
    },
    onUpdate: ({ editor }) => {
      clearTimeout(timer.current)
      pending.current = () => editor.getMarkdown()
      timer.current = setTimeout(async () => {
        const md = editor.getMarkdown()
        if (md === last.current) {
          pending.current = null
          return setStatus('saved')
        }
        setStatus('saving')
        try {
          await cmd(ws, 'note.update', { id, content_md: md })
          pending.current = null
          last.current = md
          setStatus('saved')
          onSaved()
        } catch (e) {
          // 403: access changed while editing; stop retrying and say why.
          setStatus((e as { status?: number }).status === 403 ? 'denied' : 'error')
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
          body: JSON.stringify({ id, content_md: md }),
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
  }, [ws, id])

  return (
    <div>
      {provider && <Presence provider={provider} selfId={me.user.id} />}
      <div className="prose-note">
        {editor && editable && <FixedToolbar editor={editor} />}
        {editor && editable && <EditorMenus editor={editor} />}
        <EditorContent editor={editor} />
      </div>
      <p aria-live="polite" className="mt-6 text-[12px] text-muted">
        {status === 'saved'
          ? editable
            ? 'Saved'
            : 'Read only'
          : status === 'saving'
            ? 'Saving...'
            : status === 'denied'
              ? 'You no longer have permission to edit this page; your last changes were not saved'
              : 'Could not save, retrying on next edit'}
        {ydoc ? ' · live' : ''}
      </p>
    </div>
  )
}
