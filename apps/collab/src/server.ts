// Collaboration gateway (ADR-005), TypeScript port of crates/cnote-collab: a y-websocket compatible server.
// One in-memory room per note, y-sync protocol (step 1/2, update) plus awareness relay. Every accepted update is
// committed to core.note_updates before it is relayed, and a debounced snapshot into core.notes absorbs (and
// deletes) the logged updates, so a crash loses nothing anyone has seen. Writes are fenced by
// core.notes.collab_epoch: after a restore the old room can no longer write and its clients are closed with 4409.
import { createHash, timingSafeEqual } from 'node:crypto'
import { EventEmitter } from 'node:events'
import http from 'node:http'
import * as decoding from 'lib0/decoding'
import * as encoding from 'lib0/encoding'
import pg from 'pg'
import { WebSocket, WebSocketServer } from 'ws'
import * as Y from 'yjs'
import { archivedVisible, mayWrite, trashedVisible } from './access.ts'
import { authenticate, type Principal } from './auth.ts'
import { CmdError, tenant } from './db.ts'
import { buildDoc, loadDoc } from './doc.ts'
import { CLOSE_ARCHIVED, CLOSE_DELETED, CLOSE_RELOAD, CLOSE_RETRY } from './doc-schema.ts'
import * as md from './md.ts'

export const CLOSE_STALE_EPOCH = 4409
/** How long a fold with fence:true keeps a page closed to writes (core.notes.fenced_until) while the API finishes deleting it. */
const FENCE_MS = 60_000
const MAX_FRAME = 8 << 20
const CHECKPOINT_EVERY_SECS = 600
const MSG_SYNC = 0
const MSG_AWARENESS = 1
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** The page is fenced for a delete: the edit was not logged. The peer closes with CLOSE_RETRY and resends after reconnecting. */
class FencedError extends CmdError {
  constructor() {
    super('conflict', 'page is being deleted')
  }
}

/** The page was moved to the trash: its sockets close with CLOSE_DELETED, not CLOSE_STALE_EPOCH. */
class GoneError extends CmdError {
  constructor() {
    super('conflict', 'page deleted')
  }
}

