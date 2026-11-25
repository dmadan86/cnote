// docs-parity-b 5.2 and 5.3: file links and page links are ordinary links. Both survive the editor's Markdown,
// a file link draws as a chip, and the export shows page titles only where the reader can see the page.
import { readFileSync } from 'node:fs'
import { TaskItem, TaskList } from '@tiptap/extension-list'
import { TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import TextAlign from '@tiptap/extension-text-align'
import { Color } from '@tiptap/extension-text-style'
import { Markdown } from '@tiptap/markdown'
import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import { buildExportDom } from '../components/docs/export'
import { blockExtensions } from '../components/editor/blocks'
import { FILE_HREF, FileChips } from '../components/editor/file-link'
import { AlignHeading, AlignParagraph, ColorText, HtmlTable, SafeImage } from '../components/editor/formats'
import { filterSlash } from '../components/editor/slash'

const fixtures: { id: string; md: string }[] = JSON.parse(
  readFileSync('../collab/test/fixtures/blocks.json', 'utf8'),
)
const fx = (id: string) => fixtures.find((f) => f.id === id)?.md ?? ''
const WS = '7b3f1c2e-5a4d-4e6f-8a9b-0c1d2e3f4a5b'
const ID = '0f0e0d0c-0b0a-4908-8706-050403020100'

const make = (content: string, extra: unknown[] = []) =>
  new Editor({
    element: document.createElement('div'),
    extensions: [
      StarterKit.configure({ paragraph: false, heading: false, codeBlock: false }),
      AlignParagraph,
      AlignHeading,
      TextAlign.configure({ types: ['heading', 'paragraph'] }),
      ColorText,
      Color,
      SafeImage.configure({ inline: false, allowBase64: false }),
      Markdown,
      HtmlTable.configure({ resizable: true }),
      TableRow,
      TableHeader,
      TableCell,
      TaskList,
      TaskItem.configure({ nested: true }),
      ...blockExtensions,
      ...(extra as never[]),
    ],
    content,
    contentType: 'markdown',
  })
const rt = (md: string) => {
  const e = make(md)
  const out = e
    .getMarkdown()
    .trim()
    .replace(/\n{3,}/g, '\n\n')
  e.destroy()
  return out
}

describe('file links', () => {
  it('round-trip byte for byte, in a paragraph, a list and a merged table', () => {
    expect(rt(fx('file-link'))).toBe(fx('file-link'))
  })
  it('only the own-workspace file path is a file link', () => {
    expect(FILE_HREF.test(`/api/w/${WS}/files/${ID}`)).toBe(true)
    expect(FILE_HREF.test(`/api/files/${ID}`)).toBe(false)
    expect(FILE_HREF.test(`/api/w/${WS}/files/${ID}/x`)).toBe(false)
  })
  it('is drawn as a chip, and a lookalike is not', () => {
    const e = make(`[Price](/api/w/${WS}/files/${ID}) [Old](/api/files/${ID}) [Web](https://example.com)`, [
      FileChips,
    ])
    const chips = [...e.view.dom.querySelectorAll('.file-chip')]
    expect(chips).toHaveLength(1)
    expect(chips[0].getAttribute('data-file')).toBe(ID)
    e.destroy()
  })
})

describe('page links', () => {
  it('round-trip, with [[ left alone when no page is chosen and inside code', () => {
    expect(rt(fx('page-link'))).toBe(fx('page-link'))
  })
  it('still escapes a bracket that could close a link', () => {
    expect(rt('[[a] and more')).toBe('\\[\\[a\\] and more')
  })
})

describe('slash menu', () => {
  it('offers File and Link to page', () => {
    expect(filterSlash('file').map((i) => i.title)).toContain('File')
    expect(filterSlash('link to page').map((i) => i.title)).toContain('Link to page')
  })
})

describe('export resolves page links', () => {
  const md = `[Old title](/w/${WS}/n/${ID}) and [Secret](/w/${WS}/n/11111111-1111-4111-8111-111111111111)`
  const run = async (resolveTitle: (ws: string, id: string) => Promise<string | null>) => {
    const e = make(md)
    const { root } = await buildExportDom(
      e,
      { title: 'T', status: null, labels: [], updatedAt: '' },
      { resolveTitle },
    )
    e.destroy()
    return root
  }
  it('uses the current title where the reader can see the page, plain text where not', async () => {
    const root = await run(async (_ws, id) => (id === ID ? 'New title' : null))
    const links = root.querySelectorAll('main a')
    expect(links).toHaveLength(1)
    expect(links[0].textContent).toBe('New title')
    expect(root.querySelector('main')?.textContent).toContain('Secret')
  })
})
