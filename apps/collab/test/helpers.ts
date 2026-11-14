// Integration helpers: real Postgres (DATABASE_URL, role cnote_app) and the real Rust server for fixtures and
// for the revision commands that stay in Rust (note.restore). No mocks.
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import type { AddressInfo } from 'node:net'
import * as decoding from 'lib0/decoding'
import * as encoding from 'lib0/encoding'
import { WebSocket } from 'ws'
import * as Y from 'yjs'
import { connect, tenant } from '../src/db.ts'
import { createServer } from '../src/server.ts'

try {
  process.loadEnvFile(new URL('../../../.env', import.meta.url))
} catch {}

export const RUST = (process.env.RUST_API_URL ?? 'http://localhost:4000').replace(/\/$/, '')
export const pool = connect(process.env.DATABASE_URL!, 5)

export async function gateway(debounceMs: number, recheckMs = 10_000) {
  const g = createServer({ pool, debounceMs, recheckMs })
  await new Promise<void>((r) => g.server.listen(0, '127.0.0.1', r))
  return { ...g, port: (g.server.address() as AddressInfo).port }
}

export interface User {
  id: string
  email: string
  cookie: string
}

async function rust(path: string, init: RequestInit & { cookie?: string }) {
  const r = await fetch(RUST + path, {
    ...init,
    headers: {
      'content-type': 'application/json',
      ...(init.cookie ? { cookie: `cn_session=${init.cookie}` } : {}),
    },
  })
  return r
}

export async function signup(workspace?: string): Promise<User & { ws?: string }> {
  const email = `collab-${randomUUID()}@example.test`
  const r = await rust('/api/auth/signup', {
    method: 'POST',
    body: JSON.stringify({
      email,
      name: 'Tester',
      password: randomBytes(12).toString('hex'),
      workspace_name: workspace,
    }),
  })
  if (!r.ok) throw new Error(`signup ${r.status} ${await r.text()}`)
  const body = (await r.json()) as { user_id: string; workspace_id: string | null }
  const cookie = /cn_session=([^;]+)/.exec(r.headers.get('set-cookie') ?? '')![1]
  return { id: body.user_id, email, cookie, ws: body.workspace_id ?? undefined }
}

export async function cmd(ws: string, who: User, name: string, input: object) {
  const r = await rust(`/api/w/${ws}/commands/${name}`, {
    method: 'POST',
    body: JSON.stringify(input),
    cookie: who.cookie,
  })
  const text = await r.text()
  return { status: r.status, body: text ? JSON.parse(text) : null }
}

export async function cmdOk(ws: string, who: User, name: string, input: object) {
  const r = await cmd(ws, who, name, input)
  if (r.status !== 200) throw new Error(`${name}: ${r.status} ${JSON.stringify(r.body)}`)
  return r.body
}

export interface Fixture {
  ws: string
  owner: User
  note: string
  space: string
}

/** A workspace with an owner, a private space and a note whose text is "hello world". */
export async function fixture(): Promise<Fixture> {
  const owner = await signup('Acme')
  const ws = owner.ws!
  const space = await cmdOk(ws, owner, 'space.create', { key: 'DOC', name: 'Docs', visibility: 'private' })
  const note = await cmdOk(ws, owner, 'note.create', {
    space_id: space.id,
    title: 'Plan',
    content_md: 'hello world',
  })
  return { ws, owner, note: note.id, space: space.id }
}

/** A user added to the workspace as a plain member. */
export async function addMember(f: Fixture): Promise<User> {
  const u = await signup()
  await cmdOk(f.ws, f.owner, 'member.add', { email: u.email, role: 'member' })
  return u
}

export async function sql<T = Record<string, unknown>>(
  f: Fixture,
  q: string,
  args: unknown[] = [],
): Promise<T[]> {
  return tenant(pool, f.ws, async (tx) => (await tx.query(q, args)).rows as T[])
}

export async function noteRow(f: Fixture) {
  const [r] = await sql<{ md: string; version: number; has_ydoc: boolean; epoch: number }>(
    f,
    'select content_md as md, version, content_ydoc is not null as has_ydoc, collab_epoch as epoch from core.notes where id = $1',
    [f.note],
  )
  return r
}

export async function apiKey(f: Fixture, who: User): Promise<string> {
  const prefix = randomBytes(4).toString('hex')
  const key = `cn_${prefix}_${randomBytes(18).toString('base64url')}`
  await sql(
    f,
    'insert into core.api_keys (workspace_id, user_id, name, prefix, key_hash, scopes) values ($1,$2,$3,$4,$5,$6)',
    [f.ws, who.id, 'collab test', prefix, createHash('sha256').update(key).digest('hex'), []],
  )
  return key
}

