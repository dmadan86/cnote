'use client'
import { ChevronDown, Maximize2, Minimize2, Star } from 'lucide-react'
import Link from 'next/link'
import { useCallback, useEffect, useRef, useState } from 'react'
import { cmd } from '@/lib/api'
import {
  MINE_EVENT,
  type NoteChrome,
  noteMine,
  noteStar,
  noteViewed,
  type PageRef,
  type PageStatus,
  STATUS_LABEL,
  STATUS_TONE,
} from '@/lib/docs'
import { EMOJI } from '../editor/inserts'
import { btn, Pill } from '../ui'

const tell = () => window.dispatchEvent(new Event(MINE_EVENT))

/** Closes on an outside press or Escape; returns the element to hang the popover from. */
function usePopover() {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const out = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false)
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', out)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', out)
      document.removeEventListener('keydown', esc)
    }
  }, [open])
  return { open, setOpen, ref }
}

/** Status lozenge; editors click it to choose Draft, In review, Final or none. Verified stays the freshness pill. */
export function StatusMenu({
  ws,
  id,
  status,
  canEdit,
  onChange,
}: {
  ws: string
  id: string
  status: PageStatus | null
  canEdit: boolean
  onChange: () => void
}) {
  const { open, setOpen, ref } = usePopover()
  const label = status ? STATUS_LABEL[status] : 'No status'
  if (!canEdit) return status ? <Pill tone={STATUS_TONE[status]}>{label}</Pill> : null
  const set = async (s: PageStatus | null) => {
    setOpen(false)
    await cmd(ws, 'note.update', { id, status: s })
    onChange()
  }
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-label={`Page status: ${label}`}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="inline-flex items-center gap-1 rounded-full hover:opacity-80"
      >
        <Pill tone={status ? STATUS_TONE[status] : 'neutral'}>{label}</Pill>
        <ChevronDown size={12} className="text-muted" />
      </button>
      {open && (
        <div
          role="menu"
          aria-label="Page status"
          className="absolute left-0 z-30 mt-1 min-w-40 rounded-md border border-line bg-canvas p-1 shadow-lg"
        >
          {([null, 'draft', 'in_review', 'final'] as (PageStatus | null)[]).map((s) => (
            <button
              key={s ?? 'none'}
              type="button"
              role="menuitemradio"
              aria-checked={s === status}
              onClick={() => set(s)}
              className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover aria-checked:font-semibold"
            >
              {s ? STATUS_LABEL[s] : 'No status'}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** The page emoji, left of the title. Editors pick from the editor's emoji set. */
export function EmojiPicker({
  ws,
  id,
  icon,
  canEdit,
  onChange,
}: {
  ws: string
  id: string
  icon: string | null
  canEdit: boolean
  onChange: () => void
}) {
  const { open, setOpen, ref } = usePopover()
  if (!canEdit)
    return icon ? (
      <span aria-hidden="true" className="mr-2 text-3xl">
        {icon}
      </span>
    ) : null
  const set = async (e: string | null) => {
    setOpen(false)
    await cmd(ws, 'note.update', { id, icon: e })
    onChange()
  }
  return (
    <div ref={ref} className="relative shrink-0">
      <button
        type="button"
        aria-label={icon ? `Page emoji ${icon}, change` : 'Add page emoji'}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="mr-2 grid size-10 place-items-center rounded text-3xl hover:bg-hover"
      >
        {icon ?? <span className="text-[13px] text-muted">+ Icon</span>}
      </button>
      {open && (
        <div
          role="dialog"
          aria-label="Choose an emoji"
          className="absolute left-0 z-30 mt-1 grid w-[17rem] grid-cols-8 gap-0.5 rounded-lg border border-line bg-canvas p-2 shadow-xl"
        >
          {EMOJI.map((m) => (
            <button
              key={m}
              type="button"
              aria-label={m}
              onClick={() => set(m)}
              className="grid size-8 place-items-center rounded text-[18px] hover:bg-hover focus:bg-hover focus:outline-none"
            >
              {m}
            </button>
          ))}
          {icon && (
            <button
              type="button"
              onClick={() => set(null)}
              className="col-span-8 mt-1 rounded px-2 py-1 text-left text-[13px] text-muted hover:bg-hover"
            >
              Remove emoji
            </button>
          )}
        </div>
      )}
    </div>
  )
}

export function WideToggle({
  ws,
  id,
  wide,
  onChange,
}: {
  ws: string
  id: string
  wide: boolean
  onChange: () => void
}) {
  return (
    <button
      type="button"
      aria-pressed={wide}
      aria-label="Wide page"
      title={wide ? 'Use the normal page width' : 'Use the full page width'}
      onClick={async () => {
        await cmd(ws, 'note.update', { id, wide: !wide })
        onChange()
      }}
      className={btn}
    >
      {wide ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
    </button>
  )
}

/** Star toggle; the sidebar's Starred list reloads on the shared event. */
export function StarButton({ ws, id }: { ws: string; id: string }) {
  const [starred, setStarred] = useState(false)
  const [ready, setReady] = useState(false)
  useEffect(() => {
    let live = true
    setReady(false)
    noteMine(ws, 'starred', 100)
      .then((l) => live && setStarred(l.some((p) => p.id === id)))
      .catch(() => {})
      .finally(() => live && setReady(true))
    return () => {
      live = false
    }
  }, [ws, id])
  return (
    <button
      type="button"
      aria-pressed={starred}
      aria-label={starred ? 'Unstar page' : 'Star page'}
      disabled={!ready}
      onClick={async () => {
        const next = !starred
        setStarred(next)
        try {
          await noteStar(ws, id, next)
          tell()
        } catch {
          setStarred(!next)
        }
      }}
      className={btn}
    >
      <Star size={14} className={starred ? 'fill-current' : ''} />
    </button>
  )
}

/** Records an opening after the page has stayed open for a moment, so quick passes through the tree do not count. */
export function useRecordViewed(ws: string, id: string, delayMs = 3000) {
  useEffect(() => {
    const t = setTimeout(() => {
      noteViewed(ws, id)
        .then(tell)
        .catch(() => {})
    }, delayMs)
    return () => clearTimeout(t)
  }, [ws, id, delayMs])
}

/** One of the person's own lists, kept fresh by MINE_EVENT. */
export function useMine(ws: string, list: 'recent' | 'starred', limit: number) {
  const [rows, setRows] = useState<PageRef[]>([])
  const load = useCallback(
    () =>
      noteMine(ws, list, limit)
        .then(setRows)
        .catch(() => setRows([])),
    [ws, list, limit],
  )
  useEffect(() => {
    load()
    window.addEventListener(MINE_EVENT, load)
    return () => window.removeEventListener(MINE_EVENT, load)
  }, [load])
  return rows
}

/** Starred and Recent sections for the sidebar. They render nothing while empty. */
export function MineSections({ ws }: { ws: string }) {
  const starred = useMine(ws, 'starred', 20)
  const recent = useMine(ws, 'recent', 5)
  return (
    <>
      {(
        [
          ['Starred', starred],
          ['Recent', recent],
        ] as const
      ).map(
        ([name, rows]) =>
          rows.length > 0 && (
            <div key={name} className="mb-1" data-testid={`mine-${name.toLowerCase()}`}>
              <div
                id={`mine-${name}`}
                className="px-2 py-1 text-[11px] font-semibold uppercase tracking-wide text-muted"
              >
                {name}
              </div>
              <ul aria-labelledby={`mine-${name}`}>
                {rows.map((p) => (
                  <li key={p.id}>
                    <Link
                      href={`/w/${ws}/n/${p.id}`}
                      className="flex items-center gap-1.5 rounded py-1 pr-2 pl-4 text-[13px] hover:bg-hover"
                    >
                      <span aria-hidden="true" className="w-4 shrink-0 text-center text-[12px]">
                        {p.icon ?? '·'}
                      </span>
                      <span className="truncate">{p.title || 'Untitled'}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ),
      )}
    </>
  )
}

export { ExportMenu } from './export-menu'
export type { NoteChrome }
