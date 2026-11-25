'use client'
import { Tag, X } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useId, useState } from 'react'
import { type LabelCount, labelList, labelSet } from '@/lib/docs'

const LABEL_RE = /^[^\s,]{1,40}$/

/** Label chips under the title. Editors add (with autocomplete from label.list) and remove; a chip opens search for it. */
export function LabelChips({
  ws,
  noteId,
  labels,
  canEdit,
  onChange,
}: {
  ws: string
  noteId: string
  labels: string[]
  canEdit: boolean
  onChange: (labels: string[]) => void
}) {
  const [text, setText] = useState('')
  const [adding, setAdding] = useState(false)
  const [sugg, setSugg] = useState<LabelCount[]>([])
  const [err, setErr] = useState('')
  const listId = useId()
  useEffect(() => {
    if (!adding) return
    let live = true
    const t = setTimeout(() => {
      labelList(ws, text.trim().toLowerCase(), 8)
        .then((l) => live && setSugg(l.filter((x) => !labels.includes(x.label))))
        .catch(() => {})
    }, 150)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [ws, text, adding, labels])

  async function save(next: string[]) {
    setErr('')
    try {
      onChange((await labelSet(ws, noteId, next)).labels)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Could not save labels')
    }
  }
  async function add(raw: string) {
    const l = raw.trim().toLowerCase()
    if (!l) return setAdding(false)
    if (!LABEL_RE.test(l)) return setErr('A label has no spaces or commas and is at most 40 characters')
    if (!labels.includes(l)) await save([...labels, l])
    setText('')
    setAdding(false)
  }

  if (!canEdit && labels.length === 0) return null
  return (
    <fieldset className="mb-4 flex flex-wrap items-center gap-1.5">
      <legend className="sr-only">Labels</legend>
      <Tag size={13} className="text-muted" aria-hidden="true" />
      {labels.map((l) => (
        <span
          key={l}
          className="inline-flex items-center gap-1 rounded-full bg-hover px-2 py-0.5 text-[12px]"
        >
          <Link href={`/w/${ws}/search?label=${encodeURIComponent(l)}`} className="hover:underline">
            {l}
          </Link>
          {canEdit && (
            <button
              type="button"
              aria-label={`Remove label ${l}`}
              onClick={() => save(labels.filter((x) => x !== l))}
              className="rounded text-muted hover:text-fg"
            >
              <X size={11} />
            </button>
          )}
        </span>
      ))}
      {canEdit &&
        (adding ? (
          <>
            <input
              aria-label="New label"
              list={listId}
              value={text}
              ref={(el) => el?.focus()}
              maxLength={40}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ',') {
                  e.preventDefault()
                  add(text)
                }
                if (e.key === 'Escape') {
                  setAdding(false)
                  setText('')
                }
              }}
              onBlur={() => text.trim() === '' && setAdding(false)}
              className="w-32 rounded border border-line bg-canvas px-2 py-0.5 text-[12px]"
            />
            <datalist id={listId}>
              {sugg.map((s) => (
                <option key={s.label} value={s.label}>
                  {s.count}
                </option>
              ))}
            </datalist>
          </>
        ) : (
          <button
            type="button"
            onClick={() => setAdding(true)}
            className="rounded-full border border-dashed border-line px-2 py-0.5 text-[12px] text-muted hover:bg-hover"
          >
            Add label
          </button>
        ))}
      {err && (
        <span role="alert" className="text-[12px] text-bad">
          {err}
        </span>
      )}
    </fieldset>
  )
}
