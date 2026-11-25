'use client'
import type { Editor } from '@tiptap/react'
import { ChevronDown, Download } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { btn } from '../ui'
import {
  buildExportDom,
  buildHtmlFile,
  buildMarkdown,
  download,
  type ExportMeta,
  printDom,
  safeFileName,
} from './export'

type Kind = 'pdf' | 'md' | 'html'
const ITEMS: [Kind, string][] = [
  ['pdf', 'Export as PDF'],
  ['md', 'Download Markdown'],
  ['html', 'Download HTML'],
]

/** Page export from the live editor (docs-parity 7.9). One page only: no children, no public links. */
export function ExportMenu({
  editorRef,
  meta,
}: {
  editorRef: React.RefObject<Editor | null>
  meta: ExportMeta
}) {
  const [open, setOpen] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const box = useRef<HTMLDivElement>(null)
  const first = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    first.current?.focus()
    const out = (e: MouseEvent) => !box.current?.contains(e.target as Node) && setOpen(false)
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', out)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', out)
      document.removeEventListener('keydown', esc)
    }
  }, [open])

  async function run(kind: Kind) {
    setOpen(false)
    const editor = editorRef.current
    setErr('')
    if (!editor) return setErr('The page is still loading. Try again in a moment.')
    setMsg('Preparing export…')
    try {
      let notes: string[]
      if (kind === 'md') {
        const r = await buildMarkdown(editor)
        download(safeFileName(meta.title, 'md'), r.text, 'text/markdown;charset=utf-8')
        notes = r.notes
      } else {
        const r = await buildExportDom(editor, meta)
        notes = r.notes
        if (kind === 'html')
          download(safeFileName(meta.title, 'html'), buildHtmlFile(r.root, meta), 'text/html;charset=utf-8')
        else await printDom(r.root)
      }
      setMsg(notes.length ? `Export ready. ${notes.join(' ')}` : 'Export ready.')
    } catch {
      setMsg('')
      setErr('Export failed. Try again.')
    }
  }

  return (
    <div ref={box} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className={btn}
      >
        <Download size={14} /> Export <ChevronDown size={12} />
      </button>
      {open && (
        <div
          role="menu"
          aria-label="Export"
          className="absolute right-0 z-30 mt-1 min-w-48 rounded-md border border-line bg-canvas p-1 shadow-lg"
        >
          {ITEMS.map(([k, label], i) => (
            <button
              key={k}
              ref={i === 0 ? first : undefined}
              type="button"
              role="menuitem"
              onClick={() => run(k)}
              className="flex w-full rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover"
            >
              {label}
            </button>
          ))}
        </div>
      )}
      <span role="status" aria-live="polite" className="sr-only">
        {msg}
      </span>
      <span role="alert" className="sr-only">
        {err}
      </span>
    </div>
  )
}
