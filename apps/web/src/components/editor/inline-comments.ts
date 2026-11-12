import type { Node as PMNode } from '@tiptap/pm/model'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { type Editor, Extension } from '@tiptap/react'

/** An open inline comment thread: anchored by a quote of the page text, not by a mark in the content. */
export type Thread = { id: string; anchor_text: string; anchor_prefix: string | null }

const key = new PluginKey<DecorationSet>('inlineComments')
const PREFIX_CHARS = 64

/** Text of one textblock plus where each character sits in the document. */
function blockText(block: PMNode, start: number) {
  let text = ''
  const pos: number[] = []
  block.descendants((n, p) => {
    if (!n.isText) return
    for (let i = 0; i < n.text!.length; i++) pos.push(start + p + i)
    text += n.text
  })
  return { text, pos }
}

/** Where a quote sits now: prefer the occurrence after its prefix, else the first one. Null when the text is gone. */
export function locate(doc: PMNode, t: Thread): { from: number; to: number } | null {
  let first: { from: number; to: number } | null = null
  let best: { from: number; to: number } | null = null
  doc.descendants((block, start) => {
    if (best || !block.isTextblock) return !best
    const { text, pos } = blockText(block, start + 1)
    const at = (i: number) => ({ from: pos[i], to: pos[i + t.anchor_text.length - 1] + 1 })
    if (t.anchor_prefix) {
      const i = text.indexOf(t.anchor_prefix + t.anchor_text)
      if (i >= 0) best = at(i + t.anchor_prefix.length)
    }
    if (!first) {
      const i = text.indexOf(t.anchor_text)
      if (i >= 0) first = at(i)
    }
    return false
  })
  return best ?? first
}

function decorations(doc: PMNode, threads: Thread[]) {
  const ds: Decoration[] = []
  for (const t of threads) {
    const r = locate(doc, t)
    if (r) ds.push(Decoration.inline(r.from, r.to, { class: 'cn-comment', 'data-comment-id': t.id }))
  }
  return DecorationSet.create(doc, ds)
}

/** The quote for a new inline comment from the current selection; null unless it is inside one paragraph. */
export function selectionAnchor(editor: Editor) {
  const { $from, $to, empty } = editor.state.selection
  if (empty || $from.parent !== $to.parent || !$from.parent.isTextblock) return null
  const anchor_text = editor.state.doc.textBetween($from.pos, $to.pos)
  if (!anchor_text.trim() || anchor_text.length > 500) return null
  const before = $from.parent.textBetween(0, $from.parentOffset)
  return { anchor_text, anchor_prefix: before.slice(-PREFIX_CHARS) }
}

/** Highlights open inline comment threads; clicking one calls onOpen(threadId). */
export const InlineComments = Extension.create<{ onOpen: (id: string) => void }, { threads: Thread[] }>({
  name: 'inlineComments',
  addOptions: () => ({ onOpen: () => {} }),
  addStorage: () => ({ threads: [] }),
  addProseMirrorPlugins() {
    const storage = this.storage
    const onOpen = this.options.onOpen
    return [
      new Plugin<DecorationSet>({
        key,
        state: {
          init: (_, s) => decorations(s.doc, storage.threads),
          apply: (tr, old) => (tr.docChanged || tr.getMeta(key) ? decorations(tr.doc, storage.threads) : old),
        },
        props: {
          decorations: (s) => key.getState(s),
          handleClick: (_view, _pos, e) => {
            const id = (e.target as HTMLElement).closest<HTMLElement>('[data-comment-id]')?.dataset.commentId
            if (id) onOpen(id)
            return false
          },
        },
      }),
    ]
  },
})

/** Replace the highlighted threads (e.g. after comments reload). */
export function setThreads(editor: Editor, threads: Thread[]) {
  ;(editor.storage as unknown as { inlineComments: { threads: Thread[] } }).inlineComments.threads = threads
  editor.view.dispatch(editor.state.tr.setMeta(key, true))
}
