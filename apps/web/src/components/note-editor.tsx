'use client'
import Collaboration from '@tiptap/extension-collaboration'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { useEffect, useRef, useState } from 'react'
import { WebsocketProvider } from 'y-websocket'
import * as Y from 'yjs'
import { cmd } from '@/lib/api'

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
}: {
  ws: string
  id: string
  initialMd: string
  onSaved: () => void
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
      ydoc={mode === 'collab' ? doc.current : null}
    />
  )
}

function Surface({
  ws,
  id,
  initialMd,
  onSaved,
  ydoc,
}: {
  ws: string
  id: string
  initialMd: string
  onSaved: () => void
  ydoc: Y.Doc | null
}) {
  const [status, setStatus] = useState<'saved' | 'saving' | 'error'>('saved')
  const last = useRef(initialMd)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)

  const editor = useEditor({
    immediatelyRender: false,
    extensions: [
      StarterKit.configure(ydoc ? { undoRedo: false } : {}),
      Markdown,
      ...(ydoc ? [Collaboration.configure({ document: ydoc })] : []),
    ],
    ...(ydoc ? {} : { content: initialMd, contentType: 'markdown' as const }),
    editorProps: { attributes: { 'aria-label': 'Page content', role: 'textbox', 'aria-multiline': 'true' } },
    onCreate: ({ editor }) => {
      // A fresh Yjs room has no content yet: seed it from the stored Markdown once.
      if (ydoc && ydoc.getXmlFragment('default').length === 0 && initialMd)
        editor.commands.setContent(initialMd, { contentType: 'markdown' })
    },
    onUpdate: ({ editor }) => {
      clearTimeout(timer.current)
      timer.current = setTimeout(async () => {
        const md = editor.getMarkdown()
        if (md === last.current) return setStatus('saved')
        setStatus('saving')
        try {
          await cmd(ws, 'note.update', { id, content_md: md })
          last.current = md
          setStatus('saved')
          onSaved()
        } catch {
          setStatus('error')
        }
      }, 700)
      setStatus('saving')
    },
  })

  useEffect(() => () => clearTimeout(timer.current), [])

  return (
    <div>
      <div className="prose-note">
        <EditorContent editor={editor} />
      </div>
      <p aria-live="polite" className="mt-6 text-[12px] text-muted">
        {status === 'saved'
          ? 'Saved'
          : status === 'saving'
            ? 'Saving...'
            : 'Could not save, retrying on next edit'}
        {ydoc ? ' · live' : ''}
      </p>
    </div>
  )
}