/** The page was archived (read-only until restored): its sockets close with CLOSE_ARCHIVED. */
class ArchivedError extends CmdError {
  constructor() {
    super('conflict', 'page archived')
  }
}

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
  /** core.notes.version this room last loaded or wrote. With no one connected, a different value means the API
   * edited the note since and wins; live editors also save through the API (same text), so then it is ignored. */
  version: number
  doc: Y.Doc
  /** The seed this room built from content_md, until the first logged update stores it as the snapshot base. */
  seed: Uint8Array | null
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
  /** WebSocket close code of the 'stale' fence; CLOSE_DELETED when the page was deleted. */
  closeCode: number
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
  /** Shared secret of POST /internal/render (the Go API's note.restore). Unset: the endpoint answers 404. */
  internalSecret?: string
  /** COLLAB_MIN_SCHEMA: sockets whose `schema` parameter (missing counts as 0) is lower are closed with
   * CLOSE_RELOAD, on connect and whenever the minimum is raised. Default 0: no fence until operations raise it. */
  minSchema?: number
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
  let minSchema = opts.minSchema ?? 0
  /** What /collab/min-schema reports: the raised value only once every fenced peer has stopped applying updates. */
  let reportedMin = minSchema
  const schemaOf = new WeakMap<WebSocket, number>()
  /** Sockets below the minimum: their document-update frames are dropped, queued ones included. Set synchronously when the minimum rises. */
  const fencedWs = new WeakSet<WebSocket>()
  const chains = new WeakMap<WebSocket, Promise<void>>()
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

  /** The page is in the trash: close every socket of its room with CLOSE_DELETED and drop the room. */
  function retireGone(room: Room) {
    room.closeCode = CLOSE_DELETED
    retire(room)
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
    await rooms.get(key)?.persisting // so the version read below includes this room's own last write
    const a = await tenant(pool, ws, async (tx) => {
      const write = await mayWrite(tx, p, note)
      const e = await tx.query('select collab_epoch, version from core.notes where id = $1', [note])
      return { write, epoch: e.rows[0].collab_epoch as number, version: e.rows[0].version as number }
    })
    const open = rooms.get(key)
    if (open && open.epoch === a.epoch && (open.version === a.version || open.peers.size > 0))
      return { room: open, write: a.write }
    if (open) retire(open) // restored, or edited through the API while nobody had it open
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
      version: l.version,
      doc: l.doc,
      seed: l.seed,
      peers: new Map(),
      nextPeer: 0,
      dirty: false,
      lastEdit: Date.now(),
      editor: null,
      timerRunning: false,
      applied: l.ids,
      events: new EventEmitter().setMaxListeners(0),
      retired: false,
      closeCode: CLOSE_STALE_EPOCH,
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
      const e = await tx.query(
        "select collab_epoch, deleted_at is not null as gone, freshness = 'archived' as archived from core.notes where id = $1",
        [room.note],
      )
      if (e.rows[0]?.gone) throw new GoneError()
      if (e.rows[0]?.archived) throw new ArchivedError()
      const w = await mayWrite(tx, p, room.note)
      if (e.rows[0]?.collab_epoch !== room.epoch) throw new CmdError('conflict', 'page restored')
      return w
    })
  }

  /** Commit one update to the log, fenced on the room's epoch. Conflict means the page was restored since. */
  async function logUpdate(room: Room, update: Uint8Array): Promise<string> {
    // Pin the seed the log is built on before the first update: a content_md the API writes later (a client's flush
    // on reload) then differs from the snapshot, so the API text wins instead of being replayed onto itself.
    // Its own statement, taken before any FOR SHARE below: upgrading a share lock to a write would deadlock concurrent edits.
    if (room.seed)
      await tenant(pool, room.ws, (tx) =>
        tx.query(
          `update core.notes set content_ydoc = $3 where id = $1 and collab_epoch = $2 and content_ydoc is null
           and deleted_at is null and freshness <> 'archived' and (fenced_until is null or fenced_until <= now())`,
          [room.note, room.epoch, Buffer.from(room.seed as Uint8Array)],
        ),
      )
    const id = await tenant(pool, room.ws, async (tx) => {
      // FOR SHARE: a delete holds the page row FOR UPDATE, so an edit is either logged before it (and found there) or refused after it.
      const r = await tx.query(
        `insert into core.note_updates (workspace_id, note_id, epoch, update)
         select workspace_id, id, collab_epoch, $3 from core.notes
         where id = $1 and collab_epoch = $2 and deleted_at is null and freshness <> 'archived' and (fenced_until is null or fenced_until <= now())
         for share
         returning id`,
        [room.note, room.epoch, Buffer.from(update)],
      )
      if (r.rows[0]) return { id: r.rows[0].id as string }
      const g = await tx.query(
        "select deleted_at is not null as gone, collab_epoch = $2 and fenced_until > now() as fenced, freshness = 'archived' as archived from core.notes where id = $1",
        [room.note, room.epoch],
      )
      return { gone: !!g.rows[0]?.gone, fenced: !!g.rows[0]?.fenced, archived: !!g.rows[0]?.archived }
    })
    if ('gone' in id)
      throw id.gone
        ? new GoneError()
        : id.archived
          ? new ArchivedError()
          : id.fenced
            ? new FencedError()
            : new CmdError('conflict', 'page restored')
    room.seed = null
    return String(id.id)
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
             where id = $1 and deleted_at is null and collab_epoch = $4 and (version = $5 or $6::bool) returning version`,
            [room.note, state, mdText, room.epoch, room.version, room.peers.size > 0],
          )
          if (!v.rows[0]) return false // restored, deleted, or (nobody connected) API-edited since this room loaded: fenced out
          room.version = v.rows[0].version as number
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
        if (!written) {
          const g = await tenant(
            pool,
            room.ws,
            async (tx) =>
              (
                await tx.query('select deleted_at is not null as gone from core.notes where id = $1', [
                  room.note,
                ])
              ).rows[0]?.gone,
          )
          if (g) retireGone(room)
          else retire(room)
        }
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
      if (!write) return // read-only
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
      if (room.closeCode === CLOSE_DELETED) socket.close(CLOSE_DELETED, 'page deleted')
      else if (room.closeCode === CLOSE_ARCHIVED) socket.close(CLOSE_ARCHIVED, 'page archived')
      else if (room.closeCode === CLOSE_RETRY) socket.close(CLOSE_RETRY, 'page is being deleted, retry')
      else socket.close(CLOSE_STALE_EPOCH, 'page restored')
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
          if (e instanceof GoneError) {
            room.closeCode = CLOSE_DELETED
            fence()
          } else if (e instanceof ArchivedError) {
            room.closeCode = CLOSE_ARCHIVED
            fence()
          } else if (e instanceof CmdError && e.code === 'conflict') fence()
          else if (e instanceof CmdError && (e.code === 'not_found' || e.code === 'forbidden')) socket.close()
          // else transient database error: keep the connection, retry next tick
        },
      )
    }, recheckMs)

    // Frames of one connection are handled strictly in order.
    let chain = Promise.resolve()
    chains.set(socket, chain)
    socket.on('message', (data, isBinary) => {
      if (!isBinary || done || fencedWs.has(socket)) return
      const buf = data as Buffer
      chain = chain.then(async () => {
        if (done || fencedWs.has(socket)) return
        try {
          await onFrame(room, me, p, write, new Uint8Array(buf.buffer, buf.byteOffset, buf.length))
        } catch (e) {
          if (e instanceof GoneError) room.closeCode = CLOSE_DELETED
          if (e instanceof ArchivedError) room.closeCode = CLOSE_ARCHIVED
          // The room's doc already holds the refused edit: drop the room unsaved; every client keeps its edit and resyncs.
          if (e instanceof FencedError) room.closeCode = CLOSE_RETRY
          if (e instanceof CmdError && e.code === 'conflict') return fence()
          // Not logged, so not relayed: the client still has the edit and resends it on reconnect.
          console.error('collab update not persisted', room.note, e)
          socket.close()
          cleanup()
        }
      })
      chains.set(socket, chain)
    })
    // Frames still queued when the socket closes (a page leaving mid-typing) are applied before the final snapshot.
    socket.on('close', () => void chain.then(cleanup))
    socket.on('error', cleanup)
    if (room.retired) fence()
  }

  function reject(socket: import('node:stream').Duplex, status: number, message: string) {
    socket.end(
      `HTTP/1.1 ${status} ${http.STATUS_CODES[status]}\r\nContent-Type: text/plain\r\nContent-Length: ${Buffer.byteLength(message)}\r\nConnection: close\r\n\r\n${message}`,
    )
  }

  const sha = (v: string) => createHash('sha256').update(v).digest()
  function authorised(req: http.IncomingMessage) {
    const given = req.headers['x-collab-secret']
    return (
      !!opts.internalSecret &&
      typeof given === 'string' &&
      timingSafeEqual(sha(given), sha(opts.internalSecret))
    )
  }

  /** Raise the minimum editor schema and close every open socket now below it, so an old tab cannot keep
   * editing through the change. A lower value is ignored: the minimum only goes up until the process restarts. */
  async function setMinSchema(n: number) {
    if (!Number.isInteger(n) || n <= minSchema) return
    minSchema = n
    // Fence first, synchronously: no frame of an old tab is applied from here on. Then close, then wait for the one
    // frame that may be mid-write, so a caller that sees the new minimum knows no old tab can still change a page.
    const old = [...wss.clients].filter((w) => (schemaOf.get(w) ?? 0) < n)
    for (const w of old) fencedWs.add(w)
    for (const w of old) w.close(CLOSE_RELOAD, 'reload')
    await Promise.allSettled(old.map((w) => chains.get(w)))
    reportedMin = Math.max(reportedMin, n)
  }

  function setMin(req: http.IncomingMessage, res: http.ServerResponse) {
    const send = (status: number, body: string) => {
      res.statusCode = status
      res.setHeader('content-type', 'application/json')
      res.end(body)
    }
    if (!authorised(req)) return send(opts.internalSecret ? 401 : 404, '{"error":"unauthorized"}')
    let raw = ''
    req.on('data', (c: Buffer) => {
      if (raw.length < 1024) raw += c
    })
    req.on('end', async () => {
      try {
        const n = JSON.parse(raw).min
        if (!Number.isInteger(n) || n < 0) throw new Error('bad')
        await setMinSchema(n)
        send(200, JSON.stringify({ min: reportedMin }))
      } catch {
        send(400, '{"error":"bad request"}')
      }
    })
  }

  /** Stateless: renders the Markdown of a note's live document from the rows the caller read (snapshot + log). */
  function render(req: http.IncomingMessage, res: http.ServerResponse) {
    const send = (status: number, body: string) => {
      res.statusCode = status
      res.setHeader('content-type', 'application/json')
      res.end(body)
    }
    if (!authorised(req)) return send(opts.internalSecret ? 401 : 404, '{"error":"unauthorized"}')
    const chunks: Buffer[] = []
    let size = 0
    req.on('data', (c: Buffer) => {
      size += c.length
      if (size > 64 << 20) req.destroy()
      else chunks.push(c)
    })
    req.on('end', () => {
      try {
        const b = JSON.parse(Buffer.concat(chunks).toString('utf8'))
        if (typeof b.note_id !== 'string' || !UUID.test(b.note_id) || typeof b.content_md !== 'string')
          throw new Error('bad')
        const bytes = (v: unknown) => new Uint8Array(Buffer.from(String(v), 'base64'))
        const ydoc = b.content_ydoc ? bytes(b.content_ydoc) : null
        const doc = buildDoc(
          b.note_id.toLowerCase(),
          b.content_md,
          ydoc,
          ((b.updates ?? []) as unknown[]).map(bytes),
        )
        send(200, JSON.stringify({ markdown: md.toMarkdown(doc) }))
      } catch {
        send(400, '{"error":"bad request"}')
      }
    })
  }

  /** Fold a page's snapshot plus every logged edit of its current epoch into content_md and the snapshot, in one transaction
   * that locks the page row. Works with no open room (it reads only the database). With fence the room first stops
   * accepting writes (fenced_until), so nothing can be logged after the fold. */
  async function foldNote(ws: string, note: string, fence: boolean) {
    const room = rooms.get(`${ws}/${note}`)
    if (room) {
      // Frames already queued finish and are logged before the fence takes effect (it is set in the transaction below).
      await Promise.allSettled([...room.peers.values()].map((p) => chains.get(p.ws)))
      await room.persisting
    }
    return tenant(pool, ws, async (tx) => {
      const n = await tx.query(
        'select content_md, content_ydoc, collab_epoch, version from core.notes where id = $1 for update',
        [note],
      )
      if (!n.rows[0]) throw new CmdError('not_found', 'note')
      // The fence is a database fact: logUpdate on every gateway instance refuses while it is in the future.
      if (fence)
        await tx.query(
          'update core.notes set fenced_until = now() + make_interval(secs => $2) where id = $1',
          [note, FENCE_MS / 1000],
        )
      const { content_md: mdText, content_ydoc: ydoc, collab_epoch: epoch, version } = n.rows[0]
      const log = await tx.query(
        'select id, update from core.note_updates where note_id = $1 and epoch = $2 order by id',
        [note, epoch],
      )
      if (log.rows.length === 0) return { folded: 0, version: version as number }
      const doc = buildDoc(
        note,
        mdText,
        ydoc,
        log.rows.map((r) => r.update),
      )
      const v = await tx.query(
        'update core.notes set content_ydoc = $2, content_md = $3, version = version + 1, updated_at = now() where id = $1 returning version',
        [note, Buffer.from(Y.encodeStateAsUpdate(doc)), md.toMarkdown(doc)],
      )
      await tx.query('delete from core.note_updates where id = any($1::bigint[])', [
        log.rows.map((r) => String(r.id)),
      ])
      return { folded: log.rows.length, version: v.rows[0].version as number }
    })
  }

  function fold(req: http.IncomingMessage, res: http.ServerResponse) {
    const send = (status: number, body: string) => {
      res.statusCode = status
      res.setHeader('content-type', 'application/json')
      res.end(body)
    }
    if (!authorised(req)) return send(opts.internalSecret ? 401 : 404, '{"error":"unauthorized"}')
    let raw = ''
    req.on('data', (c: Buffer) => {
      if (raw.length < 4096) raw += c
    })
    req.on('end', async () => {
      let b: { note_id?: unknown; workspace_id?: unknown; fence?: unknown }
      try {
        b = JSON.parse(raw)
        if (
          typeof b.note_id !== 'string' ||
          !UUID.test(b.note_id) ||
          typeof b.workspace_id !== 'string' ||
          !UUID.test(b.workspace_id) ||
          typeof b.fence !== 'boolean'
        )
          throw new Error('bad')
      } catch {
        return send(400, '{"error":"bad request"}')
      }
      try {
        const r = await foldNote(
          (b.workspace_id as string).toLowerCase(),
          (b.note_id as string).toLowerCase(),
          b.fence as boolean,
        )
        send(200, JSON.stringify({ ok: true, ...r }))
      } catch (e) {
        if (e instanceof CmdError) return send(e.status, JSON.stringify({ error: e.message }))
        console.error('collab fold failed', e)
        send(500, '{"error":"internal"}')
      }
    })
  }

  const server = http.createServer((req, res) => {
    if (req.url === '/healthz') return void res.end('ok')
    if (req.method === 'GET' && req.url === '/collab/min-schema') {
      // Under /collab/ because the API front forwards only that prefix. Public and harmless: the web client asks before it offers blocks an older tab would delete.
      res.setHeader('Access-Control-Allow-Origin', '*')
      res.setHeader('Content-Type', 'application/json')
      return void res.end(JSON.stringify({ min: reportedMin }))
    }
    if (req.method === 'POST' && req.url === '/internal/render') return render(req, res)
    if (req.method === 'POST' && req.url === '/internal/fold') return fold(req, res)
    if (req.method === 'POST' && req.url === '/internal/min-schema') return setMin(req, res)
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
      const schemaQ = url.searchParams.get('schema')
      if (!UUID.test(ws) || !UUID.test(note) || (epochQ !== null && !/^-?\d+$/.test(epochQ)))
        return reject(socket, 400, 'bad request')
      const schema = schemaQ !== null && /^\d{1,6}$/.test(schemaQ) ? Number(schemaQ) : 0 // missing or junk: oldest
      if (schema < minSchema) {
        // Accept the upgrade just to close with the reload code: a browser cannot read an HTTP refusal.
        return wss.handleUpgrade(req, socket, head, (s) => s.close(CLOSE_RELOAD, 'reload'))
      }
      const p = await authenticate(pool, req.headers, url.searchParams.get('token'), ws)
      if (await tenant(pool, ws, (tx) => trashedVisible(tx, p, note))) {
        // The page is in the trash: accept the upgrade just to close with the final code (a browser cannot read an HTTP refusal).
        return wss.handleUpgrade(req, socket, head, (s) => s.close(CLOSE_DELETED, 'page deleted'))
      }
      if (await tenant(pool, ws, (tx) => archivedVisible(tx, p, note))) {
        // Archived pages are read-only until restored: no live editing, the web reloads into the read-only page.
        return wss.handleUpgrade(req, socket, head, (s) => s.close(CLOSE_ARCHIVED, 'page archived'))
      }
      const { room, write } = await prepare(p, ws, note)
      wss.handleUpgrade(req, socket, head, (s) => {
        schemaOf.set(s, schema)
        if (schema < minSchema) return s.close(CLOSE_RELOAD, 'reload') // raised while this upgrade was in flight
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
    setMinSchema,
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
