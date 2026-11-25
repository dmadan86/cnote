// Docs C1 R1: highlight, cell colour, numbered column and sticky header in the web editor and its export.
import { readFileSync } from 'node:fs'
import { TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import { Color } from '@tiptap/extension-text-style'
import { Markdown } from '@tiptap/markdown'
import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import { buildExportDom, buildHtmlFile } from '../components/docs/export'
import { ColorText, HtmlTable, setHighlight } from '../components/editor/formats'
import { stickyRow } from '../components/editor/sticky-header'
import { moveRow, sortRows } from '../components/editor/table-controls'

const fixtures: { id: string; md: string }[] = JSON.parse(
  readFileSync('../collab/test/fixtures/blocks.json', 'utf8'),
)
const fx = (id: string) => fixtures.find((f) => f.id === id)?.md ?? ''

const make = (content: string) =>
  new Editor({
    element: document.createElement('div'),
    extensions: [
      StarterKit.configure({ undoRedo: false }),
      ColorText,
      Color,
      Markdown,
      HtmlTable.configure({ resizable: true }),
      TableRow,
      TableHeader,
      TableCell,
    ],
    content,
    contentType: 'markdown',
  })
const meta = { title: 'T', status: null, labels: [], updatedAt: '' }

describe('highlight', () => {
  it('applies, changes and clears on a selection, keeping the colour', () => {
    const e = make('hello world')
    e.commands.setTextSelection({ from: 1, to: 6 })
    e.chain().setColor('#c0392b').run()
    setHighlight(e, 'green')
    expect(e.getMarkdown()).toBe(
      '<mark data-color="green"><span style="color: #c0392b">hello</span></mark> world',
    )
    setHighlight(e, 'blue')
    expect(e.getMarkdown()).toContain('<mark data-color="blue">')
    setHighlight(e, null)
    expect(e.getMarkdown()).toBe('<span style="color: #c0392b">hello</span> world')
    e.destroy()
  })

  it('clears to plain text and leaves no empty mark', () => {
    const e = make('hello world')
    e.commands.setTextSelection({ from: 1, to: 6 })
    setHighlight(e, 'red')
    setHighlight(e, null)
    expect(e.getMarkdown()).toBe('hello world')
    expect(JSON.stringify(e.getJSON())).not.toContain('textStyle')
    e.destroy()
  })

  it('shows data-highlight in the editor DOM and reads a pasted <mark>', () => {
    const e = make('<mark data-color="purple">tinted</mark> and <mark>bare</mark>')
    const html = e.getHTML()
    expect(html).toContain('data-highlight="purple"')
    expect(e.getMarkdown()).toBe(
      '<mark data-color="purple">tinted</mark> and <mark data-color="yellow">bare</mark>',
    )
    e.destroy()
  })
})

describe('table cell colour and numbered column', () => {
  it('a coloured cell round-trips and colour follows its cell through a sort and a row move', () => {
    const e = make(fx('table-cell-colour').replace('<table>', '<table data-numbered="true">'))
    expect(e.getMarkdown()).toContain('data-numbered="true"')
    // plain two-column table, one coloured cell, to sort
    const t = make(
      '<table><tr><th><p>K</p></th></tr><tr><td data-bg="red"><p>b</p></td></tr><tr><td><p>a</p></td></tr></table>',
    )
    t.commands.setTextSelection(8) // inside "b"
    sortRows(t, 1)
    const md = t.getMarkdown()
    expect(md.indexOf('<p>a</p>')).toBeLessThan(md.indexOf('<p>b</p>'))
    expect(md).toContain('<td data-bg="red"><p>b</p></td>')
    t.destroy()
    e.destroy()
  })

  it('numbering is an attribute of the table, not cells, and survives a move', () => {
    const e = make(fx('table-numbered-column'))
    const cells = () => (e.getHTML().match(/<t[dh]\b/g) ?? []).length
    const before = cells()
    e.commands.setTextSelection(8)
    moveRow(e, 1)
    expect(cells()).toBe(before)
    expect(e.getHTML()).toContain('data-numbered="true"')
    e.commands.updateAttributes('table', { numbered: false })
    expect(e.getHTML()).not.toContain('data-numbered')
    e.destroy()
  })

  it('a coloured cell is stored as an HTML table even without merges', () => {
    const e = make(fx('table-cell-colour'))
    expect(e.getMarkdown()).toBe(fx('table-cell-colour'))
    e.destroy()
  })
})

describe('sticky header', () => {
  it('sticks only a header row without a row-spanning cell', () => {
    const e = make(
      '<table><tr><th><p>A</p></th><th><p>B</p></th></tr><tr><td><p>1</p></td><td><p>2</p></td></tr></table>',
    )
    expect(stickyRow(e.state.doc.child(0))).not.toBeNull()
    const spanning = make(
      '<table><tr><th rowspan="2"><p>A</p></th><th><p>B</p></th></tr><tr><th><p>C</p></th></tr></table>',
    )
    expect(stickyRow(spanning.state.doc.child(0))).toBeNull()
    const body = make('<table><tr><td><p>1</p></td></tr></table>')
    expect(stickyRow(body.state.doc.child(0))).toBeNull()
    e.destroy()
    spanning.destroy()
    body.destroy()
  })
})

describe('export', () => {
  it('keeps highlight, cell colour and numbering and carries the counter rule', async () => {
    const e = make(`${fx('highlight-with-colour')}\n\n${fx('table-numbered-coloured-merged')}`)
    const { root } = await buildExportDom(e, meta)
    const hl = root.querySelector('span[data-highlight="yellow"]') as HTMLElement
    expect(hl.getAttribute('style')).toContain('background: #fde68a')
    expect(hl.getAttribute('style')).toMatch(/color: (#c0392b|rgb\(192, 57, 43\))/)
    const tbl = root.querySelector('table') as HTMLElement
    expect(tbl.getAttribute('data-numbered')).toBe('true')
    expect(root.querySelector('td[data-bg="orange"]')?.getAttribute('style')).toContain('background: #fed7aa')
    expect(root.querySelector('th[data-bg="blue"]')?.getAttribute('colspan')).toBe('2')
    const html = buildHtmlFile(root, meta)
    expect(html).toContain('counter-increment:cn-row')
    expect(html).not.toContain('<script')
    e.destroy()
  })

  it('drops an unknown tint but keeps the text', async () => {
    const e = make('<mark data-color="chartreuse">kept words</mark>')
    const { root } = await buildExportDom(e, meta)
    expect(root.textContent).toContain('kept words')
    expect(root.querySelector('[data-highlight]')).toBeNull()
    e.destroy()
  })
})
