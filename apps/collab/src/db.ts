import pg from 'pg'

export type Tx = pg.PoolClient

export type ErrCode = 'unauthenticated' | 'forbidden' | 'not_found' | 'conflict' | 'internal'
const STATUS: Record<ErrCode, number> = {
  unauthenticated: 401,
  forbidden: 403,
  not_found: 404,
  conflict: 409,
  internal: 500,
}

/** Mirrors cnote_core::CmdError: a code (HTTP status) and a message. */
export class CmdError extends Error {
  constructor(
    readonly code: ErrCode,
    message: string,
  ) {
    super(message)
  }
  get status() {
    return STATUS[this.code]
  }
}

export function connect(url: string, max = 10) {
  const pool = new pg.Pool({ connectionString: url, max })
  pool.on('error', () => {}) // idle client dropped: the next checkout reconnects
  return pool
}

/** One transaction bound to a workspace (same GUC as the Go db.BeginTenant); RLS does the rest. */
export async function tenant<T>(pool: pg.Pool, workspaceId: string, fn: (tx: Tx) => Promise<T>): Promise<T> {
  const tx = await pool.connect()
  // A connection that dies between two queries emits 'error' on the checked-out client; unhandled, that kills the process.
  // The next query fails with the same error and the transaction is rolled back, so ignoring it here loses nothing.
  let broken = false
  const ignore = () => {
    broken = true
  }
  tx.on('error', ignore)
  try {
    await tx.query('begin')
    await tx.query("select set_config('app.workspace_id', $1, true)", [workspaceId])
    const out = await fn(tx)
    await tx.query('commit')
    return out
  } catch (e) {
    await tx.query('rollback').catch(() => {})
    throw e
  } finally {
    tx.off('error', ignore)
    tx.release(broken) // a dead connection is discarded, not pooled
  }
}
