// Find and replace (docs-parity-c 5.5). A decoration plugin, no marks and no stored form. Replacing is an ordinary
// editor transaction, so live collaboration and undo see it like any other edit. Plain functions below drive it so a bar,
// a test or a menu can use the same code.

import type { Node as PMNode } from '@tiptap/pm/model'
import { type EditorState, Plugin, PluginKey, type Transaction } from '@tiptap/pm/state'
import { Decoration, DecorationSet, type EditorView } from '@tiptap/pm/view'
import { Extension } from '@tiptap/react'

export type FindOptions = { query: string; matchCase: boolean; wholeWord: boolean; includeCode: boolean }
/** locked: the match touches a date chip's text; it is found but never replaced, because the chip must agree with its date. */
export type Match = { from: number; to: number; locked: boolean }
export type FindState = FindOptions & { matches: Match[]; current: number }

export const NO_FIND: FindOptions = { query: '', matchCase: false, wholeWord: false, includeCode: false }
const OBJ = '￼' // stands for an atom or excluded text: nothing can match across it
const WORD = /[\p{L}\p{N}_]/u

export const findKey = new PluginKey<FindState>('findReplace')

/** Every match in the document. A match lies inside one text block and may cross marks; atoms (and code, unless asked) are skipped. */
export function findMatches(doc: PMNode, o: FindOptions): Match[] {
  const q = o.query.split(OBJ).join('')
  if (!q) return []
  const re = new RegExp(q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), o.matchCase ? 'gu' : 'giu')
  const out: Match[] = []
  doc.descendants((node, pos) => {
    if (!node.isTextblock) return true
    if (node.type.spec.code && !o.includeCode) return false
    let text = ''
    const seg: { at: number; pos: number; len: number; date: boolean }[] = [] // text offset -> document position
    node.forEach((child, off) => {
      const p = pos + 1 + off
      if (child.isText && (o.includeCode || !child.marks.some((m) => m.type.name === 'code'))) {
        seg.push({
          at: text.length,
          pos: p,
          len: child.nodeSize,
          date: child.marks.some((m) => m.type.name === 'date'),
        })
        text += child.text
      } else text += OBJ.repeat(Math.max(1, child.isText ? (child.text?.length ?? 1) : 1))
    })
    for (const m of text.matchAll(re)) {
      const a = m.index ?? 0
      const b = a + m[0].length
      if (o.wholeWord && (WORD.test(text[a - 1] ?? '') || WORD.test(text[b] ?? ''))) continue
      const at = (i: number) => seg.findLast((s) => s.at <= i && i < s.at + s.len)
      const first = at(a)
      const last = at(b - 1)
      if (!first || !last) continue
      out.push({
        from: first.pos + (a - first.at),
        to: last.pos + (b - 1 - last.at) + 1,
        locked: seg.some((s) => s.date && s.at < b && s.at + s.len > a),
      })
    }
    return false
  })
  return out
}

const compute = (doc: PMNode, o: FindOptions, keep = 0): FindState => {
  const matches = findMatches(doc, o)
  return { ...o, matches, current: Math.min(keep, Math.max(0, matches.length - 1)) }
}

type Meta = { set?: Partial<FindOptions>; current?: number }

export const FindReplace = Extension.create({
  name: 'findReplace',
  addProseMirrorPlugins() {
    return [
      new Plugin<FindState>({
        key: findKey,
        state: {
          init: (_c, s) => compute(s.doc, NO_FIND),
          apply(tr: Transaction, old: FindState, _o: EditorState, next: EditorState) {
            const meta = tr.getMeta(findKey) as Meta | undefined
            if (meta?.set) return compute(next.doc, { ...old, ...meta.set })
            if (meta?.current !== undefined) return { ...old, current: meta.current }
            return tr.docChanged && old.query ? compute(next.doc, old, old.current) : old
          },
        },
        props: {
          decorations(state) {
            const s = findKey.getState(state)
            if (!s?.matches.length) return null
            return DecorationSet.create(
              state.doc,
              s.matches.map((m, i) =>
                Decoration.inline(m.from, m.to, {
                  class: i === s.current ? 'find-match find-current' : 'find-match',
                  style:
                    i === s.current
                      ? 'background: rgba(250, 160, 20, 0.6); outline: 1px solid rgb(217, 119, 6)'
                      : 'background: rgba(250, 204, 21, 0.4)',
                }),
              ),
            )
          },
        },
      }),
    ]
  },
})

export const getFind = (view: EditorView): FindState =>
  findKey.getState(view.state) ?? compute(view.state.doc, NO_FIND)

/** Changes the query or an option; the current match restarts at the first one. */
export function setFind(view: EditorView, set: Partial<FindOptions>) {
  view.dispatch(view.state.tr.setMeta(findKey, { set } satisfies Meta).setMeta('addToHistory', false))
}

/** Opens every collapsed expand around a position (the open state lives in the DOM only) and scrolls to it. */
function reveal(view: EditorView, pos: number) {
  let n: Node | null
  try {
    n = view.domAtPos(pos).node
  } catch {
    return
  }
  let el = n && (n.nodeType === 3 ? n.parentElement : (n as HTMLElement))
  const el0 = el
  for (; el; el = el.parentElement) {
    if (el.classList?.contains('cn-expand') && el.dataset.open === 'false')
      el.querySelector<HTMLButtonElement>(':scope > .cn-expand-head .cn-expand-toggle')?.click()
  }
  el0?.scrollIntoView?.({ block: 'center' })
}

/** Scrolls to the current match, opening collapsed expands around it. */
export function showCurrent(view: EditorView) {
  const s = getFind(view)
  if (s.matches[s.current]) reveal(view, s.matches[s.current].from)
}

/** Moves to the next (1) or previous (-1) match, wrapping around. */
export function stepFind(view: EditorView, dir: 1 | -1) {
  const s = getFind(view)
  if (!s.matches.length) return
  const current = (s.current + dir + s.matches.length) % s.matches.length
  view.dispatch(view.state.tr.setMeta(findKey, { current } satisfies Meta).setMeta('addToHistory', false))
  reveal(view, s.matches[current].from)
}

/** Replaces the current match and moves on. Returns false when nothing was replaced (no match, or a date chip's text). */
export function replaceCurrent(view: EditorView, text: string) {
  const s = getFind(view)
  const m = s.matches[s.current]
  if (!view.editable || !m || m.locked) return false
  view.dispatch(text ? view.state.tr.insertText(text, m.from, m.to) : view.state.tr.delete(m.from, m.to))
  // Move past the replacement, so a replacement that itself matches is not found again.
  const after = getFind(view).matches.findIndex((x) => x.from >= m.from + text.length)
  view.dispatch(
    view.state.tr
      .setMeta(findKey, { current: Math.max(after, 0) } satisfies Meta)
      .setMeta('addToHistory', false),
  )
  return true
}

/** Replaces every match in ONE transaction: one undo step, and one update for collaborators. Returns how many were replaced. */
export function replaceAll(view: EditorView, text: string) {
  const todo = getFind(view).matches.filter((m) => !m.locked)
  if (!view.editable || !todo.length) return 0
  const tr = view.state.tr
  for (const m of todo.toReversed()) text ? tr.insertText(text, m.from, m.to) : tr.delete(m.from, m.to)
  view.dispatch(tr)
  return todo.length
}
