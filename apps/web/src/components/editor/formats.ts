import { Heading } from '@tiptap/extension-heading'
import Image from '@tiptap/extension-image'
import { Paragraph } from '@tiptap/extension-paragraph'
import { Table } from '@tiptap/extension-table'
import { TextStyle } from '@tiptap/extension-text-style'
import { type Editor, Extension, type JSONContent } from '@tiptap/react'
import { esc, inlineHtml, markTag, plainText, posInt, safeSrc, span, tintOf } from './html-inline'
import { StickyHeader } from './sticky-header'

// Storage contract (FORMATS.md): rich formatting that Markdown cannot hold is stored as a small HTML subset.

let lastTint = 'yellow'
/** Apply a tint to the selection, or remove it (null) and drop a textStyle mark left empty. */
export function setHighlight(editor: Editor, tint: string | null) {
  if (tint) lastTint = tint
  const c = editor.chain().focus().setMark('textStyle', { highlight: tint })
  return (tint ? c : c.removeEmptyTextStyle()).run()
}

/** Colour span is the innermost mark (priority below bold/italic/strike/link), see contract §1. */
export const ColorText = TextStyle.extend({
  priority: 50,
  // highlight (docs-parity C1) lives next to colour on the same mark; the editor shows it through data-highlight.
  addAttributes() {
    return {
      ...this.parent?.(),
      highlight: {
        default: null,
        // A colour span inside a <mark> is a second textStyle mark that would replace the first, so it reads the tint too.
        parseHTML: (el: HTMLElement) => {
          const m =
            el.tagName === 'MARK' ? el : el.parentElement?.tagName === 'MARK' ? el.parentElement : null
          const c = el.getAttribute('data-highlight') ?? m?.getAttribute('data-color') ?? null
          return c === null ? (m ? 'yellow' : null) : tintOf(c)
        },
        renderHTML: (a: Record<string, unknown>) =>
          tintOf(a.highlight) ? { 'data-highlight': a.highlight } : {},
      },
    }
  },
  parseHTML() {
    return [...(this.parent?.() ?? []), { tag: 'span[data-highlight]' }, { tag: 'mark' }]
  },
  // Ctrl/Cmd+Shift+H applies the last tint, or removes it when the selection already has it.
  addKeyboardShortcuts() {
    return {
      'Mod-Shift-h': () =>
        setHighlight(
          this.editor,
          this.editor.getAttributes('textStyle').highlight === lastTint ? null : lastTint,
        ),
    }
  },
  renderMarkdown: (node, h) => {
    const inner = h.renderChildren(node)
    const color = node.attrs?.color as string | undefined
    const tint = tintOf(node.attrs?.highlight)
    const c = color ? span(color, inner) : inner
    return tint && inner.trim() ? markTag(tint, c) : c
  },
})

const aligned = (a: unknown, parent?: string | null) =>
  parent === 'doc' && (a === 'center' || a === 'right' || a === 'justify') ? (a as string) : null

export const AlignParagraph = Paragraph.extend({
  renderMarkdown(node, h, ctx) {
    const a = aligned(node.attrs?.textAlign, ctx?.parentType)
    if (a) return `<p style="text-align: ${a}">${inlineHtml(node.content)}</p>`
    return this.parent?.(node, h, ctx) ?? ''
  },
})

export const AlignHeading = Heading.extend({
  renderMarkdown(node, h, ctx) {
    const level = node.attrs?.level ? Number.parseInt(node.attrs.level, 10) : 1
    const a = aligned(node.attrs?.textAlign, ctx?.parentType)
    if (a) return `<h${level} style="text-align: ${a}">${inlineHtml(node.content)}</h${level}>`
    if (!node.content) return ''
    return `${'#'.repeat(level)} ${h.renderChildren(node.content)}`
  },
})

