// docs-parity C1 5.5: find and replace as editor transactions. The plain functions of editor/find-replace.ts are exercised on real
// editors, alone and as two collaborators over Yjs.
import Collaboration from '@tiptap/extension-collaboration'
import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { blockExtensions } from '../components/editor/blocks'
import {
  FindReplace,
  findMatches,
  getFind,
  NO_FIND,
  replaceAll,
  replaceCurrent,
  setFind,
  stepFind,
} from '../components/editor/find-replace'

const run = (text: string, marks: { type: string; attrs?: object }[] = []) => ({ type: 'text', text, marks })
const para = (...c: object[]) => ({ type: 'paragraph', content: c })
const doc = (...c: object[]) => ({ type: 'doc', content: c })
const code = (t: string) => ({ type: 'codeBlock', content: [{ type: 'text', text: t }] })

function mk(content: object, extra: unknown[] = [], editable = true) {
  return new Editor({
    element: document.createElement('div'),
    editable,
    extensions: [
      StarterKit.configure({ undoRedo: extra.length ? false : undefined }),
      ...blockExtensions,
      FindReplace,
      ...(extra as never[]),
    ],
    content,
  })
}
const text = (e: Editor) => e.state.doc.textContent
const find = (e: Editor, q: string, o: Partial<typeof NO_FIND> = {}) =>
  setFind(e.view, { ...NO_FIND, query: q, ...o })

describe('finding', () => {
  it('finds across marks, ignores case by default, and respects match case and whole word', () => {
    const e = mk(doc(para(run('The '), run('cat', [{ type: 'bold' }]), run('s and a Cat, concatenate'))))
    find(e, 'cats')
    expect(getFind(e.view).matches).toHaveLength(1) // "cat" (bold) + "s" crosses two text nodes
    find(e, 'cat')
    expect(getFind(e.view).matches).toHaveLength(3)
    find(e, 'cat', { matchCase: true })
    expect(getFind(e.view).matches).toHaveLength(2)
    find(e, 'cat', { wholeWord: true })
    expect(getFind(e.view).matches).toHaveLength(1)
    find(e, '')
    expect(getFind(e.view).matches).toHaveLength(0)
    e.destroy()
  })

  it('treats the query literally, not as a regular expression', () => {
    const e = mk(doc(para(run('a.c abc (x)'))))
    find(e, 'a.c')
    expect(getFind(e.view).matches).toHaveLength(1)
    find(e, '(x)')
    expect(getFind(e.view).matches).toHaveLength(1)
    e.destroy()
  })

  it('skips code blocks and inline code unless the user chooses them', () => {
    const e = mk(doc(para(run('foo '), run('foo', [{ type: 'code' }])), code('foo foo')))
    find(e, 'foo')
    expect(getFind(e.view).matches).toHaveLength(1)
    find(e, 'foo', { includeCode: true })
    expect(getFind(e.view).matches).toHaveLength(4)
    e.destroy()
  })

  it('does not match across a block boundary or an atom', () => {
    const e = mk(
      doc(para(run('ab')), para(run('cd')), {
        type: 'paragraph',
        content: [run('x'), { type: 'hardBreak' }, run('y')],
      }),
    )
    find(e, 'bc')
    expect(getFind(e.view).matches).toHaveLength(0)
    find(e, 'xy')
    expect(getFind(e.view).matches).toHaveLength(0)
    expect(findMatches(e.state.doc, { ...NO_FIND, query: 'x' })).toHaveLength(1)
    e.destroy()
  })

  it('steps through the matches and wraps', () => {
    const e = mk(doc(para(run('a b a b a'))))
    find(e, 'a')
    expect(getFind(e.view).current).toBe(0)
    stepFind(e.view, -1)
    expect(getFind(e.view).current).toBe(2)
    stepFind(e.view, 1)
    expect(getFind(e.view).current).toBe(0)
    e.destroy()
  })
})

