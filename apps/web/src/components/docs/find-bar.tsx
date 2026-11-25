'use client'
import type { Editor } from '@tiptap/react'
import { ChevronDown, ChevronUp, X } from 'lucide-react'
import { useEffect, useReducer, useRef, useState } from 'react'
import { getFind, replaceAll, replaceCurrent, setFind, showCurrent, stepFind } from '../editor/find-replace'
import { btn, inputCls } from '../ui'

/**
 * The find bar (docs-parity-c 5.5), top right of the page. Replace is hidden when the page is not editable: view access, an
 * archived page and the read-only public page still get find. Enter is next, Shift+Enter previous, Esc closes.
 */
export function FindBar({
  editor,
  editable,
  seed,
  onClose,
}: {
  editor: Editor
  editable: boolean
  seed: string
  onClose: () => void
}) {
  const [q, setQ] = useState(seed)
  const [repl, setRepl] = useState('')
  const [opts, setOpts] = useState({ matchCase: false, wholeWord: false, includeCode: false })
  const [note, setNote] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const [, redraw] = useReducer((n: number) => n + 1, 0)

  // The count follows the document: a collaborator's edit changes it too.
  useEffect(() => {
    editor.on('transaction', redraw)
    return () => void editor.off('transaction', redraw)
  }, [editor])
  useEffect(() => {
    if (editor.isDestroyed) return
    setFind(editor.view, { query: q, ...opts })
    showCurrent(editor.view)
  }, [editor, q, opts])
  useEffect(() => {
    input.current?.focus()
    input.current?.select()
    return () => {
      if (!editor.isDestroyed) setFind(editor.view, { query: '' })
    }
  }, [editor])

  const s = editor.isDestroyed ? null : getFind(editor.view)
  const total = s?.matches.length ?? 0
  const close = () => {
    onClose()
    editor.commands.focus()
  }
  const step = (d: 1 | -1) => {
    setNote('')
    stepFind(editor.view, d)
  }
  const replaceOne = () => setNote(replaceCurrent(editor.view, repl) ? '' : 'Dates cannot be replaced')
  const flip = (k: keyof typeof opts) => setOpts((o) => ({ ...o, [k]: !o[k] }))
  const box = (k: keyof typeof opts, label: string) => (
    <label className="inline-flex items-center gap-1 text-[12px]">
      <input type="checkbox" checked={opts[k]} onChange={() => flip(k)} /> {label}
    </label>
  )

  return (
    <div className="sticky top-2 z-30 mb-2 flex justify-end">
      <fieldset
        aria-label="Find and replace"
        data-testid="find-bar"
        className="m-0 w-full max-w-md space-y-2 rounded-lg border border-line bg-canvas p-2 shadow-lg"
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            close()
          }
        }}
      >
        <div className="flex items-center gap-1">
          <input
            ref={input}
            aria-label="Find"
            className={`${inputCls} min-w-0 flex-1`}
            value={q}
            placeholder="Find"
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                step(e.shiftKey ? -1 : 1)
              }
            }}
          />
          <span aria-live="polite" data-testid="find-count" className="shrink-0 px-1 text-[12px] text-muted">
            {!q ? '' : total ? `${(s?.current ?? 0) + 1} of ${total}` : 'No results'}
          </span>
          <button
            type="button"
            aria-label="Previous match"
            className={btn}
            disabled={!total}
            onClick={() => step(-1)}
          >
            <ChevronUp size={14} />
          </button>
          <button
            type="button"
            aria-label="Next match"
            className={btn}
            disabled={!total}
            onClick={() => step(1)}
          >
            <ChevronDown size={14} />
          </button>
          <button type="button" aria-label="Close find" className={btn} onClick={close}>
            <X size={14} />
          </button>
        </div>
        <div className="flex flex-wrap gap-3">
          {box('matchCase', 'Match case')}
          {box('wholeWord', 'Whole word')}
          {box('includeCode', 'Include code')}
        </div>
        {editable && (
          <div className="flex items-center gap-1">
            <input
              aria-label="Replace with"
              className={`${inputCls} min-w-0 flex-1`}
              value={repl}
              placeholder="Replace with"
              onChange={(e) => setRepl(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  replaceOne()
                }
              }}
            />
            <button type="button" className={btn} disabled={!total} onClick={replaceOne}>
              Replace
            </button>
            <button
              type="button"
              className={btn}
              disabled={!total}
              onClick={() => setNote(`Replaced ${replaceAll(editor.view, repl)}`)}
            >
              Replace all
            </button>
          </div>
        )}
        {note && (
          <p role="status" className="text-[12px] text-muted">
            {note}
          </p>
        )}
      </fieldset>
    </div>
  )
}
