import { describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { cells, FRAGMENT, same, seed, toMarkdown } from '../src/md.ts'

const DOC =
  '# Runbook\n\nRestart the pods, then wait.\n\n## Steps\n\n- check logs\n  - tail the api\n  - tail the worker\n- restart\n\n1. one\n2. two\n\n```sh\nkubectl get pods\n```\n\n> careful\n>\n> really careful'

const rt = (md: string) => {
  const doc = new Y.Doc()
  seed(doc, md)
  return toMarkdown(doc)
}
const frag = (doc: Y.Doc) => doc.getXmlFragment(FRAGMENT)
const el = (n: unknown) => {
  expect(n).toBeInstanceOf(Y.XmlElement)
  return n as Y.XmlElement
}

/** Attribute names present on the first text run whose text is `needle`. */
function marksOf(md: string, needle: string): string[] {
  const doc = new Y.Doc()
  seed(doc, md)
  const p = el(frag(doc).get(0))
  for (const c of p.toArray()) {
    if (c instanceof Y.XmlText) {
      for (const d of c.toDelta() as { insert: unknown; attributes?: Record<string, unknown> }[]) {
        if (d.insert === needle) return Object.keys(d.attributes ?? {}).sort()
      }
    }
  }
  throw new Error(`run ${JSON.stringify(needle)} not found`)
}

describe('md', () => {
  it('markdown_round_trips_through_the_fragment', () => {
    const doc = new Y.Doc()
    seed(doc, DOC)
    expect(toMarkdown(doc)).toBe(DOC)
  })

  it('fragment_has_the_tiptap_shape', () => {
    const doc = new Y.Doc()
    seed(doc, '## Title\n\n- a\n- b')
    const f = frag(doc)
    expect(f.toArray().map((c) => (c as Y.XmlElement).nodeName)).toEqual(['heading', 'bulletList'])
    expect(el(f.get(0)).getAttribute('level')).toBe(2)
    const li = el(el(f.get(1)).get(0))
    expect(li.nodeName).toBe('listItem')
    expect(el(li.get(0)).nodeName).toBe('paragraph')
  })

  it('editor_marks_become_markdown', () => {
    const doc = new Y.Doc()
    doc.transact(() => {
      const p = new Y.XmlElement('paragraph')
      frag(doc).push([p])
      const t = new Y.XmlText()
      p.push([t])
      t.insert(0, 'plain ')
      t.insert(6, 'loud', { bold: true })
      t.insert(10, ' docs', { link: { href: 'https://example.com' } })
    })
    expect(toMarkdown(doc)).toBe('plain **loud** [docs](https://example.com)')
  })

  it('tables_and_tasks_round_trip_like_tiptap', () => {
    const md =
      'Intro\n\n| Name  | Qty | Note      |\n| ----- | --- | --------- |\n| Pens  | 10  | a \\| b    |\n| Paper |     | line<br>2 |\n\n- [ ] call Priya\n- [x] send quote'
    const doc = new Y.Doc()
    seed(doc, md)
    expect(toMarkdown(doc)).toBe(md)
    expect(el(frag(doc).get(1)).nodeName).toBe('table')
    expect(el(frag(doc).get(2)).nodeName).toBe('taskList')
  })

  it('table_without_header_renders_an_empty_header_row', () => {
    const doc = new Y.Doc()
    seed(doc, '|     |     |\n| --- | --- |\n| a   | b   |')
    expect(toMarkdown(doc)).toBe('|     |     |\n| --- | --- |\n| a   | b   |')
  })

  it('browser_and_server_snapshots_compare_equal', () => {
    const browser = '## T\n\n\n| h   |\n| --- |\n| v   |\n\n\nend\n'
    const doc = new Y.Doc()
    seed(doc, browser)
    expect(same(toMarkdown(doc), browser)).toBe(true)
    expect(same('a\n\nb', 'a\nb')).toBe(false)
  })

  it('empty_input_gives_empty_doc', () => {
    const doc = new Y.Doc()
    seed(doc, '')
    expect(toMarkdown(doc)).toBe('')
  })

  it('each_inline_mark_becomes_a_yjs_attribute_and_back', () => {
    for (const [md, run, mark] of [
      ['a **b** c', 'b', 'bold'],
      ['a __b__ c', 'b', 'bold'],
      ['a *b* c', 'b', 'italic'],
      ['a _b_ c', 'b', 'italic'],
      ['a ~~b~~ c', 'b', 'strike'],
      ['a `b` c', 'b', 'code'],
      ['a [b](https://x.y) c', 'b', 'link'],
    ]) {
      expect(marksOf(md, run), md).toEqual([mark])
    }
    for (const md of ['a **b** c', 'a *b* c', 'a ~~b~~ c', 'a `b` c', 'a [b](https://x.y) c']) {
      expect(rt(md)).toBe(md)
    }
    expect(rt('a __b__ c _d_')).toBe('a **b** c *d*')
    expect(rt('a [b](https://x.y "t") c')).toBe('a [b](https://x.y) c')
  })

  it('link_mark_carries_href', () => {
    const doc = new Y.Doc()
    seed(doc, '[docs](https://example.com/a)')
    const t = el(frag(doc).get(0)).get(0) as Y.XmlText
    const d = t.toDelta() as { attributes: { link: { href: string } } }[]
    expect(d[0].attributes.link.href).toBe('https://example.com/a')
  })

  it('nesting_escapes_and_unclosed_markers', () => {
    expect(marksOf('**bold *both***', 'both')).toEqual(['bold', 'italic'])
    expect(marksOf('**bold *both***', 'bold ')).toEqual(['bold'])
    expect(rt('**bold *both***')).toBe('**bold** ***both***')
    expect(marksOf('[**x**](https://u.v)', 'x')).toEqual(['bold', 'link'])
    expect(rt('[**x**](https://u.v)')).toBe('[**x**](https://u.v)')
    expect(rt('\\*not italic\\* and \\[x\\]')).toBe('*not italic* and [x]')
    expect(marksOf('\\*lit\\*', '*lit*')).toEqual([])
    for (const lit of ['**open', 'a * b * c', 'snake_case_name', '[no](', '~one~', '`open', '2 ** 3']) {
      expect(rt(lit)).toBe(lit)
    }
  })

  it('colour_span_in_markdown', () => {
    const c = '<span style="color: #dc2626">hot</span>'
    expect(rt(c)).toBe(c)
    expect(marksOf(c, 'hot')).toEqual(['textStyle'])
    const bold = `**${c}**`
    expect(rt(bold)).toBe(bold)
    expect(marksOf(bold, 'hot')).toEqual(['bold', 'textStyle'])
    const link = '[<span style="color: #dc2626">x</span>](https://a.b)'
    expect(rt(link)).toBe(link)
    expect(marksOf(link, 'x')).toEqual(['link', 'textStyle'])
    expect(rt('<span style="color:#F00;">a &amp; b</span>')).toBe(
      '<span style="color: #ff0000">a &amp; b</span>',
    )
    expect(rt("<span style='color: rgb(37, 99, 235)'>x</span>")).toBe('<span style="color: #2563eb">x</span>')
  })

  it('aligned_paragraph_and_heading_with_inline_html', () => {
    const p =
      '<p style="text-align: center"><a href="https://x.y"><strong>Hi</strong></a> &amp; <span style="color: #2563eb">blue</span><br>next <em>it</em> <s>no</s> <code>c</code></p>'
    expect(rt(p)).toBe(p)
    const h = '<h2 style="text-align: right">T <strong>b</strong></h2>'
    expect(rt(h)).toBe(h)
    const doc = new Y.Doc()
    seed(doc, h)
    const e = el(frag(doc).get(0))
    expect(e.nodeName).toBe('heading')
    expect(e.getAttribute('textAlign')).toBe('right')
    expect(e.getAttribute('level')).toBe(2)
    expect(rt('<p class="x" style="text-align:justify;"><b>a</b><i>b</i><del>c</del><br/></p>')).toBe(
      '<p style="text-align: justify"><strong>a</strong><em>b</em><s>c</s><br></p>',
    )
    expect(rt('<p style="text-align: left">x</p>')).toBe('x')
    expect(rt('<p><blink>x</blink></p>')).toBe('<p><blink>x</blink></p>')
  })

  it('aligned_blocks_inside_lists_lose_alignment', () => {
    const doc = new Y.Doc()
    seed(doc, '<p style="text-align: center">x</p>')
    doc.transact(() => {
      const q = new Y.XmlElement('blockquote')
      frag(doc).push([q])
      const p = new Y.XmlElement('paragraph')
      q.push([p])
      p.setAttribute('textAlign', 'center')
      p.push([new Y.XmlText('in quote')])
    })
    expect(toMarkdown(doc)).toBe('<p style="text-align: center">x</p>\n\n> in quote')
  })

  const HTML_TABLE =
    '<table><tr><th colspan="2" colwidth="120,80"><p>Head <strong>A</strong></p></th><th><p>B</p></th></tr><tr><td rowspan="2"><p>x</p><p>y</p></td><td><p></p></td><td><p>a<br>b</p></td></tr><tr><td colwidth="50"><p>z &amp; w</p></td><td><p></p></td></tr></table>'

  it('html_table_round_trips_and_has_the_tiptap_shape', () => {
    expect(rt(HTML_TABLE)).toBe(HTML_TABLE)
    const doc = new Y.Doc()
    seed(doc, HTML_TABLE)
    const rows = cells(el(frag(doc).get(0)))
    expect(rows.length).toBe(3)
    expect(rows[0][0][2]).toEqual([2, 1, [120, 80]])
    expect(rows[0][0][1] && !rows[1][0][1]).toBe(true)
    expect(rows[1][0][2]).toEqual([1, 2, null])
    expect(rows[2][0][2]).toEqual([1, 1, [50]])
  })

  it('html_table_input_is_parsed_leniently', () => {
    const loose =
      '<table>\n<tbody>\n <tr> <th colspan=\'2\'> <p>A</p> </th> </tr>\n <tr><td colspan=2>x</td><td rowspan="1" colspan="1">y</td></tr>\n</tbody>\n</table>'
    expect(rt(loose)).toBe(
      '<table><tr><th colspan="2"><p>A</p></th></tr><tr><td colspan="2"><p>x</p></td><td><p>y</p></td></tr></table>',
    )
    expect(rt('<table><tr><td>x</td></tr><div></table>')).toBe('<table><tr><td>x</td></tr><div></table>')
  })

  it('gfm_table_cells_carry_inline_marks', () => {
    const md =
      '| Name      | Note           |\n| --------- | -------------- |\n| **bold**  | [a](https://x) |\n| `c` ~~s~~ | *i*<br>line 2  |'
    const doc = new Y.Doc()
    seed(doc, md)
    const out = toMarkdown(doc)
    expect(out).toBe(md)
    expect(cells(el(frag(doc).get(0))).length).toBe(3)
    expect(out.includes('<table')).toBe(false)
  })

  it('images_with_and_without_title', () => {
    for (const md of [
      '![a cat](https://x.y/c.png)',
      '![a cat](https://x.y/c.png "The Cat")',
      'text\n\n![](https://x.y/c.png)\n\nmore',
    ]) {
      expect(rt(md)).toBe(md)
    }
    const doc = new Y.Doc()
    seed(doc, '![a cat](https://x.y/c.png "The Cat")')
    const e = el(frag(doc).get(0))
    expect(e.nodeName).toBe('image')
    for (const [k, v] of [
      ['src', 'https://x.y/c.png'],
      ['alt', 'a cat'],
      ['title', 'The Cat'],
    ]) {
      expect(e.getAttribute(k)).toBe(v)
    }
  })

  it('unsafe_values_are_dropped', () => {
    expect(rt('<span style="color: red&quot; onclick=&quot;x">hi</span>')).toBe('hi')
    expect(rt('<span style="color: #12">hi</span>')).toBe('hi')
    expect(rt('<p><span style="color: #zzz">hi</span></p>')).toBe('hi')
    expect(rt('[x](javascript:alert(1))')).toBe('x')
    expect(rt('[x](  JaVaScRiPt:alert(1))')).toBe('x')
    expect(rt('<p><a href="javascript:alert(1)">x</a></p>')).toBe('x')
    expect(rt('[x](data:text/html,hi)')).toBe('x')
    for (const ok of ['https://a.b/?q=1&r=2', 'mailto:a@b.c', '/rel/path', '#frag', './x', 'page.html']) {
      expect(rt(`[x](${ok})`)).toBe(`[x](${ok})`)
    }
    const doc = new Y.Doc()
    doc.transact(() => {
      const f = frag(doc)
      const p = new Y.XmlElement('paragraph')
      f.push([p])
      const t = new Y.XmlText()
      p.push([t])
      t.insert(0, 'evil', {
        link: { href: 'javascript:alert(1)' },
        textStyle: { color: 'red" onclick="x' },
      })
      const img = new Y.XmlElement('image')
      f.push([img])
      img.setAttribute('src', 'data:image/png;base64,AAAA')
    })
    expect(toMarkdown(doc)).toBe('evil')
    const bad = '<table><tr><td colwidth="a,2" colspan="-1"><p>x</p></td></tr></table>'
    expect(rt(bad)).toBe('|     |\n| --- |\n| x   |')
    expect(rt('<table><tr><td colwidth="0,5"><p>x</p></td></tr></table>')).toBe('|     |\n| --- |\n| x   |')
    expect(rt('![x](data:image/png;base64,AAAA)')).toBe('!x')
    expect(rt('![x](javascript:alert(1))')).toBe('!x')
  })

  it('editor_fixtures_round_trip', () => {
    const fx =
      '<h2 style="text-align: center">Head</h2>\n\nship \u{1F680}\n\n**<span style="color: #dc2626">hot</span>**\n\n![cat](https://example.com/cat.png)\n\n<table><tr><th colspan="2"><p>a</p><p>b</p></th><th colwidth="244"><p>c</p></th></tr><tr><td><p></p></td><td><p></p></td><td colwidth="244"><p></p></td></tr></table>'
    expect(rt(fx)).toBe(fx)
    const esc = '<span style="color: #dc2626">a \\*b\\* \\_c &amp; &lt;d&gt;</span>'
    expect(rt(esc)).toBe(esc)
    expect(marksOf(esc, 'a *b* _c & <d>')).toEqual(['textStyle'])
    const h = '<p style="text-align: right"><span style="color: #dc2626">a *b* \\ _c</span></p>'
    expect(rt(h)).toBe(h)
  })

  // ---- extras ----
  it('round_trips_a_mixed_document', () => {
    const md =
      '# T\n\n**b** and *i* and ~~s~~ and `c` and [l](https://x.y)\n\n- a\n  - nested\n- b\n\n- [x] done\n- [ ] todo\n\n```\nplain\n```\n\n> q1\n>\n> q2\n\n![i](https://x.y/i.png "t")'
    expect(rt(md)).toBe(md)
  })

  it('same_seed_and_clientID_gives_byte_equal_updates', () => {
    const enc = () => {
      const d = new Y.Doc()
      d.clientID = 42
      seed(d, `${DOC}\n\n| a | b |\n| --- | --- |\n| **x** | y |`)
      return Y.encodeStateAsUpdate(d)
    }
    expect(Buffer.from(enc()).equals(Buffer.from(enc()))).toBe(true)
  })

  it('survives_an_update_round_trip', () => {
    const a = new Y.Doc()
    seed(a, DOC)
    const b = new Y.Doc()
    Y.applyUpdate(b, Y.encodeStateAsUpdate(a))
    expect(toMarkdown(b)).toBe(DOC)
  })
})
