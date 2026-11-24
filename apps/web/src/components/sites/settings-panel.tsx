'use client'
import { X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, ErrorText, Modal, Pill } from '@/components/ui'
import { type EditOp, type Site, type SiteData, type SiteMeta, siteApi, siteSummary } from '@/lib/sites'
import { CopyButton } from './publish-modal'

const Counter = ({ n, max }: { n: number; max: number }) => (
  <span className={`text-[12px] ${n > max ? 'text-warn' : 'text-muted'}`}>
    {n}/{max}
  </span>
)

/** Right-hand sheet from the gear in the top bar: address, how you appear on Google, form, plan, take offline. */
export function SettingsPanel({
  ws,
  site,
  data,
  meta,
  canAdmin,
  onClose,
  onEdit,
  onSite,
}: {
  ws: string
  site: Site
  data: SiteData
  meta: SiteMeta
  canAdmin: boolean
  onClose: () => void
  onEdit: (ops: EditOp[]) => Promise<void> | void
  onSite: (s: Site) => void
}) {
  const sum = siteSummary(data)
  const [title, setTitle] = useState(data.root.props.title ?? '')
  const [desc, setDesc] = useState(data.root.props.description ?? '')
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const first = useRef<HTMLButtonElement>(null)
  useEffect(() => first.current?.focus(), [])
  useEffect(() => {
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && !confirm && onClose()
    document.addEventListener('keydown', esc)
    return () => document.removeEventListener('keydown', esc)
  }, [confirm, onClose])

  const dirty = title !== (data.root.props.title ?? '') || desc !== (data.root.props.description ?? '')
  const live = !!site.published_version_id
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setErr(undefined)
    try {
      await fn()
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const host = site.url ? site.url.replace(/^https?:\/\//, '') : 'yourname.example'
  const left = meta.ai_edits ? Math.max(0, meta.ai_edits.limit - meta.ai_edits.used) : undefined

  return (
    <aside
      aria-label="Website settings"
      className="fixed inset-y-0 right-0 z-40 flex w-[480px] max-w-full flex-col border-l border-line bg-canvas shadow-xl"
    >
      <div className="flex items-center justify-between border-b border-line px-4 py-3">
        <h2 className="text-[15px] font-semibold">Website settings</h2>
        <button
          ref={first}
          type="button"
          aria-label="Close"
          onClick={onClose}
          className="rounded p-1 hover:bg-hover"
        >
          <X size={16} />
        </button>
      </div>
      <div className="flex-1 space-y-6 overflow-y-auto p-4 text-[13px]">
        <section className="space-y-2">
          <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">Address</h3>
          {site.subdomain && site.url ? (
            <div className="flex flex-wrap items-center gap-2">
              <span className="min-w-0 flex-1 truncate font-medium">{host}</span>
              <Pill tone={live ? 'ok' : 'neutral'}>{live ? 'Live' : 'Offline'}</Pill>
              <CopyButton text={site.url} label="Copy" />
            </div>
          ) : (
            <p className="text-muted">Not published yet. You choose the address when you publish.</p>
          )}
        </section>

        <section className="space-y-3">
          <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">
            How you appear on Google
          </h3>
          <div className="space-y-1">
            <div className="flex justify-between">
              <label htmlFor="seo-title" className="text-[12px] font-medium text-muted">
                Name shown on Google
              </label>
              <Counter n={title.length} max={60} />
            </div>
            <input
              id="seo-title"
              value={title}
              maxLength={200}
              onChange={(e) => setTitle(e.target.value)}
              className="w-full rounded-md border border-line bg-canvas px-2.5 py-1.5 text-[14px]"
            />
          </div>
          <div className="space-y-1">
            <div className="flex justify-between">
              <label htmlFor="seo-desc" className="text-[12px] font-medium text-muted">
                Short description
              </label>
              <Counter n={desc.length} max={160} />
            </div>
            <textarea
              id="seo-desc"
              rows={3}
              value={desc}
              onChange={(e) => setDesc(e.target.value)}
              className="w-full rounded-md border border-line bg-canvas px-2.5 py-1.5 text-[14px]"
            />
          </div>
          <figure className="rounded-md border border-line p-3">
            <p className="truncate text-[12px] text-muted">{host}</p>
            <p className="truncate text-[16px] text-accent">{title || sum.name || 'Your website'}</p>
            <p className="line-clamp-2 text-[13px]">{desc || sum.description || 'No description yet.'}</p>
            <figcaption className="mt-1 text-[11px] text-muted">Google may show this differently.</figcaption>
          </figure>
          <div className="flex justify-end">
            <button
              type="button"
              className={btnPrimary}
              disabled={!dirty || busy || desc.length > 160}
              onClick={() =>
                run(async () =>
                  onEdit([{ tool: 'set_page', input: { title: title.trim(), description: desc.trim() } }]),
                )
              }
            >
              Save
            </button>
          </div>
        </section>

        {sum.hasForm && (
          <section className="space-y-2">
            <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">Contact form</h3>
            <label className="flex items-start gap-2">
              <input
                type="checkbox"
                className="mt-0.5 size-4"
                checked={!!site.form_paused}
                disabled={busy || !canAdmin}
                onChange={(e) =>
                  run(async () => onSite((await siteApi.formPause(ws, site.id, e.target.checked)).site))
                }
              />
              <span>
                Pause the contact form
                <span className="block text-muted">
                  Visitors see your phone number instead. Changes apply within a minute.
                </span>
              </span>
            </label>
          </section>
        )}

        <section className="space-y-1">
          <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">Plan</h3>
          <p>
            {site.pro ? 'Website Pro' : 'Free plan'}
            {left !== undefined && meta.ai_edits ? ` · ${left} of ${meta.ai_edits.limit} AI edits left` : ''}
          </p>
          <p className="text-muted">
            "Made with C Note" badge: {site.pro ? 'hidden' : 'shown'}. Website Pro: more edits, no badge.
            Upgrade (coming soon).
          </p>
        </section>

        {live && canAdmin && (
          <section className="space-y-2">
            <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">Take offline</h3>
            <p className="text-muted">Hide your website from visitors. Your design is kept.</p>
            <button type="button" className={btn} onClick={() => setConfirm(true)}>
              Take offline
            </button>
          </section>
        )}
        <ErrorText error={err} />
      </div>
      <Modal open={confirm} onClose={() => setConfirm(false)} title="Take your website offline?">
        <div className="space-y-4 text-[13px]">
          <p>
            Visitors to {host} will see a "not available" page within a minute. Your design is kept and you
            can publish again any time with the same address.
          </p>
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setConfirm(false)}>
              Cancel
            </button>
            <button
              type="button"
              disabled={busy}
              className="inline-flex items-center rounded-md bg-bad px-3 py-1.5 text-[13px] font-medium text-canvas disabled:opacity-50"
              onClick={() =>
                run(async () => {
                  onSite((await siteApi.unpublish(ws, site.id)).site)
                  setConfirm(false)
                })
              }
            >
              Take offline
            </button>
          </div>
        </div>
      </Modal>
    </aside>
  )
}
