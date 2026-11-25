'use client'
import { Plus, X } from 'lucide-react'
import { useEffect, useId, useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls, Modal } from '@/components/ui'
import { createRecord, type LinkChip, searchRecords, type Table } from '@/lib/tables'

type Hit = { id: string; title: string; preview: string[] }

/** Searches the target table (record.search) and returns the chosen chips through onDone. */
export function LinkPicker({
  ws,
  target,
  value,
  multiple,
  canCreate,
  onDone,
  onClose,
}: {
  ws: string
  target: Table
  value: LinkChip[]
  multiple: boolean
  canCreate: boolean
  onDone: (chips: LinkChip[]) => void
  onClose: () => void
}) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<Hit[]>([])
  const [chosen, setChosen] = useState(value)
  const [active, setActive] = useState(0)
  const [err, setErr] = useState<string>()
  const list = useId()
  const optId = (i: number) => `${list}-${i}`

  useEffect(() => {
    let live = true
    const t = setTimeout(() => {
      searchRecords(ws, target.id, q.trim(), 20)
        .then((h) => {
          if (!live) return
          setHits(h)
          setActive(0)
          setErr(undefined)
        })
        .catch((e) => live && setErr((e as Error).message))
    }, 200)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [ws, target.id, q])

  const pick = (h: { id: string; title: string }) =>
    setChosen((c) =>
      c.some((x) => x.id === h.id)
        ? c.filter((x) => x.id !== h.id)
        : multiple
          ? [...c, { id: h.id, title: h.title }]
          : [{ id: h.id, title: h.title }],
    )
  const showCreate =
    canCreate && q.trim() !== '' && !hits.some((h) => h.title.toLowerCase() === q.trim().toLowerCase())
  const rows = hits.length + (showCreate ? 1 : 0)

  async function create() {
    try {
      const r = await createRecord(ws, target.id, { [target.primary_field_id]: q.trim() })
      pick({ id: r.id, title: q.trim() })
      setQ('')
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const activate = (i: number) => (i < hits.length ? pick(hits[i]) : create())

  return (
    <Modal open onClose={onClose} title={`Link to ${target.name}`} className="max-w-md">
      <div className="space-y-3">
        <input
          role="combobox"
          aria-expanded="true"
          aria-controls={list}
          aria-activedescendant={rows ? optId(active) : undefined}
          aria-label={`Search ${target.name}`}
          placeholder={`Search ${target.name.toLowerCase()}`}
          // biome-ignore lint/a11y/noAutofocus: the dialog exists to search
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setActive((a) => Math.min(a + 1, rows - 1))
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setActive((a) => Math.max(a - 1, 0))
            } else if (e.key === 'Enter' && rows) {
              e.preventDefault()
              activate(active)
            }
          }}
          className={inputCls}
        />
        <div
          id={list}
          role="listbox"
          aria-multiselectable={multiple}
          aria-label="Matching records"
          className="max-h-60 overflow-auto"
        >
          {hits.map((h, i) => {
            const on = chosen.some((c) => c.id === h.id)
            return (
              // biome-ignore lint/a11y/useKeyWithClickEvents: keys go through the combobox input
              // biome-ignore lint/a11y/useFocusableInteractive: options are reached by aria-activedescendant on the combobox input
              <div
                key={h.id}
                role="option"
                id={optId(i)}
                aria-selected={on}
                onClick={() => pick(h)}
                className={`flex cursor-pointer items-baseline gap-3 rounded px-2 py-1.5 text-[13px] ${i === active ? 'bg-hover' : ''} ${on ? 'font-medium' : ''}`}
              >
                <span className="min-w-0 flex-1 truncate">{h.title || 'Untitled'}</span>
                <span className="truncate text-[12px] font-normal text-muted">
                  {h.preview.filter(Boolean).join('  ')}
                </span>
              </div>
            )
          })}
          {showCreate && (
            // biome-ignore lint/a11y/useKeyWithClickEvents: keys go through the combobox input
            // biome-ignore lint/a11y/useFocusableInteractive: options are reached by aria-activedescendant on the combobox input
            <div
              id={optId(hits.length)}
              role="option"
              aria-selected={false}
              onClick={create}
              className={`flex cursor-pointer items-center gap-1.5 rounded px-2 py-1.5 text-[13px] ${active === hits.length ? 'bg-hover' : ''}`}
            >
              <Plus size={14} /> Create "{q.trim()}"
            </div>
          )}
          {rows === 0 && (
            <div className="px-2 py-1.5 text-[13px] text-muted">
              {q ? 'No records match.' : `${target.name} has no records yet.`}
            </div>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[12px] text-muted">Linked:</span>
          {chosen.length === 0 && <span className="text-[12px] text-muted">none</span>}
          {chosen.map((c) => (
            <span
              key={c.id}
              className="inline-flex items-center gap-1 rounded-full bg-hover px-2 py-0.5 text-[12px]"
            >
              {c.title}
              <button
                type="button"
                aria-label={`Remove ${c.title}`}
                onClick={() => setChosen(chosen.filter((x) => x.id !== c.id))}
              >
                <X size={12} />
              </button>
            </span>
          ))}
        </div>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btn}>
            Cancel
          </button>
          <button
            type="button"
            onClick={() => {
              onDone(chosen)
              onClose()
            }}
            className={btnPrimary}
          >
            Done
          </button>
        </div>
      </div>
    </Modal>
  )
}
