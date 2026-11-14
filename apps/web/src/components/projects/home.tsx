'use client'
import { Plus } from 'lucide-react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd, type Space } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { type Item, PROJECT_TEMPLATES } from './shared'

function CreateProject({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { ws, reload } = useShell()
  const router = useRouter()
  const [err, setErr] = useState<string>()
  async function submit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    try {
      const s = await cmd<Space>(ws, 'space.create', {
        name: f.get('name'),
        key: String(f.get('key')).toUpperCase(),
        template: f.get('template'),
        visibility: 'open',
      })
      reload()
      onClose()
      router.push(`/w/${ws}/projects/${s.key}`)
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  return (
    <Modal open={open} onClose={onClose} title="Create project">
      <form onSubmit={submit} className="space-y-3">
        <Field label="Name">{(id) => <input id={id} name="name" required className={inputCls} />}</Field>
        <Field label="Key">
          {(id) => (
            <input
              id={id}
              name="key"
              required
              maxLength={10}
              placeholder="ENG"
              className={`${inputCls} uppercase`}
            />
          )}
        </Field>
        <Field label="Template">
          {(id) => (
            <select id={id} name="template" defaultValue="software" className={inputCls}>
              {PROJECT_TEMPLATES.map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          )}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btn}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Create project
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function ProjectsHome() {
  const { ws, spaces } = useShell()
  const [dlg, setDlg] = useState(false)
  const mine = spaces.filter((s) => PROJECT_TEMPLATES.includes(s.template))
  // One query for all counts: items are few per workspace page, and the API caps at 200 anyway.
  const items = useCmd<Item[]>(ws, 'item.list', { limit: 200 })
  // home tile: ?due=me lists my open items due today or earlier (the same rule as the tile)
  const dueMe = useSearchParams().get('due') === 'me'
  const mineDue = useCmd<Item[]>(
    ws,
    'item.list',
    {
      limit: 50,
      cnql: `assignee = currentUser() AND statusCategory != "Done" AND due <= ${new Date().toISOString().slice(0, 10)} ORDER BY due ASC`,
    },
    dueMe,
  )
  const count = (id: string, open: boolean) =>
    (items.data ?? []).filter(
      (i) => i.space_id === id && !i.triage && (!open || i.status_category !== 'done'),
    ).length

  return (
    <div className="mx-auto max-w-3xl space-y-4 px-6 py-8">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold">Projects</h1>
        <Link href={`/w/${ws}/projects/triage`} className={`${btn} ml-auto`}>
          Triage
        </Link>
        <Link href={`/w/${ws}/projects/automations`} className={btn}>
          Automations
        </Link>
        <button type="button" onClick={() => setDlg(true)} className={btnPrimary}>
          <Plus size={14} /> New project
        </button>
      </div>
      {dueMe && (
        <section aria-labelledby="due-me" className="rounded-lg border border-line p-4">
          <h2 id="due-me" className="mb-2 text-[13px] font-semibold text-muted">
            My tasks due or overdue
          </h2>
          {mineDue.data?.length === 0 && <p className="text-muted">Nothing due. You are caught up.</p>}
          <ul className="space-y-1">
            {mineDue.data?.map((i) => (
              <li key={i.id} className="flex gap-2">
                <Link href={`/w/${ws}/projects/${i.key}`} className="min-w-0 flex-1 truncate hover:underline">
                  <span className="text-muted">{i.key}</span> {i.title}
                </Link>
                <span className="text-[13px] text-muted">{i.due_date}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {mine.length === 0 && (
        <p className="text-muted">No projects yet. Create one to start tracking items.</p>
      )}
      <ul className="grid gap-3 sm:grid-cols-2">
        {mine.map((s) => (
          <li key={s.id}>
            <Link
              href={`/w/${ws}/projects/${s.key}`}
              className="block rounded-lg border border-line p-4 hover:bg-hover"
            >
              <div className="flex items-center gap-2">
                <span className="font-semibold">{s.name}</span>
                <Pill>{s.key}</Pill>
                <Pill>{s.template}</Pill>
              </div>
              <p className="mt-2 text-[13px] text-muted">
                {items.data ? `${count(s.id, true)} open · ${count(s.id, false)} total` : '...'}
              </p>
            </Link>
          </li>
        ))}
      </ul>
      <CreateProject open={dlg} onClose={() => setDlg(false)} />
    </div>
  )
}
