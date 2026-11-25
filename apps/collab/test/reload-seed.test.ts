// A page has no snapshot until the gateway's debounced fold. The gateway pins the seed it built from content_md before
// the first logged update, so the log always replays onto the text it was typed on, however the page is reloaded.
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { CLOSE_RETRY } from '../src/doc-schema.ts'
import { append, cmdOk, fixture, gateway, noteRow, open, pool, sleep, text } from './helpers.ts'

const gateways: { close(): Promise<void> }[] = []
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

async function reload(g: { abandon(): Promise<void> }, f: Parameters<typeof open>[1]) {
  await g.abandon() // a crash: nothing folded, only the log has the edits
  const g2 = await gateway(60_000)
  gateways.push(g2)
  const doc = new Y.Doc()
  const s = await open(g2.port, f, { cookie: (f as { owner: { cookie: string } }).owner.cookie })
  await s.sync(doc)
  s.close()
  return text(doc)
}

describe('seed pinned before the first logged update', () => {
  it('keeps every edit once with a long log (over 300 updates)', async () => {
    const f = await fixture()
    const g = await gateway(60_000)
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    for (let i = 0; i < 350; i++) ca.send(2, append(a, 'x'))
    await sleep(2500)
    expect((await noteRow(f)).has_ydoc).toBe(true)
    expect(await reload(g, f)).toBe(`<paragraph>hello world${'x'.repeat(350)}</paragraph>`)
  })

  it('an API overwrite equal to an earlier logged state still wins', async () => {
    const f = await fixture()
    const g = await gateway(60_000)
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    ca.send(2, append(a, ' a'))
    ca.send(2, append(a, ' b'))
    await sleep(500)
    await cmdOk(f.ws, f.owner, 'note.update', {
      id: f.note,
      content_md: 'hello world a',
      schema: 2,
      expected_epoch: 1,
    })
    expect(await reload(g, f)).toBe('<paragraph>hello world a</paragraph>')
  })

  it('a server write between room load and the first update is not overwritten by a stale pin', async () => {
    const f = await fixture()
    const g = await gateway(60_000)
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    await cmdOk(f.ws, f.owner, 'note.update', {
      id: f.note,
      content_md: 'written elsewhere',
      schema: 2,
      expected_epoch: 1,
    })
    ca.send(2, append(a, ' late'))
    expect(await ca.closeCode(3000)).toBe(CLOSE_RETRY) // the stale room is dropped, the client resends on reconnect
    expect(await noteRow(f)).toMatchObject({ md: 'written elsewhere', has_ydoc: false })
    const b = new Y.Doc()
    const cb = await open(g.port, f, { cookie: f.owner.cookie })
    await cb.sync(b)
    expect(text(b)).toBe('<paragraph>written elsewhere</paragraph>')
    cb.close()
  })

  it('a stale seed drops the whole room: both peers reconnect, the refused edit is never persisted, the server write is kept', async () => {
    const f = await fixture()
    const g = await gateway(200)
    gateways.push(g)
    const a = new Y.Doc()
    const b = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    const cb = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    await cb.sync(b)
    await cmdOk(f.ws, f.owner, 'note.update', {
      id: f.note,
      content_md: 'written elsewhere',
      schema: 2,
      expected_epoch: 1,
    })
    ca.send(2, append(a, ' late'))
    expect(await ca.closeCode(3000)).toBe(CLOSE_RETRY)
    expect(await cb.closeCode(3000)).toBe(CLOSE_RETRY)
    await sleep(800) // longer than the fold debounce: a room that kept the refused edit would persist it now
    expect(await noteRow(f)).toMatchObject({ md: 'written elsewhere', has_ydoc: false })
    expect(g.rooms.size).toBe(0)

    const a2 = new Y.Doc()
    const b2 = new Y.Doc()
    const ca2 = await open(g.port, f, { cookie: f.owner.cookie })
    const cb2 = await open(g.port, f, { cookie: f.owner.cookie })
    await ca2.sync(a2)
    await cb2.sync(b2)
    expect(text(a2)).toBe('<paragraph>written elsewhere</paragraph>')
    expect(text(b2)).toBe('<paragraph>written elsewhere</paragraph>')
    ca2.send(2, append(a2, '!'))
    await sleep(800)
    expect((await noteRow(f)).md).toBe('written elsewhere!')
    ca2.close()
    cb2.close()
  })
})
