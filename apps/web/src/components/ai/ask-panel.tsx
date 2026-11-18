'use client'
import { Loader2, Send, Sparkles, Square, X } from 'lucide-react'
import Link from 'next/link'
import { useCallback, useEffect, useRef, useState } from 'react'
import { AGENT_CMD, type Run, type RunDetail, type Step, type Template } from '@/lib/agents'
import { CmdFailure, cmd } from '@/lib/api'
import { useShell } from '../shell'
import { btn, btnPrimary, EmptyState, ErrorText, Pill } from '../ui'

export const STARTERS = ['Who owes me money?', 'Revenue this FY by month', "What's overdue today?"]

const TERMINAL = ['completed', 'failed', 'held', 'cancelled', 'skipped']
const LABELS: [RegExp, string][] = [
  [/^report\./, 'Looking at reports'],
  [/^home\./, 'Checking your summary'],
  [/^search\./, 'Searching your workspace'],
  [/^invoice\./, 'Reading invoices'],
  [/^deal\./, 'Reading deals'],
  [/^ticket\./, 'Reading tickets'],
  [/^followup\./, 'Checking follow-ups'],
  [/^contact\./, 'Looking up contacts'],
  [/^note\./, 'Reading a page'],
  [/^item\./, 'Checking items'],
]
const friendly = (command: string) => LABELS.find(([re]) => re.test(command))?.[1] ?? 'Working'

function progress(run: Run, steps: Step[]) {
  if (run.status === 'queued') return 'Starting…'
  const last = [...steps].reverse().find((s) => s.kind === 'tool')
  return last && last.kind === 'tool' ? `${friendly(last.command)}…` : 'Thinking…'
}

/** Markdown-light as React nodes (never HTML): paragraphs, - / 1. lists, **bold**. */
function Bold({ text }: { text: string }) {
  return text.split(/(\*\*[^*]+\*\*)/g).map((p, i) =>
    p.startsWith('**') && p.endsWith('**') && p.length > 4 ? (
      // biome-ignore lint/suspicious/noArrayIndexKey: static split of a string
      <strong key={i}>{p.slice(2, -2)}</strong>
    ) : (
      p
    ),
  )
}
function Answer({ text }: { text: string }) {
  const blocks = text.trim().split(/\n{2,}/)
  return (
    <div className="space-y-2 break-words text-[13.5px] leading-relaxed">
      {blocks.map((b, i) => {
        const lines = b.split('\n')
        const bullet = lines.every((l) => /^\s*[-*]\s+/.test(l))
        const num = lines.every((l) => /^\s*\d+[.)]\s+/.test(l))
        if (bullet || num) {
          const L = num ? 'ol' : 'ul'
          return (
            // biome-ignore lint/suspicious/noArrayIndexKey: static split of a string
            <L key={i} className={`space-y-0.5 pl-5 ${num ? 'list-decimal' : 'list-disc'}`}>
              {lines.map((l, j) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: static split of a string
                <li key={j}>
                  <Bold text={l.replace(/^\s*([-*]|\d+[.)])\s+/, '')} />
                </li>
              ))}
            </L>
          )
        }
        return (
          // biome-ignore lint/suspicious/noArrayIndexKey: static split of a string
          <p key={i} className="whitespace-pre-wrap">
            <Bold text={b} />
          </p>
        )
      })}
    </div>
  )
}

function Sources({ runId }: { runId: string }) {
  const { ws } = useShell()
  const [steps, setSteps] = useState<Step[]>()
  const [err, setErr] = useState('')
  return (
    <details
      className="mt-2 text-[12px] text-muted"
      onToggle={(e) => {
        if (!(e.currentTarget as HTMLDetailsElement).open || steps) return
        cmd<RunDetail>(ws, AGENT_CMD.runGet, { id: runId }).then(
          (d) => setSteps(d.steps),
          (x) => setErr(x.message),
        )
      }}
    >
      <summary className="cursor-pointer select-none hover:text-fg">Sources</summary>
      <div className="mt-1 space-y-1">
        <ErrorText error={err} />
        {steps && !steps.some((s) => s.kind === 'tool') && <p>No data was looked up.</p>}
        {steps
          ?.filter((s) => s.kind === 'tool')
          .map((s) => (
            <details key={s.seq} className="rounded border border-line px-2 py-1">
              <summary className="cursor-pointer font-mono">{s.kind === 'tool' && s.command}</summary>
              <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all text-[11px]">
                {s.kind === 'tool' && JSON.stringify(s.input)}
              </pre>
            </details>
          ))}
      </div>
    </details>
  )
}

