'use client'
import { useCallback, useEffect, useId, useState } from 'react'
import { cmd } from '@/lib/api'
import {
  type SpaceB1,
  type SpaceMember,
  type SpaceMembers,
  type SpaceRole,
  spaceArchive,
  spaceMemberSet,
  spaceMembers,
  spaceUpdate,
} from '@/lib/docs'
import { useShell } from '../shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '../ui'
import { SpacePublicLinks } from './space-public-links'

const ROLES: { value: SpaceRole; label: string }[] = [
  { value: 'admin', label: 'Admin' },
  { value: 'editor', label: 'Editor' },
  { value: 'viewer', label: 'Viewer' },
]

/** Space settings: details, members with roles, public links switch, archive. Opened from the sidebar and from the space page. */
export function SpaceSettingsDialog({
  spaceId,
  open,
  onClose,
}: {
  spaceId: string
  open: boolean
  onClose: () => void
}) {
  const { ws, members: people, reload } = useShell()
  const [space, setSpace] = useState<SpaceB1>()
  const [data, setData] = useState<SpaceMembers>()
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [visibility, setVisibility] = useState<'open' | 'private'>('open')
  const [confirmPrivate, setConfirmPrivate] = useState(false)
  const [addUser, setAddUser] = useState('')
  const [addRole, setAddRole] = useState<SpaceRole>('editor')
  const ids = { hint: useId(), links: useId(), linksHint: useId() }

  const load = useCallback(async () => {
    try {
      const [all, m] = await Promise.all([
        cmd<SpaceB1[]>(ws, 'space.list', { include_archived: true }),
        spaceMembers(ws, spaceId),
      ])
      const s = all.find((x) => x.id === spaceId)
      if (!s) return setError('Space not found.')
      setSpace(s)
      setData(m)
      setName(s.name)
      setDescription(s.description ?? '')
      setVisibility(s.visibility)
      setConfirmPrivate(false)
    } catch (e) {
      setError((e as Error).message)
    }
  }, [ws, spaceId])

  useEffect(() => {
    if (open) load()
  }, [open, load])

  // Run one change, then show what the server now says. The server decides; a refusal is shown as it is.
  async function run(fn: () => Promise<unknown>) {
    setBusy(true)
    try {
      await fn()
      setError(undefined)
      await load()
      reload()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const canManage = data?.can_manage ?? false
  const listed = new Set(data?.members.map((m) => m.user_id))
  const candidates = people.filter((p) => p.role !== 'guest' && !listed.has(p.user_id))
  // Going private: workspace members (not guests) who are neither listed nor admins lose access.
  const losing = candidates.filter((p) => p.role === 'member').length

  function saveDetails(e: React.FormEvent) {
    e.preventDefault()
    if (!space) return
    if (visibility === 'private' && space.visibility === 'open' && losing > 0 && !confirmPrivate)
      return setConfirmPrivate(true)
    run(() => spaceUpdate(ws, { id: spaceId, name, description: description.trim() || null, visibility }))
  }

  return (
    <Modal open={open} onClose={onClose} title="Space settings" className="max-w-2xl">
      {!space || !data ? (
        error ? (
          <ErrorText error={error} />
        ) : (
          <p className="text-[13px] text-muted">Loading...</p>
        )
      ) : (
        <div className="max-h-[75vh] space-y-6 overflow-y-auto pr-1">
          {space.archived && (
            <p role="status" className="rounded-md bg-warn-bg px-3 py-2 text-[13px] text-warn">
              This space is archived. It is read-only for everyone until it is unarchived.
            </p>
          )}
          {!canManage && (
            <p className="text-[13px] text-muted">
              Only a space admin or a workspace admin can change these settings.
            </p>
          )}

          <form onSubmit={saveDetails} className="space-y-3" aria-labelledby={`${ids.hint}-d`}>
            <h3 id={`${ids.hint}-d`} className="text-[13px] font-semibold">
              Details
            </h3>
            <Field label="Name">
              {(id) => (
                <input
                  id={id}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  maxLength={80}
                  required
                  disabled={!canManage}
                  className={inputCls}
                />
              )}
            </Field>
            <Field label="Description">
              {(id) => (
                <textarea
                  id={id}
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  maxLength={500}
                  rows={2}
                  disabled={!canManage}
                  className={inputCls}
                />
              )}
            </Field>
            <Field label="Visibility">
              {(id) => (
                <select
                  id={id}
                  value={visibility}
                  onChange={(e) => {
                    setVisibility(e.target.value as 'open' | 'private')
                    setConfirmPrivate(false)
                  }}
                  disabled={!canManage}
                  className={inputCls}
                >
                  <option value="open">Open to the workspace</option>
                  <option value="private">Private</option>
                </select>
              )}
            </Field>
            <p className="text-[12px] text-muted">The space key {space.key} cannot be changed.</p>
            {confirmPrivate && (
              <p role="alert" className="rounded-md bg-warn-bg px-3 py-2 text-[13px] text-warn">
                {losing} workspace {losing === 1 ? 'member loses' : 'members lose'} access to this space.
                Press "Make private" again to confirm.
              </p>
            )}
            {canManage && (
              <button type="submit" disabled={busy} className={btnPrimary}>
                {confirmPrivate ? 'Make private' : 'Save details'}
              </button>
            )}
          </form>

          <section aria-labelledby={`${ids.hint}-m`} className="space-y-3">
            <h3 id={`${ids.hint}-m`} className="text-[13px] font-semibold">
              Members
            </h3>
            <p id={ids.hint} className="text-[12px] text-muted">
              {data.visibility === 'open'
                ? 'Everyone in the workspace can edit this space unless listed here as a viewer.'
                : 'Only the people listed here can open this space.'}{' '}
              A space admin manages members and settings; a restricted page stays restricted for them too.
            </p>
            <table className="w-full text-[13px]">
              <caption className="sr-only">Space members</caption>
              <thead>
                <tr className="text-left text-[12px] text-muted">
                  <th scope="col" className="py-1 font-medium">
                    Name
                  </th>
                  <th scope="col" className="py-1 font-medium">
                    Role
                  </th>
                  <th scope="col" className="py-1 text-right font-medium">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {data.members.map((m) => (
                  <MemberRow
                    key={m.user_id}
                    m={m}
                    editable={canManage && !busy}
                    onRole={(role) => run(() => spaceMemberSet(ws, spaceId, m.user_id, role))}
                  />
                ))}
              </tbody>
            </table>
            {canManage && (
              <div className="flex flex-wrap items-end gap-2">
                <Field label="Add people">
                  {(id) => (
                    <select
                      id={id}
                      value={addUser}
                      onChange={(e) => setAddUser(e.target.value)}
                      className={inputCls}
                    >
                      <option value="">Choose a workspace member</option>
                      {candidates.map((p) => (
                        <option key={p.user_id} value={p.user_id}>
                          {p.name} ({p.email})
                        </option>
                      ))}
                    </select>
                  )}
                </Field>
                <Field label="Role for new member">
                  {(id) => (
                    <select
                      id={id}
                      value={addRole}
                      onChange={(e) => setAddRole(e.target.value as SpaceRole)}
                      className={inputCls}
                    >
                      {ROLES.map((r) => (
                        <option key={r.value} value={r.value}>
                          {r.label}
                        </option>
                      ))}
                    </select>
                  )}
                </Field>
                <button
                  type="button"
                  disabled={busy || !addUser}
                  className={btnPrimary}
                  onClick={() =>
                    run(async () => {
                      await spaceMemberSet(ws, spaceId, addUser, addRole)
                      setAddUser('')
                    })
                  }
                >
                  Add
                </button>
              </div>
            )}
            <p className="text-[12px] text-muted">
              Guests cannot be added to a whole space. Share a page with them instead.
            </p>
          </section>

          <section aria-labelledby={`${ids.hint}-l`} className="space-y-2">
            <h3 id={`${ids.hint}-l`} className="text-[13px] font-semibold">
              Public links
            </h3>
            <label htmlFor={ids.links} className="flex items-center gap-2 text-[13px]">
              <input
                id={ids.links}
                type="checkbox"
                aria-describedby={ids.linksHint}
                checked={space.public_links}
                disabled={!canManage || busy}
                onChange={(e) => {
                  if (
                    !e.target.checked &&
                    !window.confirm(
                      'Turn off public page links? Every active link in this space stops working, and turning the switch back on revives none.',
                    )
                  )
                    return
                  run(() => spaceUpdate(ws, { id: spaceId, public_links: e.target.checked }))
                }}
              />
              Allow public page links in this space
            </label>
            <p id={ids.linksHint} className="text-[12px] text-muted">
              Turning this off stops every public link in this space at once. Turning it back on revives none
              of them.
            </p>
            {canManage && <SpacePublicLinks key={String(space.public_links)} ws={ws} spaceId={spaceId} />}
          </section>

          {canManage && (
            <section aria-labelledby={`${ids.hint}-a`} className="space-y-2">
              <h3 id={`${ids.hint}-a`} className="text-[13px] font-semibold">
                Archive
              </h3>
              <p className="text-[12px] text-muted">
                An archived space is read-only for everyone. It leaves the sidebar but stays readable and
                searchable.
              </p>
              <button
                type="button"
                disabled={busy}
                className={btn}
                onClick={() => {
                  if (
                    !space.archived &&
                    !window.confirm(`Archive ${space.name}? Nobody can edit it until it is unarchived.`)
                  )
                    return
                  run(() => spaceArchive(ws, spaceId, !space.archived))
                }}
              >
                {space.archived ? 'Unarchive space' : 'Archive space'}
              </button>
            </section>
          )}
          <ErrorText error={error} />
        </div>
      )}
    </Modal>
  )
}

function MemberRow({
  m,
  editable,
  onRole,
}: {
  m: SpaceMember
  editable: boolean
  onRole: (role: SpaceRole | null) => void
}) {
  const fixed = m.workspace_role === 'owner' || m.workspace_role === 'admin'
  return (
    <tr className="border-t border-line">
      <td className="py-1.5 pr-2">
        <div className="font-medium">{m.name}</div>
        <div className="text-[12px] text-muted">{m.email}</div>
      </td>
      <td className="py-1.5 pr-2">
        {fixed || !editable ? (
          <span>
            {ROLES.find((r) => r.value === m.role)?.label}
            {fixed && <span className="text-muted"> (workspace {m.workspace_role})</span>}
          </span>
        ) : (
          <select
            aria-label={`Role for ${m.name}`}
            value={m.role}
            onChange={(e) => onRole(e.target.value as SpaceRole)}
            className={inputCls}
          >
            {ROLES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        )}
      </td>
      <td className="py-1.5 text-right">
        {!fixed && editable && (
          <button type="button" aria-label={`Remove ${m.name}`} className={btn} onClick={() => onRole(null)}>
            Remove
          </button>
        )}
      </td>
    </tr>
  )
}
