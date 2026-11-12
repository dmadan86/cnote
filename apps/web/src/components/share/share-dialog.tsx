'use client'
import { Link2, Lock, X } from 'lucide-react'
import NextLink from 'next/link'
import { useState } from 'react'
import { Avatar, btn, btnPrimary, ErrorText, inputCls, Modal } from '@/components/ui'
import { cmd, type Member, type Note } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { personProps } from '../person'

type Person = { user_id: string; name: string; email: string; level: 'view' | 'edit' }
type Perms = {
  restriction: Note['restriction']
  inherited_from: { id: string; title: string; restriction: string } | null
  people: Person[]
  can_manage: boolean
}

const RESTRICTIONS: [Note['restriction'], string][] = [
  ['none', 'Anyone with access to this space can view and edit'],
  ['edit', 'Anyone can view, only specific people can edit'],
  ['view', 'Only specific people can view or edit'],
]

/** Share button + dialog: page restriction, people with access, copy link. */
export function ShareButton({
  ws,
  noteId,
  restriction,
  members,
  onChange,
}: {
  ws: string
  noteId: string
  restriction: Note['restriction']
  members: Member[]
  onChange: () => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className={btn} onClick={() => setOpen(true)}>
        {restriction !== 'none' && <Lock size={14} aria-label="Restricted" />} Share
      </button>
      <ShareDialog
        open={open}
        onClose={() => setOpen(false)}
        ws={ws}
        noteId={noteId}
        members={members}
        onChange={onChange}
      />
    </>
  )
}

function ShareDialog({
  open,
  onClose,
  ws,
  noteId,
  members,
  onChange,
}: {
  open: boolean
  onClose: () => void
  ws: string
  noteId: string
  members: Member[]
  onChange: () => void
}) {
  const { data, reload } = useCmd<Perms>(ws, 'note.permissions', { note_id: noteId }, open)
  const [err, setErr] = useState<string>()
  const [query, setQuery] = useState('')
  const [level, setLevel] = useState<'view' | 'edit'>('view')
  const [copied, setCopied] = useState(false)

  async function run(name: string, input: unknown) {
    try {
      await cmd(ws, name, input)
      setErr(undefined)
      reload()
      onChange()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const listed = new Set(data?.people.map((p) => p.user_id))
  const candidates = members.filter((m) => !listed.has(m.user_id))
  const label = (m: Member) => `${m.name} (${m.email})`
  const picked = candidates.find((m) => label(m) === query || m.email === query)
  const can = data?.can_manage ?? false

  async function add(e: React.FormEvent) {
    e.preventDefault()
    if (!picked) return setErr('Pick a workspace member from the list')
    await run('note.share', { note_id: noteId, user_id: picked.user_id, level })
    setQuery('')
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(window.location.href)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setErr('Could not copy the link')
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Share page" className="max-w-lg">
      {!data ? (
        <p className="text-muted">Loading...</p>
      ) : (
        <div className="space-y-4">
          {can && (
            <form onSubmit={add} className="flex gap-2">
              <input
                aria-label="Add people"
                list="share-members"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Add people by name or email"
                className={inputCls}
              />
              <datalist id="share-members">
                {candidates.map((m) => (
                  <option key={m.user_id} value={label(m)} />
                ))}
              </datalist>
              <select
                aria-label="New person's access"
                value={level}
                onChange={(e) => setLevel(e.target.value as 'view' | 'edit')}
                className={`${inputCls} w-28`}
              >
                <option value="view">Can view</option>
                <option value="edit">Can edit</option>
              </select>
              <button type="submit" className={btnPrimary}>
                Add
              </button>
            </form>
          )}

          <section aria-label="People with access">
            <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted">
              People with access
            </h3>
            {data.people.length === 0 && (
              <p className="text-[13px] text-muted">No one is listed. Space rules apply.</p>
            )}
            <ul className="space-y-1">
              {data.people.map((p) => (
                <li key={p.user_id} className="flex items-center gap-2 text-[13px]">
                  <span {...personProps(p.user_id)}>
                    <Avatar name={p.name} size={24} />
                  </span>
                  <span className="min-w-0 flex-1 truncate">
                    {p.name} <span className="text-muted">{p.email}</span>
                  </span>
                  <select
                    aria-label={`Access for ${p.name}`}
                    value={p.level}
                    disabled={!can}
                    onChange={(e) =>
                      run('note.share', { note_id: noteId, user_id: p.user_id, level: e.target.value })
                    }
                    className="rounded border border-line bg-canvas px-1 py-0.5"
                  >
                    <option value="view">Can view</option>
                    <option value="edit">Can edit</option>
                  </select>
                  {can && (
                    <button
                      type="button"
                      aria-label={`Remove ${p.name}`}
                      onClick={() => run('note.unshare', { note_id: noteId, user_id: p.user_id })}
                      className="rounded p-1 hover:bg-hover"
                    >
                      <X size={14} />
                    </button>
                  )}
                </li>
              ))}
            </ul>
          </section>

          <section aria-label="General access" className="space-y-1.5">
            <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">General access</h3>
            <div className="flex items-center gap-2">
              <Lock size={14} className="shrink-0 text-muted" />
              <select
                aria-label="Restriction"
                value={data.restriction}
                disabled={!can}
                onChange={(e) => run('note.restrict', { note_id: noteId, restriction: e.target.value })}
                className={inputCls}
              >
                {RESTRICTIONS.map(([k, text]) => (
                  <option key={k} value={k}>
                    {text}
                  </option>
                ))}
              </select>
            </div>
            {data.inherited_from && (
              <p className="rounded bg-warn-bg px-2 py-1 text-[12px] text-warn">
                Inherited from{' '}
                {data.inherited_from.title ? (
                  <NextLink href={`/w/${ws}/n/${data.inherited_from.id}`} className="underline">
                    {data.inherited_from.title}
                  </NextLink>
                ) : (
                  'a restricted parent page'
                )}
                . Sub-pages follow their parent's restriction.
              </p>
            )}
            {!can && (
              <p className="text-[12px] text-muted">Only people who can edit this page can change this.</p>
            )}
          </section>

          <ErrorText error={err} />
          <div className="flex items-center justify-end gap-2">
            <button type="button" className={btn} onClick={copy}>
              <Link2 size={14} /> {copied ? 'Copied' : 'Copy link'}
            </button>
            <button type="button" className={btnPrimary} onClick={onClose}>
              Done
            </button>
          </div>
        </div>
      )}
    </Modal>
  )
}
