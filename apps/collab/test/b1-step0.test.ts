// Docs parity B1, step 0 (docs/design/docs-parity-b.md 13.2.2 and 13.2.10), against the real database, the real Go API with its
// worker (WITH_WORKER=1, COLLAB_URL and COLLAB_INTERNAL_SECRET set) and a live gateway:
//   - a mention typed while two people co-edit notifies exactly once (the gateway never runs note.update, so the worker must);
//   - an archived space is read-only on the gateway too, also for a workspace admin.
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import {
  addMember,
  append,
  type Fixture,
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
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})
async function gw(debounce: number, recheck?: number) {
  const g = await gateway(debounce, recheck)
  gateways.push(g)
  return g
}

async function joinSpace(f: Fixture, userId: string, role: 'editor' | 'viewer') {
  await sql(
    f,
    'insert into core.space_members (workspace_id, space_id, user_id, role) values ($1,$2,$3,$4)',
    [f.ws, f.space, userId, role],
  )
}

const mentionCount = async (f: Fixture, user: string) =>
  Number(
    (
      await sql<{ n: string }>(
        f,
        "select count(*) as n from core.notifications where note_id = $1 and user_id = $2 and kind = 'mention'",
        [f.note, user],
      )
    )[0].n,
  )

async function until<T>(what: string, fn: () => Promise<T | false>, ms = 20_000): Promise<T> {
  const end = Date.now() + ms
  for (;;) {
    const v = await fn()
    if (v) return v
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`)
    await sleep(250)
  }
}

/** Type a mention the way the editor stores it: the text `@Name` with a link mark to the person. */
function typeMention(doc: Y.Doc, ws: string, user: string, name: string): Uint8Array {
  const before = Y.encodeStateVector(doc)
  const t = (doc.getXmlFragment('default').get(0) as Y.XmlElement).get(0) as Y.XmlText
  t.insert(t.length, ' ')
  t.insert(t.length, `@${name}`, { link: { href: `/w/${ws}/people/${user}` } })
  return Y.encodeStateAsUpdate(doc, before)
}

describe('mentions typed while co-editing', () => {
  it('notify exactly once, however much is typed around them', async () => {
    const f = await fixture()
    const g = await gw(300)
    const editor2 = await addMember(f)
    const mo = await addMember(f)
    await joinSpace(f, editor2.id, 'editor')
    await joinSpace(f, mo.id, 'viewer')

    const a = new Y.Doc()
    const b = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    const cb = await open(g.port, f, { cookie: editor2.cookie })
    await cb.sync(b)

    ca.send(2, typeMention(a, f.ws, mo.id, 'Mo'))
    const m = await cb.recv()
    expect(m?.kind).toBe('update')
    Y.applyUpdate(b, (m as { body: Uint8Array }).body)
    expect(text(b)).toContain('@Mo')

    await until('the gateway snapshot', async () => (await noteRow(f)).md.includes(`/people/${mo.id}`))
    await until('the mention notification', async () => (await mentionCount(f, mo.id)) > 0)

    // More typing by both editors, each its own snapshot and note.updated: nothing new to tell Mo.
    cb.send(2, append(b, ' and more from the second editor'))
    ca.send(2, append(a, ' and the first again'))
    await until('the next snapshot', async () => (await noteRow(f)).md.includes('first again'))
    await sleep(3000) // the worker polls twice a second; give it time to handle both events
    expect(await mentionCount(f, mo.id)).toBe(1)

    const [n] = await sql<{ actor: string; title: string }>(
      f,
      "select actor_user_id::text as actor, title from core.notifications where note_id = $1 and user_id = $2 and kind = 'mention'",
      [f.note, mo.id],
    )
    expect(n.actor).toBe(f.owner.id)
    expect(n.title).toBe('Plan')
    ca.close()
    cb.close()
  })
})

describe('archived spaces on the gateway', () => {
  it('open read-only for everyone, a workspace admin included', async () => {
    const f = await fixture()
    const g = await gw(300)
    await sql(f, 'update core.spaces set archived_at = now() where id = $1', [f.space])
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    expect(text(a)).toContain('hello world') // still readable
    ca.send(2, append(a, ' HACKED'))
    await sleep(1200)
    expect(await noteRow(f)).toMatchObject({ md: 'hello world', version: 1 })
    ca.close()
  })

  it('drop a live writer to read-only at the next recheck', async () => {
    const f = await fixture()
    const g = await gw(300, 400)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    ca.send(2, append(a, '!'))
    await until('the first edit', async () => (await noteRow(f)).md === 'hello world!')
    await sql(f, 'update core.spaces set archived_at = now() where id = $1', [f.space])
    await sleep(1500) // two rechecks
    ca.send(2, append(a, ' LATE'))
    await sleep(1200)
    expect((await noteRow(f)).md).toBe('hello world!')
    ca.close()
  })

  it('refuse a direct share that reaches the page from outside the space', async () => {
    const f = await fixture()
    const g = await gw(300, 400)
    const guest = await addMember(f) // not in the private space
    await sql(
      f,
      "insert into core.note_permissions (note_id, workspace_id, user_id, level, granted_by) values ($1,$2,$3,'edit',$4)",
      [f.note, f.ws, guest.id, f.owner.id],
    )
    await sql(f, 'update core.spaces set archived_at = now() where id = $1', [f.space])
    const v = new Y.Doc()
    const cv = await open(g.port, f, { cookie: guest.cookie })
    await cv.sync(v)
    expect(text(v)).toContain('hello world')
    cv.send(2, append(v, ' VIA SHARE'))
    await sleep(1200)
    expect((await noteRow(f)).md).toBe('hello world')
    cv.close()
  })
})
