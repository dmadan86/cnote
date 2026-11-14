// POST /internal/render: the stateless endpoint behind the Go API's note.restore. Needs only the in-process server.
import { randomUUID } from 'node:crypto'
import type { AddressInfo } from 'node:net'
import { afterAll, describe, expect, it } from 'vitest'
import * as Y from 'yjs'
import * as md from '../src/md.ts'
import { createServer } from '../src/server.ts'

const SECRET = randomUUID() // per run: nothing credential-shaped in the repo
const pool = {} as never // the endpoint never touches the database
const on = createServer({ pool, internalSecret: SECRET })
const off = createServer({ pool })
const listen = (s: ReturnType<typeof createServer>) =>
  new Promise<number>((r) =>
    s.server.listen(0, '127.0.0.1', () => r((s.server.address() as AddressInfo).port)),
  )
const post = (port: number, body: unknown, secret?: string) =>
  fetch(`http://127.0.0.1:${port}/internal/render`, {
    method: 'POST',
    headers: secret === undefined ? {} : { 'x-collab-secret': secret },
    body: JSON.stringify(body),
  })

describe('POST /internal/render', async () => {
  const [pOn, pOff] = [await listen(on), await listen(off)]
  afterAll(async () => {
    await Promise.all([on.close(), off.close()])
  })
  const note = randomUUID()

  it('is hidden without a configured secret and refuses a wrong or missing one', async () => {
    expect((await post(pOff, {}, SECRET)).status).toBe(404)
    expect((await post(pOn, {})).status).toBe(401)
    expect((await post(pOn, {}, 'nope')).status).toBe(401)
  })

  it('renders a seed (no snapshot) when nothing is logged', async () => {
    const r = await post(pOn, { note_id: note, content_md: 'hello', content_ydoc: null, updates: [] }, SECRET)
    expect(r.status).toBe(200)
    expect(((await r.json()) as { markdown: string }).markdown).toBe('hello')
  })

  it('renders a snapshot that matches the stored text, and falls back to the text when it does not', async () => {
    const d = new Y.Doc()
    md.seed(d, 'one\n\ntwo')
    const snap = Buffer.from(Y.encodeStateAsUpdate(d)).toString('base64')
    const ok = await post(pOn, { note_id: note, content_md: md.toMarkdown(d), content_ydoc: snap }, SECRET)
    expect(((await ok.json()) as { markdown: string }).markdown).toBe('one\n\ntwo')
    const edited = await post(
      pOn,
      { note_id: note, content_md: 'edited via api', content_ydoc: snap },
      SECRET,
    )
    expect(((await edited.json()) as { markdown: string }).markdown).toBe('edited via api')
  })

  it('rejects malformed bodies', async () => {
    expect((await post(pOn, { note_id: 'x', content_md: 'a' }, SECRET)).status).toBe(400)
    expect((await post(pOn, { note_id: note }, SECRET)).status).toBe(400)
  })
})
