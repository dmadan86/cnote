'use client'
import { Plus } from 'lucide-react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
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
