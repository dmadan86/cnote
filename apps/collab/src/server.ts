// Collaboration gateway (ADR-005), TypeScript port of crates/cnote-collab: a y-websocket compatible server.
// One in-memory room per note, y-sync protocol (step 1/2, update) plus awareness relay. Every accepted update is
// committed to core.note_updates before it is relayed, and a debounced snapshot into core.notes absorbs (and
// deletes) the logged updates, so a crash loses nothing anyone has seen. Writes are fenced by
// core.notes.collab_epoch: after a restore the old room can no longer write and its clients are closed with 4409.
import { EventEmitter } from 'node:events'
import http from 'node:http'
import * as decoding from 'lib0/decoding'
import * as encoding from 'lib0/encoding'
import pg from 'pg'
import { WebSocket, WebSocketServer } from 'ws'
import * as Y from 'yjs'
import { mayWrite } from './access.ts'
import { authenticate, type Principal } from './auth.ts'
import { CmdError, tenant } from './db.ts'
import { loadDoc } from './doc.ts'
import * as md from './md.ts'

export const CLOSE_STALE_EPOCH = 4409
const MAX_FRAME = 8 << 20
const CHECKPOINT_EVERY_SECS = 600
const MSG_SYNC = 0
const MSG_AWARENESS = 1
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

type Awareness = Map<number, { clock: number; json: string }>

interface Peer {
  ws: WebSocket
  awareness: Awareness
}

interface Room {
  key: string
  ws: string
  note: string
  /** core.notes.collab_epoch this room was loaded at; every write is fenced on it. */
  epoch: number
  doc: Y.Doc
  peers: Map<number, Peer>
  nextPeer: number
  dirty: boolean
  lastEdit: number
  editor: Principal | null
  timerRunning: boolean
  /** Ids in core.note_updates already applied to `doc`; the next snapshot deletes exactly these. */
  applied: string[]
  /** Fires 'stale' when the room is fenced out (restored elsewhere): every connection then closes. */
  events: EventEmitter
  retired: boolean
  /** The note's advisory lock has been released (or handed back); set once. */
  unlocked: boolean
  /** Serialises persists so an older snapshot never overwrites a newer one. */
  persisting: Promise<unknown>
}

export interface Options {
  pool: pg.Pool
  /** Idle time before a snapshot (Rust: 2s). */
  debounceMs?: number
  /** How often an open connection re-checks its page access (Rust: 10s). */
  recheckMs?: number
}

function frame(write: (e: encoding.Encoder) => void) {
  const e = encoding.createEncoder()
  write(e)
  return encoding.toUint8Array(e)
}
const syncFrame = (type: 0 | 1 | 2, body: Uint8Array) =>
  frame((e) => {
    encoding.writeVarUint(e, MSG_SYNC)
    encoding.writeVarUint(e, type)
    encoding.writeVarUint8Array(e, body)
  })
const awarenessFrame = (a: Awareness) =>
  frame((e) => {
    encoding.writeVarUint(e, MSG_AWARENESS)
    const inner = encoding.createEncoder()
    encoding.writeVarUint(inner, a.size)
    for (const [client, s] of a) {
      encoding.writeVarUint(inner, client)
      encoding.writeVarUint(inner, s.clock)
      encoding.writeVarString(inner, s.json)
    }
    encoding.writeVarUint8Array(e, encoding.toUint8Array(inner))
  })