export function AskPanel({
  open,
  onClose,
  seed,
}: {
  open: boolean
  onClose: () => void
  /** A question to submit as soon as the panel opens (from ⌘K or Home). */
  seed: { q: string; n: number } | null
}) {
  const { ws, me } = useShell()
  const isAdmin = ['owner', 'admin'].some((r) => me.workspaces.find((w) => w.id === ws)?.role === r)
  const storeKey = `ask-conv-${ws}`
  const [ready, setReady] = useState<boolean>() // undefined = checking
  const [conv, setConv] = useState<string>()
  const [runs, setRuns] = useState<Run[]>([])
  const [steps, setSteps] = useState<Step[]>([])
  const [q, setQ] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [installing, setInstalling] = useState(false)
  const input = useRef<HTMLTextAreaElement>(null)
  const end = useRef<HTMLDivElement>(null)
  const seeded = useRef(0)
  const active = runs.find((r) => !TERMINAL.includes(r.status))
  const activeId = active?.id

  // Template check + history when opened; focus in, and back to the opener on close.
  useEffect(() => {
    if (!open) return
    const opener = document.activeElement as HTMLElement | null
    setTimeout(() => input.current?.focus(), 0)
    cmd<Template[]>(ws, AGENT_CMD.templates).then(
      (t) => setReady(!!t.find((x) => x.key === 'ask')?.agent_id),
      (e) => {
        setReady(true)
        setErr(e.message)
      },
    )
    let id: string | undefined
    try {
      id = localStorage.getItem(storeKey) ?? undefined
    } catch {}
    setConv(id)
    if (id)
      cmd<Run[]>(ws, AGENT_CMD.runList, { conversation_id: id, limit: 30 }).then(
        (r) => setRuns([...r].sort((a, b) => a.created_at.localeCompare(b.created_at))),
        () => {},
      )
    return () => opener?.focus?.()
  }, [open, ws, storeKey])

  // Poll the running run every second.
  useEffect(() => {
    if (!open || !activeId) return
    let live = true
    const tick = async () => {
      try {
        const d = await cmd<RunDetail>(ws, AGENT_CMD.runGet, { id: activeId })
        if (!live) return
        setSteps(d.steps)
        setRuns((rs) => rs.map((r) => (r.id === activeId ? d.run : r)))
      } catch {}
    }
    const t = setInterval(tick, 1000)
    tick()
    return () => {
      live = false
      clearInterval(t)
    }
  }, [open, ws, activeId])

  // biome-ignore lint/correctness/useExhaustiveDependencies: scroll on new content only
  useEffect(() => {
    end.current?.scrollIntoView({ block: 'end' })
  }, [runs, steps])

  const ask = useCallback(
    async (question: string) => {
      if (!question.trim() || busy) return
      setBusy(true)
      setErr('')
      try {
        const r = await cmd<{ run_id: string; conversation_id: string }>(ws, AGENT_CMD.ask, {
          question,
          conversation_id: conv,
        })
        setConv(r.conversation_id)
        try {
          localStorage.setItem(storeKey, r.conversation_id)
        } catch {}
        setQ('')
        setSteps([])
        const run = await cmd<RunDetail>(ws, AGENT_CMD.runGet, { id: r.run_id })
        setRuns((rs) => [...rs, run.run])
      } catch (e) {
        if (e instanceof CmdFailure && e.code === 'not_found') setReady(false)
        else setErr((e as Error).message)
      } finally {
        setBusy(false)
      }
    },
    [busy, conv, storeKey, ws],
  )

  // Submit a seeded question once per request, after readiness is known.
  useEffect(() => {
    if (open && seed && ready && seeded.current !== seed.n) {
      seeded.current = seed.n
      ask(seed.q)
    }
  }, [open, seed, ready, ask])

  async function install() {
    setInstalling(true)
    setErr('')
    try {
      await cmd(ws, AGENT_CMD.install, { template: 'ask' })
      setReady(true)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setInstalling(false)
    }
  }
  const cancel = () =>
    active && cmd(ws, AGENT_CMD.runCancel, { id: active.id }).catch((e) => setErr(e.message))
  const newChat = () => {
    setConv(undefined)
    setRuns([])
    setSteps([])
    try {
      localStorage.removeItem(storeKey)
    } catch {}
    input.current?.focus()
  }

  if (!open) return null
  return (
    <aside
      role="dialog"
      aria-label="Ask your business"
      onKeyDown={(e) => e.key === 'Escape' && onClose()}
      className="fixed inset-0 z-40 flex flex-col bg-canvas sm:left-auto sm:w-[400px] sm:border-l sm:border-line sm:shadow-xl"
    >
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line px-4">
        <Sparkles size={15} className="text-muted" aria-hidden="true" />
        <h2 className="flex-1 text-[14px] font-semibold">Ask your business</h2>
        {runs.length > 0 && (
          <button type="button" className={btn} onClick={newChat}>
            New chat
          </button>
        )}
        <button type="button" aria-label="Close" onClick={onClose} className="rounded p-1 hover:bg-hover">
          <X size={16} />
        </button>
      </header>

      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-3">
        {ready === false ? (
          <EmptyState icon={Sparkles} title="Ask isn't set up" compact>
            {isAdmin
              ? 'Turn on the Ask agent to answer questions from your invoices, deals and reports.'
              : 'Ask an admin to turn on the Ask agent.'}
            <div className="mt-3 flex justify-center gap-2">
              {isAdmin && (
                <button type="button" className={btnPrimary} onClick={install} disabled={installing}>
                  {installing ? 'Installing…' : 'Install'}
                </button>
              )}
              <Link href={`/w/${ws}/settings?tab=Agents`} onClick={onClose} className={btn}>
                Settings → Agents
              </Link>
            </div>
          </EmptyState>
        ) : runs.length === 0 ? (
          <div className="space-y-3 pt-6">
            <EmptyState icon={Sparkles} title="Ask about your business" compact>
              Answers come from your own invoices, deals, tasks and reports.
            </EmptyState>
            <div className="flex flex-col gap-2">
              {STARTERS.map((s) => (
                <button
                  key={s}
                  type="button"
                  disabled={ready === undefined || busy}
                  onClick={() => ask(s)}
                  className="rounded-md border border-line px-3 py-2 text-left text-[13px] hover:bg-hover disabled:opacity-50"
                >
                  {s}
                </button>
              ))}
            </div>
          </div>
        ) : (
          runs.map((r) => (
            <div key={r.id} className="space-y-2">
              <p className="ml-8 rounded-lg bg-hover px-3 py-2 text-[13.5px] whitespace-pre-wrap">
                {r.input}
              </p>
              {r.status === 'completed' && r.output ? (
                <>
                  <Answer text={r.output} />
                  <Sources runId={r.id} />
                </>
              ) : r.status === 'failed' ? (
                <ErrorText error={r.error || 'Something went wrong. Try asking again.'} />
              ) : r.status === 'held' ? (
                <p className="text-[13px] text-muted">
                  <Pill>Waiting for approval</Pill> An action needs a person to approve it first.{' '}
                  <Link href={`/w/${ws}/approvals`} onClick={onClose} className="underline">
                    Open approvals
                  </Link>
                </p>
              ) : r.status === 'cancelled' || r.status === 'skipped' ? (
                <p className="text-[13px] text-muted">
                  {r.status === 'cancelled' ? 'Cancelled.' : 'Skipped.'}
                </p>
              ) : r.status === 'completed' ? (
                <p className="text-[13px] text-muted">No answer came back.</p>
              ) : (
                <div className="flex items-center gap-2 text-[13px] text-muted">
                  <Loader2 size={14} className="animate-spin" aria-hidden="true" />
                  <span>{progress(r, steps)}</span>
                  <button type="button" className={`${btn} ml-auto`} onClick={cancel}>
                    <Square size={11} /> Cancel
                  </button>
                </div>
              )}
            </div>
          ))
        )}
        <div ref={end} />
      </div>

      {/* One polite live region so screen readers hear progress and results. */}
      <p className="sr-only" role="status" aria-live="polite">
        {active ? progress(active, steps) : ''}
      </p>

      <form
        className="shrink-0 border-t border-line p-3"
        onSubmit={(e) => {
          e.preventDefault()
          ask(q)
        }}
      >
        <ErrorText error={err} />
        <div className="flex items-end gap-2">
          <textarea
            ref={input}
            rows={1}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                ask(q)
              }
            }}
            aria-label="Your question"
            placeholder="Ask about invoices, deals, tasks…"
            disabled={ready === false}
            className="max-h-32 flex-1 resize-none rounded-md border border-line bg-canvas px-2.5 py-1.5 text-[14px] placeholder:text-muted"
          />
          <button
            type="submit"
            aria-label="Send"
            disabled={!q.trim() || busy || !!active || ready === false}
            className={btnPrimary}
          >
            <Send size={14} />
          </button>
        </div>
        <p className="mt-1.5 text-[11px] text-muted">
          AI can make mistakes. Check Sources for important numbers.
        </p>
      </form>
    </aside>
  )
}
