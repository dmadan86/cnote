'use client'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { AssigneeSelect, assigneeInput, PROJECT_TEMPLATES, useAgents } from './shared'

type Auto = {
  id: string
  space_id: string | null
  name: string
  enabled: boolean
  draft: boolean
  trigger:
    | { kind: 'event'; event: string; cnql?: string | null }
    | { kind: 'schedule'; every_minutes: number; cnql?: string | null }
  conditions: { cnql: string }[]
  actions: { type: string; to?: string; body?: string }[]
}
type Run = { id: number; status: string; log: unknown; created_at: string }

function Runs({ id }: { id: string }) {
  const { ws } = useShell()
  const runs = useCmd<Run[]>(ws, 'automation.runs', { automation_id: id, limit: 20 })
  if (!runs.data) return <p className="text-[13px] text-muted">Loading...</p>
  if (!runs.data.length) return <p className="text-[13px] text-muted">No runs yet.</p>
  return (
    <ul className="space-y-1 text-[12px]">
      {runs.data.map((r) => (
        <li key={r.id} className="flex gap-2">
          <Pill tone={r.status === 'ok' || r.status === 'success' ? 'ok' : 'warn'}>{r.status}</Pill>
          <span className="text-muted">{ago(r.created_at)}</span>
          <code className="min-w-0 flex-1 truncate">{JSON.stringify(r.log)}</code>
        </li>
      ))}
    </ul>
  )
}

function CreateDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  onCreated: () => void
}) {
  const { ws, spaces } = useShell()
  const agents = useAgents(ws)
  const [kind, setKind] = useState<'event' | 'schedule'>('event')
  const [action, setAction] = useState('transition')
  const [who, setWho] = useState('')
  const [err, setErr] = useState<string>()

  async function submit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    const str = (k: string) => String(f.get(k) ?? '').trim()
    const trigger =
      kind === 'event'
        ? { kind, event: str('event'), cnql: str('trigger_cnql') || null }
        : { kind, every_minutes: Number(f.get('every')), cnql: str('trigger_cnql') || null }
    const act =
      action === 'transition'
        ? { type: 'transition', to: str('to') }
        : action === 'comment'
          ? { type: 'comment', body: str('body') }
          : { type: 'assign', ...assigneeInput(who) }
    try {
      await cmd(ws, 'automation.create', {
        name: str('name'),
        space_id: str('space') || null,
        trigger,
        conditions: str('condition') ? [{ cnql: str('condition') }] : [],
        actions: [act],
      })
      setErr(undefined)
      onCreated()
      onClose()
    } catch (x) {
      setErr((x as Error).message)
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Create automation" className="max-w-lg">
      <form onSubmit={submit} className="space-y-3">
        <Field label="Name">{(id) => <input id={id} name="name" required className={inputCls} />}</Field>
        <Field label="Space">
          {(id) => (
            <select id={id} name="space" className={inputCls}>
              <option value="">Whole workspace (admin)</option>
              {spaces
                .filter((s) => PROJECT_TEMPLATES.includes(s.template))
                .map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
            </select>
          )}
        </Field>
        <Field label="Trigger">
          {(id) => (
            <select
              id={id}
              value={kind}
              onChange={(e) => setKind(e.target.value as typeof kind)}
              className={inputCls}
            >
              <option value="event">Event</option>
              <option value="schedule">Schedule</option>
            </select>
          )}
        </Field>
        {kind === 'event' ? (
          <Field label="Event">
            {(id) => (
              <input id={id} name="event" required defaultValue="item.transitioned" className={inputCls} />
            )}
          </Field>
        ) : (
          <Field label="Every (minutes)">
            {(id) => (
              <input
                id={id}
                name="every"
                type="number"
                min={1}
                required
                defaultValue={60}
                className={inputCls}
              />
            )}
          </Field>
        )}
        <Field label="Trigger CNQL (optional)">
          {(id) => (
            <input
              id={id}
              name="trigger_cnql"
              placeholder="priority = urgent"
              className={`${inputCls} font-mono`}
            />
          )}
        </Field>
        <Field label="Condition CNQL (optional)">
          {(id) => (
            <input
              id={id}
              name="condition"
              placeholder='status = "In Progress"'
              className={`${inputCls} font-mono`}
            />
          )}
        </Field>
        <Field label="Action">
          {(id) => (
            <select id={id} value={action} onChange={(e) => setAction(e.target.value)} className={inputCls}>
              <option value="transition">Transition</option>
              <option value="assign">Assign</option>
              <option value="comment">Comment</option>
            </select>
          )}
        </Field>
        {action === 'transition' && (
          <Field label="To status">{(id) => <input id={id} name="to" required className={inputCls} />}</Field>
        )}
        {action === 'comment' && (
          <Field label="Comment body">
            {(id) => <input id={id} name="body" required className={inputCls} />}
          </Field>
        )}
        {action === 'assign' && (
          <Field label="Assign to">
            {(id) => (
              <AssigneeSelect id={id} value={who} agents={agents} onChange={setWho} label="Assign to" />
            )}
          </Field>
        )}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btn}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Create automation
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function AutomationsView() {
  const { ws } = useShell()
  const list = useCmd<Auto[]>(ws, 'automation.list')
  const [dlg, setDlg] = useState(false)
  const [open, setOpen] = useState<string>()
  const [err, setErr] = useState<string>()

  async function toggle(a: Auto) {
    try {
      await cmd(ws, 'automation.update', { id: a.id, enabled: !a.enabled })
      setErr(undefined)
      list.reload()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  return (
    <div className="mx-auto max-w-3xl space-y-4 px-6 py-8">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold">Automations</h1>
        <button type="button" onClick={() => setDlg(true)} className={`${btnPrimary} ml-auto`}>
          Create automation
        </button>
      </div>
      <ErrorText error={err ?? list.error} />
      {list.data?.length === 0 && <p className="text-muted">No automations yet.</p>}
      <ul className="space-y-2">
        {list.data?.map((a) => (
          <li key={a.id} className="rounded-md border border-line p-3">
            <div className="flex items-center gap-2">
              <span className="font-medium">{a.name}</span>
              {a.draft && <Pill tone="warn">draft</Pill>}
              <Pill tone={a.enabled ? 'ok' : 'neutral'}>{a.enabled ? 'enabled' : 'disabled'}</Pill>
              <button type="button" onClick={() => toggle(a)} className={`${btn} ml-auto`}>
                {a.enabled ? 'Disable' : 'Enable'}
              </button>
              <button
                type="button"
                aria-expanded={open === a.id}
                onClick={() => setOpen(open === a.id ? undefined : a.id)}
                className={btn}
              >
                Run log
              </button>
            </div>
            <p className="mt-1 text-[12px] text-muted">
              {a.trigger.kind === 'event' ? `On ${a.trigger.event}` : `Every ${a.trigger.every_minutes} min`}
              {a.trigger.cnql ? ` where ${a.trigger.cnql}` : ''}
              {a.conditions.map((c) => ` and ${c.cnql}`)} then {a.actions.map((x) => x.type).join(', ')}
            </p>
            {open === a.id && (
              <div className="mt-2 border-t border-line pt-2">
                <Runs id={a.id} />
              </div>
            )}
          </li>
        ))}
      </ul>
      <CreateDialog open={dlg} onClose={() => setDlg(false)} onCreated={list.reload} />
    </div>
  )
}