export function createServer(opts: Options) {
  const { pool } = opts
  const debounce = opts.debounceMs ?? 2000
  const recheckMs = opts.recheckMs ?? 10_000
  const rooms = new Map<string, Room>()
  const wss = new WebSocketServer({ noServer: true, maxPayload: MAX_FRAME })

  // Single-writer fence across service instances: the room owner holds a session advisory lock per note on one
  // dedicated connection. A second instance cannot open the note (409) until the first drops it or dies.
  let lockConn: pg.Client | null = null
  let lockPending: Promise<pg.Client> | null = null
  function locker(): Promise<pg.Client> {
    if (lockConn) return Promise.resolve(lockConn)
    lockPending ??= (async () => {
      const c = new pg.Client(pool.options)
      const lost = () => {
        if (lockConn !== c) return
        lockConn = null // every lock died with the session: fence all our rooms out
        for (const r of [...rooms.values()]) {
          r.unlocked = true
          retire(r)
        }
      }
      c.on('error', lost)
      c.on('end', lost)
      try {
        await c.connect()
      } finally {
        lockPending = null
      }
      lockConn = c
      return c
    })()
    return lockPending
  }
  const lockSql = (fn: 'lock' | 'unlock') =>
    `select pg_${fn === 'lock' ? 'try_' : ''}advisory_${fn}(hashtextextended('cnote-collab:' || $1, 0)) as ok`
  let lockQ: Promise<unknown> = Promise.resolve() // pg clients must not run queries concurrently
  function lockQuery(fn: 'lock' | 'unlock', note: string): Promise<boolean> {
    const run = async () => (await (await locker()).query(lockSql(fn), [note])).rows[0].ok as boolean
    const r = lockQ.then(run, run)
    lockQ = r.catch(() => {})
    return r
  }
  const lockNote = (note: string) => lockQuery('lock', note)
  function unlockNote(room: Room) {
    if (room.unlocked) return
    room.unlocked = true
    void lockQuery('unlock', room.note).catch(() => {})
  }

  /** Fence a room out: drop it from the map (if still there) and close all of its connections. */
  function retire(room: Room) {
    if (rooms.get(room.key) === room) rooms.delete(room.key)
    unlockNote(room)
    room.retired = true
    room.events.emit('stale')
  }

  const sendAll = (room: Room, except: number, data: Uint8Array) => {
    for (const [id, p] of room.peers) if (id !== except && p.ws.readyState === WebSocket.OPEN) p.ws.send(data)
  }

  /** Access check plus room lookup/creation. Returns the room and whether this caller may edit. */
  async function prepare(p: Principal, ws: string, note: string): Promise<{ room: Room; write: boolean }> {
    const key = `${ws}/${note}`
    const a = await tenant(pool, ws, async (tx) => {
      const write = await mayWrite(tx, p, note)
      const e = await tx.query('select collab_epoch from core.notes where id = $1', [note])
      return { write, epoch: e.rows[0].collab_epoch as number }
    })
    const open = rooms.get(key)
    if (open && open.epoch === a.epoch) return { room: open, write: a.write }
    if (open) retire(open) // restored since this room loaded
    // Own the note before reading it, so nobody else can log updates between our load and our first write.
    if (!(await lockNote(note))) throw new CmdError('conflict', 'note is open on another collab instance')
    let l: Awaited<ReturnType<typeof loadDoc>>
    try {
      l = await tenant(pool, ws, (tx) => loadDoc(tx, note))
    } catch (e) {
      await lockQuery('unlock', note).catch(() => {})
      throw e
    }
    const created: Room = {
      key,
      ws,
      note,
      epoch: l.epoch,
      doc: l.doc,
      peers: new Map(),
      nextPeer: 0,
      dirty: false,
      lastEdit: Date.now(),
      editor: null,
      timerRunning: false,
      applied: l.ids,
      events: new EventEmitter().setMaxListeners(0),
      retired: false,
      unlocked: false,
      persisting: Promise.resolve(),
    }
    // Another connection may have created the room while this one was loading.
    const room = rooms.get(key) ?? created
    if (room !== created) unlockNote(created)
    rooms.set(key, room)
    if (room === created && l.ids.length > 0) {
      // Updates logged before a crash: fold them into a snapshot soon.
      room.dirty = true
      kick(room)
    }
    return { room, write: a.write }
  }

  /** Sharing can change while a socket is open: a revoked user stops reading, a downgraded one stops writing. */
  async function recheck(p: Principal, room: Room): Promise<boolean> {
    return tenant(pool, room.ws, async (tx) => {
      const w = await mayWrite(tx, p, room.note)
      const e = await tx.query('select collab_epoch from core.notes where id = $1', [room.note])
      if (e.rows[0]?.collab_epoch !== room.epoch) throw new CmdError('conflict', 'page restored')
      return w
    })
  }

  /** Commit one update to the log, fenced on the room's epoch. Conflict means the page was restored since. */
  async function logUpdate(room: Room, update: Uint8Array): Promise<string> {
    const id = await tenant(pool, room.ws, async (tx) => {
      const r = await tx.query(
        `insert into core.note_updates (workspace_id, note_id, epoch, update)
         select workspace_id, id, collab_epoch, $3 from core.notes
         where id = $1 and collab_epoch = $2 and deleted_at is null
         returning id`,
        [room.note, room.epoch, Buffer.from(update)],
      )
      return r.rows[0]?.id as string | undefined
    })
    if (id === undefined) throw new CmdError('conflict', 'page restored')
    return String(id)
  }

  /** Start the idle timer for this room unless one is already running. */
  function kick(room: Room) {
    if (room.timerRunning) return
    room.timerRunning = true
    void (async () => {
      for (;;) {
        const wait = debounce - (Date.now() - room.lastEdit)
        if (wait > 0) {
          await new Promise((r) => setTimeout(r, wait))
          continue
        }
        const ok = await persist(room).then(
          () => true,
          () => false,
        )
        if (!room.dirty) {
          room.timerRunning = false
          return
        }
        if (!ok) room.lastEdit = Date.now() // back off before retrying a failed write
      }
    })()
  }

  /** Write the doc and its Markdown snapshot, bump the version, and emit note.updated as the last editor. */
  function persist(room: Room): Promise<void> {
    const run = async () => {
      if (!room.dirty) return
      room.dirty = false
      const state = Buffer.from(Y.encodeStateAsUpdate(room.doc))
      const mdText = md.toMarkdown(room.doc)
      const editor = room.editor
      // Taken together with the state: the snapshot contains exactly these logged updates.
      const applied = room.applied
      room.applied = []
      try {
        const written = await tenant(pool, room.ws, async (tx) => {
          const v = await tx.query(
            `update core.notes set content_ydoc = $2, content_md = $3, version = version + 1, updated_at = now()
             where id = $1 and deleted_at is null and collab_epoch = $4 returning version`,
            [room.note, state, mdText, room.epoch],
          )
          if (!v.rows[0]) return false // restored (or deleted) since this room loaded: fenced out
          await tx.query('delete from core.note_updates where id = any($1::bigint[])', [applied])
          await tx.query(
            `insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason, created_by)
             select n.workspace_id, n.id, coalesce((select max(number) from core.note_revisions r where r.note_id = n.id), 0) + 1,
                    n.title, n.content_md, 'edit', $2::uuid
             from core.notes n
             where n.id = $1 and n.content_md <> ''
               and n.content_md is distinct from
                   (select content_md from core.note_revisions r where r.note_id = n.id order by number desc limit 1)
               and not exists (select 1 from core.note_revisions r
                               where r.note_id = n.id and r.created_at > now() - make_interval(secs => $3::float8))`,
            [room.note, editor?.userId ?? null, CHECKPOINT_EVERY_SECS],
          )
          if (editor) {
            const [type, actor, onBehalf] =
              editor.kind === 'agent'
                ? ['agent', editor.agentId, editor.userId]
                : ['user', editor.userId, null]
            const ev = await tx.query(
              `insert into core.events (workspace_id, note_id, kind, actor_type, actor_id, on_behalf_of, payload)
               values ($1,$2,'note.updated',$3,$4,$5::uuid,$6::jsonb) returning id`,
              [
                editor.workspaceId,
                room.note,
                type,
                actor,
                onBehalf,
                JSON.stringify({ changed: ['content'], version: v.rows[0].version }),
              ],
            )
            await tx.query(
              "insert into core.jobs (workspace_id, kind, payload, run_at) values ($1,'event.dispatch',$2::jsonb,now())",
              [editor.workspaceId, JSON.stringify({ event_id: Number(ev.rows[0].id) })],
            )
          }
          return true
        })
        if (!written) retire(room)
      } catch (e) {
        console.error('collab persist failed', room.note, e)
        room.dirty = true
        room.applied.unshift(...applied)
        throw e
      }
    }
    const next = room.persisting.then(run, run)
    room.persisting = next.catch(() => {})
    return next
  }

  type Msg =
    | { t: 'step1' | 'step2' | 'update'; body: Uint8Array }
    | { t: 'awareness'; entries: [number, number, string][] }

  function parse(data: Uint8Array): Msg | null {
    try {
      const dec = decoding.createDecoder(data)
      const type = decoding.readVarUint(dec)
      if (type === MSG_SYNC) {
        const sync = decoding.readVarUint(dec)
        const body = decoding.readVarUint8Array(dec)
        return sync <= 2 ? { t: (['step1', 'step2', 'update'] as const)[sync], body } : null
      }
      if (type === MSG_AWARENESS) {
        const inner = decoding.createDecoder(decoding.readVarUint8Array(dec))
        const entries: [number, number, string][] = []
        for (let i = decoding.readVarUint(inner); i > 0; i--)
          entries.push([
            decoding.readVarUint(inner),
            decoding.readVarUint(inner),
            decoding.readVarString(inner),
          ])
        return { t: 'awareness', entries }
      }
    } catch {} // malformed frame: ignored, like the Rust gateway
    return null
  }

  async function onFrame(room: Room, me: number, p: Principal, write: boolean, data: Uint8Array) {
    const peer = room.peers.get(me)
    const msg = parse(data)
    if (!peer || !msg) return
    if (msg.t === 'step1') {
      peer.ws.send(syncFrame(1, Y.encodeStateAsUpdate(room.doc, msg.body)))
    } else if (msg.t === 'awareness') {
      sendAll(room, me, data)
      for (const [client, clock, json] of msg.entries) peer.awareness.set(client, { clock, json })
    } else {
      if (!write) return // read-only: their edits never reach the shared doc
      // The doc's own emitted update, not msg.body: an out-of-order update that completes parked ones carries them too.
      let changed: Uint8Array | null = null
      const mark = (u: Uint8Array) => {
        changed = u
      }
      room.doc.on('update', mark)
      try {
        Y.applyUpdate(room.doc, msg.body)
      } catch {
        return
      } finally {
        room.doc.off('update', mark)
      }
      if (!changed) return
      // Durable before anyone else sees it. If this fails the connection closes; the sender still holds
      // the edit and resends it on reconnect (sync step 2), and the in-memory doc reaches the next snapshot.
      const delta: Uint8Array = changed
      const id = await logUpdate(room, delta)
      room.applied.push(id)
      room.dirty = true
      room.lastEdit = Date.now()
      room.editor = p
      sendAll(room, me, syncFrame(2, delta))
      kick(room)
    }
  }

  function serve(socket: WebSocket, room: Room, p: Principal, write0: boolean) {
    let write = write0
    const me = room.nextPeer++
    const peer: Peer = { ws: socket, awareness: new Map() }
    room.peers.set(me, peer)
    socket.send(syncFrame(0, Y.encodeStateVector(room.doc)))
    for (const [id, other] of room.peers)
      if (id !== me && other.awareness.size) socket.send(awarenessFrame(other.awareness))

    let done = false
    let fenced = false
    const fence = () => {
      if (done) return
      fenced = true
      cleanup()
      retire(room)
      socket.close(CLOSE_STALE_EPOCH, 'page restored')
    }
    const cleanup = () => {
      if (done) return
      done = true
      clearInterval(tick)
      room.events.off('stale', fence)
      room.peers.delete(me)
      if (fenced) return
      // Tell the others this connection's people left, instead of waiting for their 30s timeout.
      if (peer.awareness.size) {
        const gone: Awareness = new Map()
        for (const [c, s] of peer.awareness) gone.set(c, { clock: s.clock + 1, json: 'null' })
        sendAll(room, me, awarenessFrame(gone))
      }
      if (room.peers.size === 0) {
        void persist(room)
          .catch(() => {})
          .then(() => {
            // Keep the room if someone joined meanwhile or the final write failed (the timer retries).
            if (room.peers.size === 0 && !room.dirty && rooms.get(room.key) === room) {
              rooms.delete(room.key)
              unlockNote(room)
            }
          })
      }
    }
    room.events.on('stale', fence)
    const tick = setInterval(() => {
      recheck(p, room).then(
        (w) => {
          write = write && w
        },
        (e) => {
          if (e instanceof CmdError && e.code === 'conflict') fence()
          else if (e instanceof CmdError && (e.code === 'not_found' || e.code === 'forbidden')) socket.close()
          // else transient database error: keep the connection, retry next tick
        },
      )
    }, recheckMs)

    // Frames of one connection are handled strictly in order.
    let chain = Promise.resolve()
    socket.on('message', (data, isBinary) => {
      if (!isBinary || done) return
      const buf = data as Buffer
      chain = chain.then(async () => {
        if (done) return
        try {
          await onFrame(room, me, p, write, new Uint8Array(buf.buffer, buf.byteOffset, buf.length))
        } catch (e) {
          if (e instanceof CmdError && e.code === 'conflict') return fence()
          // Not logged, so not relayed: the client still has the edit and resends it on reconnect.
          console.error('collab update not persisted', room.note, e)
          socket.close()
          cleanup()
        }
      })
    })
    socket.on('close', cleanup)
    socket.on('error', cleanup)
    if (room.retired) fence()
  }

  function reject(socket: import('node:stream').Duplex, status: number, message: string) {
    socket.end(
      `HTTP/1.1 ${status} ${http.STATUS_CODES[status]}\r\nContent-Type: text/plain\r\nContent-Length: ${Buffer.byteLength(message)}\r\nConnection: close\r\n\r\n${message}`,
    )
  }

  const server = http.createServer((req, res) => {
    if (req.url === '/healthz') return void res.end('ok')
    res.statusCode = 426
    res.end('WebSocket upgrade required')
  })

  server.on('upgrade', async (req, socket, head) => {
    socket.on('error', () => {})
    try {
      const url = new URL(req.url ?? '/', 'http://x')
      const m = /^\/collab\/([^/]+)\/([^/]+)$/.exec(url.pathname)
      if (!m) return reject(socket, 404, 'not found')
      const [ws, note] = [m[1].toLowerCase(), m[2].toLowerCase()]
      const epochQ = url.searchParams.get('epoch')
      if (!UUID.test(ws) || !UUID.test(note) || (epochQ !== null && !/^-?\d+$/.test(epochQ)))
        return reject(socket, 400, 'bad request')
      const p = await authenticate(pool, req.headers, url.searchParams.get('token'), ws)
      const { room, write } = await prepare(p, ws, note)
      wss.handleUpgrade(req, socket, head, (s) => {
        if (epochQ !== null && Number(epochQ) !== room.epoch) s.close(CLOSE_STALE_EPOCH, 'page restored')
        else serve(s, room, p, write)
      })
    } catch (e) {
      if (e instanceof CmdError) return reject(socket, e.status, e.message)
      console.error('collab upgrade failed', e)
      reject(socket, 500, 'internal error')
    }
  })

  return {
    server,
    rooms,
    /** Flush every dirty room, then close. */
    async close() {
      for (const w of wss.clients) w.terminate()
      await Promise.allSettled([...rooms.values()].map(persist))
      server.close()
      server.closeAllConnections()
      await lockConn?.end().catch(() => {})
    },
    /** Simulate a crash in-process: drop sockets and locks without flushing anything. */
    async abandon() {
      for (const w of wss.clients) w.terminate()
      rooms.clear()
      server.close()
      server.closeAllConnections()
      await lockConn?.end().catch(() => {})
    },
  }
}
