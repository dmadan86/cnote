'use client'
import type { Editor } from '@tiptap/react'
import { CalendarDays, UserPlus, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { formatDate } from './blocks'

// Tasks (docs-parity-b 4.4): in a to-do item the assignee is the first mention and the due date the first date chip, both stored
// forms that A1 already writes. This adds two buttons under the focused item that use the existing "@" picker and the date mark,
// and opens the page at a task when the URL says `#task-<n>` (n counts to-do items in document order, as the server's index does).

type Range = { from: number; to: number }
type Item = { to: number; pos: number; name?: string; link?: Range; due?: string; date?: Range }

/** The to-do item holding the selection: where its first paragraph ends, who it is assigned to and when it is due. */
function readItem(editor: Editor): Item | null {
  const { $from } = editor.state.selection
  for (let d = $from.depth; d > 0; d--) {
    const node = $from.node(d)
    if (node.type.name !== 'taskItem') continue
    const para = node.firstChild
    if (!para) return null
    const start = $from.start(d) + 1
    const item: Item = { to: start + para.content.size, pos: $from.before(d) }
    para.forEach((child, offset) => {
      const at = start + offset
      const link = child.marks.find(
        (m) => m.type.name === 'link' && String(m.attrs.href).includes('/people/'),
      )
      if (link && !item.link) {
        item.link = { from: at, to: at + child.nodeSize }
        item.name = (child.text ?? '').replace(/^@/, '')
      }
      const date = child.marks.find((m) => m.type.name === 'date')
      if (date && !item.date) {
        item.date = { from: at, to: at + child.nodeSize }
        item.due = String(date.attrs.datetime)
      }
    })
    return item
  }
  return null
}

export function TaskControls({ editor, editable }: { editor: Editor; editable: boolean }) {
  const [item, setItem] = useState<Item | null>(null)
  const [box, setBox] = useState<{ top: number; left: number } | null>(null)
  const [picking, setPicking] = useState(false)
  const dateRef = useRef<HTMLInputElement>(null)
  const hasDate = !!editor.schema.marks.date

  const refresh = useCallback(() => {
    const it = editable ? readItem(editor) : null
    setItem(it)
    if (!it) return setPicking(false)
    const el = editor.view.nodeDOM(it.pos) as HTMLElement | null
    const r = el?.getBoundingClientRect()
    setBox(r ? { top: r.top + Math.min(r.height, 28) + 2, left: Math.max(8, r.left) } : null)
  }, [editor, editable])
  useEffect(() => {
    refresh()
    editor.on('selectionUpdate', refresh)
    editor.on('transaction', refresh)
    window.addEventListener('scroll', refresh, true)
    window.addEventListener('resize', refresh)
    return () => {
      editor.off('selectionUpdate', refresh)
      editor.off('transaction', refresh)
      window.removeEventListener('scroll', refresh, true)
      window.removeEventListener('resize', refresh)
    }
  }, [editor, refresh])
  useEffect(() => {
    if (picking) dateRef.current?.focus()
  }, [picking])

  // Open the page at a task: the document may still be loading (live editing), so look for a few seconds.
  useEffect(() => {
    const n = /^#task-(\d+)$/.exec(window.location.hash)?.[1]
    if (n === undefined) return
    let tries = 0
    const t = setInterval(() => {
      let seen = -1
      let pos = -1
      editor.state.doc.descendants((node, p) => {
        if (node.type.name === 'taskItem' && ++seen === Number(n)) pos = p
        return pos < 0
      })
      const el = pos >= 0 ? (editor.view.nodeDOM(pos) as HTMLElement | null) : null
      if (el) {
        clearInterval(t)
        el.scrollIntoView({ block: 'center' })
        el.animate?.([{ backgroundColor: 'rgba(250, 204, 21, 0.45)' }, { backgroundColor: 'transparent' }], {
          duration: 2400,
        })
      } else if (++tries > 40) clearInterval(t)
    }, 250)
    return () => clearInterval(t)
  }, [editor])

  if (!editable || !item || !box) return null
  const assign = () => {
    const chain = editor.chain().focus()
    // Reassigning replaces the first mention; the "@" picker then offers the people.
    const cur = readItem(editor)
    if (cur?.link) chain.deleteRange(cur.link).insertContentAt(cur.link.from, '@')
    else chain.insertContentAt(item.to, ' @')
    chain.run()
  }
  const setDue = (iso: string) => {
    setPicking(false)
    if (!/^\d{4}-\d{2}-\d{2}$/.test(iso)) return
    const node = { type: 'text', text: formatDate(iso), marks: [{ type: 'date', attrs: { datetime: iso } }] }
    const cur = readItem(editor)
    const chain = editor.chain().focus()
    if (cur?.date) chain.insertContentAt(cur.date, node)
    else chain.insertContentAt(item.to, [{ type: 'text', text: ' ' }, node])
    chain.run()
  }
  const clearDue = () => {
    const cur = readItem(editor)
    if (cur?.date) editor.chain().focus().deleteRange(cur.date).run()
  }
  const keep = (e: React.MouseEvent) => {
    if ((e.target as HTMLElement).tagName !== 'INPUT') e.preventDefault() // keep the editor selection
  }
  const chip =
    'inline-flex items-center gap-1 rounded-md border border-line bg-canvas px-2 py-1 text-[12px] hover:bg-hover'
  return createPortal(
    <fieldset
      aria-label="Task options"
      onMouseDown={keep}
      style={{ position: 'fixed', top: box.top, left: box.left, zIndex: 40 }}
      className="flex items-center gap-1.5 rounded-lg border border-line bg-canvas p-1 text-fg shadow-lg"
    >
      <button
        type="button"
        className={chip}
        onClick={assign}
        aria-label={item.name ? `Assigned to ${item.name}, change` : 'Assign task'}
      >
        <UserPlus size={13} aria-hidden="true" />
        {item.name ? item.name : 'Assign'}
      </button>
      {hasDate &&
        (picking ? (
          <input
            ref={dateRef}
            type="date"
            aria-label="Due date"
            defaultValue={item.due}
            onChange={(e) => setDue(e.target.value)}
            onBlur={() => setPicking(false)}
            onKeyDown={(e) => e.key === 'Escape' && setPicking(false)}
            className="rounded-md border border-line bg-canvas px-1.5 py-0.5 text-[12px]"
          />
        ) : (
          <button
            type="button"
            className={chip}
            onClick={() => setPicking(true)}
            aria-label={item.due ? `Due ${formatDate(item.due)}, change` : 'Set due date'}
          >
            <CalendarDays size={13} aria-hidden="true" />
            {item.due ? formatDate(item.due) : 'Due'}
          </button>
        ))}
      {item.date && !picking && (
        <button type="button" className={chip} onClick={clearDue} aria-label="Remove due date">
          <X size={13} aria-hidden="true" />
        </button>
      )}
    </fieldset>,
    document.body,
  )
}