const cells = (row: JSONContent) => row.content ?? []
// A table needs the HTML form when a cell merges, is resized or is coloured (C1), or the table is numbered.
const needsHtml = (c: JSONContent) =>
  (c.attrs?.colspan ?? 1) > 1 ||
  (c.attrs?.rowspan ?? 1) > 1 ||
  c.attrs?.colwidth != null ||
  tintOf(c.attrs?.background) !== null

/** Cell colour is an attribute of tableCell and tableHeader, so it is declared once here, whichever file imports them. */
const TableTint = Extension.create({
  name: 'tableTint',
  addGlobalAttributes() {
    return [
      {
        types: ['tableCell', 'tableHeader'],
        attributes: {
          background: {
            default: null,
            parseHTML: (el: HTMLElement) => tintOf(el.getAttribute('data-bg')),
            renderHTML: (a: Record<string, unknown>) =>
              tintOf(a.background) ? { 'data-bg': a.background } : {},
          },
        },
      },
    ]
  },
})

export const HtmlTable = Table.extend({
  addExtensions() {
    return [TableTint, StickyHeader]
  },
  addAttributes() {
    return {
      ...this.parent?.(),
      numbered: {
        default: false,
        parseHTML: (el: HTMLElement) =>
          el.hasAttribute('data-numbered') && el.getAttribute('data-numbered') !== 'false',
        renderHTML: (a: Record<string, unknown>) => (a.numbered === true ? { 'data-numbered': 'true' } : {}),
      },
    }
  },
  renderMarkdown(node, h, ctx) {
    const rows = node.content ?? []
    const numbered = node.attrs?.numbered === true
    if (!numbered && !rows.some((r) => cells(r).some(needsHtml))) return this.parent?.(node, h, ctx) ?? ''
    const out = rows.map((r) => {
      const tds = cells(r).map((c) => {
        const tag = c.type === 'tableHeader' ? 'th' : 'td'
        const a = c.attrs ?? {}
        let attrs = ''
        if (posInt(a.colspan) && a.colspan > 1) attrs += ` colspan="${a.colspan}"`
        if (posInt(a.rowspan) && a.rowspan > 1) attrs += ` rowspan="${a.rowspan}"`
        const w = a.colwidth as unknown[] | null
        if (Array.isArray(w) && w.length && w.every(posInt)) attrs += ` colwidth="${w.join(',')}"`
        const bg = tintOf(a.background)
        if (bg) attrs += ` data-bg="${bg}"`
        const ps = (c.content ?? []).map((p) =>
          p.type === 'paragraph' ? `<p>${inlineHtml(p.content)}</p>` : `<p>${esc(plainText(p))}</p>`,
        )
        return `<${tag}${attrs}>${ps.join('')}</${tag}>`
      })
      return `<tr>${tds.join('')}</tr>`
    })
    return `<table${numbered ? ' data-numbered="true"' : ''}>${out.join('')}</table>`
  },
})

/** Only http(s) images and the one uploaded-file path are stored; any other image keeps its alt text, not its URL. */
export const SafeImage = Image.extend({
  parseMarkdown(token, h) {
    // the Markdown parser keeps the backslash before a backslash; the other escapes it removes itself
    const un = (v: unknown) => (typeof v === 'string' ? v.replace(/\\([\\[\]"])/g, '$1') : v)
    return h.createNode('image', { src: token.href, title: un(token.title), alt: un(token.text) })
  },
  renderMarkdown(node) {
    const alt = String(node.attrs?.alt ?? '')
    const src = String(node.attrs?.src ?? '')
    if (!safeSrc(src)) return alt
    const title = node.attrs?.title ? String(node.attrs.title) : ''
    // `\`, `[` and `]` are escaped in the alt text and `\` and `"` in the title, so they survive a save (md.ts reads the same).
    const a = alt.replace(/[\\[\]]/g, '\\$&')
    return title ? `![${a}](${src} "${title.replace(/["\\]/g, '\\$&')}")` : `![${a}](${src})`
  },
})
