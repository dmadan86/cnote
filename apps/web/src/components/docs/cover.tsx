'use client'
import { ImagePlus } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { type Cover as CoverData, setCover } from '@/lib/docs'
import { uploadImage } from '../editor/blocks'
import { btn, btnPrimary, ErrorText } from '../ui'

/** Built-in covers are drawn here and uploaded as an ordinary page image, so every consumer sees one kind of cover. */
export const GRADIENTS: { name: string; from: string; to: string }[] = [
  { name: 'Sunset', from: '#f97316', to: '#db2777' },
  { name: 'Ocean', from: '#0ea5e9', to: '#4f46e5' },
  { name: 'Forest', from: '#16a34a', to: '#0f766e' },
  { name: 'Dusk', from: '#7c3aed', to: '#1e3a8a' },
  { name: 'Sand', from: '#fbbf24', to: '#fb7185' },
  { name: 'Slate', from: '#64748b', to: '#1e293b' },
]

function gradientFile(g: (typeof GRADIENTS)[number]): Promise<File> {
  const c = document.createElement('canvas')
  c.width = 1600
  c.height = 400
  const x = c.getContext('2d')
  if (!x) return Promise.reject(new Error('This browser cannot draw a cover.'))
  const grad = x.createLinearGradient(0, 0, 1600, 400)
  grad.addColorStop(0, g.from)
  grad.addColorStop(1, g.to)
  x.fillStyle = grad
  x.fillRect(0, 0, 1600, 400)
  return new Promise((ok, no) =>
    c.toBlob(
      (b) =>
        b
          ? ok(new File([b], `cover-${g.name.toLowerCase()}.png`, { type: 'image/png' }))
          : no(new Error('Could not draw the cover.')),
      'image/png',
    ),
  )
}

const clamp = (n: number) => Math.min(100, Math.max(0, Math.round(n)))
const STEP = 5

/**
 * The page cover (docs-parity-c 5.3), above the title. Anyone who can read the page sees it; `canEdit` (edit access and not archived)
 * adds Add cover, Change, Reposition and Remove. The server checks the same rules. The image is a page file shown through
 * the page's own file route, so it uses the existing upload and session.
 */
