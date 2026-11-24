'use client'
import { Check, X } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { CmdFailure } from '@/lib/api'
import { getVersion, restoreAsNew, type Site, type SiteData, type SiteVersion, siteApi } from '@/lib/sites'

const PAGE = 50
const SOURCE: Record<string, string> = {
  chat: 'Chat',
  edit: 'Toolbar',
  editor: 'Editor',
  restore: 'Restore',
  template: 'Template',
}
const who = (v: SiteVersion) => (v.source === 'chat' ? 'C Note AI' : v.created_by?.name || 'You')
const stamp = (iso: string) =>
  new Date(iso).toLocaleString(undefined, {
    day: 'numeric',
    month: 'short',
    hour: 'numeric',
    minute: '2-digit',
  })
const dayKey = (iso: string) => new Date(iso).toDateString()
function dayLabel(iso: string) {
  const d = new Date(iso)
  const today = new Date()
  const yest = new Date(today.getTime() - 86_400_000)
  if (d.toDateString() === today.toDateString()) return 'Today'
  if (d.toDateString() === yest.toDateString()) return 'Yesterday'
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'long' })
}

/**
 * Version history (Phase 2 design section 7, 17.12). Selecting a row calls onPreview with that version's document
 * (read-only); the parent shows it in the preview frame and disables chat and Edit while it is set.
 * Restore always confirms, then makes a new draft version on top (nothing is replaced).
 */
