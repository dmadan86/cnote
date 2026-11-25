'use client'
import { Copy, Globe, MessageCircle } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { btn, btnPrimary, ErrorText, Pill } from '@/components/ui'
import { type PageLinkGet, pageLinkCreate, pageLinkGet, pageLinkRevoke } from '@/lib/docs'
import { QrSvg } from './qr-svg'

/** "Public" chip for the page header: shown to every reader while a link is serving, so nobody types into a public page unaware. */
export function PublicChip({ ws, noteId, version = 0 }: { ws: string; noteId: string; version?: number }) {
  const [on, setOn] = useState(false)
  // biome-ignore lint/correctness/useExhaustiveDependencies: version re-reads the state after the share dialog closes
  useEffect(() => {
    let live = true
    pageLinkGet(ws, noteId)
      .then((g) => live && setOn(g.link?.status === 'active'))
      .catch(() => live && setOn(false))
    return () => {
      live = false
    }
  }, [ws, noteId, version])
  if (!on) return null
  return (
    <Pill tone="warn">
      <span className="inline-flex items-center gap-1">
        <Globe size={12} aria-hidden="true" /> Public
      </span>
    </Pill>
  )
}

/** "Share publicly" in the share dialog: create the link, copy it once, replace it, turn it off. */
export function PublicLink({
  ws,
  noteId,
  open,
  canEdit,
  onChange,
}: {
  ws: string
  noteId: string
  open: boolean
  canEdit: boolean
  onChange: () => void
}) {
  const [g, setG] = useState<PageLinkGet>()
  const [url, setUrl] = useState<string>() // the address, kept only while the dialog stays open: it cannot be shown again
  const [copied, setCopied] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  const [sure, setSure] = useState(false)

  const load = useCallback(() => {
    pageLinkGet(ws, noteId)
      .then(setG)
      .catch((e) => setErr((e as Error).message))
  }, [ws, noteId])
  useEffect(() => {
    if (open) load()
    else {
      setUrl(undefined)
      setSure(false)
      setCopied(false)
    }
  }, [open, load])

  async function run(f: () => Promise<void>) {
    setBusy(true)
    setErr(undefined)
    try {
      await f()
      load()
      onChange()
    } catch (e) {
      setErr((e as Error).message)
    }
    setBusy(false)
  }
  const create = () =>
    run(async () => {
      const r = await pageLinkCreate(ws, noteId)
      setUrl(r.url)
      setSure(false)
    })
  const off = () =>
    run(async () => {
      await pageLinkRevoke(ws, noteId)
      setUrl(undefined)
    })
  async function copy() {
    if (!url) return
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setErr('Could not copy the link')
    }
  }

  const link = g?.link
  const serving = link?.status === 'active'
  return (
    <section aria-label="Share publicly" className="space-y-2">
      <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">Share publicly</h3>
      {!g ? (
        <p className="text-[13px] text-muted">Loading...</p>
      ) : (
        <>
          {!link && (
            <p className="text-[13px] text-muted">
              Anyone with the link can read this page, without signing in. They cannot see comments, sub-pages
              or other files.
            </p>
          )}
          {link && (
            <p role="status" className="text-[13px]">
              {serving ? 'Public link is on.' : 'Public link is not serving right now.'}{' '}
              <span className="text-muted">
                Created by {link.created_by_name ?? 'someone'} on {link.created_at.slice(0, 10)}.{' '}
                {link.view_count} {link.view_count === 1 ? 'view' : 'views'}.
              </span>
            </p>
          )}
          {g.reason && (link || !g.allowed) && (
            <p className="rounded bg-warn-bg px-2 py-1 text-[12px] text-warn">{g.reason}</p>
          )}
          {url && (
            <div className="space-y-2 rounded border border-line p-2">
              <label className="block text-[12px] text-muted" htmlFor="public-link-url">
                Public link. This link is shown only once.
              </label>
              <div className="flex gap-2">
                <input
                  id="public-link-url"
                  readOnly
                  value={url}
                  onFocus={(e) => e.currentTarget.select()}
                  className="w-full min-w-0 rounded border border-line bg-canvas px-2 py-1 text-[13px]"
                />
                <button type="button" className={btn} onClick={copy}>
                  <Copy size={14} aria-hidden="true" /> {copied ? 'Copied' : 'Copy'}
                </button>
                <a
                  className={btn}
                  href={`https://wa.me/?text=${encodeURIComponent(url)}`}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <MessageCircle size={14} aria-hidden="true" /> WhatsApp
                </a>
              </div>
              <QrSvg text={url} label="QR code of the public link" size={132} />
            </div>
          )}
          {canEdit && (
            <div className="flex flex-wrap items-center gap-2">
              {!link && g.allowed && (
                <button type="button" className={btnPrimary} disabled={busy} onClick={create}>
                  Create public link
                </button>
              )}
              {link && g.allowed && !sure && (
                <button type="button" className={btn} disabled={busy} onClick={() => setSure(true)}>
                  Create new link
                </button>
              )}
              {link && (
                <button type="button" className={btn} disabled={busy} onClick={off}>
                  Turn off
                </button>
              )}
            </div>
          )}
          {sure && (
            <div role="alert" className="space-y-2 rounded bg-warn-bg p-2 text-[13px] text-warn">
              <p>A new link replaces the old one. The old link stops working at once.</p>
              <div className="flex gap-2">
                <button type="button" className={btnPrimary} disabled={busy} onClick={create}>
                  Replace link
                </button>
                <button type="button" className={btn} onClick={() => setSure(false)}>
                  Cancel
                </button>
              </div>
            </div>
          )}
          {!canEdit && !link && (
            <p className="text-[12px] text-muted">
              Only people who can edit this page can make a public link.
            </p>
          )}
        </>
      )}
      <ErrorText error={err} />
    </section>
  )
}
