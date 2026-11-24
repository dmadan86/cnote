'use client'
import { ShieldAlert } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { btn, btnPrimary, EmptyState, ErrorText, inputCls, Pill } from '@/components/ui'
import { auth } from '@/lib/api'
import { type AbuseAction, type AbuseCase, type AbuseSignal, type AbuseStatus, siteApi3b } from '@/lib/sites'

const TONE = { open: 'warn', dismissed: 'neutral', taken_down: 'bad', restored: 'ok' } as const
const LABEL: Record<AbuseStatus, string> = {
  open: 'Open',
  dismissed: 'Dismissed',
  taken_down: 'Taken down',
  restored: 'Restored',
}
const ACTIONS: { action: AbuseAction; label: string; from: AbuseStatus[] }[] = [
  { action: 'dismiss', label: 'Dismiss', from: ['open'] },
  { action: 'takedown', label: 'Take down', from: ['open', 'dismissed', 'restored'] },
  { action: 'restore', label: 'Restore', from: ['taken_down'] },
]
const when = (iso: string) => new Date(iso).toLocaleString('en-IN', { timeZone: 'Asia/Kolkata' })

/** Evidence is text only: React escapes it, nothing is rendered live or fetched. */
function Signals({ title, rows }: { title: string; rows: AbuseSignal[] }) {
  if (!rows.length) return null
  const groups = new Map<string, AbuseSignal[]>()
  for (const s of rows) groups.set(s.rule, [...(groups.get(s.rule) ?? []), s])
  return (
    <section className="space-y-2">
      <h3 className="text-[13px] font-semibold">{title}</h3>
      {[...groups].map(([rule, list]) => (
        <div key={rule} className="rounded-md border border-line p-2">
          <p className="text-[13px] font-medium">
            {rule} <span className="text-muted">({list.length})</span>
          </p>
          <ul className="mt-1 space-y-1">
            {list.map((s, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: static list, no reordering
              <li key={i} className="text-[12px]">
                <span className="text-muted">{s.location}: </span>
                <code className="whitespace-pre-wrap break-all rounded bg-hover px-1">{s.snippet}</code>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </section>
  )
}

function Detail({ ws, id, onChanged }: { ws: string; id: string; onChanged: () => void }) {
  const [c, setC] = useState<AbuseCase | null>(null)
  const [err, setErr] = useState<Error | null>(null)
  const [note, setNote] = useState('')
  const [confirm, setConfirm] = useState<AbuseAction | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    setC(null)
    setConfirm(null)
    setNote('')
    siteApi3b.abuseGet(ws, id).then(setC, setErr)
  }, [ws, id])
  if (err) return <ErrorText error={err} />
  if (!c) return <p className="text-[13px] text-muted">Loading…</p>
  const act = async () => {
    if (!confirm) return
    setBusy(true)
    try {
      setC(await siteApi3b.abuseAct(ws, id, confirm, note.trim()))
      setConfirm(null)
      setNote('')
      onChanged()
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const sig = c.signals ?? []
  // The audit trail; the shape is assumed (not in the AbuseCase type yet).
  const history =
    (c as AbuseCase & { history?: { at: string; action: string; note?: string }[] }).history ?? []
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-[15px] font-semibold">{c.site_name}</h2>
        <Pill tone={TONE[c.status]}>{LABEL[c.status]}</Pill>
        <span className="text-[12px] text-muted">
          score {c.score} · opened {when(c.opened_at)}
        </span>
      </div>
      <Signals
        title="Verified evidence (scan)"
        rows={sig.filter((s) => !s.reported && s.rule !== 'web_risk')}
      />
      <Signals title="Web Risk matches" rows={sig.filter((s) => !s.reported && s.rule === 'web_risk')} />
      <Signals title="Visitor reports (not verified)" rows={sig.filter((s) => s.reported)} />
      {!!c.not_checked?.length && (
        <section>
          <h3 className="text-[13px] font-semibold">Not checked</h3>
          <ul className="text-[12px]">
            {c.not_checked.map((u) => (
              <li key={u} className="break-all">
                {u}
              </li>
            ))}
          </ul>
        </section>
      )}
      {c.appeal && (
        <section className="rounded-md border border-line p-2 text-[13px]">
          <h3 className="font-semibold">Owner appeal ({when(c.appeal.at)})</h3>
          <p className="whitespace-pre-wrap">{c.appeal.note}</p>
        </section>
      )}
      {!!history.length && (
        <section>
          <h3 className="text-[13px] font-semibold">History</h3>
          <ul className="text-[12px]">
            {history.map((h, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: static list
              <li key={i}>
                {when(h.at)}: {h.action}
                {h.note ? ` (${h.note})` : ''}
              </li>
            ))}
          </ul>
        </section>
      )}
      <section className="space-y-2 border-t border-line pt-3">
        <label className="block text-[13px]">
          Note
          <textarea
            className={`${inputCls} mt-1 w-full`}
            rows={2}
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
        </label>
        <div className="flex flex-wrap gap-2">
          {ACTIONS.filter((a) => a.from.includes(c.status)).map((a) => (
            <button key={a.action} type="button" className={btn} onClick={() => setConfirm(a.action)}>
              {a.label}
            </button>
          ))}
        </div>
        {confirm && (
          <div role="alertdialog" aria-label="Confirm action" className="rounded-md bg-hover p-2 text-[13px]">
            <p>
              {ACTIONS.find((a) => a.action === confirm)?.label} {c.site_name}? This is recorded in the
              history.
            </p>
            <div className="mt-2 flex gap-2">
              <button type="button" className={btnPrimary} disabled={busy} onClick={act}>
                Confirm
              </button>
              <button type="button" className={btn} onClick={() => setConfirm(null)}>
                Cancel
              </button>
            </div>
          </div>
        )}
      </section>
    </div>
  )
}

export function Review() {
  const [ws, setWs] = useState<string | null>(null)
  const [cases, setCases] = useState<AbuseCase[] | null>(null)
  const [sel, setSel] = useState<string | null>(null)
  const [err, setErr] = useState<Error | null>(null)
  const [denied, setDenied] = useState(false)
  const load = useCallback(
    (w: string) =>
      siteApi3b.abuseList(w).then(
        (r) => setCases([...r.cases].sort((a, b) => b.score - a.score)),
        (e: Error & { status?: number; code?: string }) => {
          if (e.status === 403 || e.code === 'forbidden') setDenied(true)
          else setErr(e)
        },
      ),
    [],
  )
  useEffect(() => {
    auth.me().then(
      (m) => {
        const w = m.workspaces[0]?.id
        if (!w) return setDenied(true)
        setWs(w)
        void load(w)
      },
      (e) => setErr(e as Error),
    )
  }, [load])
  if (denied) return <p role="alert">This page is for C Note platform administrators only.</p>
  if (err) return <ErrorText error={err} />
  if (!cases || !ws) return <p className="text-[13px] text-muted">Loading…</p>
  if (!cases.length)
    return (
      <EmptyState icon={ShieldAlert} title="No cases">
        Nothing to review right now.
      </EmptyState>
    )
  return (
    <div className="grid gap-4 md:grid-cols-[16rem_1fr]">
      <ul className="space-y-1" aria-label="Cases">
        {cases.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              onClick={() => setSel(c.id)}
              aria-current={sel === c.id}
              className={`w-full rounded-md border border-line p-2 text-left text-[13px] hover:bg-hover ${sel === c.id ? 'bg-hover' : ''}`}
            >
              <span className="block truncate font-medium">{c.site_name}</span>
              <span className="text-[12px] text-muted">
                {LABEL[c.status]} · score {c.score}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {sel ? (
        <Detail ws={ws} id={sel} onChanged={() => load(ws)} />
      ) : (
        <p className="text-[13px] text-muted">Choose a case.</p>
      )}
    </div>
  )
}
