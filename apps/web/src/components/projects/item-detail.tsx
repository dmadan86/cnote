'use client'
import { X } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { CommentBody, MentionField } from '@/components/mention-field'
import { NoteEditor } from '@/components/note-editor'
import { personProps } from '@/components/person'
import { useShell } from '@/components/shell'
import { Avatar, btn, ErrorText, inputCls } from '@/components/ui'
import { type Activity, activity, activityRest, type Evt } from '@/lib/activity'
import { cmd, type NoteDetail } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import {
  AssigneeSelect,
  assigneeInput,
  assigneeValue,
  type ItemDetail,
  PRIORITIES,
  type Sprint,
  useAgents,
  useToast,
} from './shared'

type Comment = {
  id: string
  author_id: string | null
  author_name: string | null
  body: string
  created_at: string
}

const REL_KINDS = ['blocks', 'depends_on', 'bills']
const sel = 'w-full rounded-md border border-line bg-canvas px-2 py-1 text-[13px]'

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-line bg-side p-3">
      <h2 className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-muted">{title}</h2>
      <div className="space-y-2.5">{children}</div>
    </section>
  )
}

function Prop({ label, children }: { label: string; children: (id: string) => React.ReactNode }) {
  const id = `p-${label.replace(/\W/g, '')}`
  return (
    <div className="grid grid-cols-[88px_1fr] items-center gap-2">
      <label htmlFor={id} className="text-[12px] text-muted">
        {label}
      </label>
      {children(id)}
    </div>
  )
}

/** Versions and components have create commands but no list command yet, so we can only create-and-assign or clear. */
function RefProp({
  label,
  value,
  options,
  onPick,
  onCreate,
}: {
  label: string
  value: string | null
  options: { id: string; name: string }[]
  onPick: (id: string | null) => void
  onCreate: (name: string) => void
}) {
  return (
    <Prop label={label}>
      {(id) => (
        <div className="flex flex-col gap-1">
          <select
            id={id}
            value={value ?? ''}
            onChange={(e) => onPick(e.target.value || null)}
            className={sel}
          >
            <option value="">None</option>
            {options.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
          </select>
          <input
            aria-label={`New ${label.toLowerCase()}`}
            placeholder={`New ${label.toLowerCase()}, Enter`}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && e.currentTarget.value.trim()) {
                onCreate(e.currentTarget.value.trim())
                e.currentTarget.value = ''
              }
            }}
            className={sel}
          />
        </div>
      )}
    </Prop>
  )
}

