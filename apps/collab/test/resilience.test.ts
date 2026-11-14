// QA plan cn-qa-001 sections 3.4 (live-editing parts), 4.1, 4.2, 4.4: convergence, durability, fencing, restore races.
// Real Postgres, real Rust (fixtures, note.restore), real y-websocket clients; no mocks.
import { type ChildProcess, spawn } from 'node:child_process'
import net from 'node:net'
import fc from 'fast-check'
import { afterAll, describe, expect, it } from 'vitest'
import { WebSocket } from 'ws'
import { WebsocketProvider } from 'y-websocket'
import * as Y from 'yjs'
import { connect } from '../src/db.ts'
import * as md from '../src/md.ts'
import { createServer } from '../src/server.ts'
import {
  append,
  cmdOk,
  type Fixture,
  fixture,
  gateway,
  noteRow,
  open,
  pool,
  type Sock,
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
const children: ChildProcess[] = []
afterAll(async () => {
  for (const c of children) c.kill('SIGKILL')
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

/** Seeded fast-check config; a failure prints the seed. FC_SEED / FC_RUNS override (nightly runs larger). */
const fcConf = (runs: number) => ({
  numRuns: Number(process.env.FC_RUNS ?? runs),
  ...(process.env.FC_SEED ? { seed: Number(process.env.FC_SEED) } : {}),
})

async function join(port: number, f: Fixture) {
  const doc = new Y.Doc()
  const s = await open(port, f, { cookie: f.owner.cookie })
  await s.sync(doc)
  return { s, doc }
}
/** Apply relayed updates until `done` holds. */
async function pump(s: Sock, doc: Y.Doc, done: () => boolean, ms = 8000) {
  const end = Date.now() + ms
  while (!done() && Date.now() < end) {
    const m = await s.recv(300)
    if (m && 'body' in m && m.kind !== 'step1') Y.applyUpdate(doc, m.body)
  }
}
const until = async (ok: () => boolean | Promise<boolean>, ms = 10_000) => {
  for (const end = Date.now() + ms; Date.now() < end; await sleep(100)) if (await ok()) return true
  return false
}
const logRows = async (f: Fixture, epoch?: number) =>
  Number(
    (
      await sql<{ n: string }>(
        f,
        `select count(*) as n from core.note_updates where note_id = $1 ${epoch ? `and epoch = ${epoch}` : ''}`,
        [f.note],
      )
    )[0].n,
  )

// ---------------------------------------------------------------- 4.4 pure Yjs property tests
type Op =
  | { k: 'ins'; c: number; pos: number; s: string }
  | { k: 'del'; c: number; pos: number; n: number }
  | { k: 'fmt'; c: number; pos: number; n: number }
  | { k: 'deliver'; from: number; to: number; dup: boolean; rev: boolean }
const opArb = (n: number): fc.Arbitrary<Op> =>
  fc.oneof(
    fc.record({
      k: fc.constant('ins' as const),
      c: fc.nat(n - 1),
      pos: fc.nat(40),
      s: fc.stringMatching(/^[a-z]{1,4}$/),
    }),
    fc.record({
      k: fc.constant('del' as const),
      c: fc.nat(n - 1),
      pos: fc.nat(40),
      n: fc.integer({ min: 1, max: 5 }),
    }),
    fc.record({
      k: fc.constant('fmt' as const),
      c: fc.nat(n - 1),
      pos: fc.nat(40),
      n: fc.integer({ min: 1, max: 5 }),
    }),
    fc.record({
      k: fc.constant('deliver' as const),
      from: fc.nat(n - 1),
      to: fc.nat(n - 1),
      dup: fc.boolean(),
      rev: fc.boolean(),
    }),
  )

function base() {
  const d = new Y.Doc()
  md.seed(d, 'hello world')
  return Y.encodeStateAsUpdate(d)
}
const para = (d: Y.Doc) => (d.getXmlFragment('default').get(0) as Y.XmlElement).get(0) as Y.XmlText
const canon = (d: Y.Doc) => JSON.stringify(para(d).toDelta())

describe('4.4 Yjs property-based convergence', () => {
  it('4.4/CO-002/CO-003/CO-004/CO-008/CO-009: random ops, partitions, duplicated and reordered delivery converge', async () => {
    const seedUpdate = base()
    await fc.assert(
      fc.property(
        fc
          .integer({ min: 2, max: 5 })
          .chain((n) => fc.tuple(fc.constant(n), fc.array(opArb(n), { maxLength: 60 }))),
        ([n, ops]) => {
          const docs = Array.from({ length: n }, () => {
            const d = new Y.Doc()
            Y.applyUpdate(d, seedUpdate)
            return d
          })
          const out: Uint8Array[][] = docs.map(() => [])
          for (const [i, d] of docs.entries())
            d.on('update', (u: Uint8Array, origin: unknown) => {
              if (origin !== 'remote') out[i].push(u)
            })
          const deliver = (from: number, to: number, dup: boolean, rev: boolean) => {
            const us = rev ? [...out[from]].reverse() : out[from]
            for (const u of dup ? [...us, ...us] : us) Y.applyUpdate(docs[to], u, 'remote')
          }
          for (const o of ops) {
            if (o.k === 'deliver') deliver(o.from, o.to, o.dup, o.rev)
            else {
              const t = para(docs[o.c])
              if (o.k === 'ins') t.insert(o.pos % (t.length + 1), o.s)
              else if (t.length > 0) {
                const p = o.pos % t.length
                const len = Math.min(o.n, t.length - p)
                if (o.k === 'del') t.delete(p, len)
                else t.format(p, len, { bold: true })
              }
            }
          }
          // Heal the partitions: everybody gets everything, in hostile order.
          for (const rev of [false, true])
            for (let i = 0; i < n; i++) for (let j = 0; j < n; j++) i !== j && deliver(i, j, true, rev)
          for (let i = 1; i < n; i++) {
            expect(canon(docs[i])).toBe(canon(docs[0]))
            expect(Y.encodeStateVector(docs[i])).toEqual(Y.encodeStateVector(docs[0]))
          }
          md.toMarkdown(docs[0]) // schema validity: still serialises
        },
      ),
      fcConf(150),
    )
  })
})

// ---------------------------------------------------------------- 4.1 through the gateway
describe('4.1 concurrency and convergence through the gateway', () => {
  it('CO-001/CO-002/CO-003: five concurrent writers at the same position converge and persist', async () => {
    const f = await fixture()
    const g = await gw(300)
    const cs = await Promise.all([1, 2, 3, 4, 5].map(() => join(g.port, f)))
    cs.forEach((c, i) => {
      c.s.send(2, append(c.doc, ` w${'abcde'[i]}x`))
    })
    const all = () => cs.every((c) => 'abcde'.split('').every((l) => text(c.doc).includes(` w${l}x`)))
    await Promise.all(cs.map((c) => pump(c.s, c.doc, all)))
    expect(all()).toBe(true)
    for (const c of cs) expect(text(c.doc)).toBe(text(cs[0].doc))
    expect(await until(async () => (await noteRow(f)).md === md.toMarkdown(cs[0].doc))).toBe(true)
    cs.forEach((c) => {
      c.s.close()
    })
  })

  it('CO-008/CO-009: duplicated updates are not applied twice; reordered updates are logged and relayed once complete', async () => {
    const f = await fixture()
    const g = await gw(3_600_000)
    const a = await join(g.port, f)
    const b = await join(g.port, f)
    const src = new Y.Doc()
    Y.applyUpdate(src, Y.encodeStateAsUpdate(a.doc))
    const u1 = append(src, ' one')
    const u2 = append(src, ' two')
    const u3 = append(src, ' three')
    for (const u of [u3, u2, u1, u1, u3]) a.s.send(2, u)
    await pump(b.s, b.doc, () => text(b.doc).includes('three'))
    expect(text(b.doc)).toContain('hello world one two three')
    expect(text(b.doc).match(/one/g)).toHaveLength(1)
    // What was relayed must be durable: a restarted service serves the same text from the log alone.
    await g.abandon()
    const g2 = await gw(300)
    const c = await join(g2.port, f)
    expect(text(c.doc)).toContain('hello world one two three')
    expect(text(c.doc).match(/(one|two|three)/g)).toHaveLength(3)
    ;[a, b, c].forEach((x) => {
      x.s.close()
    })
  })

  it('CO-006/4.4: y-websocket clients with random offline stretches reconnect and converge with the persisted note', async () => {
    const f = await fixture()
    const g = await gw(300)
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
    const step = fc.record({
      c: fc.nat(2),
      k: fc.constantFrom('ins', 'ins', 'del', 'off', 'on'),
      pos: fc.nat(30),
      s: fc.stringMatching(/^[a-z]{1,3}$/),
    })
    await fc.assert(
      fc.asyncProperty(fc.array(step, { minLength: 3, maxLength: 14 }), async (steps) => {
        const cs = [mk(), mk(), mk()]
        await Promise.all(
          cs.map((c) => new Promise<void>((r) => (c.p.synced ? r() : c.p.once('sync', () => r())))),
        )
        try {
          for (const o of steps) {
            const c = cs[o.c]
            const t = para(c.doc)
            if (o.k === 'ins') t.insert(o.pos % (t.length + 1), o.s)
            else if (o.k === 'del' && t.length > 2) t.delete(o.pos % (t.length - 1), 2)
            else if (o.k === 'off') c.p.disconnect()
            else if (o.k === 'on') c.p.connect()
            await sleep(15)
          }
          cs.forEach((c) => {
            c.p.connect()
          })
          const same = () => cs.every((c) => text(c.doc) === text(cs[0].doc))
          expect(await until(same, 15_000), 'replicas converge').toBe(true)
          const want = md.toMarkdown(cs[0].doc)
          expect(
            await until(async () => (await noteRow(f)).md === want, 10_000),
            'persisted content_md matches',
          ).toBe(true)
        } finally {
          cs.forEach((c) => {
            c.p.destroy()
          })
        }
      }),
      fcConf(5),
    )
  })

  it('CO-005: two editors in different cells of the same table keep a valid table', async () => {
    const f = await fixture()
    const note = await cmdOk(f.ws, f.owner, 'note.create', {
      space_id: f.space,
      title: 'Table',
      content_md: '| a | b |\n| --- | --- |\n| c | d |',
    })
    const tf = { ...f, note: note.id }
    const g = await gw(300)
    const x = await join(g.port, tf)
    const y = await join(g.port, tf)
    const cells = (d: Y.Doc) => {
      const out: Y.XmlText[] = []
      const walk = (n: Y.XmlElement | Y.XmlFragment) =>
        n.toArray().forEach((c) => {
          if (c instanceof Y.XmlText) out.push(c)
          else if (c instanceof Y.XmlElement) walk(c)
        })
      walk(d.getXmlFragment('default'))
      return out
    }
    expect(cells(x.doc).length).toBeGreaterThanOrEqual(4)
    const send = (c: { s: Sock; doc: Y.Doc }, i: number, s: string) => {
      const before = Y.encodeStateVector(c.doc)
      const t = cells(c.doc)[i]
      t.insert(t.length, s)
      c.s.send(2, Y.encodeStateAsUpdate(c.doc, before))
    }
    send(x, 0, 'X')
    send(y, 3, 'Y')
    send(x, 2, 'P') // same cell as y's next edit
    send(y, 2, 'Q')
    const done = (c: { doc: Y.Doc }) => ['X', 'Y', 'P', 'Q'].every((l) => text(c.doc).includes(l))
    await Promise.all([x, y].map((c) => pump(c.s, c.doc, () => done(c))))
    expect(text(x.doc)).toBe(text(y.doc))
    expect(done(x)).toBe(true)
    const want = md.toMarkdown(x.doc)
    expect(await until(async () => (await noteRow(tf)).md === want)).toBe(true)
    expect(want).toMatch(/^\| *aX *\| *b *\|/)
    expect(md.toMarkdown(x.doc)).toBe(want)
    x.s.close()
    y.s.close()
  })

  it('CO-010/CO-013: 50 concurrent editors all land and replicas agree', async () => {
    const f = await fixture()
    const g = await gw(300)
    const cs = await Promise.all(Array.from({ length: 50 }, () => join(g.port, f)))
    const t0 = Date.now()
    cs.forEach((c, i) => {
      c.s.send(2, append(c.doc, ` e${String(i).padStart(2, '0')}z`))
    })
    const all = (d: Y.Doc) => cs.every((_, i) => text(d).includes(` e${String(i).padStart(2, '0')}z`))
    await Promise.all([0, 49].map((i) => pump(cs[i].s, cs[i].doc, () => all(cs[i].doc), 15_000)))
    expect(all(cs[0].doc) && all(cs[49].doc)).toBe(true)
    expect(Date.now() - t0).toBeLessThan(15_000)
    const late = await join(g.port, f) // a "different device" joining later sees the same state
    expect(text(late.doc)).toBe(text(cs[0].doc))
    cs.forEach((c) => {
      c.s.close()
    })
    late.s.close()
  })
})

// ---------------------------------------------------------------- 4.2 durability and fencing
function startService(port: number) {
  const c = spawn(process.execPath, ['--import', 'tsx', 'src/main.ts'], {
    cwd: new URL('..', import.meta.url).pathname,
    env: { ...process.env, COLLAB_PORT: String(port), COLLAB_DEBOUNCE_MS: '3600000' },
    stdio: 'ignore',
  })
  children.push(c)
  return c
}
async function ready(port: number) {
  expect(
    await until(
      async () => (await fetch(`http://127.0.0.1:${port}/healthz`).catch(() => null))?.ok === true,
      20_000,
    ),
    'service up',
  ).toBe(true)
}
async function openRetry(port: number, f: Fixture) {
  for (let i = 0; ; i++)
    try {
      return await open(port, f, { cookie: f.owner.cookie })
    } catch (e) {
      if (i > 40) throw e
      await sleep(250) // 409 while the dead process's lock is still being reaped
    }
}

describe('4.2 durability and failure injection', () => {
  it('DU-003/4.2: every acknowledged update survives kill -9 and restart (replayed from core.note_updates)', async () => {
    const f = await fixture()
    const port = 4710
    const p1 = startService(port)
    await ready(port)
    const a = new Y.Doc()
    const b = new Y.Doc()
    const ca = await openRetry(port, f)
    await ca.sync(a)
    const cb = await openRetry(port, f)
    await cb.sync(b)
    // "Acknowledged" = the server logged it before relaying: the observer receiving the relay is the ack.
    const acked: string[] = []
    for (let i = 0; i < 25; i++) {
      const tok = ` op${String(i).padStart(2, '0')}k`
      ca.send(2, append(a, tok))
      const m = await cb.recv()
      expect(m?.kind).toBe('update')
      acked.push(tok)
    }
    for (let i = 25; i < 30; i++) ca.send(2, append(a, ` op${i}k`)) // in flight at the kill: may be lost, never required
    p1.kill('SIGKILL')
    await new Promise((r) => p1.once('exit', r))
    expect((await noteRow(f)).md).toBe('hello world') // nothing was snapshotted: only the log can have it

    startService(port)
    await ready(port)
    const c = new Y.Doc()
    const cc = await openRetry(port, f)
    await cc.sync(c)
    for (const tok of acked) expect(text(c), tok).toContain(tok)
    const unacked = (text(c).match(/op\d+k/g) ?? []).length
    expect(unacked).toBeGreaterThanOrEqual(acked.length)
    ;[ca, cb, cc].forEach((s) => {
      s.close()
    })
  })

  it('DU-001/DU-004/DU-010: with the database unreachable an edit is refused, not relayed and not logged; resent edits land after recovery', async () => {
    const f = await fixture()
    const u = new URL(process.env.DATABASE_URL!)
    const target = { host: u.hostname, port: Number(u.port || 5432) }
    const socks = new Set<net.Socket>()
    const proxy = net.createServer((c) => {
      const up = net.connect(target)
      for (const s of [c, up]) {
        socks.add(s)
        s.on('error', () => s.destroy())
        s.on('close', () => {
          socks.delete(s)
          ;(s === c ? up : c).destroy()
        })
      }
      c.pipe(up).pipe(c)
    })
    await new Promise<void>((r) => proxy.listen(0, '127.0.0.1', r))
    const pport = (proxy.address() as net.AddressInfo).port
    const listenAgain = () => new Promise<void>((r) => proxy.listen(pport, '127.0.0.1', r))
    u.host = `127.0.0.1:${pport}`
    const g = createServer({ pool: connect(u.toString(), 5), debounceMs: 3_600_000, recheckMs: 600_000 })
    await new Promise<void>((r) => g.server.listen(0, '127.0.0.1', r))
    const port = (g.server.address() as net.AddressInfo).port
    gateways.push(g)

    const a = await join(port, f)
    const b = await join(port, f)
    a.s.send(2, append(a.doc, ' before'))
    await pump(b.s, b.doc, () => text(b.doc).includes('before'))
    expect(await logRows(f)).toBe(1)

    proxy.close()
    for (const s of socks) s.destroy()
    const lost = append(a.doc, ' during')
    a.s.send(2, lost)
    expect(await b.s.recv(1500)).toBeNull() // no false acknowledgment
    expect(await a.s.closeCode(3000)).not.toBeNull() // the writer is told: it must keep the edit and resend
    expect(await logRows(f)).toBe(1)
    expect(text(b.doc)).not.toContain('during')

    await listenAgain()
    const a2 = new Y.Doc()
    Y.applyUpdate(a2, Y.encodeStateAsUpdate(a.doc)) // the client still holds its unacknowledged edit
    let s2: Sock | undefined
    for (let i = 0; !s2 && i < 40; i++)
      s2 = await open(port, f, { cookie: f.owner.cookie }).catch(() => sleep(250).then(() => undefined))
    await s2!.sync(a2)
    expect(await until(async () => (await logRows(f)) >= 2 || (await noteRow(f)).md.includes('during'))).toBe(
      true,
    )
    const fresh = await join(port, f)
    expect(text(fresh.doc)).toContain('before during')
    for (const s of [b.s, s2!, fresh.s]) s.close()
    proxy.close()
  })

  it('DU-008/DU-009: only one instance may own a note; the second is refused with 409 until the first lets go', async () => {
    const f = await fixture()
    const g1 = await gw(3_600_000)
    const g2 = await gw(300)
    const a = await join(g1.port, f)
    a.s.send(2, append(a.doc, ' from-one'))
    await sleep(300)
    await expect(open(g2.port, f, { cookie: f.owner.cookie })).rejects.toMatchObject({ status: 409 })
    // The fenced instance never wrote: the owner's edit is the only log entry.
    expect(await logRows(f)).toBe(1)

    await g1.abandon() // owner dies without flushing; its lock dies with its session
    expect(await a.s.closeCode()).not.toBeNull()
    let b: Awaited<ReturnType<typeof join>> | undefined
    expect(
      await until(async () => {
        b = await join(g2.port, f).catch(() => undefined)
        return !!b
      }, 5000),
    ).toBe(true)
    expect(text(b!.doc)).toContain('hello world from-one') // failover recovers from the log
    b!.s.send(2, append(b!.doc, ' from-two'))
    expect(await until(async () => (await noteRow(f)).md === 'hello world from-one from-two')).toBe(true)
    b!.s.close()
  })

  it('DU-008: a note stays refusable while owned, but is released when the last editor leaves', async () => {
    const f = await fixture()
    const g1 = await gw(300)
    const g2 = await gw(300)
    const a = await join(g1.port, f)
    a.s.send(2, append(a.doc, '!'))
    a.s.close()
    let ok = false
    for (let i = 0; i < 40 && !ok; i++)
      ok = await join(g2.port, f).then(
        (c) => {
          c.s.close()
          return true
        },
        () => sleep(250).then(() => false),
      )
    expect(ok, JSON.stringify([...g1.rooms.keys()])).toBe(true)
  })

  it.skip('DU-002: crash after commit before ack, retry deduplicated — needs a durable-ack message in the protocol (ack is implicit in the relay); Yjs idempotence is covered by CO-008', () => {})
  it.skip('DU-005/DU-006/DU-012: snapshot-worker crash, checksum corruption, storage quota — no checksum column or quota enforcement exists (feature work)', () => {})
})

// ---------------------------------------------------------------- 3.4 restore races
describe('3.4 restore while editing', () => {
  it('RV-004/RV-005/DU-011: no update from the old epoch lands after a restore, even with edits in flight', async () => {
    const f = await fixture()
    const g = await gw(400)
    const { s, doc } = await join(g.port, f)
    s.send(2, append(doc, ' v2'))
    await sleep(1000) // snapshot + first revision
    const revs = await cmdOk(f.ws, f.owner, 'note.revisions', { note_id: f.note })
    const v2 = revs[0].id

    let stop = false
    const spam = (async () => {
      for (let i = 0; !stop && s.closed === null && i < 2000; i++) {
        s.send(2, append(doc, 'x'))
        await sleep(3)
      }
    })()
    await sleep(150)
    const r = await cmdOk(f.ws, f.owner, 'note.restore', {
      note_id: f.note,
      revision_id: v2,
      expected_epoch: 1,
    })
    expect(r.collab_epoch).toBe(2)
    expect(await s.closeCode()).toBe(4409)
    stop = true
    await spam
    await sleep(800)

    expect((await noteRow(f)).md).toBe('hello world v2')
    expect(await logRows(f, 2), 'nothing from the old session in the new epoch').toBe(0)
    // A stale row smuggled into the log under the old epoch is never replayed.
    await sql(f, 'insert into core.note_updates (workspace_id, note_id, epoch, update) values ($1,$2,1,$3)', [
      f.ws,
      f.note,
      Buffer.from(append(doc, 'ZZZ')),
    ])
    const n = await join(g.port, f)
    expect(text(n.doc)).toContain('hello world v2')
    expect(text(n.doc)).not.toContain('ZZZ')
    n.s.close()
    await sleep(600)
    expect((await noteRow(f)).md).toBe('hello world v2')
  })

  it('RV-001: revisions are immutable for the app role (update and delete refused)', async () => {
    const f = await fixture()
    const g = await gw(300)
    const { s, doc } = await join(g.port, f)
    s.send(2, append(doc, ' v2'))
    expect(
      await until(
        async () =>
          (await sql(f, 'select 1 from core.note_revisions where note_id = $1', [f.note])).length > 0,
      ),
    ).toBe(true)
    await expect(
      sql(f, "update core.note_revisions set title = 'x' where note_id = $1", [f.note]),
    ).rejects.toThrow()
    await expect(sql(f, 'delete from core.note_revisions where note_id = $1', [f.note])).rejects.toThrow()
    s.close()
  })

  it.skip('RV-009: crash during restore — restore is a Rust transaction (outside apps/collab); needs fault injection in the Rust server', () => {})
})
