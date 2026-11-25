// docs-parity 7.3: the rollout fence. Same setup as collab.test.ts (real Postgres, real Go API).
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { CLOSE_RELOAD, DOC_SCHEMA } from '../src/doc-schema.ts'
import { append, cmd, fixture, gateway, noteRow, open, pool, sleep, text } from './helpers.ts'

const gateways: { close(): Promise<void> }[] = []
async function gw(minSchema?: number) {
  const g = await gateway(300, 10_000, { minSchema, internalSecret: 'fence-test-secret' })
  gateways.push(g)
  return g
}
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

describe('schema fence', () => {
  it('the shared schema number is 2', () => expect(DOC_SCHEMA).toBe(2))

  it('with no minimum every tab is admitted, with or without a schema parameter', async () => {
    const f = await fixture()
    const g = await gw()
    for (const query of [undefined, 'schema=2']) {
      const c = await open(g.port, f, { cookie: f.owner.cookie, query })
      await c.sync(new Y.Doc())
      c.close()
    }
  })

  it('refuses connections below the minimum with the reload code; a missing parameter counts as 0', async () => {
    const f = await fixture()
    const g = await gw(2)
    for (const query of [undefined, 'schema=0', 'schema=1', 'schema=junk']) {
      const c = await open(g.port, f, { cookie: f.owner.cookie, query })
      expect(await c.closeCode(), `schema ${query}`).toBe(CLOSE_RELOAD)
    }
    const ok = await open(g.port, f, { cookie: f.owner.cookie, query: 'schema=2' })
    await ok.sync(new Y.Doc())
    ok.close()
  })

  it('raising the minimum closes an old tab that was connected, and its late edit never lands', async () => {
    const f = await fixture()
    const g = await gw()
    const oldDoc = new Y.Doc()
    const old = await open(g.port, f, { cookie: f.owner.cookie }) // an old tab: no schema parameter
    await old.sync(oldDoc)
    const fresh = await open(g.port, f, { cookie: f.owner.cookie, query: 'schema=2' })
    const freshDoc = new Y.Doc()
    await fresh.sync(freshDoc)

    old.send(2, append(oldDoc, ', before')) // still allowed before the raise
    expect((await fresh.recv())?.kind).toBe('update')

    g.setMinSchema(2)
    expect(await old.closeCode()).toBe(CLOSE_RELOAD)
    const late = append(oldDoc, ' FLATTENED')
    try {
      old.send(2, late)
    } catch {} // socket already closed
    await sleep(1200)
    expect(await fresh.recv(300)).toBeNull() // nothing from the closed tab reaches the others
    expect(fresh.closed).toBeNull() // a tab at the minimum stays
    expect(text(freshDoc)).not.toContain('FLATTENED')
    expect((await noteRow(f)).md).toBe('hello world, before')
    fresh.close()
  })

  it('an old tab that keeps sending through a raise has no update applied once the raise begins', async () => {
    const f = await fixture()
    const g = await gw()
    const oldDoc = new Y.Doc()
    const old = await open(g.port, f, { cookie: f.owner.cookie })
    await old.sync(oldDoc)
    const fresh = await open(g.port, f, { cookie: f.owner.cookie, query: 'schema=2' })
    await fresh.sync(new Y.Doc())
    let stop = false
    const spam = (async () => {
      for (let i = 0; !stop; i++) {
        try {
          old.send(2, append(oldDoc, `.${i}`))
        } catch {}
        if (i % 4 === 0) await sleep(1)
      }
    })()
    await sleep(100)
    const room = [...g.rooms.values()][0]
    const raise = g.setMinSchema(2)
    const at = text(room.doc) // frames queued before the raise may not land after it
    await raise
    const settled = text(room.doc)
    expect(settled).toBe(at)
    expect(
      ((await (await fetch(`http://127.0.0.1:${g.port}/collab/min-schema`)).json()) as { min: number }).min,
    ).toBe(2)
    await sleep(500)
    stop = true
    await spam
    expect(text(room.doc)).toBe(at)
    expect(await old.closeCode()).toBe(CLOSE_RELOAD)
    fresh.close()
  })

  it('the minimum only goes up, and the internal endpoint needs the secret', async () => {
    const f = await fixture()
    const g = await gw()
    const url = `http://127.0.0.1:${g.port}/internal/min-schema`
    const post = (secret: string | null, min: number) =>
      fetch(url, {
        method: 'POST',
        headers: secret ? { 'x-collab-secret': secret } : {},
        body: JSON.stringify({ min }),
      })
    expect((await post(null, 2)).status).toBe(401)
    expect((await post('wrong', 2)).status).toBe(401)
    const old = await open(g.port, f, { cookie: f.owner.cookie })
    await old.sync(new Y.Doc())
    expect((await post('fence-test-secret', 2)).status).toBe(200)
    expect(await old.closeCode()).toBe(CLOSE_RELOAD)
    expect(((await (await post('fence-test-secret', 1)).json()) as { min: number }).min).toBe(2)
    const again = await open(g.port, f, { cookie: f.owner.cookie })
    expect(await again.closeCode()).toBe(CLOSE_RELOAD)
  })
})

describe('note.update body saves (needs the Go API)', () => {
  it('old clients get 426, a missing expected_epoch is refused, current clients save; title saves are not fenced', async () => {
    const f = await fixture()
    const save = (extra: object) =>
      cmd(f.ws, f.owner, 'note.update', { id: f.note, content_md: 'saved', ...extra })
    const old = await save({ expected_epoch: 1 })
    expect(old.status).toBe(426)
    expect(old.body.error.code).toBe('upgrade_required')
    expect((await save({ expected_epoch: 1, schema: 1 })).status).toBe(426)
    expect((await save({ schema: DOC_SCHEMA })).status).toBe(400)
    expect((await save({ schema: DOC_SCHEMA, expected_epoch: 9 })).status).toBe(409)
    expect((await noteRow(f)).md).toBe('hello world')
    expect((await save({ schema: DOC_SCHEMA, expected_epoch: 1 })).status).toBe(200)
    expect((await cmd(f.ws, f.owner, 'note.update', { id: f.note, title: 'Renamed' })).status).toBe(200)
  })
})
