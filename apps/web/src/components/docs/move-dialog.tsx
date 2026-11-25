'use client'
import { useEffect, useMemo, useState } from 'react'
import { cmd } from '@/lib/api'
import { noteMove, noteReorder, TREE_EVENT } from '@/lib/docs'
import { useShell } from '../shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '../ui'
import { type PageRef, useTreeActions } from './page-actions'
import { flatten, kidKey, subtreeIds } from './tree-model'

type Hit = { id: string; space_id: string; title: string; types?: string[] }

/**
 * Keyboard and phone alternative to dragging: pick a new parent in the same space and a position. The parents are the pages the
 * tree has loaded; "Find a page" reaches the rest by title, and the position list loads the chosen parent's level.
 */
export function MoveDialog({ note, onClose }: { note: PageRef; onClose: () => void }) {
  const { ws, spaces, tree } = useShell()
  const { kids, announce } = useTreeActions()
  const [parent, setParent] = useState(note.parent_id && tree.byId.has(note.parent_id) ? note.parent_id : '')
  const [before, setBefore] = useState('')
  const [q, setQ] = useState('')
  const [found, setFound] = useState<Hit[]>([])
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const own = useMemo(() => subtreeIds(kids, note.id), [kids, note.id])
  const options = useMemo(() => flatten(kids, note.space_id, own), [kids, note.space_id, own])
  const listed = useMemo(() => new Set(options.map((o) => o.note.id)), [options])
  const extra = found.filter((h) => !listed.has(h.id) && !own.has(h.id) && !h.types?.length)
  // the chosen parent's children, for the position list
  const { ensure } = tree
  useEffect(() => ensure(note.space_id, parent || null), [ensure, note.space_id, parent])
  const key = kidKey(note.space_id, parent || null)
  const sibs = (kids.get(key) ?? []).filter((s) => s.id !== note.id)
  const root = tree.levels.get(kidKey(note.space_id, null))
  const space = spaces.find((s) => s.id === note.space_id)?.name ?? 'Space'

  useEffect(() => {
    if (q.trim().length < 2) return setFound([])
    let live = true
    const t = setTimeout(
      () =>
        cmd<Hit[]>(ws, 'search.notes', { q, title_only: true, space_id: note.space_id, limit: 10 })
          .then((r) => live && setFound(r))
          .catch(() => live && setFound([])),
      200,
    )
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [ws, q, note.space_id])

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
        <Field label="Find a page">
          {(id) => (
            <input
              id={id}
              className={inputCls}
              value={q}
              placeholder="Search by title to pick a page that is not listed"
              onChange={(e) => setQ(e.target.value)}
            />
          )}
        </Field>
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
              {extra.length > 0 && (
                <optgroup label="Search results">
                  {extra.map((h) => (
                    <option key={h.id} value={h.id}>
                      {h.title || 'Untitled'}
                    </option>
                  ))}
                </optgroup>
              )}
              {parent && !listed.has(parent) && !extra.some((h) => h.id === parent) && (
                <option value={parent}>{tree.byId.get(parent)?.title || 'Untitled'}</option>
              )}
            </select>
          )}
        </Field>
        {root?.next && (
          <button type="button" className={btn} onClick={() => tree.more(note.space_id, null, 'next')}>
            Show more top-level pages
          </button>
        )}
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
        {tree.levels.get(key)?.next && (
          <button
            type="button"
            className={btn}
            onClick={() => tree.more(note.space_id, parent || null, 'next')}
          >
            Show more positions
          </button>
        )}
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