// ---- wire client (y-sync, spoken by hand like the Rust test) ----

export type Msg = { kind: 'step1' | 'step2' | 'update'; body: Uint8Array } | { kind: 'awareness' }

export class Sock {
  ws: WebSocket
  queue: Msg[] = []
  closed: number | null = null
  private waiters: (() => void)[] = []
  constructor(ws: WebSocket) {
    this.ws = ws
    ws.on('message', (d: Buffer) => {
      const dec = decoding.createDecoder(new Uint8Array(d))
      if (decoding.readVarUint(dec) === 0) {
        const kind = (['step1', 'step2', 'update'] as const)[decoding.readVarUint(dec)]
        this.queue.push({ kind, body: decoding.readVarUint8Array(dec) })
      } else this.queue.push({ kind: 'awareness' })
      this.wake()
    })
    ws.on('close', (code) => {
      this.closed = code
      this.wake()
    })
  }
  private wake() {
    for (const w of this.waiters.splice(0)) w()
  }
  private wait(ms: number) {
    return new Promise<void>((r) => {
      const t = setTimeout(r, ms)
      this.waiters.push(() => {
        clearTimeout(t)
        r()
      })
    })
  }
  /** Next sync message, or null after `ms` of silence. */
  async recv(ms = 1500): Promise<Msg | null> {
    const end = Date.now() + ms
    for (;;) {
      const m = this.queue.find((x) => x.kind !== 'awareness')
      if (m) return this.queue.splice(this.queue.indexOf(m), 1)[0]
      if (this.closed !== null || Date.now() >= end) return null
      await this.wait(end - Date.now())
    }
  }
  send(kind: 0 | 1 | 2, body: Uint8Array) {
    const e = encoding.createEncoder()
    encoding.writeVarUint(e, 0)
    encoding.writeVarUint(e, kind)
    encoding.writeVarUint8Array(e, body)
    this.ws.send(encoding.toUint8Array(e))
  }
  /** y-sync handshake: answer the server's step 1 with ours and apply its step 2. */
  async sync(doc: Y.Doc) {
    this.send(0, Y.encodeStateVector(doc))
    for (;;) {
      const m = await this.recv()
      if (!m) throw new Error('no server message')
      if (m.kind === 'step1') this.send(1, Y.encodeStateAsUpdate(doc, m.body))
      else if (m.kind === 'step2') return void Y.applyUpdate(doc, m.body)
    }
  }
  /** The close code the server ends this socket with (null if it stays open). */
  async closeCode(ms = 3000): Promise<number | null> {
    const end = Date.now() + ms
    while (this.closed === null && Date.now() < end) await this.wait(end - Date.now())
    return this.closed
  }
  async untilClosed(ms: number) {
    return (await this.closeCode(ms)) !== null
  }
  close() {
    this.ws.close()
  }
}

export function open(
  port: number,
  f: Fixture,
  opts: { cookie?: string; token?: string; query?: string; origin?: string } = {},
) {
  const q = new URLSearchParams(opts.query ?? '')
  if (opts.token) q.set('token', opts.token)
  const url = `ws://127.0.0.1:${port}/collab/${f.ws}/${f.note}${q.size ? `?${q}` : ''}`
  const ws = new WebSocket(url, {
    headers: {
      ...(opts.cookie ? { cookie: `cn_session=${opts.cookie}` } : {}),
      ...(opts.origin ? { origin: opts.origin } : {}),
    },
  })
  return new Promise<Sock>((resolve, reject) => {
    const s = new Sock(ws)
    ws.once('open', () => resolve(s))
    ws.once('unexpected-response', (_req, res) =>
      reject(Object.assign(new Error(`HTTP ${res.statusCode}`), { status: res.statusCode })),
    )
    ws.once('error', () => {})
  })
}

export const text = (doc: Y.Doc) => doc.getXmlFragment('default').toString()

/** Append to the first paragraph and return the update that carries the change. */
export function append(doc: Y.Doc, s: string): Uint8Array {
  const before = Y.encodeStateVector(doc)
  const p = doc.getXmlFragment('default').get(0) as Y.XmlElement
  const t = p.get(0) as Y.XmlText
  t.insert(t.length, s)
  return Y.encodeStateAsUpdate(doc, before)
}

export const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
