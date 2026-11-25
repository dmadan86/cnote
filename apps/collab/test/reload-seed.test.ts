// A page edited and reloaded before the debounced snapshot: the client's flush writes content_md through the API
// while the edit is still only in the log. The next load must not replay that edit on top of the text containing it.
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import { append, cmdOk, fixture, gateway, open, pool, sleep, text } from './helpers.ts'

const gateways: { close(): Promise<void> }[] = []
afterAll(async () => {
  await Promise.all(gateways.map((g) => g.close()))
  await pool.end()
})

describe('reload before the snapshot', () => {
  it('shows an edit once after the API saved the same text into content_md', async () => {
    const f = await fixture()
    const g = await gateway(60_000) // no snapshot in time: the edit lives only in the log
    gateways.push(g)
    const a = new Y.Doc()
    const ca = await open(g.port, f, { cookie: f.owner.cookie })
    await ca.sync(a)
    ca.send(2, append(a, ' more'))
    await sleep(300)
    await cmdOk(f.ws, f.owner, 'note.update', {
      id: f.note,
      content_md: 'hello world more',
      schema: 2,
      expected_epoch: 1,
    })
    ca.close()
    await sleep(300)

    const b = new Y.Doc()
    const cb = await open(g.port, f, { cookie: f.owner.cookie })
    await cb.sync(b)
    expect(text(b)).toBe('<paragraph>hello world more</paragraph>')
    cb.close()
  })
})
