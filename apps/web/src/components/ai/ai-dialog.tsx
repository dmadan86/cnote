'use client'
import { useEffect, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { cmd, type NoteDetail } from '@/lib/api'

export type AiMode = 'summarize' | 'extract' | 'draft'
type Task = { title: string; assignee?: string | null; due?: string | null }

const TITLES: Record<AiMode, string> = {
  summarize: 'Summary',
  extract: 'Extract tasks',
  draft: 'Draft with AI',
}

/** Summarize / extract tasks / draft: results are shown for review; nothing is saved without a click. */
export function AiDialog({
  ws,
  mode,
  note,
  onClose,
}: {
  ws: string
  mode: AiMode
  note: NoteDetail
  onClose: () => void
}) {
  const { spaces } = useShell()
  const projects = spaces.filter((s) => s.template === 'software' || s.template === 'kanban')
  const [text, setText] = useState<string>()
  const [tasks, setTasks] = useState<Task[]>()
  const [picked, setPicked] = useState<Set<number>>(new Set())
  const [target, setTarget] = useState(projects[0]?.id ?? '')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(mode !== 'draft')
  const [made, setMade] = useState<number>()

  // biome-ignore lint/correctness/useExhaustiveDependencies: one call per open
  useEffect(() => {
    if (mode === 'draft') return
    const run =
      mode === 'summarize'
        ? cmd<{ text: string }>(ws, 'ai.summarize', { note_id: note.id }).then((r) => setText(r.text))
        : cmd<{ suggested_tasks: Task[] }>(ws, 'ai.extract_tasks', { note_id: note.id }).then((r) => {
            setTasks(r.suggested_tasks)
            setPicked(new Set(r.suggested_tasks.map((_, i) => i)))
          })
    run.catch((e) => setErr(e.message)).finally(() => setBusy(false))
  }, [])

  async function create() {
    setBusy(true)
    try {
      const sel = (tasks ?? []).filter((_, i) => picked.has(i))
      for (const t of sel) await cmd(ws, 'item.create', { space_id: target, title: t.title })
      setMade(sel.length)
      setErr(undefined)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal open onClose={onClose} title={TITLES[mode]} className="max-w-xl">
      <div className="space-y-3">
        {busy && <p className="text-muted">Working...</p>}
        {mode === 'draft' && (
          <form
            className="space-y-2"
            onSubmit={async (e) => {
              e.preventDefault()
              setBusy(true)
              try {
                const r = await cmd<{ text: string }>(ws, 'ai.draft', {
                  prompt: new FormData(e.currentTarget).get('prompt'),
                  note_id: note.id,
                })
                setText(r.text)
                setErr(undefined)
              } catch (x) {
                setErr((x as Error).message)
              } finally {
                setBusy(false)
              }
            }}
          >
            <Field label="What should it write?">
              {(id) => <textarea id={id} name="prompt" required rows={3} className={inputCls} />}
            </Field>
            <button type="submit" disabled={busy} className={btnPrimary}>
              Draft
            </button>
          </form>
        )}
        {text && (
          <pre className="max-h-72 overflow-auto whitespace-pre-wrap rounded-md bg-side p-3 font-sans text-[13px]">
            {text}
          </pre>
        )}
        {text && mode === 'draft' && (
          <button type="button" className={btn} onClick={() => navigator.clipboard?.writeText(text)}>
            Copy draft
          </button>
        )}
        {tasks?.length === 0 && <p className="text-muted">No tasks found in this page.</p>}
        {tasks && tasks.length > 0 && (
          <>
            <ul className="space-y-1">
              {tasks.map((t, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: static list
                <li key={i}>
                  <label className="flex items-start gap-2 text-[14px]">
                    <input
                      type="checkbox"
                      className="mt-1"
                      checked={picked.has(i)}
                      onChange={(e) => {
                        const n = new Set(picked)
                        if (e.target.checked) n.add(i)
                        else n.delete(i)
                        setPicked(n)
                      }}
                    />
                    <span>
                      {t.title}
                      {(t.assignee || t.due) && (
                        <span className="ml-2 text-[12px] text-muted">
                          {[t.assignee, t.due].filter(Boolean).join(' · ')}
                        </span>
                      )}
                    </span>
                  </label>
                </li>
              ))}
            </ul>
            {projects.length === 0 ? (
              <p className="text-muted">Create a software or kanban space to receive the tasks.</p>
            ) : (
              <div className="flex items-end gap-2">
                <div className="flex-1">
                  <Field label="Project space">
                    {(id) => (
                      <select
                        id={id}
                        value={target}
                        onChange={(e) => setTarget(e.target.value)}
                        className={inputCls}
                      >
                        {projects.map((s) => (
                          <option key={s.id} value={s.id}>
                            {s.name}
                          </option>
                        ))}
                      </select>
                    )}
                  </Field>
                </div>
                <button
                  type="button"
                  disabled={busy || picked.size === 0 || !target}
                  onClick={create}
                  className={btnPrimary}
                >
                  Create selected
                </button>
              </div>
            )}
            {made !== undefined && (
              <p role="status" className="text-[13px] text-ok">
                Created {made} item{made === 1 ? '' : 's'}.
              </p>
            )}
          </>
        )}
        <ErrorText error={err} />
      </div>
    </Modal>
  )
}
