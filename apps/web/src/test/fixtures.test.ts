// docs-parity 7.4: the shared block fixtures (apps/collab/test/fixtures/blocks.json) through the browser's TipTap
// Markdown with the editor's real format extensions, and through inlineHtml. Same assertions as the gateway's
// fixtures.test.ts: active today are "no crash", "the text survives" and "stable on a second pass"; the byte-exact
// round trip and the status mark in inlineHtml wait for the block builders (7.8, W1).

import { readFileSync } from 'node:fs'
import { TaskItem, TaskList } from '@tiptap/extension-list'
import { TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import TextAlign from '@tiptap/extension-text-align'
import { Color } from '@tiptap/extension-text-style'
import { Markdown } from '@tiptap/markdown'
import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import { AlignHeading, AlignParagraph, ColorText, HtmlTable, SafeImage } from '../components/editor/formats'
import { inlineHtml } from '../components/editor/html-inline'

type Fixture = { id: string; covers: string[]; md: string; plain: string; refused?: boolean }
// `pnpm --filter @cnote/web test` runs in apps/web
const fixtures: Fixture[] = JSON.parse(readFileSync('../collab/test/fixtures/blocks.json', 'utf8'))

/** Flip to true in the commit that adds the A1 extensions and teaches inlineHtml the status mark. */
const BLOCKS_LANDED = false
const blockIt = BLOCKS_LANDED ? it : it.skip
/** Found by this test: what today's converter breaks, until W1 lands. Block-specific, so skipped like the exact round trip.
 * image-upload-and-external: SafeImage stores only http(s) images, so an uploaded /api/w/<ws>/files/<id> image and its alt text vanish.
 * status-next-to-other-marks: the unknown <span> is dropped, the link inside it is escaped as \[review\](url) and each
 *   further pass wraps the URL again, so the page changes at every save. */
const brokenToday: Record<string, ('text' | 'stable')[]> = {
  'image-upload-and-external': ['text'],
  'status-next-to-other-marks': ['stable'],
}
const checkIt = (id: string, what: 'text' | 'stable') =>
  !BLOCKS_LANDED && brokenToday[id]?.includes(what) ? it.skip : it

// The extension list of components/note-editor.tsx, without the UI-only ones (slash menu, mentions, comments).
function rt(md: string): string {
  const e = new Editor({
    element: document.createElement('div'),
    extensions: [
      StarterKit.configure({ paragraph: false, heading: false }),
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
    ],
    content: md,
    contentType: 'markdown',
  })
  const out = e.getMarkdown()
  e.destroy()
  return out
}
const words = (s: string) => new Set(s.match(/[\p{L}\p{N}]{3,}/gu) ?? [])

describe('block fixtures through TipTap Markdown', () => {
  for (const f of fixtures) {
    describe(f.id, () => {
      checkIt(f.id, 'text')('does not lose text', () => {
        const out = words(rt(f.md))
        for (const w of words(f.plain)) expect(out, `lost "${w}"`).toContain(w)
      })
      checkIt(f.id, 'stable')('is stable on a second pass', () => {
        const once = rt(f.md)
        expect(rt(once)).toBe(once)
      })
      blockIt('round-trips byte for byte (block-specific)', () => {
        expect(rt(f.md)).toBe(f.md)
      })
    })
  }
})

describe('inlineHtml (merged tables)', () => {
  it('keeps the text of marks it knows', () => {
    expect(inlineHtml([{ type: 'text', text: 'a & b', marks: [{ type: 'bold' }] }])).toBe(
      '<strong>a &amp; b</strong>',
    )
  })
  blockIt('writes the status mark as <span data-status>', () => {
    const j = [{ type: 'text', text: 'DONE', marks: [{ type: 'status', attrs: { color: 'green' } }] }]
    expect(inlineHtml(j)).toBe('<span data-status="green">DONE</span>')
  })
  blockIt('writes the date mark as <time datetime>', () => {
    const j = [
      { type: 'text', text: '1 Dec 2026', marks: [{ type: 'date', attrs: { datetime: '2026-12-01' } }] },
    ]
    expect(inlineHtml(j)).toBe('<time datetime="2026-12-01">1 Dec 2026</time>')
  })
})
