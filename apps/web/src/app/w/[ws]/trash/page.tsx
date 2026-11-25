'use client'
import { FileText } from 'lucide-react'
import Link from 'next/link'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, EmptyState, ErrorText, inputCls, Modal } from '@/components/ui'
import { cmd } from '@/lib/api'
import {
  getTrashRetention,
  noteUndelete,
  purgeDeletion,
  TREE_EVENT,
  type TrashEntry,
  type TrashRetention,
} from '@/lib/docs'
import { ago } from '@/lib/hooks'

const PAGE = 50
const DAY = 86_400_000

/** "Deleted for good in 12 days" from the server's purge_at; null when trash is kept forever. */
function purgeText(at: string | null) {
  if (!at) return null
  const days = Math.ceil((new Date(at).getTime() - Date.now()) / DAY)
  if (days <= 0) return 'Deleted for good soon'
  return `Deleted for good in ${days} ${days === 1 ? 'day' : 'days'}`
}

function DeleteForever({
  entry,
  onClose,
  onDone,
}: {
  entry: TrashEntry
  onClose: () => void
  onDone: () => void
}) {
  const { ws } = useShell()
  const [typed, setTyped] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const n = entry.page_count
  async function go() {
    setBusy(true)
    setErr(undefined)
    try {
      await purgeDeletion(ws, entry.deletion_id)
      onDone()
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }
  return (
    <Modal open onClose={onClose} title="Delete forever">
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (typed === 'DELETE') go()
        }}
      >
        <p className="mb-3 text-[13px]">
          Delete "{entry.root.title || 'Untitled'}"
          {n > 1 ? ` and its ${n - 1} ${n === 2 ? 'sub-page' : 'sub-pages'}` : ''} for good? That is {n}{' '}
          {n === 1 ? 'page' : 'pages'} in all, with their comments, history and files.{' '}
          <strong>This cannot be undone.</strong>
        </p>
        <label className="mb-3 block text-[13px]">
          Type DELETE to confirm
          <input
            className={`${inputCls} mt-1`}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        </label>
        <ErrorText error={err} />
        <div className="mt-3 flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={`${btn} text-bad`} disabled={busy || typed !== 'DELETE'}>
            Delete forever
          </button>
        </div>
      </form>
    </Modal>
  )
}
type Outcome = { ok: true; text: string; rootId: string } | { ok: false; text: string }

