// docs-parity A1: edge cases of the block converters beyond the shared fixtures.
import { TaskItem, TaskList } from '@tiptap/extension-list'
import { TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import TextAlign from '@tiptap/extension-text-align'
import { Color } from '@tiptap/extension-text-style'
import { Markdown } from '@tiptap/markdown'
import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import { blockExtensions, blocksEnabled, setGatewayMinSchema } from '../components/editor/blocks'
import { AlignHeading, AlignParagraph, ColorText, HtmlTable, SafeImage } from '../components/editor/formats'
import { filterSlash } from '../components/editor/slash'

function rt(md: string): string {
  const e = new Editor({
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
    content: md,
    contentType: 'markdown',
  })
  const out = e.getMarkdown()
  e.destroy()
  return out
}

describe('block converters', () => {
  it('drops a status colour that is not on the list and keeps the text', () => {
    expect(rt('<span data-status="pink">DONE</span> ok')).toBe('DONE ok')
  })
  it('keeps a status span around a link stable over many saves', () => {
    const md = '<span data-status="yellow">[review](https://example.com/r)</span>'
    expect(rt(md)).toBe(md)
    expect(rt(rt(md))).toBe(md)
  })
  it('keeps alt text, not the URL, of an image outside the allowed forms', () => {
    expect(rt('![logo](javascript:alert(1))')).not.toContain('javascript')
    expect(
      rt(
        '![uploaded](/api/w/7b3f1c2e-5a4d-4e6f-8a9b-0c1d2e3f4a5b/files/0f0e0d0c-0b0a-4908-8706-050403020100)',
      ),
    ).toContain('/files/0f0e0d0c-0b0a-4908-8706-050403020100')
  })
  it('gives an empty panel and expand a paragraph, and escapes the expand title', () => {
    expect(rt('> [!INFO]')).toBe('> [!INFO]\n>')
    expect(rt('<details><summary>a &lt;b&gt; &amp; "c"</summary>\n\nx\n\n</details>')).toContain(
      '<summary>a &lt;b&gt; &amp; &quot;c&quot;</summary>',
    )
  })
  it('hides the new slash entries until the gateway minimum is 2, keeps the others', () => {
    setGatewayMinSchema(0)
    expect(blocksEnabled()).toBe(false)
    expect(filterSlash('panel')).toHaveLength(0)
    expect(filterSlash('upload').map((i) => i.title)).toEqual(['Upload image'])
    setGatewayMinSchema(2)
    expect(filterSlash('panel').length).toBe(5)
    expect(filterSlash('').map((i) => i.title)).toEqual(
      expect.arrayContaining(['Expand', 'Status', 'Date', 'Table of contents']),
    )
    setGatewayMinSchema(0)
  })
})