export function ItemDetailView({ itemKey }: { itemKey: string }) {
  const { ws } = useShell()
  const agents = useAgents(ws)
  const { toast, push } = useToast()
  const item = useCmd<ItemDetail>(ws, 'item.get', { key: itemKey })
  const id = item.data?.id
  const note = useCmd<NoteDetail>(ws, 'note.get', { id }, !!id)
  const history = useCmd<Evt[]>(ws, 'note.history', { id }, !!id)
  const comments = useCmd<Comment[]>(ws, 'comment.list', { note_id: id }, !!id)
  const sprints = useCmd<Sprint[]>(ws, 'sprint.list', { space_id: item.data?.space_id }, !!item.data)
  type Ref = { id: string; name: string }
  const versions = useCmd<Ref[]>(ws, 'version.list', { space_id: item.data?.space_id }, !!item.data)
  const components = useCmd<Ref[]>(ws, 'component.list', { space_id: item.data?.space_id }, !!item.data)
  const [title, setTitle] = useState('')
  const [relErr, setRelErr] = useState<string>()
  const [cErr, setCErr] = useState<string>()

  // biome-ignore lint/correctness/useExhaustiveDependencies: only reset the draft title on navigation
  useEffect(() => {
    if (item.data) setTitle(item.data.title)
  }, [item.data?.id])

  const it = item.data
  if (item.error)
    return <p className="p-6 text-muted">This item does not exist or you do not have access to it.</p>
  if (!it) return null

  const refresh = () => {
    item.reload()
    history.reload()
  }
  async function act(fn: () => Promise<unknown>) {
    try {
      await fn()
    } catch (e) {
      push((e as Error).message)
    }
    refresh()
  }
  const update = (patch: object) => act(() => cmd(ws, 'item.update', { id: it.id, ...patch }))
  const create = (kind: 'version' | 'component', field: string) => (name: string) =>
    act(async () => {
      const r = await cmd<{ id: string }>(ws, `${kind}.create`, { space_id: it.space_id, name })
      await cmd(ws, 'item.update', { id: it.id, [field]: r.id })
      ;(kind === 'version' ? versions : components).reload()
    })

  async function addComment(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    try {
      await cmd(ws, 'comment.add', { note_id: it?.id, body: String(new FormData(form).get('body')) })
      form.reset()
      setCErr(undefined)
      comments.reload()
    } catch (x) {
      setCErr((x as Error).message)
    }
  }

  async function addRelation(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const f = new FormData(form)
    try {
      const other = await cmd<{ id: string }>(ws, 'item.get', {
        key: String(f.get('key')).trim().toUpperCase(),
      })
      await cmd(ws, 'relation.add', { from_id: it?.id, to_id: other.id, kind: f.get('kind') })
      form.reset()
      setRelErr(undefined)
      note.reload()
    } catch (x) {
      setRelErr((x as Error).message)
    }
  }

  const targets = [...new Set([it.status, ...it.transitions.map((t) => t.to)])]
  const timeline = [
    // The comments themselves are listed, so their "commented" events would only repeat them.
    ...activity((history.data ?? []).filter((h) => h.kind !== 'comment.added')).map((h) => ({
      at: h.at,
      h,
      c: undefined as Comment | undefined,
    })),
    ...(comments.data ?? []).map((c) => ({ at: c.created_at, c, h: undefined as Activity | undefined })),
  ].sort((a, b) => b.at.localeCompare(a.at))

  return (
    <div className="mx-auto grid max-w-6xl gap-6 px-6 py-8 lg:grid-cols-[1fr_320px]">
      <div className="min-w-0 space-y-8">
        <div>
          <p className="mb-1 text-[12px] text-muted">{it.key}</p>
          <input
            aria-label="Item title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onBlur={() => title.trim() && title.trim() !== it.title && update({ title: title.trim() })}
            onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
            className="w-full bg-transparent text-2xl font-bold"
          />
        </div>
        <section aria-label="Description">
          {note.data ? (
            <NoteEditor
              key={it.id}
              ws={ws}
              id={it.id}
              initialMd={note.data.content_md}
              onSaved={history.reload}
            />
          ) : (
            <p className="text-muted">Loading editor...</p>
          )}
        </section>
        <section aria-label="Timeline" className="space-y-3">
          <h2 className="text-[13px] font-semibold">Timeline</h2>
          <form onSubmit={addComment} className="space-y-2">
            <MentionField
              name="body"
              multiline
              required
              rows={3}
              aria-label="Add a comment"
              placeholder="Add a comment... (@ to mention)"
              className={inputCls}
            />
            <ErrorText error={cErr} />
            <button type="submit" className={btn}>
              Comment
            </button>
          </form>
          <ul className="space-y-3">
            {timeline.map((t) =>
              t.c ? (
                <li key={`c${t.c.id}`} className="rounded-md border border-line p-3">
                  <div className="flex items-center gap-2 text-[12px] text-muted">
                    <span {...personProps(t.c.author_id)} className="inline-flex items-center gap-2">
                      <Avatar name={t.c.author_name} size={18} /> {t.c.author_name ?? 'Agent'}
                    </span>{' '}
                    · {ago(t.c.created_at)}
                  </div>
                  <p className="mt-1 whitespace-pre-wrap">
                    <CommentBody body={t.c.body} />
                  </p>
                </li>
              ) : t.h ? (
                <li key={`h${t.h.id}`} className="flex justify-between text-[12px] text-muted">
                  <span>
                    <span {...personProps(t.h.userId)}>{t.h.who}</span> {activityRest(t.h)}
                  </span>
                  <span className="shrink-0">{ago(t.h.at)}</span>
                </li>
              ) : null,
            )}
          </ul>
        </section>
      </div>

      <aside aria-label="Properties" className="space-y-3">
        <Card title="Properties">
          <Prop label="Status">
            {(pid) => (
              <select
                id={pid}
                value={it.status}
                onChange={(e) => act(() => cmd(ws, 'item.transition', { id: it.id, to: e.target.value }))}
                className={sel}
              >
                {targets.map((s) => (
                  <option key={s}>{s}</option>
                ))}
              </select>
            )}
          </Prop>
          <Prop label="Priority">
            {(pid) => (
              <select
                id={pid}
                value={it.priority}
                onChange={(e) => update({ priority: e.target.value })}
                className={sel}
              >
                {PRIORITIES.map((p) => (
                  <option key={p}>{p}</option>
                ))}
              </select>
            )}
          </Prop>
          <Prop label="Assignee">
            {(pid) => (
              <AssigneeSelect
                id={pid}
                value={assigneeValue(it)}
                agents={agents}
                onChange={(v) => act(() => cmd(ws, 'item.assign', { id: it.id, ...assigneeInput(v) }))}
              />
            )}
          </Prop>
          <Prop label="Sprint">
            {(pid) => (
              <select
                id={pid}
                value={it.sprint_id ?? ''}
                onChange={(e) =>
                  update(e.target.value ? { sprint_id: e.target.value } : { clear: ['sprint'] })
                }
                className={sel}
              >
                <option value="">None</option>
                {(sprints.data ?? [])
                  .filter((s) => s.state !== 'closed' || s.id === it.sprint_id)
                  .map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
              </select>
            )}
          </Prop>
          <RefProp
            label="Version"
            value={it.fix_version_id}
            options={versions.data ?? []}
            onPick={(v) => update(v ? { fix_version_id: v } : { clear: ['fix_version'] })}
            onCreate={create('version', 'fix_version_id')}
          />
          <RefProp
            label="Component"
            value={it.component_id}
            options={components.data ?? []}
            onPick={(v) => update(v ? { component_id: v } : { clear: ['component'] })}
            onCreate={create('component', 'component_id')}
          />
          <Prop label="Labels">
            {(pid) => (
              <input
                id={pid}
                defaultValue={it.labels.join(', ')}
                placeholder="comma, separated"
                onBlur={(e) => {
                  const labels = e.target.value
                    .split(',')
                    .map((l) => l.trim())
                    .filter(Boolean)
                  if (labels.join() !== it.labels.join()) update({ labels })
                }}
                className={sel}
              />
            )}
          </Prop>
          <Prop label="Estimate">
            {(pid) => (
              <input
                id={pid}
                type="number"
                min={0}
                step="0.5"
                defaultValue={it.estimate ?? ''}
                onBlur={(e) => {
                  const v = e.target.value
                  if (v === String(it.estimate ?? '')) return
                  update(v === '' ? { clear: ['estimate'] } : { estimate: Number(v) })
                }}
                className={sel}
              />
            )}
          </Prop>
          <Prop label="Due date">
            {(pid) => (
              <input
                id={pid}
                type="date"
                value={it.due_date ?? ''}
                onChange={(e) =>
                  update(e.target.value ? { due_date: e.target.value } : { clear: ['due_date'] })
                }
                className={sel}
              />
            )}
          </Prop>
        </Card>

        <Card title="Relations">
          {note.data?.relations.length === 0 && <p className="text-[13px] text-muted">No relations yet.</p>}
          {note.data?.relations.map((r) => {
            const out = r.from_id === it.id
            return (
              <div key={r.id} className="flex items-center gap-2 text-[13px]">
                <span className="rounded bg-hover px-1 text-[11px] text-muted">
                  {out ? r.kind.replace('_', ' ') : `${r.kind.replace('_', ' ')} (incoming)`}
                </span>
                <Link
                  href={`/w/${ws}/n/${out ? r.to_id : r.from_id}`}
                  className="min-w-0 flex-1 truncate hover:underline"
                >
                  {r.other_title ?? 'Untitled'}
                </Link>
                {out && (
                  <button
                    type="button"
                    aria-label="Remove relation"
                    onClick={() =>
                      cmd(ws, 'relation.remove', { from_id: r.from_id, to_id: r.to_id, kind: r.kind }).then(
                        note.reload,
                        (e) => setRelErr(e.message),
                      )
                    }
                    className="rounded p-0.5 hover:bg-hover"
                  >
                    <X size={12} />
                  </button>
                )}
              </div>
            )
          })}
          <form onSubmit={addRelation} className="flex gap-1.5">
            <select
              name="kind"
              aria-label="Relation kind"
              className="rounded-md border border-line bg-canvas px-1 py-1 text-[12px]"
            >
              {REL_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k.replace('_', ' ')}
                </option>
              ))}
            </select>
            <input name="key" required aria-label="Related item key" placeholder="ENG-2" className={sel} />
            <button type="submit" className={btn}>
              Add
            </button>
          </form>
          <ErrorText error={relErr} />
        </Card>
      </aside>
      {toast}
    </div>
  )
}
