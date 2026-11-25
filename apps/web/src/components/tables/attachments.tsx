'use client'
import { FileText, Paperclip, Upload, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { btn } from '@/components/ui'
import { CmdFailure } from '@/lib/api'
import {
  discardFile,
  FILE_ACCEPT,
  FILE_TYPE_HELP,
  type Field,
  type FileMeta,
  fileUrl,
  MAX_FILE_BYTES,
  MAX_FILES_PER_CELL,
  stageFile,
  type TableRecordWithLinks,
  updateRecord,
} from '@/lib/tables'

const IMAGE = /^image\/(png|jpeg|webp|gif)$/
const EXT = /\.(png|jpe?g|webp|gif|pdf)$/i
const UNDO_MS = 5000

export const sizeText = (n: number) =>
  n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`

/** The cell's text in the grid: a paperclip, the count and the first file name. */
export function AttachmentSummary({ files }: { files: unknown }) {
  const list = Array.isArray(files) ? (files as FileMeta[]) : []
  if (!list.length) return null
  return (
    <span className="flex min-w-0 items-center gap-1">
      <Paperclip size={12} className="shrink-0 text-muted" aria-hidden />
      <span className="shrink-0 text-muted">{list.length}</span>
      <span className="truncate">{list[0].name}</span>
    </span>
  )
}

type Up = { key: number; name: string; pct: number }

/**
 * File list, uploader and remover for one attachment cell of a saved record.
 * Upload: stage each file (progress, cancel), then one record.update with all handles.
 * Remove: the file leaves the list at once, an Undo shows for a few seconds, then the removal is saved.
 */
export function AttachmentEditor({
  ws,
  tableId,
  field,
  recordId,
  value,
  readOnly,
  onChange,
  onFlush,
  run,
}: {
  ws: string
  tableId: string
  field: Field
  recordId: string
  value: unknown
  readOnly?: boolean
  onChange: (r: TableRecordWithLinks) => void
  /** Registers a function that saves removals still waiting out their Undo; returns the unregister. */
  onFlush?: (flush: () => Promise<boolean>) => () => void
  /** Runs one upload or save of this record: one at a time, and the panel waits for it before closing. */
  run: (job: () => Promise<boolean>) => Promise<boolean>
}) {
  const files = Array.isArray(value) ? (value as FileMeta[]) : []
  const [gone, setGone] = useState<FileMeta[]>([]) // removed, waiting out the Undo
  const [ups, setUps] = useState<Up[]>([])
  const [errs, setErrs] = useState<string[]>([])
  const [over, setOver] = useState(false)
  const [kept, setKept] = useState<string[]>([]) // staged handles whose save failed for a reason that may pass
  const input = useRef<HTMLInputElement>(null)
  const abort = useRef<AbortController>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>(null)
  const keptRef = useRef(kept)
  keptRef.current = kept
  const goneRef = useRef(gone)
  goneRef.current = gone
  const changeRef = useRef(onChange)
  changeRef.current = onChange
  const shown = files.filter((f) => !gone.some((g) => g.id === f.id))
  const busy = ups.length > 0

  const save = useCallback(
    async (files: { add?: string[]; remove?: string[] }) => {
      changeRef.current(await updateRecord(ws, recordId, { files: { [field.id]: files } }))
    },
    [ws, recordId, field.id],
  )

  const flush = useCallback(async () => {
    if (timer.current) clearTimeout(timer.current)
    const ids = goneRef.current.map((f) => f.id)
    if (!ids.length) return keptRef.current.length === 0 // staged files not yet saved keep the panel open
    setGone([])
    return run(async () => {
      try {
        await save({ remove: ids })
        return true
      } catch (x) {
        setErrs([(x as Error).message])
        return false
      }
    })
  }, [save, run])

  /** Attaches staged handles. Only a definite rejection (4xx) discards them; on a network error or 5xx they are kept for Try again. */
  const attach = useCallback(
    async (handles: string[]) => {
      try {
        await save({ add: handles })
        setKept([])
        return true
      } catch (x) {
        const st = x instanceof CmdFailure ? x.status : 0
        if (st >= 400 && st < 500 && ![408, 425, 429].includes(st)) {
          setKept([])
          await Promise.all(handles.map((h) => discardFile(ws, h)))
        } else setKept(handles)
        setErrs((e) => [...e, (x as Error).message])
        return false
      }
    },
    [save, ws],
  )

  useEffect(() => onFlush?.(flush), [onFlush, flush])
  useEffect(
    () => () => {
      void flush()
    },
    [flush],
  )

  const remove = (f: FileMeta) => {
    setGone((g) => [...g, f])
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => void flush(), UNDO_MS)
  }
  const undo = (f: FileMeta) => {
    setGone((g) => g.filter((x) => x.id !== f.id))
  }

  async function add(picked: File[]) {
    const room = MAX_FILES_PER_CELL - shown.length
    const msgs: string[] = []
    const refuse = (m: string) => {
      msgs.push(m)
      return false
    }
    const ok = picked.filter((f) =>
      !EXT.test(f.name)
        ? refuse(`"${f.name}" was not added. ${FILE_TYPE_HELP}`)
        : f.size > MAX_FILE_BYTES
          ? refuse(`"${f.name}" is larger than 10 MB, so it was not added.`)
          : f.size === 0
            ? refuse(`"${f.name}" is empty, so it was not added.`)
            : true,
    )
    if (ok.length > room) {
      msgs.push(`A cell holds up to ${MAX_FILES_PER_CELL} files. ${ok.length - room} not added.`)
      ok.length = Math.max(room, 0)
    }
    setErrs(msgs)
    if (!ok.length) return
    const ctl = new AbortController()
    abort.current = ctl
    await run(async () => {
      const handles: string[] = []
      const failed: string[] = []
      setUps(ok.map((f, i) => ({ key: i, name: f.name, pct: 0 })))
      for (const [i, f] of ok.entries()) {
        try {
          const s = await stageFile(ws, tableId, field.id, f, {
            signal: ctl.signal,
            onProgress: (p) =>
              setUps((u) => u.map((x) => (x.key === i ? { ...x, pct: Math.round(p * 100) } : x))),
          })
          handles.push(s.handle)
        } catch (x) {
          if ((x as Error).name === 'AbortError') break
          failed.push(`"${f.name}" was not added. ${(x as Error).message}`)
        }
      }
      const saved = handles.length ? await attach(handles) : true
      abort.current = null
      setUps([])
      if (failed.length) setErrs((e) => [...e, ...failed])
      return saved
    })
  }

  const retry = () => {
    const handles = kept
    setErrs([])
    void run(() => attach(handles))
  }

  const pick = (list: FileList | null) => {
    if (list?.length) void add([...list])
    if (input.current) input.current.value = ''
  }

  return (
    <div className="space-y-2">
      {shown.length === 0 && !busy && <p className="text-[14px] text-muted">No files.</p>}
      {shown.length > 0 && (
        <ul aria-label={`Files in ${field.name}`} className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {shown.map((f) => (
            <li key={f.id} className="flex items-center gap-2 rounded-md border border-line p-1.5">
              {IMAGE.test(f.type) ? (
                // biome-ignore lint/performance/noImgElement: the served bytes are the thumbnail
                <img
                  src={fileUrl(ws, recordId, f.id)}
                  alt={f.name}
                  loading="lazy"
                  className="h-24 w-24 shrink-0 rounded object-cover"
                />
              ) : (
                <span className="flex h-24 w-24 shrink-0 items-center justify-center rounded bg-hover">
                  <FileText size={28} aria-hidden className="text-muted" />
                </span>
              )}
              <div className="min-w-0 flex-1">
                <a
                  href={fileUrl(ws, recordId, f.id)}
                  download={f.name}
                  className="block truncate text-[13px] font-medium text-accent hover:underline"
                  title={f.name}
                >
                  {f.name}
                </a>
                <p className="text-[12px] text-muted">{sizeText(f.size)}</p>
              </div>
              {!readOnly && (
                <button
                  type="button"
                  aria-label={`Remove ${f.name}`}
                  onClick={() => remove(f)}
                  className="flex min-h-11 min-w-11 items-center justify-center rounded hover:bg-hover sm:min-h-0 sm:min-w-0 sm:p-1.5"
                >
                  <X size={14} />
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      <div aria-live="polite" className="space-y-1">
        {gone.map((f) => (
          <p key={f.id} className="flex items-center gap-2 text-[13px]">
            <span className="min-w-0 truncate">Removed {f.name}.</span>
            <button type="button" onClick={() => undo(f)} className="font-medium text-accent hover:underline">
              Undo
            </button>
          </p>
        ))}
      </div>
      {ups.map((u) => (
        <div key={u.key} className="flex items-center gap-2 text-[13px]">
          <span className="min-w-0 flex-1 truncate">Uploading {u.name}</span>
          <progress aria-label={`Uploading ${u.name}`} max={100} value={u.pct} className="w-24 shrink-0" />
        </div>
      ))}
      {busy && (
        <button type="button" onClick={() => abort.current?.abort()} className={`${btn} min-h-11 sm:min-h-0`}>
          Cancel upload
        </button>
      )}
      {kept.length > 0 && !busy && (
        <button type="button" onClick={retry} className={`${btn} min-h-11 sm:min-h-0`}>
          Try again
        </button>
      )}
      {errs.map((m) => (
        <p key={m} role="alert" className="text-[13px] text-bad">
          {m}
        </p>
      ))}
      {!readOnly && (
        // biome-ignore lint/a11y/noStaticElementInteractions: drop target; the button inside is the keyboard route
        <div
          onDragOver={(e) => {
            e.preventDefault()
            setOver(true)
          }}
          onDragLeave={() => setOver(false)}
          onDrop={(e) => {
            e.preventDefault()
            setOver(false)
            pick(e.dataTransfer.files)
          }}
          className={`flex flex-col items-center gap-1 rounded-md border border-dashed p-3 text-center ${over ? 'border-accent bg-accent/10' : 'border-line'}`}
        >
          <input
            ref={input}
            type="file"
            multiple
            accept={FILE_ACCEPT}
            aria-label={`Choose files for ${field.name}`}
            className="sr-only"
            tabIndex={-1}
            disabled={busy}
            onChange={(e) => pick(e.target.files)}
          />
          <button
            type="button"
            disabled={busy || shown.length >= MAX_FILES_PER_CELL}
            onClick={() => input.current?.click()}
            className={`${btn} min-h-11 sm:min-h-0`}
          >
            <Upload size={14} /> Add file
          </button>
          <p className="text-[12px] text-muted">
            or drop files here. Pictures and PDF, up to 10 MB each, {MAX_FILES_PER_CELL} per cell.
          </p>
        </div>
      )}
    </div>
  )
}
