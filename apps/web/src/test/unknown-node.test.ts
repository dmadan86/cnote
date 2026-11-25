// docs-parity 7.3: what the locked y-tiptap, Yjs and TipTap do when a client meets a node and a mark its schema
// lacks. Client A's schema has a `panel` node and a `status` mark; client B's has neither. The assertions record
// today's behaviour: if an upgrade changes it, this test fails and the rollout fence (7.3) must be re-judged.
import Collaboration from '@tiptap/extension-collaboration'
import { Editor, Mark, mergeAttributes, Node } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { DOC_SCHEMA } from '../lib/doc-schema'

const Panel = Node.create({
  name: 'panel',
  group: 'block',
  content: 'block+',
  addAttributes: () => ({ kind: { default: 'info' } }),
  parseHTML: () => [{ tag: 'div[data-panel]' }],
  renderHTML: ({ HTMLAttributes }) => ['div', mergeAttributes(HTMLAttributes, { 'data-panel': '' }), 0],
})
const Status = Mark.create({
  name: 'status',
  addAttributes: () => ({ color: { default: 'grey' } }),
  parseHTML: () => [{ tag: 'span[data-status]' }],
  renderHTML: ({ HTMLAttributes }) => ['span', mergeAttributes(HTMLAttributes, { 'data-status': '' }), 0],
})

const mount = (doc: Y.Doc, extra: (Node | Mark)[]) =>
  new Editor({
    element: document.createElement('div'),
    extensions: [
      StarterKit.configure({ undoRedo: false }),
      Collaboration.configure({ document: doc }),
      ...extra,
    ],
  })

/** Two Y docs joined by a relay that can be cut and rejoined (a reconnect is a state-vector exchange). */
function link(a: Y.Doc, b: Y.Doc) {
  let up = true
  const to = (dst: Y.Doc) => (u: Uint8Array, origin: unknown) => {
    if (up && origin !== 'relay') Y.applyUpdate(dst, u, 'relay')
  }
  a.on('update', to(b))
  b.on('update', to(a))
  const sync = () => {
    Y.applyUpdate(b, Y.encodeStateAsUpdate(a, Y.encodeStateVector(b)), 'relay')
    Y.applyUpdate(a, Y.encodeStateAsUpdate(b, Y.encodeStateVector(a)), 'relay')
  }
  return {
    cut: () => {
      up = false
    },
    rejoin: () => {
      up = true
      sync()
    },
  }
}
const settle = () => new Promise((r) => setTimeout(r, 50))
const names = (e: Editor) => (e.getJSON().content ?? []).map((n) => n.type)
const frag = (d: Y.Doc) => d.getXmlFragment('default').toString()
const seed = (e: Editor) =>
  e.commands.setContent({
    type: 'doc',
    content: [
      { type: 'paragraph', content: [{ type: 'text', text: 'before' }] },
      {
        type: 'panel',
        attrs: { kind: 'warning' },
        content: [{ type: 'paragraph', content: [{ type: 'text', text: 'boxed' }] }],
      },
      {
        type: 'paragraph',
        content: [
          { type: 'text', text: 'DONE', marks: [{ type: 'status', attrs: { color: 'green' } }] },
          { type: 'text', text: ' after' },
        ],
      },
    ],
  })

it('web and the gateway share one schema number', () => expect(DOC_SCHEMA).toBe(2))

describe('a client whose schema lacks a node and a mark', () => {
  it('initial sync: B cannot render them, deletes them from the shared document, and A loses them too', async () => {
    const da = new Y.Doc()
    const db = new Y.Doc()
    const net = link(da, db)
    const a = mount(da, [Panel, Status])
    seed(a)
    await settle()
    expect(frag(da)).toContain('<panel kind="warning">')
    expect(frag(da)).toContain('<status color="green">DONE</status> after')

    const b = mount(db, []) // B opens the page and syncs A's state
    net.rejoin()
    await settle()
    expect(names(b)).toEqual(['paragraph', 'paragraph'])
    // The whole panel and the whole paragraph that held the status mark, text included (" after" too), are gone
    // from B's document and, because the deletion is an ordinary Yjs update, from A's.
    for (const d of [da, db]) {
      expect(frag(d)).toBe('<paragraph>before</paragraph><paragraph></paragraph>')
    }
    expect(names(a)).toEqual(['paragraph', 'paragraph'])
    a.destroy()
    b.destroy()
  })

  it('local edit in B: the deletion happens when B renders, not when it types; it syncs at the next connection', async () => {
    const da = new Y.Doc()
    const db = new Y.Doc()
    const a = mount(da, [Panel, Status])
    seed(a)
    await settle()
    Y.applyUpdate(db, Y.encodeStateAsUpdate(da), 'relay') // delivered while B has no editor yet, and no live link
    const b = mount(db, [])
    await settle()
    expect(frag(db)).not.toContain('panel') // already deleted locally, before B typed anything
    expect(frag(da)).toContain('<panel') // A is untouched until B's update reaches it

    b.commands.focus('start')
    b.commands.insertContent('typed in B ')
    await settle()
    expect(frag(db)).toContain('typed in B before')
    expect(frag(da)).toContain('<panel') // still not connected

    link(da, db).rejoin()
    await settle()
    expect(frag(da)).not.toContain('panel')
    expect(frag(da)).not.toContain('DONE')
    expect(frag(da)).toContain('typed in B before') // the ordinary edit is kept
    a.destroy()
    b.destroy()
  })

  it("reconnect: B comes back from offline and A's new blocks, and edits inside them, are removed", async () => {
    const da = new Y.Doc()
    const db = new Y.Doc()
    const net = link(da, db)
    const a = mount(da, [Panel, Status])
    const b = mount(db, [])
    a.commands.setContent('<p>start</p>')
    await settle()
    net.cut() // B's socket drops
    seed(a)
    await settle()
    b.commands.focus('end')
    b.commands.insertContent(' b-offline')
    await settle()
    expect(frag(da)).toContain('<panel')

    net.rejoin()
    await settle()
    expect(frag(da)).not.toContain('panel')
    expect(frag(da)).not.toContain('boxed') // A's text inside the panel went with it
    expect(frag(da)).toContain('b-offline') // B's own offline edit survives
    expect(frag(db)).toBe(frag(da))
    a.destroy()
    b.destroy()
  })
})
