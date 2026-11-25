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
    const stale = g.rooms.get(`${f.ws}/${f.note}`)
    expect(stale).toBeDefined()
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
    expect(g.rooms.size).toBe(0) // the failed room is out of the map

    const a2 = new Y.Doc()
    const b2 = new Y.Doc()
    const ca2 = await open(g.port, f, { cookie: f.owner.cookie })
    const cb2 = await open(g.port, f, { cookie: f.owner.cookie })
    await ca2.sync(a2)
    await cb2.sync(b2)
    const fresh = g.rooms.get(`${f.ws}/${f.note}`)
    expect(fresh).toBeDefined()
    expect(fresh).not.toBe(stale) // never the room whose pin failed
    expect(text(a2)).toBe('<paragraph>written elsewhere</paragraph>')
    expect(text(b2)).toBe('<paragraph>written elsewhere</paragraph>')
    ca2.send(2, append(a2, '!'))
    await sleep(800)
    expect((await noteRow(f)).md).toBe('written elsewhere!')
    ca2.close()
    cb2.close()
  })

  it("another peer's edits made after this tab's last save: kept while anyone stays connected, dropped once the API text wins over an unfolded log", async () => {
    const f = await fixture()
    const g = await gateway(60_000)
    gateways.push(g)
    const a = new Y.Doc()
    const b = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    const cb = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    await cb.sync(b)
    ca.send(2, append(a, ' a'))
    await sleep(200)
    cb.send(2, append(b, ' b')) // peer B edits after A's last sync of its own text
    await sleep(300)
    // A's close save writes what A saw ("hello world a"); B is still connected, so the live room keeps both.
    await cmdOk(f.ws, f.owner, 'note.update', {
      id: f.note,
      content_md: 'hello world a',
      schema: 2,
      expected_epoch: 1,
    })
    ca.close()
    await sleep(300)
    const c = new Y.Doc()
    const cc = await open(g.port, f, { cookie: f.owner.cookie })
    await cc.sync(c)
    expect(text(c)).toMatch(/^<paragraph>hello world (a b|b a)<\/paragraph>$/) // concurrent appends: either order
    cc.close()
    // B leaves too: nobody is connected, the log is unfolded and the API text differs from the snapshot, so the API
    // text wins and B's unfolded edit is dropped. This is the existing buildDoc rule, unchanged.
    cb.close()
    await sleep(300)
    expect(await reload(g, f)).toBe('<paragraph>hello world a</paragraph>')
  })
})