describe('replacing', () => {
  it('keeps the marks of the replaced text', () => {
    const e = mk(doc(para(run('say '), run('hello', [{ type: 'bold' }]), run(' now'))))
    find(e, 'hello')
    expect(replaceCurrent(e.view, 'bye')).toBe(true)
    expect(e.getHTML()).toBe('<p>say <strong>bye</strong> now</p>')
    e.destroy()
  })

  it('replaces all in one transaction, which is one undo step', () => {
    const e = mk(doc(para(run('x y x')), para(run('x'))))
    let changing = 0
    e.on('transaction', ({ transaction }) => {
      if (transaction.docChanged) changing++
    })
    find(e, 'x')
    expect(replaceAll(e.view, 'zz')).toBe(3)
    expect(changing).toBe(1)
    expect(text(e)).toBe('zz y zzzz')
    e.commands.undo()
    expect(text(e)).toBe('x y xx')
    e.destroy()
  })

  it('does not find the replacement again when it contains the query', () => {
    const e = mk(doc(para(run('a a'))))
    find(e, 'a')
    replaceCurrent(e.view, 'aa')
    expect(text(e)).toBe('aa a')
    expect(getFind(e.view).current).toBe(2) // the next original match, not the one just written
    e.destroy()
  })

  it('finds text in a date chip but never replaces it', () => {
    const e = mk(
      doc(
        para(
          run('due '),
          run('2026-12-01', [{ type: 'date', attrs: { datetime: '2026-12-01' } }]),
          run(' and 2026-12-01'),
        ),
      ),
    )
    find(e, '2026-12-01')
    expect(getFind(e.view).matches.map((m) => m.locked)).toEqual([true, false])
    expect(replaceAll(e.view, 'later')).toBe(1)
    expect(text(e)).toBe('due 2026-12-01 and later')
    e.destroy()
  })

  it('replaces nothing when the editor is read-only, but still finds', () => {
    const e = mk(doc(para(run('one one'))), [], false)
    find(e, 'one')
    expect(getFind(e.view).matches).toHaveLength(2)
    expect(replaceAll(e.view, 'two')).toBe(0)
    expect(replaceCurrent(e.view, 'two')).toBe(false)
    expect(text(e)).toBe('one one')
    e.destroy()
  })

  it('never replaces inside a code block unless asked', () => {
    const e = mk(doc(para(run('foo')), code('foo')))
    find(e, 'foo')
    replaceAll(e.view, 'bar')
    expect(e.state.doc.child(1).textContent).toBe('foo')
    find(e, 'foo', { includeCode: true })
    replaceAll(e.view, 'bar')
    expect(e.state.doc.child(1).textContent).toBe('bar')
    e.destroy()
  })
})

// ---- two collaborators ----

const settle = () => new Promise((r) => setTimeout(r, 20))
function pair() {
  const da = new Y.Doc()
  const db = new Y.Doc()
  da.on('update', (u: Uint8Array, o: unknown) => o !== 'relay' && Y.applyUpdate(db, u, 'relay'))
  db.on('update', (u: Uint8Array, o: unknown) => o !== 'relay' && Y.applyUpdate(da, u, 'relay'))
  const a = mk(doc(para()), [Collaboration.configure({ document: da })])
  a.commands.setContent(doc(para(run('red car, red bus'))))
  const b = mk(doc(para()), [Collaboration.configure({ document: db })])
  return { a, b, da, db }
}

describe('two collaborators', () => {
  it('a replace all by one is seen by the other, and so is the undo', async () => {
    const { a, b } = pair()
    await settle()
    expect(text(b)).toBe('red car, red bus')
    await new Promise((r) => setTimeout(r, 600)) // past the undo manager's capture window, so undo only reverts the replace
    find(a, 'red')
    find(b, 'red') // the other tab has its own find open
    expect(getFind(b.view).matches).toHaveLength(2)
    expect(replaceAll(a.view, 'blue')).toBe(2)
    await settle()
    expect(text(b)).toBe('blue car, blue bus')
    expect(getFind(b.view).matches).toHaveLength(0) // B's matches follow the document
    a.commands.undo()
    await settle()
    expect(text(b)).toBe('red car, red bus')
    expect(getFind(b.view).matches).toHaveLength(2)
    a.destroy()
    b.destroy()
  })

  it('a single replace by one is seen by the other', async () => {
    const { a, b } = pair()
    await settle()
    find(a, 'bus')
    expect(replaceCurrent(a.view, 'tram')).toBe(true)
    await settle()
    expect(text(b)).toBe('red car, red tram')
    a.destroy()
    b.destroy()
  })

  it('a read-only collaborator finds but cannot replace', async () => {
    const { a, b } = pair()
    await settle()
    b.setEditable(false)
    find(b, 'red')
    expect(getFind(b.view).matches).toHaveLength(2)
    expect(replaceAll(b.view, 'x')).toBe(0)
    await settle()
    expect(text(a)).toBe('red car, red bus')
    a.destroy()
    b.destroy()
  })
})
