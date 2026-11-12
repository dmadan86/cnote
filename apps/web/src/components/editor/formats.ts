import { Heading } from '@tiptap/extension-heading'
import Image from '@tiptap/extension-image'
import { Paragraph } from '@tiptap/extension-paragraph'
import { Table } from '@tiptap/extension-table'
import { TextStyle } from '@tiptap/extension-text-style'
import type { JSONContent } from '@tiptap/react'
import { esc, inlineHtml, plainText, posInt, safeSrc, span } from './html-inline'

// Storage contract (FORMATS.md): rich formatting that Markdown cannot hold is stored as a small HTML subset.

/** Colour span is the innermost mark (priority below bold/italic/strike/link), see contract §1. */
export const ColorText = TextStyle.extend({
  priority: 50,
  renderMarkdown: (node, h) => {
    const inner = h.renderChildren(node)
    const color = node.attrs?.color as string | undefined
    return color ? span(color, inner) : inner
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
const isMerged = (c: JSONContent) =>
  (c.attrs?.colspan ?? 1) > 1 || (c.attrs?.rowspan ?? 1) > 1 || c.attrs?.colwidth != null

export const HtmlTable = Table.extend({
  renderMarkdown(node, h, ctx) {
    const rows = node.content ?? []
    if (!rows.some((r) => cells(r).some(isMerged))) return this.parent?.(node, h, ctx) ?? ''
    const out = rows.map((r) => {
      const tds = cells(r).map((c) => {
        const tag = c.type === 'tableHeader' ? 'th' : 'td'
        const a = c.attrs ?? {}
        let attrs = ''
        if (posInt(a.colspan) && a.colspan > 1) attrs += ` colspan="${a.colspan}"`
        if (posInt(a.rowspan) && a.rowspan > 1) attrs += ` rowspan="${a.rowspan}"`
        const w = a.colwidth as unknown[] | null
        if (Array.isArray(w) && w.length && w.every(posInt)) attrs += ` colwidth="${w.join(',')}"`
        const ps = (c.content ?? []).map((p) =>
          p.type === 'paragraph' ? `<p>${inlineHtml(p.content)}</p>` : `<p>${esc(plainText(p))}</p>`,
        )
        return `<${tag}${attrs}>${ps.join('')}</${tag}>`
      })
      return `<tr>${tds.join('')}</tr>`
    })
    return `<table>${out.join('')}</table>`
  },
})

/** Only http(s) images are stored (the dialog enforces it too; pasted images are dropped here). */
export const SafeImage = Image.extend({
  renderMarkdown(node, h, ctx) {
    return safeSrc(String(node.attrs?.src ?? '')) ? (this.parent?.(node, h, ctx) ?? '') : ''
  },
})
