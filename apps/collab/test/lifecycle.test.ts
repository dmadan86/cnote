// Docs A2 page lifecycle against the gateway (docs-parity 7.9 and 7.10.4): the page goes to the trash while people are
// editing it. The API under test (API_URL) folds through COLLAB_URL, a separate gateway process: the in-process gateway
// below only has to notice the delete (recheck) and the fence (POST /internal/fold on itself).
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { append, cmdOk, fixture, gateway, noteRow, open, pool, sleep, sql, text } from './helpers.ts'

const SECRET = process.env.COLLAB_INTERNAL_SECRET ?? ''
const gateways: { close(): Promise<void> }[] = []
async function gw(debounce: number, recheck = 10_000) {
  const g = await gateway(debounce, recheck, { internalSecret: SECRET })
  gateways.push(g)
  return g
}
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

const fold = (port: number, f: { ws: string; note: string }, fence: boolean, secret = SECRET) =>
  fetch(`http://127.0.0.1:${port}/internal/fold`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-collab-secret': secret },
    body: JSON.stringify({ note_id: f.note, workspace_id: f.ws, fence }),
  })

describe('page lifecycle through the gateway', () => {
  it('delete with two peers: the last edit is saved, both sockets close with 4411, restore shows the edit', async () => {
    const f = await fixture()
    const g = await gw(60_000, 200) // no snapshot: the edit stays in the log until the API folds it
    const a = new Y.Doc()
    const b = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const cb = await open(g.port, f, { cookie: f.owner.cookie })
    await cb.sync(b)
    ca.send(2, append(a, ' and more'))
    await cb.recv()
    expect(
      (await sql(f, 'select count(*)::int as n from core.note_updates where note_id = $1', [f.note]))[0],
    ).toEqual({ n: 1 })

    const del = await cmdOk(f.ws, f.owner, 'note.delete', { id: f.note })
    expect(del).toMatchObject({ deleted: true, count: 1 })
    expect((await noteRow(f)).md).toBe('hello world and more')
    expect(await ca.closeCode(3000)).toBe(4411)
    expect(await cb.closeCode(3000)).toBe(4411)

    // reconnecting to a deleted page is refused with the same final code
    const again = await open(g.port, f, { cookie: f.owner.cookie })
    expect(await again.closeCode(2000)).toBe(4411)

    // restore: the epoch moves, the room built afterwards has the edit
    await cmdOk(f.ws, f.owner, 'note.undelete', { deletion_id: del.deletion_id })
    expect((await noteRow(f)).epoch).toBe(2)
    const c = new Y.Doc()
    const cc = await open(g.port, f, { cookie: f.owner.cookie })
    await cc.sync(c)
    expect(text(c)).toContain('hello world and more')
    cc.close()
  })

  it('a fold fences the page: later frames are refused with the retry code, and the fold works with no room', async () => {
    const f = await fixture()
    const g = await gw(60_000)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    ca.send(2, append(a, ' one'))
    await sleep(300)
    expect((await fold(g.port, f, false)).status).toBe(200) // no fence: editing goes on
    expect((await noteRow(f)).md).toBe('hello world one')
    ca.send(2, append(a, ' two'))
    await sleep(300)

    const r = await fold(g.port, f, true)
    expect(await r.json()).toMatchObject({ ok: true, folded: 1 })
    expect((await noteRow(f)).md).toBe('hello world one two')
    ca.send(2, append(a, ' three')) // fenced: refused with the retry code, never logged
    expect(await ca.closeCode(3000)).toBe(4412)
    expect(
      (await sql(f, 'select count(*)::int as n from core.note_updates where note_id = $1', [f.note]))[0],
    ).toEqual({ n: 0 })

    // wrong or missing secret
    expect((await fold(g.port, f, false, 'nope')).status).toBe(401)
    // a page nobody has open (a gateway that crashed left its edit in the log): folded without a room
    const h = await fixture()
    const g2 = await gw(60_000)
    const doc = new Y.Doc()
    const s = await open(g2.port, h, { cookie: h.owner.cookie })
    await s.sync(doc)
    s.send(2, append(doc, ' crashed'))
    await sleep(300)
    expect((await noteRow(h)).md).toBe('hello world') // not snapshotted: the edit is only in the log
    await g2.abandon() // crash (the dying sockets may still flush; the fold below is correct either way)
    const g3 = await gw(60_000) // a restarted gateway with no room for the page
    expect((await fold(g3.port, h, false)).status).toBe(200)
    expect((await noteRow(h)).md).toBe('hello world crashed')
    expect((await fold(g3.port, h, false)).status).toBe(200) // idempotent
  })

  it('a fenced room closes with 4411 within the poll interval once the delete commits', async () => {
    const f = await fixture()
    const g = await gw(200, 200)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    ca.send(2, append(a, ' x'))
    await sleep(600)
    expect((await fold(g.port, f, true)).status).toBe(200)
    await cmdOk(f.ws, f.owner, 'note.delete', { id: f.note })
    expect(await ca.closeCode(3000)).toBe(4411)
  })

  it('frames already queued when the fence is requested are saved, not dropped', async () => {
    const f = await fixture()
    const g = await gw(60_000)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    for (let i = 0; i < 40; i++) ca.send(2, append(a, ` e${i}`))
    expect((await fold(g.port, f, true)).status).toBe(200)
    const md = (await noteRow(f)).md
    for (let i = 0; i < 40; i++) expect(md).toContain(` e${i}`)
    expect(
      (await sql(f, 'select count(*)::int as n from core.note_updates where note_id = $1', [f.note]))[0],
    ).toEqual({ n: 0 })
  })

  it('an edit during the fence is refused, kept by the client, and saved after reconnect once the delete fails', async () => {
    const f = await fixture()
    const g = await gw(60_000)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    expect((await fold(g.port, f, true)).status).toBe(200)
    ca.send(2, append(a, ' kept')) // the client keeps this edit in its own doc
    expect(await ca.closeCode(3000)).toBe(4412)
    expect((await noteRow(f)).md).toBe('hello world')
    // the delete fails: the fence expires (simulated), the client reconnects and the resync sends what the server lacks
    await sql(f, 'update core.notes set fenced_until = null where id = $1', [f.note])
    const again = await open(g.port, f, { cookie: f.owner.cookie })
    await again.sync(a)
    await sleep(300)
    expect((await fold(g.port, f, false)).status).toBe(200)
    expect((await noteRow(f)).md).toBe('hello world kept')
  })

  it('the fence is a database fact: a fold through one gateway instance fences a room held by another', async () => {
    const f = await fixture()
    const holder = await gw(60_000)
    const other = await gw(60_000)
    const a = new Y.Doc()
    const ca = await open(holder.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    expect((await fold(other.port, f, true)).status).toBe(200)
    ca.send(2, append(a, ' lost?'))
    expect(await ca.closeCode(3000)).toBe(4412)
    expect((await noteRow(f)).md).toBe('hello world')
  })

  it('a page with an open room and no pending edits: an edit racing the delete is saved or refused, never lost', async () => {
    const f = await fixture()
    const g = await gw(60_000, 200)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a) // an open room, nothing in the log
    const sent: string[] = []
    let stop = false
    const pump = (async () => {
      for (let i = 0; !stop && ca.closed === null; i++) {
        sent.push(` r${i}`)
        ca.send(2, append(a, ` r${i}`))
        await sleep(3)
      }
    })()
    const del = await cmdOk(f.ws, f.owner, 'note.delete', { id: f.note }) // folds with the fence through COLLAB_URL
    stop = true
    await pump
    expect(del).toMatchObject({ deleted: true })
    expect([4411, 4412]).toContain(await ca.closeCode(3000))
    expect(
      (await sql(f, 'select count(*)::int as n from core.note_updates where note_id = $1', [f.note]))[0],
    ).toEqual({ n: 0 }) // nothing accepted and stranded: every logged edit was folded into the deleted page
  })
})
