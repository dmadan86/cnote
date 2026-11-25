// docs-parity C1 13.3.5, gateway side: a page whose Yjs document already carries the C1 attributes (highlight on textStyle,
// background on cells, numbered on a table) is folded to Markdown by today's converter. The attributes are not stored yet (R1
// teaches md.ts), but no text may be lost. The byte-exact forms are in fixtures/blocks.json (pending: "c1").
import { describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { toMarkdown } from '../src/md.ts'

const doc = (build: (f: Y.XmlFragment) => void) => {
  const d = new Y.Doc()
  build(d.getXmlFragment('default'))
  return toMarkdown(d)
}
const para = (text: string, fmt?: Record<string, unknown>) => {
  const p = new Y.XmlElement('paragraph')
  const t = new Y.XmlText()
  p.insert(0, [t])
  t.insert(0, text, fmt)
  return p
}
const cell = (kind: string, text: string, attrs: Record<string, string> = {}) => {
  const c = new Y.XmlElement(kind)
  for (const [k, v] of Object.entries(attrs)) c.setAttribute(k, v)
  c.insert(0, [para(text)])
  return c
}

describe('the folding converter meets C1 attributes', () => {
  it('highlight, and colour with highlight, keep their text', () => {
    const md = doc((f) =>
      f.insert(0, [
        para('only highlighted', { textStyle: { highlight: 'yellow' } }),
        para('colour and highlight', { textStyle: { color: '#c0392b', highlight: 'green' } }),
      ]),
    )
    expect(md).toContain('only highlighted')
    expect(md).toContain('colour and highlight')
  })

  it('a coloured cell and a numbered table keep their text', () => {
    const md = doc((f) => {
      const t = new Y.XmlElement('table')
      t.setAttribute('numbered', 'true')
      for (const cells of [
        [cell('tableHeader', 'Head one', { background: 'grey' }), cell('tableHeader', 'Head two')],
        [cell('tableCell', 'Green cell', { background: 'green' }), cell('tableCell', 'Plain cell')],
      ]) {
        const r = new Y.XmlElement('tableRow')
        r.insert(0, cells)
        t.insert(t.length, [r])
      }
      f.insert(0, [t])
    })
    for (const w of ['Head one', 'Head two', 'Green cell', 'Plain cell']) expect(md).toContain(w)
  })
})
