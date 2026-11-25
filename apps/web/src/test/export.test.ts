// Docs A2 export builders over the shared A1 fixtures (docs-parity 7.9, 7.10).
import { readFileSync } from 'node:fs'
import { TaskItem, TaskList } from '@tiptap/extension-list'
import { TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import TextAlign from '@tiptap/extension-text-align'
import { Color } from '@tiptap/extension-text-style'
import { Markdown } from '@tiptap/markdown'
import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import {
  buildExportDom,
  buildHtmlFile,
  buildMarkdown,
  MAX_IMAGE_BYTES,
  safeFileName,
} from '../components/docs/export'
import { blockExtensions } from '../components/editor/blocks'
import { AlignHeading, AlignParagraph, ColorText, HtmlTable, SafeImage } from '../components/editor/formats'

const fixtures: { id: string; md: string }[] = JSON.parse(
  readFileSync('../collab/test/fixtures/blocks.json', 'utf8'),
)
const md = (...ids: string[]) => ids.map((i) => fixtures.find((f) => f.id === i)?.md).join('\n\n')

const editor = (content: string) =>
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
    ],
    content,
    contentType: 'markdown',
  })

const meta = { title: 'Leave policy', status: 'Final', labels: ['hr', 'policy'], updatedAt: '25 Nov 2026' }
const PNG = new Blob([new Uint8Array([137, 80, 78, 71])], { type: 'image/png' })
const IMG =
  '![Bill photo](/api/w/7b3f1c2e-5a4d-4e6f-8a9b-0c1d2e3f4a5b/files/0f0e0d0c-0b0a-4908-8706-050403020100)'

describe('export DOM and HTML', () => {
  it('opens expands, resolves the TOC, labels code and shows meta and panel types as text', async () => {
    const e = editor(
      md('expand-nested-in-expand-with-panel', 'toc', 'code-language-and-backticks', 'panel-each-kind'),
    )
    const { root } = await buildExportDom(e, meta)
    const details = [...root.querySelectorAll('details')]
    expect(details.length).toBe(2)
    for (const d of details) expect(d.hasAttribute('open')).toBe(true)
    expect(root.querySelector('summary')?.textContent).toBe('Outer')

    const links = [...root.querySelectorAll('nav[data-toc] a')]
    expect(links.map((a) => a.textContent)).toEqual(['Policy', 'Scope', 'Leave'])
    for (const a of links) expect(root.querySelector(a.getAttribute('href') as string)).not.toBeNull()
    expect(root.querySelector('nav[data-toc] ul ul')).not.toBeNull()
    expect(root.textContent).not.toContain('[[toc]]')

    expect(root.querySelector('code.language-ts')).not.toBeNull()
    expect(root.querySelector('.cn-code-lang')?.textContent).toBe('ts')
    expect(root.textContent).toContain('Warning:')
    expect(root.textContent).toContain('Status: Final')
    expect(root.textContent).toContain('Labels: hr, policy')
    expect(root.textContent).toContain('Last updated: 25 Nov 2026')
    e.destroy()
  })

  it('keeps status and date text, including inside a table', async () => {
    const e = editor(md('status-in-merged-table', 'date-inline-and-in-table'))
    const { root } = await buildExportDom(e, meta)
    expect(root.querySelector('table span[data-status="green"]')).not.toBeNull()
    expect(root.querySelector('time')?.textContent).toBe('1 Dec 2026')
    e.destroy()
  })

  it('embeds images as data URIs, and keeps alt text with a note when a fetch fails', async () => {
    const e = editor(IMG)
    const ok = await buildExportDom(e, meta, { fetchImage: async () => PNG })
    expect(ok.root.querySelector('img')?.getAttribute('src')).toMatch(/^data:image\/png;base64,/)
    expect(ok.notes).toEqual([])
    const bad = await buildExportDom(e, meta, {
      fetchImage: async () => {
        throw new Error('403')
      },
    })
    expect(bad.root.querySelector('img')).toBeNull()
    expect(bad.root.textContent).toContain('Bill photo')
    expect(bad.notes[0]).toContain('1 image could not be embedded')
    e.destroy()
  })

  it('stops embedding past 25 MB and says so', async () => {
    const e = editor(`${IMG}\n\n${IMG.replace('0f0e0d0c-0b0a', '1f0e0d0c-0b0a')}`)
    const r = await buildExportDom(e, meta, {
      fetchImage: async () =>
        Object.defineProperty(new Blob(['x'], { type: 'image/png' }), 'size', { value: MAX_IMAGE_BYTES - 1 }),
    })
    expect(r.root.querySelectorAll('img').length).toBe(1)
    expect(r.notes.join(' ')).toContain('25 MB')
    e.destroy()
  })

  it('produces HTML with no script, event handler or javascript: URL, and escapes the title', async () => {
    const hostile =
      '# T\n\n<script>alert(1)</script><img src=x onerror=alert(1)> [x](javascript:alert(1)) <a href="javascript:1" onclick="x()">a</a>\n\n<span data-status="red" onclick="x()">R</span>'
    const e = editor(hostile)
    const evil = { ...meta, title: '</title><script>alert(1)</script>', labels: ['<b onmouseover=1>'] }
    const { root } = await buildExportDom(e, evil)
    const html = buildHtmlFile(root, evil)
    expect(html.startsWith('<!doctype html>')).toBe(true)
    const out = new DOMParser().parseFromString(html, 'text/html')
    expect(out.querySelector('script')).toBeNull()
    expect(out.title).toBe(evil.title)
    for (const n of out.querySelectorAll('*')) {
      for (const a of n.attributes) {
        expect(a.name).not.toMatch(/^on/i)
        expect(a.value).not.toMatch(/^\s*javascript:/i)
      }
    }
    expect(out.querySelector('a[href^="javascript"]')).toBeNull()
    expect(out.querySelector('b')).toBeNull()
    expect(html).toContain('<meta charset="utf-8">')
    e.destroy()
  })
})

describe('Markdown and file names', () => {
  it('embeds uploaded images and leaves everything else as stored', async () => {
    const e = editor(`${md('panel-each-kind')}\n\n${IMG}`)
    const r = await buildMarkdown(e, { fetchImage: async () => PNG })
    expect(r.text).toContain('> [!INFO]')
    expect(r.text).toMatch(/!\[Bill photo\]\(data:image\/png;base64,/)
    expect(r.text).not.toContain('/api/w/')
    const f = await buildMarkdown(e, {
      fetchImage: async () => {
        throw new Error('x')
      },
    })
    expect(f.text).toContain('Bill photo (image not embedded)')
    expect(f.notes.length).toBe(1)
    e.destroy()
  })

  it('cleans file names', () => {
    expect(safeFileName('../a/b:c*?.md', 'md')).toBe('abcmd.md')
    expect(safeFileName('', 'html')).toBe('page.html')
    expect(safeFileName('x'.repeat(200), 'md').length).toBe(83)
  })
})
