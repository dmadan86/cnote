// The 12 built-in templates (apps/api/internal/modules/docs/builtin.go) through the gateway's Markdown converter:
// the text survives, a second pass changes nothing, and the stored form comes back byte for byte (tables cell by cell).
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { seed, toMarkdown } from '../src/md.ts'

const go = readFileSync(new URL('../../api/internal/modules/docs/builtin.go', import.meta.url), 'utf8')
const bodies = [...go.matchAll(/`([^`]*)`/g)].map((m) => m[1])
const keys = [...go.matchAll(/^\t\{"([a-z0-9-]+)", "/gm)].map((m) => m[1])

const rt = (md: string) => {
  const doc = new Y.Doc()
  seed(doc, md)
  return toMarkdown(doc)
}
const words = (s: string) => new Set(s.match(/[\p{L}\p{N}]{3,}/gu) ?? [])
// Table padding is the converters' business (fixtures.test.ts), so compare tables cell by cell.
const flat = (md: string) =>
  md
    .trimEnd()
    .split('\n')
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

describe('built-in templates through md.ts', () => {
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
