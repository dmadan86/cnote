// Port of crates/cnote-core/src/access.rs (as in apps/api/internal/access): space access on top of RLS and
// page access via core.note_access.
import { ADMIN, GUEST, type Principal } from './auth.ts'
import { CmdError, type Tx } from './db.ts'

async function assertSpaceAccess(tx: Tx, p: Principal, spaceId: string, write: boolean) {
  const r = await tx.query(
    `select s.visibility, sm.role from core.spaces s
     left join core.space_members sm on sm.space_id = s.id and sm.user_id = $2::uuid
     where s.id = $1::uuid`,
    [spaceId, p.userId],
  )
  const row = r.rows[0]
  if (!row) throw new CmdError('not_found', 'space')
  if (p.role >= ADMIN) return
  const member = row.role as string | null
  if ((row.visibility === 'private' || p.role === GUEST) && member === null)
    throw new CmdError('not_found', 'space')
  if (write && (member === 'viewer' || (p.role === GUEST && member === null)))
    throw new CmdError('forbidden', 'read-only access to this space')
}

async function readableSpaces(tx: Tx, p: Principal): Promise<string[]> {
  const r = await tx.query(
    `select s.id::text from core.spaces s
     left join core.space_members sm on sm.space_id = s.id and sm.user_id = $1::uuid
     where $2 or sm.user_id is not null or (s.visibility = 'open' and $3)`,
    [p.userId, p.role >= ADMIN, p.role !== GUEST],
  )
  return r.rows.map((x) => x.id)
}

/** The note's space id. An unreadable page is not found; a read-only page is forbidden for writes. */
export async function assertNoteAccess(tx: Tx, p: Principal, noteId: string, write: boolean) {
  const n = await tx.query(
    'select space_id::text from core.notes where id = $1::uuid and deleted_at is null',
    [noteId],
  )
  if (!n.rows[0]) throw new CmdError('not_found', 'note')
  const space = n.rows[0].space_id as string
  const spaces = await readableSpaces(tx, p)
  const lv = await tx.query('select core.note_access($1::uuid,$2::uuid,$3,$4::text[]::uuid[]) as level', [
    noteId,
    p.userId,
    p.role >= ADMIN,
    spaces,
  ])
  const level = lv.rows[0].level as string
  if (level === 'none') throw new CmdError('not_found', 'note')
  if (spaces.includes(space)) await assertSpaceAccess(tx, p, space, write)
  if (write && level !== 'edit') throw new CmdError('forbidden', 'you can only view this page')
  return space
}

/** Whether the caller may edit the note; throws when they may not even read it. */
export async function mayWrite(tx: Tx, p: Principal, noteId: string): Promise<boolean> {
  const scopeOk = p.scopes === null || p.scopes.some((g) => g === '*' || g === 'write_notes')
  try {
    await assertNoteAccess(tx, p, noteId, true)
    return scopeOk
  } catch (e) {
    if (!(e instanceof CmdError) || e.code !== 'forbidden') throw e
    await assertNoteAccess(tx, p, noteId, false)
    return false
  }
}
