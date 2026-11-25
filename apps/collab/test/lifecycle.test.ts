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

  it('a fold fences the room: later frames are dropped, new sockets are refused, and the fold works with no room', async () => {
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
    ca.send(2, append(a, ' three')) // the room is closed to writes: this is not logged
    await sleep(300)
    expect(
      (await sql(f, 'select count(*)::int as n from core.note_updates where note_id = $1', [f.note]))[0],
    ).toEqual({ n: 0 })
    const late = await open(g.port, f, { cookie: f.owner.cookie }).then(
      () => 0,
      (e) => e.status,
    )
    expect(late).toBe(409)

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
})
