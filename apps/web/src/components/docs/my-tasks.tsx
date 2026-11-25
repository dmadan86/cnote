'use client'
import { CheckCircle2, Circle, ListChecks } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { type MyTask, taskMine } from '@/lib/docs'
import { formatDate, todayIso } from '../editor/blocks'
import { EmptyState, ErrorText } from '../ui'
import { GROUPS, type Group, groupOf } from './task-groups'

// "My tasks" (docs-parity-b 4.4): the to-do items assigned to me across pages, grouped by due date. A row opens the page at the
// task; ticking is done on the page.

export function MyTasks({ ws }: { ws: string }) {
  const [tasks, setTasks] = useState<MyTask[]>()
  const [err, setErr] = useState<string>()
  const [showDone, setShowDone] = useState(false)
  useEffect(() => {
    taskMine(ws, { done: true, limit: 200 }).then(setTasks, (e: Error) => setErr(e.message))
  }, [ws])

  const today = todayIso()
  const open = (tasks ?? []).filter((t) => !t.done)
  const shown = showDone ? (tasks ?? []) : open
  const by = (g: Group) => shown.filter((t) => groupOf(t, today) === g)

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-8">
      <div className="mb-5 flex items-center justify-between gap-3">
        <h1 className="text-[22px] font-semibold">My tasks</h1>
        <label className="flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            checked={showDone}
            onChange={(e) => setShowDone(e.target.checked)}
            className="h-4 w-4"
          />
          Show completed
        </label>
      </div>
      <p aria-live="polite" className="mb-4 text-[13px] text-muted">
        {tasks ? `${open.length} open` : 'Loading...'}
      </p>
      <ErrorText error={err} />
      {tasks && shown.length === 0 && (
        <EmptyState icon={ListChecks} title="Nothing assigned to you">
          Mention someone in a to-do item on any page (@name) and it shows up in their list. Add a date to
          give it a due day.
        </EmptyState>
      )}
      {GROUPS.map((g) => {
        const rows = by(g)
        if (rows.length === 0) return null
        return (
          <section key={g} aria-labelledby={`tasks-${g}`} className="mb-6">
            <h2 id={`tasks-${g}`} className="mb-1 text-[13px] font-semibold text-muted">
              {g} <span className="font-normal">({rows.length})</span>
            </h2>
            <ul className="divide-y divide-line rounded-lg border border-line">
              {rows.map((t) => (
                <li key={`${t.note_id}:${t.ord}`}>
                  <Link
                    href={`/w/${ws}/n/${t.note_id}#task-${t.ord}`}
                    className="flex items-start gap-3 px-3 py-2.5 hover:bg-hover focus-visible:outline focus-visible:outline-2"
                  >
                    {t.done ? (
                      <CheckCircle2 size={16} className="mt-0.5 shrink-0 text-muted" aria-label="Done" />
                    ) : (
                      <Circle size={16} className="mt-0.5 shrink-0 text-muted" aria-label="Open" />
                    )}
                    <span className="min-w-0 flex-1">
                      <span className={`block text-[14px] ${t.done ? 'text-muted line-through' : ''}`}>
                        {t.text || 'Untitled task'}
                      </span>
                      <span className="block truncate text-[12px] text-muted">
                        <span aria-hidden="true">{t.icon ?? '📄'} </span>
                        {t.note_title || 'Untitled'}
                      </span>
                    </span>
                    {t.due && (
                      <span
                        className={`shrink-0 rounded-full border px-2 py-0.5 text-[12px] ${
                          g === 'Overdue' && !t.done ? 'border-bad text-bad' : 'border-line text-muted'
                        }`}
                      >
                        {g === 'Overdue' && !t.done
                          ? 'Past due · '
                          : g === 'Today' && !t.done
                            ? 'Due today · '
                            : ''}
                        {formatDate(t.due)}
                      </span>
                    )}
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}
