// Ports the socket scenarios of crates/cnote-collab/tests/collab.rs against the real database and the real
// Rust server (fixtures, note.restore). Needs DATABASE_URL (.env) and Rust on RUST_API_URL (default :4000).
import { afterAll, describe, expect, it } from 'vitest'
import { WebSocket } from 'ws'
import { WebsocketProvider } from 'y-websocket'
import * as Y from 'yjs'
import {
  addMember,
  apiKey,
  append,
  cmd,
  cmdOk,
  fixture,
  gateway,
  noteRow,
  open,
  pool,
  sleep,
  sql,
  text,
} from './helpers.ts'

const gateways: { close(): Promise<void> }[] = []
async function gw(debounce: number, recheck?: number) {
  const g = await gateway(debounce, recheck)
  gateways.push(g)
  return g
}
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

const rejectedWith = async (p: Promise<unknown>) =>
  (await p.then(
    () => null,
    (e) => e.status,
  )) as number | null

describe('collab gateway', () => {
  it('edits flow between clients and persist', async () => {
    const f = await fixture()
    const g = await gw(400)
    const a = new Y.Doc()
    const b = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const key = await apiKey(f, f.owner)
    const cb = await open(g.port, f, { token: key })
    await cb.sync(b)
    expect(text(a)).toContain('hello world')
    expect(text(b)).toContain('hello world')

    ca.send(2, append(a, ', team'))
    const m = await cb.recv()
    expect(m?.kind).toBe('update')
    Y.applyUpdate(b, (m as { body: Uint8Array }).body)
    expect(text(b)).toContain('hello world, team')

    await sleep(1200)
    const row = await noteRow(f)
    expect(row).toMatchObject({ md: 'hello world, team', version: 2, has_ydoc: true })
    const [ev] = await sql<{ changed: unknown; actor_id: string }>(
      f,
      "select payload->'changed' as changed, actor_id from core.events where note_id = $1 and kind = 'note.updated'",
      [f.note],
    )
    expect(ev.changed).toEqual(['content'])
    expect(ev.actor_id).toBe(f.owner.id)
    const [job] = await sql<{ n: string }>(
      f,
      "select count(*) as n from core.jobs where kind = 'event.dispatch' and payload->>'event_id' = (select id::text from core.events where note_id = $1 and kind = 'note.updated' limit 1)",
      [f.note],
    )
    expect(job.n).toBe('1')

    // Last disconnect flushes immediately: edit, then leave before the debounce fires.
    cb.send(2, append(b, '!'))
    await sleep(100)
    ca.close()
    cb.close()
    await sleep(500)
    expect((await noteRow(f)).md).toBe('hello world, team!')

    const c = new Y.Doc()
    const cc = await open(g.port, f, { cookie: f.owner.cookie })
    await cc.sync(c)
    expect(text(c)).toContain('hello world, team!')
    cc.close()
  })

  it('refuses strangers and keeps viewers read-only', async () => {
    const f = await fixture()
    const g = await gw(400)
    expect(await rejectedWith(open(g.port, f))).toBe(401)
    expect(await rejectedWith(open(g.port, f, { token: 'cn_nope_nope' }))).toBe(401)
    expect(await rejectedWith(open(g.port, f, { cookie: 'bogus' }))).toBe(401)

    const other = await addMember(f)
    expect(await rejectedWith(open(g.port, f, { cookie: other.cookie }))).toBe(404)
    await sql(
      f,
      "insert into core.space_members (workspace_id, space_id, user_id, role) values ($1,$2,$3,'viewer')",
      [f.ws, f.space, other.id],
    )

    const a = new Y.Doc()
    const v = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const cv = await open(g.port, f, { cookie: other.cookie })
    await cv.sync(v)
    expect(text(v)).toContain('hello world')

    cv.send(2, append(v, ' HACKED'))
    expect(await ca.recv()).toBeNull()
    await sleep(1000)
    expect(await noteRow(f)).toMatchObject({ md: 'hello world', version: 1, has_ydoc: false })

    ca.send(2, append(a, '!'))
    expect((await cv.recv())?.kind).toBe('update')
    ca.close()
    cv.close()
  })

  it('page restrictions and shares bind websocket writers, and revoked users are dropped', async () => {
    const f = await fixture()
    const g = await gw(400, 500)
    const other = await addMember(f)
    await sql(
      f,
      "insert into core.space_members (workspace_id, space_id, user_id, role) values ($1,$2,$3,'editor')",
      [f.ws, f.space, other.id],
    )
    const run = (name: string, input: object) => cmdOk(f.ws, f.owner, name, { note_id: f.note, ...input })

    await run('note.restrict', { restriction: 'view' })
    expect(await rejectedWith(open(g.port, f, { cookie: other.cookie }))).toBe(404)

    await run('note.share', { user_id: other.id, level: 'view' })
    const a = new Y.Doc()
    const v = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const cv = await open(g.port, f, { cookie: other.cookie })
    await cv.sync(v)
    expect(text(v)).toContain('hello world')
    cv.send(2, append(v, ' HACKED'))
    expect(await ca.recv()).toBeNull()
    await sleep(1000)
    expect(await noteRow(f)).toMatchObject({ md: 'hello world', version: 1, has_ydoc: false })

    await run('note.unshare', { user_id: other.id })
    await run('note.restrict', { restriction: 'edit' })
    const cv2 = await open(g.port, f, { cookie: other.cookie })
    const v2 = new Y.Doc()
    await cv2.sync(v2)
    cv2.send(2, append(v2, ' HACKED'))
    expect(await ca.recv()).toBeNull()

    await run('note.share', { user_id: other.id, level: 'edit' })
    const cv3 = await open(g.port, f, { cookie: other.cookie })
    const v3 = new Y.Doc()
    await cv3.sync(v3)
    cv3.send(2, append(v3, '?'))
    expect((await ca.recv())?.kind).toBe('update')

    await run('note.restrict', { restriction: 'view' })
    await run('note.unshare', { user_id: other.id })
    expect(await cv3.untilClosed(15_000), 'revoked user must be disconnected').toBe(true)
    ca.close()
  })

  it('relayed edits survive a crash before the snapshot', async () => {
    const f = await fixture()
    const first = await gw(3_600_000) // never snapshots on its own: only the update log can save the edit
    const a = new Y.Doc()
    const b = new Y.Doc()
    const ca = await open(first.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const cb = await open(first.port, f, { cookie: f.owner.cookie })
    await cb.sync(b)
    ca.send(2, append(a, ', team'))
    expect((await cb.recv())?.kind).toBe('update')
    const [{ n }] = await sql<{ n: string }>(
      f,
      'select count(*) as n from core.note_updates where note_id = $1',
      [f.note],
    )
    expect(Number(n)).toBeGreaterThanOrEqual(1)
    expect((await noteRow(f)).md).toBe('hello world')

    // "Crash": a new gateway with no rooms in memory serves the page from snapshot + log.
    await first.abandon()
    const second = await gw(300)
    const c = new Y.Doc()
    const cc = await open(second.port, f, { cookie: f.owner.cookie })
    await cc.sync(c)
    expect(text(c)).toContain('hello world, team')
    await sleep(1200)
    expect((await noteRow(f)).md).toBe('hello world, team')
    const [{ n: left }] = await sql<{ n: string }>(
      f,
      'select count(*) as n from core.note_updates where note_id = $1',
      [f.note],
    )
    expect(left).toBe('0')
    for (const s of [ca, cb, cc]) s.close()
  })

  it('restore fences open editors with 4409 and keeps every version', async () => {
    const f = await fixture()
    const g = await gw(400)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    ca.send(2, append(a, ' v2'))
    await sleep(1000) // snapshot + first version
    ca.send(2, append(a, ' v3'))
    await sleep(200) // logged, not yet snapshotted

    const revs = await cmdOk(f.ws, f.owner, 'note.revisions', { note_id: f.note })
    expect(revs).toHaveLength(1)
    const v2 = revs[0].id

    const stale = await cmd(f.ws, f.owner, 'note.restore', {
      note_id: f.note,
      revision_id: v2,
      expected_epoch: 7,
    })
    expect(stale.status).not.toBe(200)
    const r = await cmdOk(f.ws, f.owner, 'note.restore', {
      note_id: f.note,
      revision_id: v2,
      expected_epoch: 1,
    })
    expect(r.collab_epoch).toBe(2)

    ca.send(2, append(a, ' lost'))
    expect(await ca.closeCode()).toBe(4409)
    await sleep(800)
    expect((await noteRow(f)).md).toBe('hello world v2')

    const after = await cmdOk(f.ws, f.owner, 'note.revisions', { note_id: f.note })
    expect(after.map((x: { reason: string }) => x.reason)).toEqual(['restore', 'before_restore', 'edit'])
    const before = await cmdOk(f.ws, f.owner, 'note.revision', { id: after[1].id })
    expect(before.content_md).toBe('hello world v2 v3') // Rust's Markdown of the TS-persisted state
    expect(after[0].restored_from).toBe(v2)

    const staleSave = await cmd(f.ws, f.owner, 'note.update', {
      id: f.note,
      content_md: 'stale',
      expected_epoch: 1,
    })
    expect(staleSave.status).not.toBe(200)
    await expect(
      sql(f, "update core.note_revisions set content_md = 'x' where note_id = $1", [f.note]),
    ).rejects.toThrow()

    const old = await open(g.port, f, { cookie: f.owner.cookie, query: 'epoch=1' })
    expect(await old.closeCode()).toBe(4409)
    const n = new Y.Doc()
    const cn = await open(g.port, f, { cookie: f.owner.cookie, query: 'epoch=2' })
    await cn.sync(n)
    expect(text(n)).toContain('hello world v2')
    expect(text(n)).not.toContain('v3')
    cn.close()
  })

  it('refuses cookie logins from foreign origins unless allowed', async () => {
    const f = await fixture()
    const g = await gw(400)
    expect(
      await rejectedWith(open(g.port, f, { cookie: f.owner.cookie, origin: 'https://evil.example' })),
    ).toBe(403)
    const same = await open(g.port, f, { cookie: f.owner.cookie, origin: `http://127.0.0.1:${g.port}` })
    same.close()
  })

  it('two y-websocket clients converge', async () => {
    const f = await fixture()
    const g = await gw(400)
    const mk = () => {
      const doc = new Y.Doc()
      const p = new WebsocketProvider(`ws://127.0.0.1:${g.port}/collab`, `${f.ws}/${f.note}`, doc, {
        WebSocketPolyfill: class extends WebSocket {
          constructor(url: string, protocols?: string | string[]) {
            super(url, protocols, { headers: { cookie: `cn_session=${f.owner.cookie}` } })
          }
        } as never,
        disableBc: true,
        params: { epoch: '1' },
      })
      return { doc, p }
    }
    const x = mk()
    const y = mk()
    await Promise.all(
      [x, y].map((c) => new Promise<void>((r) => (c.p.synced ? r() : c.p.once('sync', () => r())))),
    )
    append(x.doc, ' and more')
    for (let i = 0; i < 50 && !text(y.doc).includes('and more'); i++) await sleep(100)
    expect(text(y.doc)).toContain('hello world and more')
    x.p.destroy()
    y.p.destroy()
  })
})
