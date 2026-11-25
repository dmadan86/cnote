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
import { blockExtensions } from '../components/editor/blocks'
import { AlignHeading, AlignParagraph, ColorText, HtmlTable, SafeImage } from '../components/editor/formats'
import { inlineHtml } from '../components/editor/html-inline'

type Fixture = {
  id: string
  covers: string[]
  md: string
  plain: string
  refused?: boolean
  pending?: string
}
// `pnpm --filter @cnote/web test` runs in apps/web
const fixtures: Fixture[] = JSON.parse(readFileSync('../collab/test/fixtures/blocks.json', 'utf8'))

/** Flip to true in the commit that adds the A1 extensions and teaches inlineHtml the status mark. */
const BLOCKS_LANDED = true
const blockIt = BLOCKS_LANDED ? it : it.skip
/** Flip to true in the commit that adds the C1 extensions (highlight, cell colour, numbered tables); see the collab copy. */
const C1_LANDED = false
const c1It = (f: Fixture) => (f.pending === 'c1' && !C1_LANDED ? it.skip : blockIt)
/** Found by this test before the blocks landed (both fixed by W1, so they are asserted now). Skipped only while BLOCKS_LANDED is false.
 * image-upload-and-external: SafeImage stored only http(s) images, so an uploaded /api/w/<ws>/files/<id> image and its alt text vanished.
 * status-next-to-other-marks: the unknown <span> was dropped, the link inside it escaped as \[review\](url) and each
 *   further pass wrapped the URL again, so the page changed at every save. */
const brokenToday: Record<string, ('text' | 'stable')[]> = {
  'image-upload-and-external': ['text'],
  'status-next-to-other-marks': ['stable'],
}
const checkIt = (id: string, what: 'text' | 'stable') =>
  !BLOCKS_LANDED && brokenToday[id]?.includes(what) ? it.skip : it
/** C1 step 0 finding: before R1 the editor has no <mark>, so markdown inside it (a link) is escaped and the URL is wrapped
 * again at every save of an old tab. No text is lost. R1 lands the mark and removes this entry. */
const c1Unstable = new Set(['highlight-next-to-other-marks'])
const stableIt = (id: string) => (c1Unstable.has(id) && !C1_LANDED ? it.skip : checkIt(id, 'stable'))

// The extension list of components/note-editor.tsx, without the UI-only ones (slash menu, mentions, comments).
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
const words = (s: string) => new Set(s.match(/[\p{L}\p{N}]{3,}/gu) ?? [])

describe('block fixtures through TipTap Markdown', () => {
  for (const f of fixtures) {
    describe(f.id, () => {
      checkIt(f.id, 'text')('does not lose text', () => {
        const out = words(rt(f.md))
        for (const w of words(f.plain)) expect(out, `lost "${w}"`).toContain(w)
      })
      stableIt(f.id)('is stable on a second pass', () => {
        const once = rt(f.md)
        expect(rt(once)).toBe(once)
      })
      c1It(f)('round-trips byte for byte (block-specific)', () => {
        // The table writer wraps a table in blank lines; the gateway's contract ignores blank-line runs and the ends.
        const norm = (t: string) => t.trim().replace(/\n{3,}/g, '\n\n')
        // A refused image keeps its alt text and loses its URL here; the gateway keeps the inert text as it was.
        if (f.refused) expect(rt(f.md)).toBe('x and y and z')
        else expect(norm(rt(f.md))).toBe(f.md)
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
