'use client'
import Link from 'next/link'
import { useCallback, useEffect, useState } from 'react'
import { btn, btnPrimary, ErrorText, Field, inputCls, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'

type Ev = {
  id: number
  kind: string
  note_id: string | null
  note_title: string | null
  actor_type: string
  actor_id: string | null
  actor_name: string | null
  payload: { command?: string; reason?: string; caller?: string }
  created_at: string
}
type Page = { events: Ev[]; next_cursor: string | null }

export function download(name: string, text: string, type: string) {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([text], { type }))
  a.download = name
  a.click()
  URL.revokeObjectURL(a.href)
}

const when = (iso: string) => new Date(iso).toLocaleString()

export function AuditLog({ ws }: { ws: string }) {
  const [f, setF] = useState({ kind_prefix: '', from: '', to: '', denied: false })
  const [applied, setApplied] = useState(f)
  const [rows, setRows] = useState<Ev[]>([])
  const [next, setNext] = useState<string | null>(null)
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')

  const filters = useCallback(
    (a: typeof f) => ({
      kind_prefix: a.denied ? 'command.denied' : a.kind_prefix.trim() || undefined,
      from: a.from || undefined,
      to: a.to || undefined,
    }),
    [],
  )
  const load = useCallback(
    async (a: typeof f, cursor?: string) => {
      setBusy(true)
      try {
        const p = await cmd<Page>(ws, 'audit.list', { ...filters(a), cursor, limit: 50 })
        setRows((r) => (cursor ? [...r, ...p.events] : p.events))
        setNext(p.next_cursor)
        setNote(cursor ? `${p.events.length} more events loaded` : '')
        setErr(undefined)
      } catch (e) {
        setErr((e as Error).message)
      } finally {
        setBusy(false)
      }
    },
    [ws, filters],
  )
  useEffect(() => {
    load(applied)
  }, [applied, load])

  const exportCsv = async () => {
    try {
      const r = await cmd<{ csv?: string; filename?: string; confirmation_required?: boolean }>(
        ws,
        'audit.export',
        filters(applied),
      )
      if (r.csv) download(r.filename ?? 'audit-log.csv', r.csv, 'text/csv')
      setErr(undefined)
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  return (
    <div className="space-y-4">
      <form
        className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"
        onSubmit={(e) => {
          e.preventDefault()
          setApplied(f)
        }}
      >
        <Field label="Action starts with (for example invoice.)">
          {(id) => (
            <input
              id={id}
              className={inputCls}
              value={f.kind_prefix}
              onChange={(e) => setF({ ...f, kind_prefix: e.target.value })}
            />
          )}
        </Field>
        <Field label="From date">
          {(id) => (
            <input
              id={id}
              type="date"
              className={inputCls}
              value={f.from}
              onChange={(e) => setF({ ...f, from: e.target.value })}
            />
          )}
        </Field>
        <Field label="To date (inclusive)">
          {(id) => (
            <input
              id={id}
              type="date"
              className={inputCls}
              value={f.to}
              onChange={(e) => setF({ ...f, to: e.target.value })}
            />
          )}
        </Field>
        <label className="flex items-end gap-2 pb-1.5 text-[13px]">
          <input
            type="checkbox"
            checked={f.denied}
            onChange={(e) => setF({ ...f, denied: e.target.checked })}
          />
          Only denied agent actions
        </label>
        <div className="flex gap-2 sm:col-span-2 lg:col-span-4">
          <button type="submit" className={btnPrimary}>
            Apply filters
          </button>
          <button type="button" className={btn} onClick={exportCsv}>
            Export filtered CSV
          </button>
          <span className="self-center text-[12px] text-muted">
            Dates use your browser's day; CSV holds every match, up to 50,000.
          </span>
        </div>
      </form>
      <ErrorText error={err} />
      {rows.length === 0 && !busy && <p className="text-muted">No events match.</p>}
      <ul className="divide-y divide-line rounded-md border border-line" aria-label="Audit events">
        {rows.map((e) => {
          const denied = e.kind === 'command.denied'
          return (
            <li
              key={e.id}
              className={`grid gap-x-3 px-3 py-2 sm:grid-cols-[11rem_9rem_1fr] ${denied ? 'bg-bad-bg' : ''}`}
            >
              <time dateTime={e.created_at} className="text-[12px] text-muted">
                {when(e.created_at)}
              </time>
              <span className="truncate font-medium">
                <span className="sr-only">Actor: </span>
                {e.actor_name ?? e.actor_type}
                {e.actor_type === 'agent' && <span className="text-[12px] text-muted"> (agent)</span>}
              </span>
              <span className="min-w-0 break-words">
                {denied && <Pill tone="bad">Denied</Pill>}{' '}
                <code className="text-[13px]">{denied ? e.payload.command : e.kind}</code>
                {denied && <span className="text-[13px] text-muted"> {e.payload.reason}</span>}
                {e.note_id && (
                  <>
                    {' '}
                    <Link href={`/w/${ws}/n/${e.note_id}`} className="text-[13px] underline">
                      {e.note_title || 'Untitled'}
                    </Link>
                  </>
                )}
              </span>
            </li>
          )
        })}
      </ul>
      <p role="status" className="sr-only">
        {note}
      </p>
      {next && (
        <button type="button" className={btn} disabled={busy} onClick={() => load(applied, next)}>
          Load more
        </button>
      )}
    </div>
  )
}