export function VersionHistory({
  ws,
  siteId,
  draftVersionId,
  onPreview,
  onRestored,
  onReload,
  onClose,
}: {
  ws: string
  siteId: string
  /** The current draft: the expected_version_id of a restore. */
  draftVersionId: string
  /** A version's document while one is selected; null for "Back to now". */
  onPreview: (p: { version: SiteVersion; data: SiteData } | null) => void
  onRestored: (r: { site: Site; data: SiteData }) => void
  onReload?: () => void
  onClose: () => void
}) {
  const [rows, setRows] = useState<SiteVersion[]>([])
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [sel, setSel] = useState<SiteVersion | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [conflict, setConflict] = useState(false)

  const load = useCallback(
    async (before?: number) => {
      setLoading(true)
      setErr('')
      try {
        const r = await siteApi.versions(ws, siteId, before, PAGE)
        setRows((l) => (before ? [...l, ...r] : r))
        setMore(r.length === PAGE)
      } catch (e) {
        setErr(e instanceof Error ? e.message : "History couldn't be loaded")
      } finally {
        setLoading(false)
      }
    },
    [ws, siteId],
  )
  useEffect(() => {
    load()
  }, [load])

  const back = () => {
    setSel(null)
    setConfirm(false)
    onPreview(null)
  }
  const select = async (v: SiteVersion) => {
    if (v.id === draftVersionId) return back()
    setSel(v)
    setConfirm(false)
    setErr('')
    try {
      onPreview({ version: v, data: (await getVersion(ws, siteId, v.id)).data })
    } catch (e) {
      setSel(null)
      setErr(e instanceof Error ? e.message : "That version couldn't be opened")
    }
  }
  const restore = async () => {
    if (!sel) return
    setBusy(true)
    setErr('')
    try {
      const r = await restoreAsNew(ws, siteId, draftVersionId, sel.id)
      setSel(null)
      setConfirm(false)
      onPreview(null)
      onRestored(r)
      load()
    } catch (e) {
      if (e instanceof CmdFailure && e.code === 'conflict') setConflict(true)
      else setErr(e instanceof Error ? e.message : "Couldn't restore this version")
    } finally {
      setBusy(false)
    }
  }

  const later = sel ? rows.filter((r) => r.seq > sel.seq).length : 0
  let day = ''
  return (
    <section aria-label="Version history" className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b px-4 py-3">
        <h2 className="text-base font-semibold">Version history</h2>
        <button
          type="button"
          aria-label="Close version history"
          onClick={() => {
            back()
            onClose()
          }}
          className="rounded p-2"
        >
          <X size={18} aria-hidden="true" />
        </button>
      </header>
      {sel && (
        <div className="space-y-2 border-b bg-amber-50 px-4 py-3 text-sm text-black">
          <p>Viewing {stamp(sel.created_at)} · not your live site</p>
          {conflict ? (
            <p role="alert">
              Your website changed while this was open, so nothing was restored.{' '}
              {onReload && (
                <button type="button" onClick={onReload} className="font-semibold underline">
                  Reload
                </button>
              )}
            </p>
          ) : confirm ? (
            <div role="alertdialog" aria-label="Restore this version?" className="space-y-2">
              <p className="font-semibold">Restore this version?</p>
              <p>
                Your website goes back to how it was on {stamp(sel.created_at)}. {later} later{' '}
                {later === 1 ? 'change is' : 'changes are'} kept in history. Your live site changes only when
                you publish.
              </p>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setConfirm(false)}
                  className="min-h-10 rounded-md border bg-white px-3"
                >
                  Cancel
                </button>
                <button
                  type="button"
                  disabled={busy}
                  onClick={restore}
                  className="min-h-10 rounded-md bg-black px-3 text-white disabled:opacity-60"
                >
                  {busy ? 'Restoring…' : 'Restore'}
                </button>
              </div>
            </div>
          ) : (
            <div className="flex gap-2">
              <button type="button" onClick={back} className="min-h-10 rounded-md border bg-white px-3">
                Back to now
              </button>
              <button
                type="button"
                onClick={() => setConfirm(true)}
                className="min-h-10 rounded-md bg-black px-3 text-white"
              >
                Restore
              </button>
            </div>
          )}
        </div>
      )}
      {err && (
        <p role="alert" className="px-4 py-2 text-sm text-red-700">
          {err}
        </p>
      )}
      <ol className="min-h-0 flex-1 overflow-y-auto">
        {rows.map((v) => {
          const k = dayKey(v.created_at)
          const head = k !== day ? dayLabel(v.created_at) : null
          day = k
          const current = v.id === draftVersionId
          return (
            <li key={v.id}>
              {head && <p className="px-4 pb-1 pt-3 text-xs font-semibold uppercase text-gray-500">{head}</p>}
              <button
                type="button"
                aria-pressed={sel?.id === v.id}
                onClick={() => select(v)}
                className={`block w-full px-4 py-2 text-left text-sm ${sel?.id === v.id ? 'bg-blue-50' : 'hover:bg-gray-50'}`}
              >
                <span className="flex items-baseline justify-between gap-2">
                  <span>
                    <span className="text-gray-500">
                      {new Date(v.created_at).toLocaleTimeString(undefined, {
                        hour: 'numeric',
                        minute: '2-digit',
                      })}
                    </span>{' '}
                    {v.summary || 'Change'}
                  </span>
                  <span className="flex shrink-0 gap-1 text-xs">
                    {v.is_published && (
                      <span className="rounded bg-green-100 px-1.5 py-0.5 text-green-800">▲ Published</span>
                    )}
                    {current && (
                      <span className="rounded bg-gray-200 px-1.5 py-0.5">
                        <Check size={10} className="mr-0.5 inline" aria-hidden="true" />
                        Current
                      </span>
                    )}
                  </span>
                </span>
                <span className="block text-xs text-gray-500">
                  {who(v)} · {SOURCE[v.source] ?? v.source}
                </span>
              </button>
            </li>
          )
        })}
        {loading && <li className="px-4 py-3 text-sm text-gray-500">Loading…</li>}
        {more && !loading && (
          <li className="px-4 py-3">
            <button
              type="button"
              onClick={() => load(rows[rows.length - 1]?.seq)}
              className="text-sm underline"
            >
              Show older
            </button>
          </li>
        )}
      </ol>
      <footer className="border-t px-4 py-3 text-xs text-gray-500">
        History keeps your last 200 changes.
      </footer>
    </section>
  )
}
