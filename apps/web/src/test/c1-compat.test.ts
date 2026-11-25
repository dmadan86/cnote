// docs-parity C1 13.3.5: what an old tab does to a block that carries the C1 attributes (highlight on textStyle,
// background on cells, numbered on a table). C1 keeps schema 2, so a tab that has not reloaded keeps its old schema.
// Client A's schema has the new attributes, client B's (the real editor extensions as they are today) does not.
// The assertions record the behaviour of the old schema. The contract is: B's edit never loses text; formatting-only loss is accepted.
import Collaboration from '@tiptap/extension-collaboration'
import { TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import { Color } from '@tiptap/extension-text-style'
import { Markdown } from '@tiptap/markdown'
import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { ColorText, HtmlTable } from '../components/editor/formats'
import { ColorText as OldColorText, HtmlTable as OldHtmlTable } from './fixtures/old-formats'

// B: the editor as it was before C1. A: the real extensions, which carry the new attributes (HtmlTable brings the cell tint).
const base = [
  StarterKit.configure({ undoRedo: false }),
  OldColorText,
  Color,
  OldHtmlTable.configure({ resizable: true }),
  TableRow,
  TableHeader,
  TableCell,
]
const newer = [
  StarterKit.configure({ undoRedo: false }),
  ColorText,
  Color,
  HtmlTable.configure({ resizable: true }),
  TableRow,
  TableHeader,
  TableCell,
]

const mount = (doc: Y.Doc, extensions: unknown[]) =>
  new Editor({
    element: document.createElement('div'),
    extensions: [...(extensions as any[]), Collaboration.configure({ document: doc })],
  })
const settle = () => new Promise((r) => setTimeout(r, 50))
const frag = (d: Y.Doc) => d.getXmlFragment('default').toString()
const words = (s: string) => new Set(s.replace(/<[^>]*>/g, ' ').match(/[\p{L}\p{N}]{3,}/gu) ?? [])
const textOf = (d: Y.Doc) => [...words(frag(d))]

/** A seeds the document, B joins with the old schema, types inside the block, and the result syncs back to A. */
async function oldTabEdits(content: object, typeAt: (b: Editor) => void) {
  const da = new Y.Doc()
  const db = new Y.Doc()
  const a = mount(da, newer)
  a.commands.setContent(content)
  await settle()
  const before = frag(da)
  Y.applyUpdate(db, Y.encodeStateAsUpdate(da), 'relay')
  const b = mount(db, base)
  await settle()
  typeAt(b)
  await settle()
  Y.applyUpdate(da, Y.encodeStateAsUpdate(db, Y.encodeStateVector(da)), 'relay')
  Y.applyUpdate(db, Y.encodeStateAsUpdate(da, Y.encodeStateVector(db)), 'relay')
  await settle()
  const out = { before, a: frag(da), b: frag(db), wordsA: textOf(da), wordsBefore: [...words(before)] }
  a.destroy()
  b.destroy()
  return out
}
const para = (...runs: object[]) => ({ type: 'paragraph', content: runs })
const run = (text: string, marks: object[] = []) => ({ type: 'text', text, marks })
const style = (attrs: object) => ({ type: 'textStyle', attrs })
const cell = (type: 'tableCell' | 'tableHeader', text: string, attrs: object = {}) => ({
  type,
  attrs,
  content: [para(run(text))],
})
const table = (attrs: object, ...rows: object[][]) => ({
  type: 'table',
  attrs,
  content: rows.map((r) => ({ type: 'tableRow', content: r })),
})
const typeStart = (b: Editor) => {
  b.commands.focus('start')
  b.commands.insertContent('oldtab ')
}
const typeEnd = (b: Editor) => {
  b.commands.focus('end')
  b.commands.insertContent(' oldtab')
}

describe('an old tab edits blocks that carry the C1 attributes', () => {
  it('sanity: client A really stores the new attributes', async () => {
    const r = await oldTabEdits(
      { type: 'doc', content: [para(run('marked', [style({ highlight: 'yellow' })]))] },
      typeEnd,
    )
    expect(r.before).toContain('highlight="yellow"')
  })

  it('highlighted text keeps all its text', async () => {
    const r = await oldTabEdits(
      {
        type: 'doc',
        content: [
          para(run('plain '), run('highlighted words', [style({ highlight: 'yellow' })]), run(' tail')),
        ],
      },
      typeStart,
    )
    for (const w of r.wordsBefore) expect(r.wordsA).toContain(w)
    expect(r.wordsA).toContain('oldtab')
    expect(r.b).toBe(r.a)
  })

  it('colour plus highlight on one mark keeps all its text', async () => {
    const r = await oldTabEdits(
      {
        type: 'doc',
        content: [
          para(run('both marks here', [style({ color: '#c0392b', highlight: 'green' })]), run(' after')),
        ],
      },
      typeEnd,
    )
    for (const w of r.wordsBefore) expect(r.wordsA).toContain(w)
    expect(r.wordsA).toContain('oldtab')
  })

  it('a coloured cell keeps all its text', async () => {
    const t = table(
      {},
      [cell('tableHeader', 'Header one', { background: 'grey' }), cell('tableHeader', 'Header two')],
      [cell('tableCell', 'Green cell', { background: 'green' }), cell('tableCell', 'Plain cell')],
    )
    const r = await oldTabEdits({ type: 'doc', content: [t, para(run('end'))] }, (b) => {
      b.commands.focus('end')
      b.commands.insertContent(' oldtab')
      b.commands.setTextSelection(4) // start of the first cell
      b.commands.insertContent('oldcell ')
    })
    for (const w of r.wordsBefore) expect(r.wordsA).toContain(w)
    expect(r.wordsA).toContain('oldcell')
  })

  it('a numbered table keeps all its text', async () => {
    const t = table(
      { numbered: true },
      [cell('tableHeader', 'Item name'), cell('tableHeader', 'Quantity')],
      [cell('tableCell', 'Rice bags'), cell('tableCell', 'Forty')],
    )
    const r = await oldTabEdits({ type: 'doc', content: [t, para(run('end'))] }, (b) => {
      b.commands.setTextSelection(4)
      b.commands.insertContent('oldcell ')
    })
    for (const w of r.wordsBefore) expect(r.wordsA).toContain(w)
    expect(r.wordsA).toContain('oldcell')
  })

  it('loses only the formatting of the block the old tab edits', async () => {
    const hl = await oldTabEdits(
      {
        type: 'doc',
        content: [para(run('marked text', [style({ highlight: 'yellow', color: '#c0392b' })]))],
      },
      (b) => {
        b.commands.setTextSelection(4)
        b.commands.insertContent('X')
      },
    )
    const tb = await oldTabEdits(
      {
        type: 'doc',
        content: [
          table(
            { numbered: true },
            [cell('tableHeader', 'Head', { background: 'grey' })],
            [cell('tableCell', 'Body', { background: 'red' })],
          ),
        ],
      },
      (b) => {
        b.commands.setTextSelection(6)
        b.commands.insertContent('X')
      },
    )
    // Observed with the locked y-tiptap: the edited block is rewritten with the old schema's attributes only.
    // Highlight goes, the colour on the same mark stays. The table loses `numbered` and the edited cell loses its
    // background; a cell nobody touched keeps its background. Formatting is lost, no text.
    expect(hl.before).toContain('highlight="yellow"')
    expect(hl.a).not.toContain('highlight')
    expect(hl.a).toContain('color="#c0392b"')
    expect(tb.before).toContain('numbered="true"')
    expect(tb.a).not.toContain('numbered')
    expect(tb.a).not.toContain('background="grey"')
    expect(tb.a).toContain('background="red"')
  })
})

// The old tab's Markdown converter, saving the stored form again and again: the link must not change (C1 13.6 finding,
// fixed by writing the highlight inside the link text, `[<mark>text</mark>](url)`, which an old reader keeps as it is).
describe('an old tab saves a highlighted link repeatedly', () => {
  const save = (md: string) => {
    const e = new Editor({
      element: document.createElement('div'),
      extensions: [...base, Markdown],
      content: md,
      contentType: 'markdown',
    })
    const out = e.getMarkdown()
    e.destroy()
    return out
  }
  it('keeps the link target and text stable over many saves', () => {
    const stored = 'See [<mark data-color="blue">a link</mark>](https://example.com/guide) now.'
    let cur = stored
    const seen: string[] = []
    for (let i = 0; i < 5; i++) {
      cur = save(cur)
      seen.push(cur)
    }
    expect(new Set(seen).size).toBe(1) // stable from the first save
    expect(cur.match(/https:\/\/example\.com\/guide/g)).toHaveLength(1)
    expect(cur).toContain('a link')
    expect(cur).not.toContain('](\\[')
  })
  it('the new editor writes exactly that form', () => {
    const e = new Editor({
      element: document.createElement('div'),
      extensions: [...newer, Markdown],
      content: 'See [<mark data-color="blue">a link</mark>](https://example.com/guide) now.',
      contentType: 'markdown',
    })
    expect(e.getMarkdown()).toBe(
      'See [<mark data-color="blue">a link</mark>](https://example.com/guide) now.',
    )
    e.destroy()
  })
})
