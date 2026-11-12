'use client'
import type { Editor } from '@tiptap/react'
import { useEffect, useRef, useState } from 'react'
import { btnPrimary, Field, inputCls, Modal } from '../ui'

export const EMOJI = [
  '😀',
  '😂',
  '😊',
  '😍',
  '🥰',
  '😎',
  '🤔',
  '😅',
  '😢',
  '😭',
  '😡',
  '🙏',
  '👍',
  '👎',
  '👏',
  '🙌',
  '💪',
  '👀',
  '🎉',
  '🔥',
  '✨',
  '⭐',
  '❤️',
  '💡',
  '✅',
  '❌',
  '⚠️',
  '❓',
  '❗',
  '📌',
  '📎',
  '📝',
  '📅',
  '📈',
  '🚀',
  '🐛',
  '🔒',
  '🔑',
  '☕',
  '🍕',
] // plain Unicode: no emoji library

/** Emoji grid at the caret. Opened by the toolbar button and the "/" menu through a DOM event, like the table picker. */
export function EmojiPicker({ editor }: { editor: Editor }) {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const dom = editor.view.dom
    const show = () => {
      const p = editor.view.coordsAtPos(editor.state.selection.from)
      setAt({ x: p.left, y: p.bottom + 6 })
    }
    dom.addEventListener('cn:emoji-picker', show)
    return () => dom.removeEventListener('cn:emoji-picker', show)
  }, [editor])
  useEffect(() => {
    if (!at) return
    ref.current?.querySelector('button')?.focus()
    const down = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setAt(null)
    document.addEventListener('mousedown', down)
    return () => document.removeEventListener('mousedown', down)
  }, [at])
  if (!at) return null
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label="Emoji"
      style={{ left: at.x, top: at.y }}
      className="fixed z-50 grid w-[17rem] grid-cols-10 gap-0.5 rounded-lg border border-line bg-canvas p-2 text-fg shadow-xl"
      onKeyDown={(e) => {
        if (e.key !== 'Escape') return
        setAt(null)
        editor.commands.focus()
      }}
    >
      {EMOJI.map((m) => (
        <button
          key={m}
          type="button"
          aria-label={m}
          onClick={() => {
            setAt(null)
            editor.chain().focus().insertContent(m).run()
          }}
          className="grid size-6 place-items-center rounded text-[16px] hover:bg-hover focus:bg-hover focus:outline-none"
        >
          {m}
        </button>
      ))}
    </div>
  )
}

/** Insert an image by URL (http/https only; no uploads). */
export function ImageDialog({ editor }: { editor: Editor }) {
  const [open, setOpen] = useState(false)
  const [src, setSrc] = useState('')
  const [alt, setAlt] = useState('')
  const [err, setErr] = useState('')
  useEffect(() => {
    const dom = editor.view.dom
    const show = () => {
      setSrc('')
      setAlt('')
      setErr('')
      setOpen(true)
    }
    dom.addEventListener('cn:image-dialog', show)
    return () => dom.removeEventListener('cn:image-dialog', show)
  }, [editor])
  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    let ok = false
    try {
      ok = /^https?:$/.test(new URL(src.trim()).protocol)
    } catch {}
    if (!ok) return setErr('Enter a full http:// or https:// image URL')
    setOpen(false)
    editor.chain().focus().setImage({ src: src.trim(), alt: alt.trim() }).run()
  }
  return (
    <Modal open={open} onClose={() => setOpen(false)} title="Insert image">
      <form onSubmit={submit} className="space-y-3">
        <Field label="Image URL">
          {(id) => (
            <input
              // biome-ignore lint/a11y/noAutofocus: the dialog exists only to take this URL
              autoFocus
              id={id}
              value={src}
              onChange={(e) => setSrc(e.target.value)}
              className={inputCls}
              placeholder="https://"
            />
          )}
        </Field>
        <Field label="Alt text">
          {(id) => (
            <input id={id} value={alt} onChange={(e) => setAlt(e.target.value)} className={inputCls} />
          )}
        </Field>
        {err && (
          <p role="alert" className="text-[13px] text-red-600">
            {err}
          </p>
        )}
        <div className="flex justify-end">
          <button type="submit" className={btnPrimary}>
            Insert image
          </button>
        </div>
      </form>
    </Modal>
  )
}
