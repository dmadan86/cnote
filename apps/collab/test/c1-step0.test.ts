// docs-parity C1 13.3.5, gateway side: a page whose Yjs document already carries the C1 attributes (highlight on textStyle,
// background on cells, numbered on a table) is folded to Markdown. R1 teaches md.ts to store them; no text may be lost
// either way. The byte-exact forms are in fixtures/blocks.json.
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

describe('the folding converter writes the C1 forms (R1)', () => {
  it('highlight beside colour, and the table attributes, are stored', () => {
    const md = doc((f) =>
      f.insert(0, [
        para('only highlighted', { textStyle: { highlight: 'yellow' } }),
        para('both', { textStyle: { color: '#c0392b', highlight: 'green' } }),
        para('linked', { link: { href: 'https://example.com/a' }, textStyle: { highlight: 'blue' } }),
        para('code', { code: {}, textStyle: { highlight: 'red' } }),
      ]),
    )
    expect(md).toContain('<mark data-color="yellow">only highlighted</mark>')
    expect(md).toContain('<mark data-color="green"><span style="color: #c0392b">both</span></mark>')
    expect(md).toContain('[<mark data-color="blue">linked</mark>](https://example.com/a)')
    expect(md).toContain('`code`')
    const t = doc((f) => {
      const t = new Y.XmlElement('table')
      t.setAttribute('numbered', true as never)
      const r = new Y.XmlElement('tableRow')
      r.insert(0, [
        cell('tableHeader', 'Head', { background: 'grey' }),
        cell('tableHeader', 'Bad', { background: 'mauve' }),
      ])
      t.insert(0, [r])
      f.insert(0, [t])
    })
    expect(t).toBe(
      '<table data-numbered="true"><tr><th data-bg="grey"><p>Head</p></th><th><p>Bad</p></th></tr></table>',
    )
  })

  it('an old tab that dropped the attributes leaves plain text, never less', () => {
    const md = doc((f) => f.insert(0, [para('oldtab words kept', { textStyle: { color: '#c0392b' } })]))
    expect(md).toBe('<span style="color: #c0392b">oldtab words kept</span>')
  })
})
