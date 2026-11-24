'use client'
import { Check, Copy, ExternalLink, Loader2, X } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls, Modal } from '@/components/ui'
import type { CmdFailure } from '@/lib/api'
import { qrMatrix, qrPath } from '@/lib/qr'
import { pageOgId, type Site, type SiteMeta, siteApi, validSub } from '@/lib/sites'

type View = 'choose' | 'verify' | 'publishing' | 'live' | 'unavailable'
type Avail = { s: 'idle' | 'checking' | 'ok' } | { s: 'bad'; reason: string; suggest: string[] }

const slug = (n: string) =>
  n
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
    .replace(/-+$/, '')

/** A PNG of the site's QR code (dark on white, with its quiet zone) so owners can print it for the counter. */
export async function downloadQr(url: string, name: string) {
  const q = qrPath(qrMatrix(url))
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 ${q.size} ${q.size}" shape-rendering="crispEdges"><rect width="${q.size}" height="${q.size}" fill="#fff"/><path d="${q.d}" fill="#000"/></svg>`
  const img = new Image()
  img.src = `data:image/svg+xml,${encodeURIComponent(svg)}`
  await img.decode()
  const c = document.createElement('canvas')
  c.width = c.height = 1024
  const g = c.getContext('2d')
  if (!g) return
  g.imageSmoothingEnabled = false
  g.drawImage(img, 0, 0, 1024, 1024)
  const blob = await new Promise<Blob | null>((ok) => c.toBlob(ok, 'image/png'))
  if (!blob) return
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `${name}-qr.png`
  a.click()
  setTimeout(() => URL.revokeObjectURL(a.href), 1000)
}

/** How a link looks when shared: a WhatsApp bubble first, Facebook and X second. `image` is the stored share picture. */
export function ShareCard({
  image,
  title,
  description,
  host,
}: {
  image?: string
  title: string
  description: string
  host: string
}) {
  const [tab, setTab] = useState<'wa' | 'fb'>('wa')
  const card = (
    <div className="overflow-hidden rounded-md border border-line bg-canvas">
      {image ? (
        // biome-ignore lint/performance/noImgElement: a site image served by the API
        <img src={image} alt="" className="aspect-[1200/630] w-full object-cover" />
      ) : (
        <div className="flex aspect-[1200/630] w-full items-center justify-center bg-hover px-3 text-center text-[12px] text-muted">
          The picture is made for you when you publish.
        </div>
      )}
      <div className="space-y-0.5 p-2">
        <p className="line-clamp-1 text-[13px] font-semibold">{title || 'Your website'}</p>
        <p className="line-clamp-2 text-[12px] text-muted">{description}</p>
        <p className="truncate text-[11px] text-muted">{host}</p>
      </div>
    </div>
  )
  return (
    <div className="space-y-2" data-testid="share-card">
      <div role="tablist" aria-label="Where it is shared" className="flex gap-1">
        {(['wa', 'fb'] as const).map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            onClick={() => setTab(t)}
            className={`rounded-md px-2.5 py-1 text-[12px] font-medium ${tab === t ? 'bg-hover' : 'text-muted hover:bg-hover'}`}
          >
            {t === 'wa' ? 'WhatsApp' : 'Facebook & X'}
          </button>
        ))}
      </div>
      <div className={`rounded-lg p-3 ${tab === 'wa' ? 'bg-ok-bg' : 'bg-hover'}`}>
        <div className={tab === 'wa' ? 'ml-auto max-w-[280px]' : 'mx-auto max-w-[360px]'}>{card}</div>
      </div>
      <p className="text-[12px] text-muted">Apps keep the old picture for a while after you change it.</p>
    </div>
  )
}

export const waShare = (url: string) => `https://wa.me/?text=${encodeURIComponent(url)}`

export function CopyButton({ text, label = 'Copy link' }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={btn}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text)
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        } catch {
          // clipboard blocked: the address is visible and selectable beside the button
        }
      }}
    >
      {done ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
      {done ? 'Copied' : label}
    </button>
  )
}

