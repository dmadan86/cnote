// B2 lead decision: an archived page is read-only until restored. The gateway refuses admission and closes open sockets.
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { CLOSE_ARCHIVED } from '../src/doc-schema.ts'
import { append, cmdOk, fixture, gateway, noteRow, open, pool, sleep, sql } from './helpers.ts'

const gateways: { close(): Promise<void> }[] = []
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

describe('archived pages through the gateway', () => {
  it('a live socket closes with 4413 when the page is archived; its later edit is not logged; reconnects are refused; restore reopens', async () => {
    const f = await fixture()
    const g = await gateway(200, 200)
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)

    await cmdOk(f.ws, f.owner, 'doc.archive', { id: f.note })
    expect(await ca.closeCode(3000)).toBe(CLOSE_ARCHIVED)
    expect((await noteRow(f)).md).toBe('hello world')

    const again = await open(g.port, f, { cookie: f.owner.cookie })
    expect(await again.closeCode(2000)).toBe(CLOSE_ARCHIVED)

    await cmdOk(f.ws, f.owner, 'doc.restore', { note_id: f.note })
    const b = new Y.Doc()
    const cb = await open(g.port, f, { cookie: f.owner.cookie })
    await cb.sync(b)
    cb.send(2, append(b, ' back'))
    await sleep(600)
    expect((await noteRow(f)).md).toBe('hello world back')
    cb.close()
  })

  it('an edit sent between the archive and the recheck is refused, not logged', async () => {
    const f = await fixture()
    const g = await gateway(60_000, 60_000) // no recheck in time: only the write path can refuse
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    await cmdOk(f.ws, f.owner, 'doc.archive', { id: f.note })
    ca.send(2, append(a, ' sneaky'))
    expect(await ca.closeCode(3000)).toBe(CLOSE_ARCHIVED)
    expect(
      (await sql(f, 'select count(*)::int as n from core.note_updates where note_id = $1', [f.note]))[0],
    ).toEqual({ n: 0 })
  })
})