export function Cover({
  ws,
  noteId,
  title,
  cover,
  wide,
  canEdit,
  onChange,
}: {
  ws: string
  noteId: string
  title: string
  cover?: CoverData
  wide: boolean
  canEdit: boolean
  onChange: () => void
}) {
  const [menu, setMenu] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const [broken, setBroken] = useState(false) // the file is gone: a cover whose file was deleted does not render
  const [drag, setDrag] = useState<number | null>(null) // the position while repositioning
  const band = useRef<HTMLDivElement>(null)
  const start = useRef<{ y: number; at: number } | null>(null)
  const pick = useRef<HTMLInputElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new file shows again
  useEffect(() => setBroken(false), [cover?.file_id])
  useEffect(() => {
    if (!menu) return
    const out = (e: MouseEvent) => !menuRef.current?.contains(e.target as Node) && setMenu(false)
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setMenu(false)
    document.addEventListener('mousedown', out)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', out)
      document.removeEventListener('keydown', esc)
    }
  }, [menu])

  const run = async (f: () => Promise<unknown>) => {
    setBusy(true)
    setErr(undefined)
    try {
      await f()
      onChange()
      setBusy(false)
      return true
    } catch (e) {
      setErr(e as Error)
    }
    setBusy(false)
    return false
  }
  const upload = (get: () => Promise<File>) =>
    run(async () => {
      setMenu(false)
      const img = await uploadImage(ws, noteId, await get())
      await setCover(ws, noteId, { file_id: img.src.slice(img.src.lastIndexOf('/') + 1), y: 50 })
    })

  const y = drag ?? cover?.y ?? 50
  const shown = cover && !broken
  if (!shown && !canEdit) return null

  const tools =
    'flex flex-wrap gap-1 opacity-100 transition-opacity md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100'
  const chooser = (
    <div
      ref={menuRef}
      role="dialog"
      aria-label="Choose a cover"
      className="absolute left-0 top-full z-30 mt-1 w-72 max-w-[calc(100vw-2rem)] space-y-2 rounded-lg border border-line bg-canvas p-2 shadow-xl"
    >
      <button type="button" className={`${btn} w-full`} onClick={() => pick.current?.click()}>
        <ImagePlus size={14} /> Upload an image
      </button>
      <fieldset className="m-0 grid grid-cols-3 gap-1.5 border-0 p-0">
        <legend className="sr-only">Built-in covers</legend>
        {GRADIENTS.map((g) => (
          <button
            key={g.name}
            type="button"
            aria-label={`${g.name} gradient`}
            title={g.name}
            onClick={() => upload(() => gradientFile(g))}
            className="h-10 rounded border border-line focus:outline-2 focus:outline-accent"
            style={{ background: `linear-gradient(135deg, ${g.from}, ${g.to})` }}
          />
        ))}
      </fieldset>
    </div>
  )
  const fileInput = (
    <input
      ref={pick}
      type="file"
      hidden
      data-testid="cover-file"
      accept="image/png,image/jpeg,image/gif,image/webp"
      onChange={(e) => {
        const f = e.target.files?.[0]
        e.target.value = ''
        if (f) upload(async () => f)
      }}
    />
  )

  if (!shown)
    return (
      <div className="group relative mb-2 h-8">
        <div className={tools}>
          <button
            type="button"
            className={btn}
            aria-haspopup="dialog"
            aria-expanded={menu}
            onClick={() => setMenu(!menu)}
          >
            <ImagePlus size={14} /> Add cover
          </button>
        </div>
        {menu && chooser}
        {fileInput}
        <ErrorText error={err} />
      </div>
    )

  return (
    <div className="group relative mb-4" data-testid="page-cover">
      <div
        ref={band}
        className={`relative overflow-hidden rounded-lg bg-hover ${wide ? 'h-[280px]' : 'h-[200px]'} max-md:h-[140px] ${drag !== null ? 'ring-2 ring-accent' : ''}`}
      >
        {drag !== null && (
          <div
            role="slider"
            tabIndex={0}
            aria-label="Cover position"
            aria-orientation="vertical"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={drag}
            className="absolute inset-0 z-10 cursor-grab touch-none"
            onPointerDown={(e) => {
              e.currentTarget.setPointerCapture(e.pointerId)
              start.current = { y: e.clientY, at: drag }
            }}
            onPointerMove={(e) => {
              const s = start.current
              if (s && band.current)
                setDrag(clamp(s.at - ((e.clientY - s.y) / band.current.clientHeight) * 100))
            }}
            onPointerUp={() => {
              start.current = null
            }}
            onKeyDown={(e) => {
              if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
              e.preventDefault()
              setDrag(clamp(drag + (e.key === 'ArrowDown' ? STEP : -STEP)))
            }}
          />
        )}
        {/* biome-ignore lint/performance/noImgElement: a same-origin, session-authenticated page file */}
        <img
          src={`/api/w/${ws}/files/${cover.file_id}`}
          alt={`Cover of ${title || 'Untitled'}`}
          draggable={false}
          onError={() => setBroken(true)}
          className="size-full select-none object-cover"
          style={{ objectPosition: `50% ${y}%` }}
        />
      </div>
      {canEdit && (
        <div className="absolute right-2 bottom-2 z-20 flex flex-col items-end gap-1">
          {drag !== null ? (
            <div className="flex gap-1">
              <span className="rounded bg-canvas/90 px-2 py-1 text-[12px]">Drag image to reposition</span>
              <button type="button" className={btn} onClick={() => setDrag(null)}>
                Cancel
              </button>
              <button
                type="button"
                className={btnPrimary}
                disabled={busy}
                onClick={async () =>
                  (await run(() => setCover(ws, noteId, { file_id: cover.file_id, y: drag }))) &&
                  setDrag(null)
                }
              >
                Save position
              </button>
            </div>
          ) : (
            <div className={tools}>
              <button
                type="button"
                className={`${btn} bg-canvas/90`}
                aria-haspopup="dialog"
                aria-expanded={menu}
                onClick={() => setMenu(!menu)}
              >
                Change
              </button>
              <button type="button" className={`${btn} bg-canvas/90`} onClick={() => setDrag(cover.y ?? 50)}>
                Reposition
              </button>
              <button
                type="button"
                className={`${btn} bg-canvas/90`}
                disabled={busy}
                onClick={() => run(() => setCover(ws, noteId, null))}
              >
                Remove
              </button>
            </div>
          )}
        </div>
      )}
      {menu && chooser}
      {fileInput}
      <ErrorText error={err} />
    </div>
  )
}
