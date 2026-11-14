'use client'
import Link from 'next/link'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { Avatar, btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

export type Person = {
  user_id: string
  email: string
  name: string
  role: string
  department_id: string | null
  department: string | null
  manager_user_id: string | null
  manager_name: string | null
  designation: string | null
  phone: string | null
  joined_on: string | null
  left_on: string | null
  finance_access: boolean
  deactivated_at: string | null
}
type Dept = { id: string; name: string; head_name: string | null; members: number }

export function PeopleDirectory() {
  const { ws, me } = useShell()
  const people = useCmd<Person[]>(ws, 'people.list')
  const depts = useCmd<Dept[]>(ws, 'department.list')
  const [q, setQ] = useState('')
  const [dept, setDept] = useState('')
  const [inactive, setInactive] = useState(false)
  const [edit, setEdit] = useState<Person>()
  const role = me.workspaces.find((w) => w.id === ws)?.role
  const admin = role === 'owner' || role === 'admin'
  const reload = () => {
    people.reload()
    depts.reload()
  }
  const rows = (people.data ?? []).filter(
    (p) =>
      (inactive || !p.deactivated_at) &&
      (!dept || p.department_id === dept) &&
      `${p.name} ${p.email} ${p.designation ?? ''}`.toLowerCase().includes(q.toLowerCase()),
  )
  return (
    <div className="mx-auto max-w-4xl space-y-8 px-6 py-8">
      <h1 className="text-xl font-semibold">People</h1>
      <section aria-label="Directory" className="space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-48 flex-1">
            <Field label="Search">
              {(id) => (
                <input
                  id={id}
                  type="search"
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  className={inputCls}
                />
              )}
            </Field>
          </div>
          <Field label="Department">
            {(id) => (
              <select id={id} value={dept} onChange={(e) => setDept(e.target.value)} className={inputCls}>
                <option value="">All</option>
                {depts.data?.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                  </option>
                ))}
              </select>
            )}
          </Field>
          <label className="flex items-center gap-2 pb-2 text-[13px]">
            <input type="checkbox" checked={inactive} onChange={(e) => setInactive(e.target.checked)} />
            Include inactive
          </label>
        </div>
        <ul aria-label="People" className="divide-y divide-line rounded-md border border-line">
          {rows.map((p) => (
            <li
              key={p.user_id}
              className={`flex flex-wrap items-center gap-3 px-3 py-2 ${p.deactivated_at ? 'opacity-70' : ''}`}
            >
              <Avatar name={p.name} size={32} />
              <div className="min-w-0 flex-1">
                <Link href={`/w/${ws}/people/${p.user_id}`} className="font-medium hover:underline">
                  {p.name}
                </Link>
                {p.deactivated_at && <Pill tone="neutral">Inactive</Pill>}
                <p className="truncate text-[12px] text-muted">
                  {[p.designation, p.department, p.manager_name && `reports to ${p.manager_name}`]
                    .filter(Boolean)
                    .join(' · ') || p.email}
                </p>
              </div>
              <span className="rounded bg-hover px-1.5 text-[12px] capitalize">{p.role}</span>
              {admin && !p.finance_access && <Pill tone="warn">No finance</Pill>}
              {admin && (
                <button
                  type="button"
                  className={btn}
                  aria-label={`Edit ${p.name}`}
                  onClick={() => setEdit(p)}
                >
                  Edit
                </button>
              )}
            </li>
          ))}
          {people.data && rows.length === 0 && (
            <li className="px-3 py-4 text-[13px] text-muted">No one matches.</li>
          )}
        </ul>
      </section>
      {admin && <Departments ws={ws} depts={depts.data ?? []} onChange={reload} />}
      {edit && (
        <EditPerson
          key={edit.user_id}
          ws={ws}
          person={edit}
          all={people.data ?? []}
          depts={depts.data ?? []}
          meId={me.user.id}
          onClose={() => setEdit(undefined)}
          onSaved={() => {
            setEdit(undefined)
            reload()
          }}
        />
      )}
    </div>
  )
}

