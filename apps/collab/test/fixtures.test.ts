// docs-parity 7.4: the shared block fixtures (fixtures/blocks.json) through the gateway's Markdown converter.
// Active today: nothing crashes, the text survives (older clients must read new blocks acceptably, 5.1), and a
// second pass changes nothing. Skipped until the block builders land (7.8, W1): the byte-exact round trip.
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { seed, toMarkdown } from '../src/md.ts'

type Fixture = { id: string; covers: string[]; md: string; plain: string; refused?: boolean }
const fixtures: Fixture[] = JSON.parse(
  readFileSync(new URL('./fixtures/blocks.json', import.meta.url), 'utf8'),
)

/** Flip to true in the commit that teaches md.ts the A1 blocks. */
const BLOCKS_LANDED = true
const blockIt = BLOCKS_LANDED ? it : it.skip

const rt = (md: string) => {
  const doc = new Y.Doc()
  seed(doc, md)
  return toMarkdown(doc)
}
const words = (s: string) => new Set(s.match(/[\p{L}\p{N}]{3,}/gu) ?? [])

describe('block fixtures through md.ts', () => {
  it('covers every A1 block, with nesting, tables, lookalikes and images', () => {
    const covered = new Set(fixtures.flatMap((f) => f.covers))
    for (const c of [
      'panel',
      'expand',
      'status',
      'date',
      'toc',
      'code',
      'image',
      'nesting',
      'table',
      'merged',
      'lookalike',
    ])
      expect(covered, c).toContain(c)
    expect(new Set(fixtures.map((f) => f.id)).size).toBe(fixtures.length)
  })

  for (const f of fixtures) {
    describe(f.id, () => {
      it('does not lose text', () => {
        const out = words(rt(f.md))
        // `plain` holds words that already pass through today; the status word, panel text and so on.
        for (const w of words(f.plain)) expect(out, `lost "${w}"`).toContain(w)
      })
      it('is stable on a second pass', () => {
        const once = rt(f.md)
        expect(rt(once)).toBe(once)
      })
      blockIt('round-trips byte for byte (block-specific)', () => {
        expect(rt(f.md)).toBe(f.md)
      })
    })
  }
})
