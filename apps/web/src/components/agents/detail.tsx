'use client'
import { ArrowLeft, Play } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { btn, btnPrimary, ErrorText, Pill } from '@/components/ui'
import type { Agent } from '@/lib/agents'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { RunsTable, toolLabel } from './runs'

export const triggerText = (a: Pick<Agent, 'events' | 'every_minutes' | 'next_run_at'>) => {
  const t = [...a.events.map((e) => `when ${e.replace(/[._]/g, ' ')}`)]
  if (a.every_minutes) t.push(a.every_minutes % 1440 === 0 ? 'every day' : `every ${a.every_minutes} minutes`)
  return t.length ? t.join(', ') : 'When someone asks'
}

export function AgentDetail({
  ws,
  id,
  admin,
  back,
}: {
  ws: string
  id: string
  admin: boolean
  back: string
}) {
  const { data: a, error, reload } = useCmd<Agent>(ws, 'agent.get', { id })
  const [text, setText] = useState('')
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  const [tick, setTick] = useState(0)
  useEffect(() => {
    if (a) setText(a.instructions ?? a.instructions_default ?? '')
  }, [a])
  const act = async (fn: () => Promise<unknown>, done: string) => {
    try {
      await fn()
      setErr(undefined)
      setMsg(done)
      reload()
      setTick((t) => t + 1)
    } catch (x) {
      setMsg(undefined)
      setErr((x as Error).message)
    }
  }
  const upd = (patch: object, done: string) => act(() => cmd(ws, 'agent.update', { id, ...patch }), done)
  return (
    <div className="space-y-6">
      <Link href={back} className="inline-flex items-center gap-1 text-[13px] text-muted hover:text-fg">
        <ArrowLeft size={14} /> All AI employees
      </Link>
      {error && <ErrorText error={error} />}
      {!a && !error && <p className="text-[13px] text-muted">Loading…</p>}
      {a && (
        <>
          <div className="flex items-center justify-between gap-3">
            <div>
              <h3 className="text-[16px] font-semibold">{a.name}</h3>
              <p className="text-[13px] text-muted">{a.description}</p>
            </div>
            <div className="flex items-center gap-2">
              <Pill tone={a.enabled ? 'ok' : 'neutral'}>{a.enabled ? 'On' : 'Paused'}</Pill>
              {admin && (
                <>
                  <button
                    type="button"
                    className={btn}
                    onClick={() => upd({ enabled: !a.enabled }, a.enabled ? 'Paused.' : 'Turned on.')}
                  >
                    {a.enabled ? 'Pause' : 'Turn on'}
                  </button>
                  <button
                    type="button"
                    className={`${btn} whitespace-nowrap`}
                    disabled={!a.enabled}
                    onClick={() => act(() => cmd(ws, 'agent.run', { agent_id: id }), 'Started a run.')}
                  >
                    <Play size={13} /> Run now
                  </button>
                </>
              )}
            </div>
          </div>
          {msg && (
            <p role="status" className="text-[13px] text-muted">
              {msg}
            </p>
          )}
          <ErrorText error={err} />

          <section className="space-y-2 rounded-md border border-line p-4">
            <h4 className="text-[13px] font-semibold">Instructions</h4>
            <p className="text-[12px] text-muted">
              What this employee should do and how. Safety rules are added automatically and cannot be edited.
            </p>
            <textarea
              aria-label="Instructions"
              className="min-h-32 w-full rounded-md border border-line bg-canvas p-2 text-[14px] disabled:opacity-60"
              value={text}
              disabled={!admin}
              onChange={(e) => setText(e.target.value)}
            />
            {admin && (
              <div className="flex gap-2">
                <button
                  type="button"
                  className={btnPrimary}
                  disabled={text === (a.instructions ?? a.instructions_default ?? '')}
                  onClick={() => upd({ instructions: text }, 'Instructions saved.')}
                >
                  Save
                </button>
                {a.instructions != null && a.instructions !== a.instructions_default && (
                  <button
                    type="button"
                    className={btn}
                    onClick={() => upd({ instructions: null }, 'Back to the default instructions.')}
                  >
                    Reset to default
                  </button>
                )}
              </div>
            )}
          </section>

          <section className="space-y-2 rounded-md border border-line p-4">
            <h4 className="text-[13px] font-semibold">What it can use</h4>
            <p className="text-[12px] text-muted">
              Anything that sends or changes something outside C Note waits for a person to approve it.
            </p>
            <ul className="flex flex-wrap gap-1.5">
              {a.tools.map((t) => (
                <li key={t} className="rounded-full border border-line px-2 py-0.5 text-[12px]" title={t}>
                  {toolLabel(t)}
                </li>
              ))}
            </ul>
          </section>

          <section className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1 rounded-md border border-line p-4 text-[13px]">
              <h4 className="font-semibold">When it works</h4>
              <p className="capitalize-first">{triggerText(a)}</p>
              {a.next_run_at && (
                <p className="text-muted">Next run {new Date(a.next_run_at).toLocaleString()}</p>
              )}
              <p className="text-muted">Runs for {a.owner_name} unless someone asks it directly.</p>
            </div>
            <dl className="space-y-1 rounded-md border border-line p-4 text-[13px]">
              <h4 className="font-semibold">Limits</h4>
              {(
                [
                  ['Steps per run', a.max_steps],
                  ['Tokens per run', a.token_budget],
                  ['Runs per day', a.daily_runs],
                ] as const
              ).map(([k, v]) => (
                <div key={k} className="flex justify-between">
                  <dt className="text-muted">{k}</dt>
                  <dd>{v.toLocaleString('en-IN')}</dd>
                </div>
              ))}
            </dl>
          </section>

          <section className="space-y-2">
            <h4 className="text-[13px] font-semibold">Runs</h4>
            <RunsTable key={tick} ws={ws} agentId={id} />
          </section>
        </>
      )}
    </div>
  )
}
