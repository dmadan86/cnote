'use client'
import Link from 'next/link'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, ErrorText, inputCls, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

type Flag = { id: string; title: string; verify_by: string | null; reasons: string[] }
type Score = {
  total: number
  verified: number
  score: number
  spaces: { space_id: string; key: string; total: number; verified: number; score: number }[]
}
type Capture = { id: string; question: string; routed_to: string | null; created_at: string }
type Hit = { id: string; title: string; freshness: string }

const REASONS: Record<string, string> = {
  binding_changed: 'Binding changed',
  date_lapsed: 'Date lapsed',
  imported: 'Imported',
}

function Meter({ label, value, detail }: { label: string; value: number; detail: string }) {
  const pct = Math.round(value * 100)
  return (
    <div>
      <div className="mb-1 flex justify-between text-[13px]">
        <span className="font-medium">{label}</span>
        <span className="text-muted">
          {pct}% · {detail}
        </span>
      </div>
      {/* biome-ignore lint/a11y/useSemanticElements: meter element cannot be themed across browsers */}
      <div
        role="meter"
        aria-label={`${label} freshness`}
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
        className="h-2 overflow-hidden rounded-full bg-hover"
      >
        <div
          className={`h-full ${pct >= 70 ? 'bg-ok' : pct >= 40 ? 'bg-warn' : 'bg-bad'}`}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  )
}

const H = ({ children }: { children: React.ReactNode }) => (
  <h2 className="mb-2 text-[15px] font-semibold">{children}</h2>
)

export function Dashboard() {
  const { ws, members, notes } = useShell()
  const flags = useCmd<Flag[]>(ws, 'doc.flags')
  const score = useCmd<Score>(ws, 'doc.freshness_score')
  const caps = useCmd<Capture[]>(ws, 'capture.list')
  const [archived, setArchived] = useState<Hit[]>()
  const [err, setErr] = useState<string>()

  const groups = Object.keys(REASONS)
    .map((r) => [r, (flags.data ?? []).filter((f) => f.reasons[0] === r)] as const)
    .filter(([, l]) => l.length)

  return (
    <div className="space-y-8">
      <section aria-label="Freshness">
        <H>Freshness</H>
        {score.data && (
          <div className="space-y-3">
            <Meter
              label="Workspace"
              value={score.data.score}
              detail={`${score.data.verified} of ${score.data.total} pages verified`}
            />
            {score.data.spaces.map((s) => (
              <Meter key={s.space_id} label={s.key} value={s.score} detail={`${s.verified} of ${s.total}`} />
            ))}
          </div>
        )}
      </section>

      <section aria-label="Needs review">
        <H>My pages needing review</H>
        {flags.data?.length === 0 && <p className="text-muted">Nothing needs review. Nice.</p>}
        {groups.map(([r, list]) => (
          <div key={r} className="mb-3">
            <h3 className="mb-1 text-[12px] font-medium uppercase tracking-wide text-muted">{REASONS[r]}</h3>
            <ul className="divide-y divide-line rounded-md border border-line">
              {list.map((f) => (
                <li key={f.id} className="flex items-center gap-2 px-3 py-2">
                  <Link href={`/w/${ws}/n/${f.id}`} className="font-medium hover:underline">
                    {f.title || 'Untitled'}
                  </Link>
                  {f.verify_by && (
                    <span className="ml-auto text-[12px] text-muted">verify by {f.verify_by}</span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        ))}
      </section>

      <section aria-label="Capture requests">
        <H>Capture requests</H>
        {caps.data?.length === 0 && <p className="text-muted">No open questions.</p>}
        <ul className="space-y-2">
          {caps.data?.map((c) => (
            <li key={c.id} className="rounded-md border border-line p-3">
              <p className="font-medium">{c.question}</p>
              <form
                className="mt-2 flex items-center gap-2"
                onSubmit={async (e) => {
                  e.preventDefault()
                  try {
                    await cmd(ws, 'capture.answer', {
                      request_id: c.id,
                      note_id: new FormData(e.currentTarget).get('note'),
                    })
                    setErr(undefined)
                    caps.reload()
                  } catch (x) {
                    setErr((x as Error).message)
                  }
                }}
              >
                <span className="text-[12px] text-muted">
                  To {members.find((m) => m.user_id === c.routed_to)?.name ?? 'admins'} · {ago(c.created_at)}
                </span>
                <select
                  name="note"
                  required
                  aria-label={`Answering page for ${c.question}`}
                  className={`${inputCls} ml-auto max-w-56`}
                >
                  <option value="">Link a page...</option>
                  {notes
                    .filter((n) => n.types.length === 0)
                    .map((n) => (
                      <option key={n.id} value={n.id}>
                        {n.title || 'Untitled'}
                      </option>
                    ))}
                </select>
                <button type="submit" className={btn}>
                  Answer
                </button>
              </form>
            </li>
          ))}
        </ul>
        <ErrorText error={err} />
      </section>

      <section aria-label="Archived pages">
        <H>Archived pages</H>
        <form
          className="flex gap-2"
          onSubmit={async (e) => {
            e.preventDefault()
            try {
              const r = await cmd<Hit[]>(ws, 'search.notes', {
                q: new FormData(e.currentTarget).get('q'),
                include_archived: true,
                limit: 50,
              })
              setArchived(r.filter((h) => h.freshness === 'archived'))
            } catch (x) {
              setErr((x as Error).message)
            }
          }}
        >
          <input
            name="q"
            required
            aria-label="Find archived pages"
            placeholder="Search archived pages..."
            className={inputCls}
          />
          <button type="submit" className={btn}>
            Find
          </button>
        </form>
        {archived?.length === 0 && <p className="mt-2 text-muted">No archived pages match.</p>}
        <ul className="mt-2 space-y-1">
          {archived?.map((h) => (
            <li key={h.id} className="flex items-center gap-2 rounded-md border border-line px-3 py-2">
              <span>{h.title || 'Untitled'}</span>
              <Pill>Archived</Pill>
              <button
                type="button"
                className={`${btn} ml-auto`}
                onClick={async () => {
                  try {
                    await cmd(ws, 'doc.restore', { note_id: h.id })
                    setArchived((a) => a?.filter((x) => x.id !== h.id))
                    flags.reload()
                  } catch (x) {
                    setErr((x as Error).message)
                  }
                }}
              >
                Restore
              </button>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}
