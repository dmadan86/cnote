'use client'
import { BookOpen, FileSpreadsheet, Package, Scale, Upload, Users } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, EmptyState, ErrorText, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { DocxImportButton } from './docx-button'
import { ImportForm } from './import-form'
import { JobList } from './job-list'
import { jobHref, KIND_LABEL } from './lib'
import type { DataJob, Job } from './types'

const CARDS = [
  { href: 'contacts', icon: Users, title: 'Contacts', desc: 'Customers and vendors from a CSV file.' },
  { href: 'items', icon: Package, title: 'Items', desc: 'Products with GST rate, HSN and opening stock.' },
  {
    href: 'opening',
    icon: Scale,
    title: 'Opening balances',
    desc: 'What you had and owed on your go-live date.',
  },
  {
    href: 'tally',
    icon: BookOpen,
    title: 'From Tally',
    desc: 'Parties, items and balances from TallyPrime.',
  },
]
const tone = {
  preview: 'neutral',
  queued: 'neutral',
  running: 'warn',
  done: 'ok',
  failed: 'bad',
  undone: 'neutral',
} as const
const stateLabel = {
  preview: 'Not started',
  queued: 'Queued',
  running: 'Running',
  done: 'Done',
  failed: 'Failed',
  undone: 'Undone',
}

export function ImportHub() {
  const { ws, spaces } = useShell()
  const data = useCmd<DataJob[]>(ws, 'data_import.list')
  const jobs = useCmd<Job[]>(ws, 'import.list')
  const [undo, setUndo] = useState<DataJob>()
  const [err, setErr] = useState<string>()
  const [wordSpace, setWordSpace] = useState('')
  const live = data.data?.some((j) => j.status === 'queued' || j.status === 'running')
  const { reload } = data
  useEffect(() => {
    if (!live) return
    const t = setInterval(reload, 1500)
    return () => clearInterval(t)
  }, [live, reload])
  const liveOld = jobs.data?.some((j) => j.status === 'queued' || j.status === 'running')
  const reloadOld = jobs.reload
  useEffect(() => {
    if (!liveOld) return
    const t = setInterval(reloadOld, 1500)
    return () => clearInterval(t)
  }, [liveOld, reloadOld])
  const canUndo = (j: DataJob) =>
    j.status === 'done' && (!j.undo_until || new Date(j.undo_until).getTime() > Date.now())

  return (
    <div className="mx-auto max-w-3xl space-y-8 px-6 py-8">
      <header>
        <h1 className="text-xl font-semibold">Import data</h1>
        <p className="mt-1 text-[13px] text-muted">
          Bring in customers, items, opening balances or your Tally data.
        </p>
      </header>
      <div className="grid gap-3 sm:grid-cols-2">
        {CARDS.map((c) => (
          <Link
            key={c.href}
            href={`/w/${ws}/import/${c.href}`}
            className="flex items-start gap-3 rounded-lg border border-line p-4 hover:bg-hover"
          >
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-line text-muted">
              <c.icon size={16} aria-hidden />
            </span>
            <span>
              <span className="block text-[14px] font-semibold">{c.title}</span>
              <span className="block text-[13px] text-muted">{c.desc}</span>
            </span>
          </Link>
        ))}
      </div>

      <section aria-label="Word document" className="space-y-2">
        <h2 className="text-[15px] font-semibold">Word document</h2>
        <p className="text-[13px] text-muted">
          One .docx file becomes one page: headings, lists, tables, links and pictures are kept.
        </p>
        <div className="flex flex-wrap items-start gap-2">
          <select
            aria-label="Space for the Word import"
            className="rounded-md border border-line bg-surface px-2 py-1.5 text-[13px]"
            value={wordSpace || spaces[0]?.id || ''}
            onChange={(e) => setWordSpace(e.target.value)}
          >
            {spaces.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
          {(wordSpace || spaces[0]?.id) && <DocxImportButton spaceId={wordSpace || spaces[0].id} />}
        </div>
      </section>

      <section aria-label="Recent imports" className="space-y-3">
        <h2 className="text-[15px] font-semibold">Recent imports</h2>
        <ErrorText error={err} />
        {data.error && <p className="text-muted">Data imports are available to workspace admins.</p>}
        {data.data?.length === 0 && (
          <EmptyState compact icon={FileSpreadsheet} title="No imports yet">
            Pick one of the cards above to start.
          </EmptyState>
        )}
        <ul className="space-y-2">
          {data.data?.map((j) => (
            <li
              key={j.id}
              className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-line p-3"
            >
              <span className="font-medium">{KIND_LABEL[j.kind] ?? j.kind}</span>
              <Pill tone={tone[j.status]}>{stateLabel[j.status] ?? j.status}</Pill>
              <span className="text-[13px] text-muted tabular-nums">
                {j.added ?? 0} added · {j.skipped ?? 0} skipped
              </span>
              <span className="ml-auto text-[12px] text-muted">{ago(j.created_at)}</span>
              <Link href={jobHref(ws, j)} className={btn}>
                View
              </Link>
              {canUndo(j) && (
                <button type="button" className={btn} onClick={() => setUndo(j)}>
                  Undo
                </button>
              )}
            </li>
          ))}
        </ul>
        <p className="text-[12px] text-muted">You can undo an import for 7 days after it finishes.</p>
      </section>

      <Modal open={!!undo} onClose={() => setUndo(undefined)} title="Undo this import?">
        <div className="space-y-3 text-[13px]">
          <p>
            This removes what the import added. Anything already used on an invoice or bill, or paid, cannot
            be removed, and then nothing is undone. Skipped rows and things you added yourself are not
            touched. You can undo an import for 7 days after it finishes.
          </p>
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setUndo(undefined)}>
              Keep it
            </button>
            <button
              type="button"
              className={btnPrimary}
              onClick={async () => {
                try {
                  await cmd(ws, 'data_import.undo', { id: undo?.id })
                  setErr(undefined)
                } catch (x) {
                  setErr((x as Error).message)
                }
                setUndo(undefined)
                reload()
              }}
            >
              Undo import
            </button>
          </div>
        </div>
      </Modal>

      <section aria-label="Other sources" className="space-y-4">
        <h2 className="flex items-center gap-2 text-[15px] font-semibold">
          <Upload size={15} aria-hidden /> Other sources
        </h2>
        <p className="-mt-2 text-[13px] text-muted">Jira, Confluence, Notion, Trello and CSV pages.</p>
        <ImportForm ws={ws} onStarted={jobs.reload} />
        <JobList jobs={jobs.data} error={jobs.error} />
      </section>
    </div>
  )
}
