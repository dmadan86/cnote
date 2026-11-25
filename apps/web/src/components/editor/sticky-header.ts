import type { Node as PMNode } from '@tiptap/pm/model'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { Extension } from '@tiptap/react'

// docs-parity C1 5.2: the header row follows the scroll while its table crosses the top of the page. Always on, never
// stored. The offset is a decoration, not a style written to the DOM, so ProseMirror never sees a foreign mutation.
// shortcut: no per-table switch; add a table attribute in a later schema bump if someone asks to turn it off.

const key = new PluginKey<Map<number, number>>('stickyHeader')

/** The first row sticks only when every cell is a header and none spans rows (a spanning header cannot move alone). */
export function stickyRow(table: PMNode): PMNode | null {
  const row = table.firstChild
  if (!row || row.childCount === 0) return null
  for (let i = 0; i < row.childCount; i++) {
    const c = row.child(i)
    if (c.type.name !== 'tableHeader' || c.attrs.rowspan > 1) return null
  }
  return row
}

export const StickyHeader = Extension.create({
  name: 'stickyHeader',
  addProseMirrorPlugins() {
    return [
      new Plugin<Map<number, number>>({
        key,
        state: {
          init: () => new Map(),
          // an edit moves positions: forget the offsets, the view measures again
          apply: (tr, old) =>
            (tr.getMeta(key) as Map<number, number> | undefined) ?? (tr.docChanged ? new Map() : old),
        },
        props: {
          decorations(state) {
            const offsets = key.getState(state)
            if (!offsets?.size) return null
            const out: Decoration[] = []
            for (const [pos, y] of offsets) {
              const table = state.doc.nodeAt(pos)
              const row = table && stickyRow(table)
              if (!table || !row) continue
              let at = pos + 2 // into the first row
              row.forEach((cell) => {
                out.push(
                  Decoration.node(at, at + cell.nodeSize, {
                    class: 'cn-sticky-cell',
                    style: `transform: translateY(${y}px)`,
                  }),
                )
                at += cell.nodeSize
              })
            }
            return DecorationSet.create(state.doc, out)
          },
        },
        view(view) {
          let frame = 0
          const measure = () => {
            frame = 0
            const next = new Map<number, number>()
            // The editor toolbar is sticky itself: the header sticks under it.
            const bar = document.querySelector('[role=toolbar][aria-label="Editor toolbar"]')
            const top = bar ? Math.max(0, bar.getBoundingClientRect().bottom) : 0
            view.state.doc.forEach((node, offset) => {
              if (node.type.name !== 'table' || !stickyRow(node)) return
              const dom = view.nodeDOM(offset) as HTMLElement | null
              const table = (dom?.tagName === 'TABLE' ? dom : dom?.querySelector('table')) as
                | HTMLTableElement
                | null
                | undefined
              const head = table?.rows[0]
              if (!table || !head) return
              const t = table.getBoundingClientRect()
              const room = t.height - head.offsetHeight
              const y = Math.round(Math.min(Math.max(top - t.top, 0), Math.max(room, 0)))
              if (y > 0) next.set(offset, y)
            })
            const cur = key.getState(view.state) ?? new Map()
            const same = cur.size === next.size && [...next].every(([k, v]) => cur.get(k) === v)
            if (!same) view.dispatch(view.state.tr.setMeta(key, next).setMeta('addToHistory', false))
          }
          const schedule = () => {
            if (!frame) frame = requestAnimationFrame(measure)
          }
          window.addEventListener('scroll', schedule, true)
          window.addEventListener('resize', schedule)
          schedule()
          return {
            update: (v, prev) => {
              if (v.state.doc !== prev.doc) schedule()
            },
            destroy: () => {
              window.removeEventListener('scroll', schedule, true)
              window.removeEventListener('resize', schedule)
              if (frame) cancelAnimationFrame(frame)
            },
          }
        },
      }),
    ]
  },
})
