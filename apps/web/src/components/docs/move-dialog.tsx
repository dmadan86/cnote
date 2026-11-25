'use client'
import { useMemo, useState } from 'react'
import type { Note } from '@/lib/api'
import { noteMove, noteReorder, TREE_EVENT } from '@/lib/docs'
import { useShell } from '../shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '../ui'
import { useTreeActions } from './page-actions'
import { flatten, kidKey, subtreeIds } from './tree-model'

/** Keyboard and phone alternative to dragging: pick a new parent in the same space and a position. */
export function MoveDialog({ note, onClose }: { note: Note; onClose: () => void }) {
  const { ws, spaces } = useShell()
  const { kids, announce } = useTreeActions()
  const [parent, setParent] = useState(note.parent_id ?? '')
  const [before, setBefore] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const own = useMemo(() => subtreeIds(kids, note.id), [kids, note.id])
  const options = useMemo(() => flatten(kids, note.space_id, own), [kids, note.space_id, own])
  const sibs = (kids.get(kidKey(note.space_id, parent || null)) ?? []).filter((s) => s.id !== note.id)
  const space = spaces.find((s) => s.id === note.space_id)?.name ?? 'Space'

  async function go() {
    setBusy(true)
    setErr(undefined)
    try {
      const p = parent || null
      if (p === (note.parent_id ?? null)) await noteReorder(ws, note.id, before || null)
      else await noteMove(ws, note.id, p, before || null)
      announce(`Moved ${note.title || 'Untitled'}.`)
      window.dispatchEvent(new Event(TREE_EVENT))
      onClose()
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }
  return (
    <Modal open onClose={onClose} title="Move page">
      <p className="mb-3 text-[13px] text-muted">
        Move "{note.title || 'Untitled'}" and its sub-pages within {space}.
      </p>
      <div className="space-y-3">
        <Field label="New parent">
          {(id) => (
            <select
              id={id}
              className={inputCls}
              value={parent}
              onChange={(e) => {
                setParent(e.target.value)
                setBefore('')
              }}
            >
              <option value="">{space} (top level)</option>
              {options.map(({ note: o, depth }) => (
                <option key={o.id} value={o.id}>
                  {'– '.repeat(depth)}
                  {o.title || 'Untitled'}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Position">
          {(id) => (
            <select id={id} className={inputCls} value={before} onChange={(e) => setBefore(e.target.value)}>
              <option value="">At the end</option>
              {sibs.map((s) => (
                <option key={s.id} value={s.id}>
                  Before {s.title || 'Untitled'}
                </option>
              ))}
            </select>
          )}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="button" className={btnPrimary} disabled={busy} onClick={go}>
            Move
          </button>
        </div>
      </div>
    </Modal>
  )
}
