// POST /collab/{ws}/{note}/update: the page is closing with an edit its socket may not have delivered.
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { append, cmdOk, fixture, gateway, noteRow, open, pool, sleep, sql, text } from './helpers.ts'

const gateways: { close(): Promise<void> }[] = []
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

const post = (port: number, f: { ws: string; note: string }, body: string, cookie?: string, q = '') =>
  fetch(`http://127.0.0.1:${port}/collab/${f.ws}/${f.note}/update${q}`, {
    method: 'POST',
    headers: { 'content-type': 'text/plain', ...(cookie ? { cookie: `cn_session=${cookie}` } : {}) },
    body,
  })
const b64 = (u: Uint8Array) => Buffer.from(u).toString('base64')

describe('close beacon', () => {
  it('logs an edit the socket never delivered, once, however often it is resent', async () => {
    const f = await fixture()
    const g = await gateway(200)
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const u = append(a, ' typed') // never sent on the socket: the frame was lost
    expect((await post(g.port, f, b64(u), f.owner.cookie)).status).toBe(204)
    expect((await post(g.port, f, b64(u), f.owner.cookie)).status).toBe(204)
    ca.close()
    await sleep(800)
    expect((await noteRow(f)).md).toBe('hello world typed')
    const b = new Y.Doc()
    const cb = await open(g.port, f, { cookie: f.owner.cookie })
    await cb.sync(b)
    expect(text(b)).toBe('<paragraph>hello world typed</paragraph>')
    cb.close()
  })

  it('an edit the socket did deliver is not applied twice', async () => {
    const f = await fixture()
    const g = await gateway(200)
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const u = append(a, ' once')
    ca.send(2, u)
    await sleep(200)
    expect((await post(g.port, f, b64(u), f.owner.cookie)).status).toBe(204)
    ca.close()
    await sleep(800)
    expect((await noteRow(f)).md).toBe('hello world once')
    const [{ n }] = await sql<{ n: number }>(
      f,
      'select count(*)::int as n from core.note_updates where note_id = $1',
      [f.note],
    )
    expect(n).toBe(0) // folded; and nothing was logged twice before
  })

  it("works with no room open on an empty page; a page with text and no snapshot skips it (the row has the tab's save)", async () => {
    const f = await fixture()
    await cmdOk(f.ws, f.owner, 'note.update', { id: f.note, content_md: '', schema: 2, expected_epoch: 1 })
    const g = await gateway(200)
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const before = Y.encodeStateVector(a)
    const para = new Y.XmlElement('paragraph')
    a.getXmlFragment('default').insert(0, [para])
    para.insert(0, [new Y.XmlText('late')])
    const u = Y.encodeStateAsUpdate(a, before)
    ca.close()
    await sleep(500)
    expect(g.rooms.size).toBe(0)
    expect((await post(g.port, f, b64(u), f.owner.cookie)).status).toBe(204)
    await sleep(800)
    expect(g.rooms.size).toBe(0) // released again
    expect((await noteRow(f)).md).toBe('late')

    const h = await fixture() // text, no snapshot, room settled: nothing to build on safely
    const d = new Y.Doc()
    const cd = await open(g.port, h, { cookie: h.owner.cookie })
    await cd.sync(d)
    const v = append(d, ' more')
    cd.close()
    await sleep(500)
    expect((await post(g.port, h, b64(v), h.owner.cookie)).status).toBe(204)
    await sleep(800)
    expect((await noteRow(h)).md).toBe('hello world')
  })

  it('applies the same checks as a socket: session, schema fence, epoch, archive, access, size', async () => {
    const f = await fixture()
    const g = await gateway(200)
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const u = b64(append(a, ' x'))
    expect((await post(g.port, f, u)).status).toBe(401) // no session
    expect((await post(g.port, f, u, 'nope')).status).toBe(401)
    expect((await post(g.port, f, u, f.owner.cookie, '?epoch=7')).status).toBe(409) // wrong epoch
    expect((await post(g.port, f, 'A'.repeat(200 * 1024), f.owner.cookie)).status).toBe(413)
    expect((await post(g.port, f, '', f.owner.cookie)).status).toBe(400)
    expect((await noteRow(f)).md).toBe('hello world')
    await cmdOk(f.ws, f.owner, 'doc.archive', { id: f.note })
    expect((await post(g.port, f, u, f.owner.cookie)).status).toBe(409)
    await cmdOk(f.ws, f.owner, 'doc.restore', { note_id: f.note })
    await g.setMinSchema(5)
    expect((await post(g.port, f, u, f.owner.cookie, '?schema=2')).status).toBe(426) // old editor
    ca.close()
  })
})
