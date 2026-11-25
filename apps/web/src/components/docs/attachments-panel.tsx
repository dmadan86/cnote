'use client'
import type { Editor } from '@tiptap/react'
import { Download, FileText, Paperclip, Plus, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { FILES_EVENT, fileHref, insertFileLink, uploadFile } from '../editor/file-link'
import { btn, ErrorText } from '../ui'

// docs-parity-b 5.2: every file of the page with size, uploader and date, and download, insert and delete.
// The text stores a file as an ordinary link or image to /api/w/<ws>/files/<id>; this panel only lists and manages them.

export type PageFile = {
  id: string
  filename: string
  content_type: string
  size_bytes: number
  created_at: string
  created_by_name?: string
}

export const fmtSize = (n: number) =>
  n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : n >= 1 << 10 ? `${Math.round(n / 1024)} KB` : `${n} B`

export function AttachmentsPanel({
  ws,
  noteId,
  canEdit,
  editorRef,
}: {
  ws: string
  noteId: string
  canEdit: boolean
  editorRef: React.RefObject<Editor | null>
}) {
  const { data, error, reload } = useCmd<PageFile[]>(ws, 'file.list', { note_id: noteId })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {
    window.addEventListener(FILES_EVENT, reload)
    return () => window.removeEventListener(FILES_EVENT, reload)
  }, [reload])
  if (!data?.length && !canEdit) return null

  const upload = async (files: File[]) => {
    setBusy(true)
    setErr(undefined)
    try {
      for (const f of files) await uploadFile(ws, noteId, f)
    } catch (e) {
      setErr(e as Error)
    }
    setBusy(false)
    reload()
  }
  const insert = (f: PageFile) => {
    const ed = editorRef.current
    if (!ed) return
    if (f.content_type.startsWith('image/'))
      ed.chain()
        .focus()
        .insertContent({ type: 'image', attrs: { src: fileHref(ws, f.id), alt: f.filename } })
        .run()
    else insertFileLink(ed, fileHref(ws, f.id), f.filename)
  }
  const remove = async (f: PageFile) => {
    const used = editorRef.current?.getMarkdown().includes(`/files/${f.id}`)
    const msg = used
      ? `"${f.filename}" is used in the text. Deleting it breaks that link or image. Delete anyway?`
      : `Delete "${f.filename}"?`
    if (!window.confirm(msg)) return
    try {
      await cmd(ws, 'file.delete', { id: f.id })
    } catch (e) {
      setErr(e as Error)
    }
    reload()
  }

  return (
    <section aria-label="Attachments" className="mt-8 border-t border-line pt-4">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="flex items-center gap-1.5 text-[13px] font-semibold">
          <Paperclip size={14} /> Attachments{data ? ` (${data.length})` : ''}
        </h2>
        {canEdit && (
          <>
            <input
              ref={input}
              type="file"
              hidden
              multiple
              aria-label="Upload files"
              accept="application/pdf,image/png,image/jpeg,image/gif,image/webp"
              onChange={(e) => {
                upload([...(e.target.files ?? [])])
                e.target.value = ''
              }}
            />
            <button type="button" className={btn} disabled={busy} onClick={() => input.current?.click()}>
              <Plus size={14} /> {busy ? 'Uploading...' : 'Upload'}
            </button>
          </>
        )}
      </div>
      <ErrorText error={err ?? error} />
      <ul className="divide-y divide-line text-[13px]">
        {data?.map((f) => (
          <li key={f.id} className="flex items-center gap-3 py-2">
            <FileText size={16} className="shrink-0 text-muted" />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-medium">{f.filename}</span>
              <span className="block truncate text-[12px] text-muted">
                {fmtSize(f.size_bytes)} · {f.created_by_name ?? 'Unknown'} ·{' '}
                {new Date(f.created_at).toLocaleDateString(undefined, {
                  day: 'numeric',
                  month: 'short',
                  year: 'numeric',
                })}
              </span>
            </span>
            <a
              href={fileHref(ws, f.id)}
              download={f.filename}
              aria-label={`Download ${f.filename}`}
              className="rounded p-1.5 hover:bg-hover"
            >
              <Download size={15} />
            </a>
            {canEdit && (
              <>
                <button
                  type="button"
                  className={btn}
                  onClick={() => insert(f)}
                  aria-label={`Insert ${f.filename} into page`}
                >
                  Insert
                </button>
                <button
                  type="button"
                  aria-label={`Delete ${f.filename}`}
                  onClick={() => remove(f)}
                  className="rounded p-1.5 hover:bg-hover"
                >
                  <Trash2 size={15} />
                </button>
              </>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}