export default function TrashPage() {
  const { ws, spaces } = useShell()
  const [space, setSpace] = useState('')
  const [rows, setRows] = useState<TrashEntry[]>([])
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string>()
  const [out, setOut] = useState<Record<string, Outcome>>({})
  const [busy, setBusy] = useState<string>()
  const [ret, setRet] = useState<TrashRetention>()
  const [forever, setForever] = useState<TrashEntry>()
  const [gone, setGone] = useState('')
  const own = useRef(false) // our own TREE_EVENT: the shell reloads its tree; this list keeps the row and its outcome

  // Keyset paging: the cursor is the last row's (deleted_at, deletion_id).
  const load = useCallback(
    async (after?: TrashEntry) => {
      setLoading(true)
      try {
        const r = await cmd<TrashEntry[]>(ws, 'note.trash', {
          space_id: space || undefined,
          limit: PAGE,
          before: after?.deleted_at,
          before_id: after?.deletion_id,
        })
        setRows((o) => {
          const seen = new Set((after ? o : []).map((x) => x.deletion_id))
          return [...(after ? o : []), ...r.filter((x) => !seen.has(x.deletion_id))]
        })
        setMore(r.length >= PAGE)
        setErr(undefined)
      } catch (e) {
        setErr((e as Error).message)
      }
      setLoading(false)
    },
    [ws, space],
  )
  useEffect(() => {
    load()
  }, [load])
  useEffect(() => {
    getTrashRetention(ws).then(setRet, () => {})
  }, [ws])
  useEffect(() => {
    const f = () => {
      if (own.current) own.current = false
      else load()
    }
    window.addEventListener(TREE_EVENT, f)
    return () => window.removeEventListener(TREE_EVENT, f)
  }, [load])

  async function restore(t: TrashEntry) {
    setBusy(t.deletion_id)
    try {
      const r = await noteUndelete(ws, t.deletion_id)
      setOut((o) => ({
        ...o,
        [t.deletion_id]: {
          ok: true,
          rootId: r.root_id,
          text: r.placed === 'original' ? 'Restored in place' : 'Restored at the top level of the space',
        },
      }))
      own.current = true
      window.dispatchEvent(new Event(TREE_EVENT))
    } catch (e) {
      setOut((o) => ({ ...o, [t.deletion_id]: { ok: false, text: `Not restored: ${(e as Error).message}` } }))
    }
    setBusy(undefined)
  }

  return (
    <div className="mx-auto max-w-3xl space-y-4 px-6 py-8">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Trash</h1>
        <label className="ml-auto flex items-center gap-2 text-[13px] text-muted">
          Space
          <select
            className={`${inputCls} w-auto`}
            value={space}
            onChange={(e) => {
              setSpace(e.target.value)
              setRows([])
            }}
          >
            <option value="">All spaces</option>
            {spaces.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      <p className="text-[13px] text-muted">
        Deleted pages you could edit. Restore brings back a page with its sub-pages.
      </p>
      {ret && (
        <p className="rounded-md border border-line bg-hover px-3 py-2 text-[13px]">
          {ret.days > 0
            ? `Pages in Trash are deleted for good after ${ret.days} days.`
            : 'Pages in Trash stay until an admin deletes them for good.'}
        </p>
      )}
      <p role="status" className="text-[13px] text-ok">
        {gone}
      </p>
      <ErrorText error={err} />
      {!loading && !err && rows.length === 0 && (
        <EmptyState icon={FileText} title="Trash is empty">
          Deleted pages show up here.
        </EmptyState>
      )}
      <ul className="divide-y divide-line rounded-md border border-line" aria-label="Deleted pages">
        {rows.map((t) => {
          const o = out[t.deletion_id]
          const blocked = t.restore_to === 'blocked'
          return (
            <li key={t.deletion_id} className="flex flex-wrap items-center gap-3 px-3 py-2.5">
              <span aria-hidden className="w-5 text-center">
                {t.root.icon ?? <FileText size={14} className="inline text-muted" />}
              </span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-[14px] font-medium">{t.root.title || 'Untitled'}</div>
                <div className="text-[12px] text-muted">
                  {t.page_count} {t.page_count === 1 ? 'page' : 'pages'} · Deleted by{' '}
                  {t.deleted_by?.name ?? 'someone'} ·{' '}
                  <time dateTime={t.deleted_at} title={new Date(t.deleted_at).toLocaleString()}>
                    {ago(t.deleted_at)}
                  </time>
                  {!o && purgeText(t.purge_at) && ` · ${purgeText(t.purge_at)}`}
                  {!o &&
                    t.restore_to === 'space_root' &&
                    ' · Its parent is gone, so it returns to the top level'}
                </div>
                {blocked && !o && (
                  <div className="text-[12px] text-bad">{t.blocked_reason ?? 'It cannot be restored.'}</div>
                )}
                {o && (
                  <div
                    role={o.ok ? 'status' : 'alert'}
                    className={`text-[12px] ${o.ok ? 'text-ok' : 'text-bad'}`}
                  >
                    {o.text}
                    {o.ok && (
                      <>
                        {' '}
                        <Link className="underline" href={`/w/${ws}/n/${o.rootId}`}>
                          Open page
                        </Link>
                      </>
                    )}
                  </div>
                )}
              </div>
              {!o?.ok && (
                <button
                  type="button"
                  className={btn}
                  disabled={blocked || busy === t.deletion_id}
                  onClick={() => restore(t)}
                >
                  Restore
                </button>
              )}
              {!o?.ok && t.can_purge && (
                <button
                  type="button"
                  className={`${btn} text-bad`}
                  aria-label={`Delete forever: ${t.root.title || 'Untitled'}`}
                  onClick={() => setForever(t)}
                >
                  Delete forever
                </button>
              )}
            </li>
          )
        })}
      </ul>
      {forever && (
        <DeleteForever
          entry={forever}
          onClose={() => setForever(undefined)}
          onDone={() => {
            const f = forever
            setForever(undefined)
            setRows((r) => r.filter((x) => x.deletion_id !== f.deletion_id))
            setGone(`Deleted "${f.root.title || 'Untitled'}" for good`)
            own.current = true
            window.dispatchEvent(new Event(TREE_EVENT))
          }}
        />
      )}
      {more && (
        <button type="button" className={btn} disabled={loading} onClick={() => load(rows[rows.length - 1])}>
          Load more
        </button>
      )}
    </div>
  )
}
