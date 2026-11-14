// Principal resolution for the collab socket: same SQL as crates/cnote-core/src/auth.rs
// (bearer API key or ?token=, otherwise the cn_session cookie).
import { createHash } from 'node:crypto'
import type { IncomingHttpHeaders } from 'node:http'
import type pg from 'pg'
import { CmdError, tenant } from './db.ts'

export const SESSION_COOKIE = 'cn_session'
export const GUEST = 0
export const MEMBER = 1
export const ADMIN = 2
const ROLES: Record<string, number> = { guest: 0, member: 1, admin: 2, owner: 3 }

export interface Principal {
  kind: 'user' | 'api_key' | 'agent'
  userId: string
  agentId: string | null
  workspaceId: string
  role: number
  scopes: string[] | null
}

const sha256 = (s: string) => createHash('sha256').update(s).digest('hex')

function cookie(headers: IncomingHttpHeaders, name: string) {
  for (const kv of (headers.cookie ?? '').split(';')) {
    const i = kv.indexOf('=')
    if (i > 0 && kv.slice(0, i).trim() === name) return kv.slice(i + 1).trim()
  }
  return null
}

/** Browsers attach cookies to cross-site WebSocket handshakes: a cookie login counts only for same-origin pages
 * or origins listed in COLLAB_ALLOWED_ORIGINS (comma separated). */
export function originOk(headers: IncomingHttpHeaders, allowed = process.env.COLLAB_ALLOWED_ORIGINS ?? '') {
  const origin = headers.origin
  if (!origin) return true // not a browser
  const host = headers.host ?? ''
  const same = origin.split('://')[1] === host
  return same || allowed.split(',').some((o) => o.trim() === origin)
}

async function userPrincipal(pool: pg.Pool, userId: string, workspaceId: string): Promise<Principal> {
  const role = await tenant(pool, workspaceId, async (tx) => {
    const r = await tx.query('select role from core.memberships where user_id = $1', [userId])
    return r.rows[0]?.role as string | undefined
  })
  if (role === undefined) throw new CmdError('forbidden', 'not a member of this workspace')
  return { kind: 'user', userId, agentId: null, workspaceId, role: ROLES[role] ?? GUEST, scopes: null }
}

async function principalFromApiKey(pool: pg.Pool, key: string): Promise<Principal | null> {
  if (!key.startsWith('cn_')) return null
  const r = await pool.query('select workspace_id, user_id, agent_id, scopes from core.resolve_api_key($1)', [
    sha256(key),
  ])
  const row = r.rows[0]
  if (!row) return null
  const owner = await userPrincipal(pool, row.user_id, row.workspace_id).catch(() => null)
  if (!owner) return null // owner left the workspace: the key is dead
  const scopes = row.scopes as string[] | null
  return {
    kind: row.agent_id ? 'agent' : 'api_key',
    userId: row.user_id,
    agentId: row.agent_id,
    workspaceId: row.workspace_id,
    role: Math.min(owner.role, ADMIN),
    scopes: scopes && scopes.length > 0 ? scopes : null,
  }
}

export async function authenticate(
  pool: pg.Pool,
  headers: IncomingHttpHeaders,
  token: string | null,
  workspaceId: string,
): Promise<Principal> {
  const auth = headers.authorization
  const bearer = (auth?.startsWith('Bearer ') ? auth.slice(7).trim() : null) ?? token
  if (bearer !== null) {
    const p = await principalFromApiKey(pool, bearer)
    if (!p) throw new CmdError('unauthenticated', 'invalid API key')
    if (p.workspaceId !== workspaceId) throw new CmdError('forbidden', 'API key belongs to another workspace')
    return p
  }
  const session = cookie(headers, SESSION_COOKIE)
  if (!session) throw new CmdError('unauthenticated', 'sign in required')
  if (!originOk(headers)) throw new CmdError('forbidden', 'cross-origin collaboration request')
  const r = await pool.query(
    'select u.id from core.sessions s join core.users u on u.id = s.user_id where s.id = $1 and s.expires_at > now()',
    [sha256(session)],
  )
  if (!r.rows[0]) throw new CmdError('unauthenticated', 'session expired')
  return userPrincipal(pool, r.rows[0].id, workspaceId)
}