/** Publish: one modal, choose address, publishing, live. Republishing never opens it (the builder does that inline). */
export function PublishModal({
  ws,
  site,
  meta,
  domain,
  open,
  onClose,
  onSite,
  onReload,
  shareDesc = '',
}: {
  ws: string
  site: Site
  meta: SiteMeta
  domain?: string
  /** The Home page's description, shown on the share card. */
  shareDesc?: string
  open: boolean
  onClose: () => void
  onSite: (s: Site) => void
  onReload: () => void
}) {
  const live = !!site.published_version_id && !!site.url
  const [sub, setSub] = useState(() => slug(site.name))
  const [check, setCheck] = useState<Avail>({ s: 'idle' })
  const [view, setView] = useState<View>('choose')
  const [err, setErr] = useState<Error>()
  const [url, setUrl] = useState('')
  const [code, setCode] = useState('')
  const [sent, setSent] = useState(false)
  const [busy, setBusy] = useState(false)
  const [nonce, setNonce] = useState(0)
  const seq = useRef(0)
  const scheme = /^localhost(:|$)/.test(domain ?? '') ? 'http' : 'https'
  const unavailable = !domain || meta.can_publish?.configured === false
  const unverified = meta.can_publish?.verified === false
  const og = pageOgId(site, '')
  const shareImage = og ? `/api/w/${ws}/sites/${site.id}/img/${og}` : undefined

  // Each time the modal opens, start from the right view.
  // biome-ignore lint/correctness/useExhaustiveDependencies: only the open edge matters
  useEffect(() => {
    if (!open) return
    setErr(undefined)
    setCode('')
    setSent(false)
    setUrl(site.url ?? '')
    setView(unavailable ? 'unavailable' : live ? 'live' : unverified ? 'verify' : 'choose')
  }, [open])

  // Verified while the modal is open: move on to choosing the address.
  useEffect(() => {
    if (open && view === 'verify' && !unverified) setView('choose')
  }, [open, view, unverified])

  // Availability, checked as the owner types (400 ms debounce).
  useEffect(() => {
    if (view !== 'choose') return
    const s = sub.trim().toLowerCase()
    if (!s) return setCheck({ s: 'idle' })
    if (!validSub(s))
      return setCheck({ s: 'bad', reason: 'Use 3 to 40 small letters, numbers or hyphens.', suggest: [] })
    setCheck({ s: 'checking' })
    const mine = ++seq.current
    const t = setTimeout(async () => {
      try {
        const r = await siteApi.subdomainCheck(ws, site.id, s)
        if (mine !== seq.current) return
        if (r.available) return setCheck({ s: 'ok' })
        const alt = [2, 3].map((n) => `${s}-${n}`.slice(0, 40))
        setCheck(
          r.reason === 'taken'
            ? { s: 'bad', reason: 'That address is taken.', suggest: alt }
            : r.reason === 'reserved'
              ? { s: 'bad', reason: "That address can't be used. Please pick another.", suggest: [] }
              : { s: 'bad', reason: 'Use 3 to 40 small letters, numbers or hyphens.', suggest: [] },
        )
      } catch (e) {
        if (mine === seq.current) setCheck({ s: 'bad', reason: (e as Error).message, suggest: [] })
      }
    }, 400)
    return () => clearTimeout(t)
  }, [sub, view, ws, site.id])

  const publish = async () => {
    setErr(undefined)
    setView('publishing')
    try {
      const r = await siteApi.publish(ws, site.id, site.draft_version_id, sub.trim().toLowerCase())
      setUrl(r.url)
      onSite(r.site)
      setView('live')
    } catch (e) {
      const f = e as CmdFailure
      if (f.code === 'forbidden' && /verify/i.test(f.message)) {
        onReload()
        setView('verify')
      } else {
        setErr(f.code === 'conflict' ? new Error('That address was just taken. Try another one.') : f)
        setView('choose')
      }
    }
  }

  const sendCode = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      await siteApi.verifyRequest()
      setSent(true)
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const confirmCode = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      await siteApi.verifyConfirm(code.trim())
      onReload()
      setView('choose')
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }

  const full = `${scheme}://${sub.trim().toLowerCase()}.${domain}`
  const title =
    view === 'live'
      ? 'Your website is live'
      : view === 'verify'
        ? 'Verify your email'
        : 'Publish your website'
  return (
    <Modal
      key={nonce}
      open={open}
      // Publishing cannot be dismissed: re-mounting re-opens the dialog the browser just closed.
      onClose={() => (view === 'publishing' ? setNonce((n) => n + 1) : onClose())}
      title={title}
    >
      {view === 'unavailable' && (
        <div className="space-y-3 text-[13px]">
          <p role="status">
            Publishing isn't set up on this server yet. Ask whoever runs C Note for your team to set it up.
            Your website is saved as a draft.
          </p>
          <div className="flex justify-end">
            <button type="button" className={btn} onClick={onClose}>
              Close
            </button>
          </div>
        </div>
      )}

      {view === 'verify' && (
        <div className="space-y-3 text-[13px]">
          <p>To keep the web safe, we check that the person publishing owns their email address.</p>
          {!sent ? (
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={onClose}>
                Cancel
              </button>
              <button type="button" className={btnPrimary} disabled={busy} onClick={sendCode}>
                Email me a code
              </button>
            </div>
          ) : (
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault()
                confirmCode()
              }}
            >
              <label className="block space-y-1">
                <span className="text-[12px] font-medium text-muted">6-digit code from your email</span>
                <input
                  className={`${inputCls} tracking-[0.3em]`}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={6}
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                />
              </label>
              <div className="flex items-center justify-between gap-2">
                <button type="button" className="text-[12px] underline" disabled={busy} onClick={sendCode}>
                  Send a new code
                </button>
                <button type="submit" className={btnPrimary} disabled={busy || code.length !== 6}>
                  Verify
                </button>
              </div>
            </form>
          )}
          <ErrorText error={err} />
        </div>
      )}

      {view === 'choose' && (
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (check.s === 'ok') publish()
          }}
        >
          <label htmlFor="site-sub" className="block text-[13px] font-medium">
            Choose your website address
          </label>
          <div className="flex items-center rounded-md border border-line focus-within:border-accent">
            <input
              id="site-sub"
              // biome-ignore lint/a11y/noAutofocus: the one field of this dialog
              autoFocus
              value={sub}
              maxLength={40}
              autoComplete="off"
              spellCheck={false}
              aria-describedby="site-sub-note"
              onChange={(e) => setSub(e.target.value.toLowerCase().replace(/\s+/g, '-'))}
              className="min-w-0 flex-1 bg-transparent px-2.5 py-1.5 text-[14px] focus:outline-none"
            />
            <span className="pr-2.5 text-[14px] text-muted">.{domain}</span>
            {check.s === 'checking' && (
              <Loader2 size={14} className="mr-2 animate-spin" aria-label="Checking" />
            )}
          </div>
          <div id="site-sub-note" role="status" className="min-h-10 text-[13px]">
            {check.s === 'ok' && (
              <p className="flex items-center gap-1.5 text-ok">
                <Check size={14} aria-hidden="true" /> {full} is free
              </p>
            )}
            {check.s === 'bad' && (
              <p className="text-bad">
                <X size={14} aria-hidden="true" className="mr-1 inline" />
                {check.reason}
                {check.suggest.length > 0 && (
                  <>
                    {' '}
                    Try:{' '}
                    {check.suggest.map((x, i) => (
                      <span key={x}>
                        {i > 0 && ' · '}
                        <button type="button" className="underline" onClick={() => setSub(x)}>
                          {x}
                        </button>
                      </span>
                    ))}
                  </>
                )}
              </p>
            )}
            {(check.s === 'idle' || check.s === 'checking') && (
              <p className="text-muted">
                Small letters, numbers and hyphens. The address can't be changed later.
              </p>
            )}
          </div>
          <p className="text-[12px] text-muted">
            New enquiries appear in Leads{meta.lead_space ? `, in ${meta.lead_space.name}` : ''}.
          </p>
          <ErrorText error={err} />
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className={btnPrimary} disabled={check.s !== 'ok'}>
              Publish website
            </button>
          </div>
        </form>
      )}

      {view === 'publishing' && (
        <p className="flex items-center gap-2 py-6 text-[13px]" role="status">
          <Loader2 size={16} className="animate-spin" aria-hidden="true" /> Publishing… this takes a few
          seconds.
        </p>
      )}

      {view === 'live' && (
        <div className="space-y-3 text-[13px]">
          <p className="flex items-center gap-2 rounded-md bg-hover px-3 py-2 font-medium">
            <span className="min-w-0 flex-1 select-all truncate" data-testid="live-url">
              {url}
            </span>
          </p>
          <ShareCard
            image={shareImage}
            title={site.name}
            description={shareDesc}
            host={url.replace(/^https?:\/\//, '')}
          />
          <div className="flex flex-wrap gap-2">
            <CopyButton text={url} />
            <a className={btn} href={waShare(url)} target="_blank" rel="noopener noreferrer">
              Share on WhatsApp
            </a>
          </div>
          <a className={`${btnPrimary} w-full`} href={url} target="_blank" rel="noopener noreferrer">
            Visit website <ExternalLink size={14} aria-hidden="true" />
          </a>
          <button
            type="button"
            className="text-[12px] underline"
            onClick={() => downloadQr(url, site.subdomain || 'website')}
          >
            Download QR code
          </button>
          <hr className="border-line" />
          <p className="text-muted">
            New enquiries appear in Leads{meta.lead_space ? `, in ${meta.lead_space.name}` : ''}.{' '}
            <Link href={`/w/${ws}/crm/inbox`} className="underline">
              Open Leads
            </Link>
          </p>
        </div>
      )}
    </Modal>
  )
}
