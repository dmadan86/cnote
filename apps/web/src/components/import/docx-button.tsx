'use client'
import { FileUp } from 'lucide-react'
import { useRouter } from 'next/navigation'
import { useRef, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary } from '@/components/ui'
import type { DocxImportResult } from '@/lib/docs'

const MAX = 10 * 1024 * 1024

/** The raw route answers with the command's JSON, errors included. */
function upload(url: string, file: File, onProgress: (pct: number) => void): Promise<DocxImportResult> {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest()
    x.open('POST', url)
    x.withCredentials = true
    x.upload.onprogress = (e) => e.lengthComputable && onProgress(Math.round((e.loaded / e.total) * 100))
    x.onerror = () => reject(new Error('The upload failed. Check your connection and try again.'))
    x.onload = () => {
      let body: { error?: { message?: string } } & Partial<DocxImportResult> = {}
      try {
        body = JSON.parse(x.responseText)
      } catch {}
      if (x.status >= 200 && x.status < 300 && body.id) resolve(body as DocxImportResult)
      else reject(new Error(body.error?.message ?? `The import failed (${x.status}).`))
    }
    x.send(file)
  })
}

/**
 * "Import Word file": one .docx becomes one page in the space (under `parentId` when given). The page opens at once,
 * unless the import dropped something, in which case the list is shown first and can be dismissed.
 */
export function DocxImportButton({
  spaceId,
  parentId,
  className = btn,
}: {
  spaceId: string
  parentId?: string
  className?: string
}) {
  const { ws } = useShell()
  const router = useRouter()
  const input = useRef<HTMLInputElement>(null)
  const [pct, setPct] = useState<number>()
  const [err, setErr] = useState<string>()
  const [done, setDone] = useState<DocxImportResult>()

  async function pick(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    setErr(undefined)
    setDone(undefined)
    if (!/\.docx$/i.test(file.name)) return setErr('Choose a Word document saved as .docx.')
    if (file.size > MAX) return setErr('That file is larger than 10 MB.')
    const q = new URLSearchParams({ space_id: spaceId, filename: file.name })
    if (parentId) q.set('parent_id', parentId)
    setPct(0)
    try {
      const r = await upload(`/api/w/${ws}/import/docx?${q}`, file, setPct)
      if (r.warnings.length === 0) router.push(`/w/${ws}/n/${r.id}`)
      else setDone(r)
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setPct(undefined)
    }
  }

  return (
    <div className="space-y-2">
      <input
        ref={input}
        type="file"
        accept=".docx,application/vnd.openxmlformats-officedocument.wordprocessingml.document"
        className="sr-only"
        aria-label="Word file"
        tabIndex={-1}
        onChange={pick}
      />
      <button
        type="button"
        className={className}
        disabled={pct !== undefined}
        onClick={() => input.current?.click()}
      >
        <FileUp size={14} /> Import Word file
      </button>
      {pct !== undefined && (
        <p role="status" className="text-[13px] text-muted">
          {pct < 100 ? `Uploading ${pct}%` : 'Converting the document'}
        </p>
      )}
      {err && (
        <p role="alert" className="text-[13px] text-bad">
          {err}
        </p>
      )}
      {done && (
        <div role="status" className="space-y-2 rounded-md bg-warn-bg px-3 py-2 text-[13px] text-warn">
          <p>Imported "{done.title}". Some things were left out:</p>
          <ul className="ml-4 list-disc">
            {done.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
          <div className="flex gap-2">
            <button type="button" className={btnPrimary} onClick={() => router.push(`/w/${ws}/n/${done.id}`)}>
              Open page
            </button>
            <button type="button" className={btn} onClick={() => setDone(undefined)}>
              Dismiss
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
