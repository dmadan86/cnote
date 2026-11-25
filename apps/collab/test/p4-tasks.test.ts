// Docs parity B1, P4 (docs-parity-b 4.4, 13.2.6): a task assigned while two people co-edit reaches the index and the assignee's
// bell exactly once. Needs the real database, the Go API with its worker and a live gateway, like b1-step0.test.ts.
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { seed } from '../src/md.ts'
import { addMember, append, fixture, gateway, noteRow, open, pool, sleep, sql } from './helpers.ts'

const gateways: { close(): Promise<void> }[] = []
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

async function until<T>(what: string, fn: () => Promise<T | false>, ms = 20_000): Promise<T> {
  const end = Date.now() + ms
  for (;;) {
    const v = await fn()
    if (v) return v
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`)
    await sleep(250)
  }
}

/** Add a block parsed from Markdown after the first one, the way a pasted or typed list reaches the document. */
function addBlock(doc: Y.Doc, md: string): Uint8Array {
  const tmp = new Y.Doc()
  seed(tmp, md)
  const el = (tmp.getXmlFragment('default').get(0) as Y.XmlElement).clone()
  const before = Y.encodeStateVector(doc)
  doc.getXmlFragment('default').insert(1, [el])
  return Y.encodeStateAsUpdate(doc, before)
}

describe('a task assigned while co-editing', () => {
  it('is indexed and notifies the assignee once', async () => {
    const f = await fixture()
    const g = await gateway(300)
    gateways.push(g)
    const mo = await addMember(f)
    await sql(
      f,
      'insert into core.space_members (workspace_id, space_id, user_id, role) values ($1,$2,$3,$4)',
      [f.ws, f.space, mo.id, 'viewer'],
    )
    const count = async (kind: string) =>
      Number(
        (
          await sql<{ n: string }>(
            f,
            'select count(*) as n from core.notifications where note_id = $1 and user_id = $2 and kind = $3',
            [f.note, mo.id, kind],
          )
        )[0].n,
      )

    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    // Mo is first mentioned in plain text: that is the mention notification.
    ca.send(2, append(a, ` cc [@Mo](/w/${f.ws}/people/${mo.id})`))
    await until('the mention', async () => (await count('mention')) === 1)

    // Then a task for Mo is added: the assignment, which the mention path did not cover.
    ca.send(
      2,
      addBlock(
        a,
        `- [ ] Send the GST summary [@Mo](/w/${f.ws}/people/${mo.id}) <time datetime="2026-12-01">1 Dec 2026</time>`,
      ),
    )
    await until('the task row', async () => {
      const r = await sql<{ n: string }>(
        f,
        "select count(*) as n from core.note_tasks where note_id = $1 and assignee_id = $2 and due = '2026-12-01'",
        [f.note, mo.id],
      )
      return Number(r[0].n) === 1
    })
    await until('the assignment', async () => (await count('assigned')) === 1)

    // More typing is new versions and new events, never a new assignment.
    ca.send(2, append(a, ' and more'))
    await until('the next snapshot', async () => (await noteRow(f)).md.includes('and more'))
    await sleep(3000)
    expect(await count('assigned')).toBe(1)
    expect(await count('mention')).toBe(1)
    ca.close()
  })
})