function EditPerson({
  ws,
  person: p,
  all,
  depts,
  meId,
  onClose,
  onSaved,
}: {
  ws: string
  person: Person
  all: Person[]
  depts: Dept[]
  meId: string
  onClose: () => void
  onSaved: () => void
}) {
  const [err, setErr] = useState<string>()
  const [confirm, setConfirm] = useState(false)
  const run = async (name: string, input: object) => {
    try {
      await cmd(ws, name, input)
      onSaved()
    } catch (x) {
      setErr((x as Error).message)
      setConfirm(false)
    }
  }
  return (
    <Modal open onClose={onClose} title={`Edit ${p.name}`} className="max-w-lg">
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          const t = (k: string) => String(f.get(k) ?? '').trim()
          run('member.update', {
            user_id: p.user_id,
            ...(p.role !== 'owner' && { role: t('role') }),
            department_id: t('department_id') || null,
            manager_user_id: t('manager_user_id') || null,
            designation: t('designation') || null,
            phone: t('phone') || null,
            finance_access: f.get('finance_access') === 'on',
          })
        }}
      >
        <p className="text-[12px] text-muted">{p.email}</p>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Role">
            {(id) =>
              p.role === 'owner' ? (
                <input id={id} value="owner" readOnly className={inputCls} />
              ) : (
                <select id={id} name="role" defaultValue={p.role} className={inputCls}>
                  <option>admin</option>
                  <option>member</option>
                  <option>guest</option>
                </select>
              )
            }
          </Field>
          <Field label="Department">
            {(id) => (
              <select id={id} name="department_id" defaultValue={p.department_id ?? ''} className={inputCls}>
                <option value="">None</option>
                {depts.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                  </option>
                ))}
              </select>
            )}
          </Field>
          <Field label="Reports to">
            {(id) => (
              <select
                id={id}
                name="manager_user_id"
                defaultValue={p.manager_user_id ?? ''}
                className={inputCls}
              >
                <option value="">No one</option>
                {all
                  .filter((o) => o.user_id !== p.user_id && !o.deactivated_at)
                  .map((o) => (
                    <option key={o.user_id} value={o.user_id}>
                      {o.name}
                    </option>
                  ))}
              </select>
            )}
          </Field>
          <Field label="Designation">
            {(id) => (
              <input id={id} name="designation" defaultValue={p.designation ?? ''} className={inputCls} />
            )}
          </Field>
          <Field label="Phone">
            {(id) => (
              <input
                id={id}
                name="phone"
                type="tel"
                defaultValue={p.phone ?? ''}
                placeholder="+91 98765 43210"
                className={inputCls}
              />
            )}
          </Field>
        </div>
        <label className="flex items-start gap-2 text-[13px]">
          <input type="checkbox" name="finance_access" defaultChecked={p.finance_access} className="mt-1" />
          <span>
            Can see finance
            <span className="block text-[12px] text-muted">
              Invoices, bills, payments and finance reports. Admins and owners always can.
            </span>
          </span>
        </label>
        <ErrorText error={err} />
        <div className="flex flex-wrap items-center gap-2">
          <button type="submit" className={btnPrimary}>
            Save
          </button>
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          {p.deactivated_at ? (
            <button
              type="button"
              className={`${btn} ml-auto`}
              onClick={() => run('member.reactivate', { user_id: p.user_id })}
            >
              Reactivate
            </button>
          ) : (
            p.user_id !== meId && (
              <button type="button" className={`${btn} ml-auto text-bad`} onClick={() => setConfirm(true)}>
                Deactivate
              </button>
            )
          )}
        </div>
      </form>
      <Modal open={confirm} onClose={() => setConfirm(false)} title={`Deactivate ${p.name}?`}>
        <p className="mb-3 text-[13px]">
          {p.name} will lose access to this workspace and their API keys stop working. Their pages, comments
          and records stay. You can reactivate them later, but their old API keys stay revoked.
        </p>
        <div className="flex gap-2">
          <button
            type="button"
            className={btnPrimary}
            onClick={() => run('member.deactivate', { user_id: p.user_id })}
          >
            Deactivate {p.name}
          </button>
          <button type="button" className={btn} onClick={() => setConfirm(false)}>
            Cancel
          </button>
        </div>
      </Modal>
    </Modal>
  )
}

function Departments({ ws, depts, onChange }: { ws: string; depts: Dept[]; onChange: () => void }) {
  const [err, setErr] = useState<string>()
  const [del, setDel] = useState<Dept>()
  const run = async (name: string, input: object) => {
    try {
      await cmd(ws, name, input)
      setErr(undefined)
      setDel(undefined)
      onChange()
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <section aria-label="Departments" className="space-y-3">
      <h2 className="text-[14px] font-semibold">Departments</h2>
      <ul className="divide-y divide-line rounded-md border border-line">
        {depts.map((d) => (
          <li key={d.id} className="flex items-center gap-3 px-3 py-2">
            <form
              className="flex min-w-0 flex-1 items-center gap-2"
              onSubmit={(e) => {
                e.preventDefault()
                const name = String(new FormData(e.currentTarget).get('name') ?? '')
                if (name !== d.name) run('department.update', { id: d.id, name })
              }}
            >
              <input
                aria-label={`Name of ${d.name}`}
                name="name"
                defaultValue={d.name}
                className={`${inputCls} max-w-xs`}
              />
              <button type="submit" className={btn}>
                Rename
              </button>
            </form>
            <span className="text-[12px] text-muted">{d.members} people</span>
            <button type="button" className={btn} aria-label={`Delete ${d.name}`} onClick={() => setDel(d)}>
              Delete
            </button>
          </li>
        ))}
        {depts.length === 0 && <li className="px-3 py-3 text-[13px] text-muted">No departments yet.</li>}
      </ul>
      <form
        className="flex max-w-md items-end gap-2"
        onSubmit={async (e) => {
          e.preventDefault()
          const form = e.currentTarget
          await run('department.create', { name: new FormData(form).get('name') })
          form.reset()
        }}
      >
        <div className="flex-1">
          <Field label="New department">
            {(id) => <input id={id} name="name" required className={inputCls} />}
          </Field>
        </div>
        <button type="submit" className={btnPrimary}>
          Add department
        </button>
      </form>
      <ErrorText error={err} />
      <Modal open={!!del} onClose={() => setDel(undefined)} title={`Delete ${del?.name ?? ''}?`}>
        <p className="mb-3 text-[13px]">
          {del?.members ? `${del.members} people will be left without a department.` : 'No one is in it.'}
        </p>
        <div className="flex gap-2">
          <button
            type="button"
            className={btnPrimary}
            onClick={() => del && run('department.delete', { id: del.id })}
          >
            Delete department
          </button>
          <button type="button" className={btn} onClick={() => setDel(undefined)}>
            Cancel
          </button>
        </div>
      </Modal>
    </section>
  )
}
