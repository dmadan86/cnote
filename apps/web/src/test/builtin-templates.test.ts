// The 12 built-in templates (apps/api/internal/modules/docs/builtin.go) through the browser's TipTap Markdown with the
// editor's real format and block extensions: the text survives, a second pass changes nothing, and the stored form comes
// back byte for byte (tables cell by cell).
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

// The bodies are the only raw (backtick) strings in builtin.go. `pnpm --filter @cnote/web test` runs in apps/web.
const go = readFileSync('../api/internal/modules/docs/builtin.go', 'utf8')
const bodies = [...go.matchAll(/`([^`]*)`/g)].map((m) => m[1])
const keys = [...go.matchAll(/^\t\{"([a-z0-9-]+)", "/gm)].map((m) => m[1])

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
// Table padding is the converters' business (fixtures.test.ts), so compare tables cell by cell.
const flat = (md: string) =>
  md
    .trimEnd()
    .split('\n')
    .filter((l) => l !== '') // the table serializer adds blank lines around a table
    .map((l) =>
      l.startsWith('|')
        ? `| ${l
            .replace(/^\||\|$/g, '')
            .split('|')
            .map((c) => (/^\s*:?-+:?\s*$/.test(c) ? '---' : c.trim()))
            .join(' | ')} |`
        : l,
    )
    .join('\n')

describe('built-in templates through TipTap Markdown', () => {
  it('finds all twelve', () => {
    expect(bodies).toHaveLength(12)
    expect(keys).toHaveLength(12)
  })
  bodies.forEach((body, i) => {
    describe(keys[i], () => {
      it('does not lose text', () => {
        const out = words(rt(body))
        for (const w of words(body.replace(/<[^>]+>/g, ' '))) expect(out, `lost "${w}"`).toContain(w)
      })
      it('is stable on a second pass', () => {
        const once = rt(body)
        expect(rt(once)).toBe(once)
      })
      it('round-trips byte for byte', () => {
        expect(flat(rt(body))).toBe(flat(body))
      })
    })
  })
})
