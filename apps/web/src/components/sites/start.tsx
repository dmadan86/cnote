'use client'
import { Building2, Globe } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, EmptyState, ErrorText, Modal } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { siteApi, type Template, type TemplateChoice } from '@/lib/sites'
import { DeviceToggle, PreviewFrame } from './renderer'

/** A 1280px page scaled into the card. */
function Thumb({ t }: { t: Template }) {
  const box = useRef<HTMLDivElement>(null)
  const [scale, setScale] = useState(0.25)
  useEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(() => setScale(el.clientWidth / 1280))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return (
    <div
      ref={box}
      className="relative aspect-[16/10] overflow-hidden rounded-t-lg border-b border-line bg-hover"
    >
      <div
        className="pointer-events-none absolute left-0 top-0 origin-top-left"
        style={{ transform: `scale(${scale})` }}
      >
        <PreviewFrame data={t.data} decorative hints className="h-[800px] w-[1280px]" />
      </div>
    </div>
  )
}

function Summary({ t }: { t: Template }) {
  const c = (k: string) => String(t.data.content.find((b) => b.type === 'ContactForm')?.props?.[k] ?? '')
  return <>{[t.data.root.props.title, c('phone'), c('email'), c('address')].filter(Boolean).join(' · ')}</>
}

const STYLES = ['Minimal', 'Bold', 'Warm', 'Corporate', 'Playful']
const chip = (on: boolean) =>
  `shrink-0 rounded-full border px-3 py-1 text-[13px] ${on ? 'border-accent bg-accent text-accent-fg' : 'border-line hover:bg-hover'}`

function Chips({
  label,
  all,
  items,
  value,
  onChange,
}: {
  label: string
  all: string
  items: string[]
  value: string
  onChange: (v: string) => void
}) {
  return (
    <fieldset className="m-0 flex min-w-0 gap-2 overflow-x-auto border-0 p-0 pb-1" aria-label={label}>
      {[all, ...items].map((c) => (
        <button
          key={c}
          type="button"
          aria-pressed={(c === all ? '' : c) === value}
          className={chip((c === all ? '' : c) === value)}
          onClick={() => onChange(c === all ? '' : c)}
        >
          {c}
        </button>
      ))}
    </fieldset>
  )
}

const Dots = ({ p }: { p: Template['palette'] }) => (
  <span aria-hidden="true" className="flex -space-x-1">
    {[p.background, p.primary, p.text].map((c, i) => (
      // biome-ignore lint/suspicious/noArrayIndexKey: three fixed colours
      <span key={i} className="size-4 rounded-full border border-line" style={{ background: c }} />
    ))}
  </span>
)

function Card({
  t,
  brand,
  busy,
  onPreview,
  onUse,
}: {
  t: Template
  brand: boolean
  busy: boolean
  onPreview: () => void
  onUse: () => void
}) {
  return (
    <li className="rounded-lg border border-line">
      <Thumb t={t} />
      <div className="space-y-3 p-3">
        <div>
          <h2 className="text-[14px] font-semibold">{t.name}</h2>
          <p className="mt-1 flex flex-wrap gap-1.5 text-[11px] text-muted">
            <span className="rounded border border-line px-1.5 py-0.5">{t.vertical}</span>
            <span className="rounded border border-line px-1.5 py-0.5">{t.style}</span>
          </p>
          <p className="mt-1.5 text-[13px] text-muted">{t.description}</p>
          {brand && t.brand_available && !t.brand_used && (
            <p className="mt-1 text-[12px] text-warn">Your brand colour is too faint for this design.</p>
          )}
        </div>
        <div className="flex gap-2">
          <button type="button" className={btn} onClick={onPreview}>
            Preview
          </button>
          <button type="button" className={btnPrimary} disabled={busy} onClick={onUse}>
            Use this
          </button>
        </div>
      </div>
    </li>
  )
}

export function Start({
  ws,
  canCreate,
  onCreated,
}: {
  ws: string
  canCreate: boolean
  onCreated: () => void
}) {
  const [brand, setBrand] = useState(false)
  const tpl = useCmd<Template[]>(ws, 'site.templates', { brand })
  const [previewKey, setPreviewKey] = useState<string | null>(null)
  // The palette picked in Preview: the key of the template it comes from; null is the design's own.
  const [paletteKey, setPaletteKey] = useState<string | null>(null)
  const [vertical, setVertical] = useState('')
  const [style, setStyle] = useState('')
  const [mobile, setMobile] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const list = tpl.data ?? []
  const first = list[0]
  const preview = list.find((t) => t.key === previewKey)
  const palette = list.find((t) => t.key === paletteKey)?.palette
  const requestKey = JSON.stringify({ key: previewKey, brand, theme: palette })
  const [result, setResult] = useState<{ key: string; template: Template }>()
  useEffect(() => {
    if (!previewKey) return
    let live = true
    siteApi
      .templates(ws, JSON.parse(requestKey))
      .then((templates) => {
        if (live && templates[0]) setResult({ key: requestKey, template: templates[0] })
      })
      .catch((error) => {
        if (live) setErr(error as Error)
      })
    return () => {
      live = false
    }
  }, [ws, previewKey, requestKey])
  const shown = result?.key === requestKey ? result.template : undefined
  const verticals = [...new Set(list.map((t) => t.vertical))]
  const styles = STYLES.filter((x) => list.some((t) => t.style === x))
  const match = (t: Template) => (!vertical || t.vertical === vertical) && (!style || t.style === style)
  const filtered = !!vertical || !!style
  const mine = filtered ? list.filter(match) : list
  const other = filtered ? list.filter((t) => !match(t)) : []
  // Other families' palettes to try on the open design: one per style, in the picker's order.
  const swatches = preview
    ? styles
        .filter((x) => x !== preview.style)
        .map((x) => list.find((t) => t.style === x))
        .filter((t): t is Template => !!t)
        .slice(0, 4)
    : []

  const use = async (t: Template, choice: TemplateChoice) => {
    setBusy(true)
    setErr(undefined)
    try {
      await siteApi.create(ws, t.key, { brand, ...choice })
      onCreated()
    } catch (e) {
      setErr(e as Error)
      setBusy(false)
    }
  }
  const openPreview = (t: Template) => {
    setPaletteKey(null)
    setPreviewKey(t.key)
  }
  const grid = (items: Template[]) => (
    <ul className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
      {items.map((t) => (
        <Card
          key={t.key}
          t={t}
          brand={brand}
          busy={busy}
          onPreview={() => openPreview(t)}
          onUse={() => use(t, {})}
        />
      ))}
    </ul>
  )

  if (!canCreate)
    return (
      <div className="p-6">
        <EmptyState icon={Globe} title="No website yet">
          Ask an admin to create it. It takes a few minutes.
        </EmptyState>
      </div>
    )
  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <div>
        <h1 className="text-xl font-semibold">Create your website</h1>
        <p className="text-[13px] text-muted">
          No website yet. Pick a design and get a first draft to edit. We will fill in your business details.
        </p>
      </div>
      {first && (
        <div className="flex items-start gap-3 rounded-lg border border-line p-3">
          <Building2 size={18} className="mt-0.5 shrink-0 text-muted" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-[14px] font-medium">
              <Summary t={first} />
            </p>
            <p className="text-[12px] text-muted">Copied into your site when it's created.</p>
          </div>
          <Link href={`/w/${ws}/settings?tab=Company`} className={btn}>
            Edit
          </Link>
        </div>
      )}
      {first?.too_long?.map((f) => (
        <p key={f} className="text-[13px] text-warn">
          Your {f} is too long for the site; you can type a shorter one in the chat.
        </p>
      ))}
      <ErrorText error={err ?? tpl.error} />
      {first?.brand_available && (
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={brand} onChange={(e) => setBrand(e.target.checked)} />
          Use my brand colour
          <span className="text-muted">(each design keeps its own colours unless you tick this)</span>
        </label>
      )}
      {list.length > 0 && (
        <div className="space-y-2">
          <Chips label="Business type" all="All" items={verticals} value={vertical} onChange={setVertical} />
          <Chips label="Style" all="Any style" items={styles} value={style} onChange={setStyle} />
        </div>
      )}
      {tpl.data?.length === 0 && (
        <EmptyState icon={Globe} title="No designs yet">
          Designs will appear here soon.
        </EmptyState>
      )}
      {mine.length > 0 ? (
        grid(mine)
      ) : (
        <p className="text-[13px] text-muted">No design matches both filters. Here are the others.</p>
      )}
      {other.length > 0 && (
        <>
          <h2 className="pt-2 text-[14px] font-semibold">Other designs</h2>
          {grid(other)}
        </>
      )}
      <Modal
        open={!!previewKey}
        onClose={() => setPreviewKey(null)}
        title={`Preview: ${shown?.name ?? ''}`}
        className="max-w-6xl"
      >
        {preview && (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <DeviceToggle mobile={mobile} onChange={setMobile} />
              <fieldset className="m-0 flex min-w-0 items-center gap-2 border-0 p-0" aria-label="Colours">
                {[null, ...swatches].map((t) => {
                  const p = t ? t.palette : preview.palette
                  const on = (t?.key ?? null) === paletteKey
                  const label = t ? `Colours from ${t.name}` : `${preview.name}'s own colours`
                  return (
                    <button
                      key={t?.key ?? 'own'}
                      type="button"
                      aria-pressed={on}
                      aria-label={label}
                      title={label}
                      onClick={() => setPaletteKey(t?.key ?? null)}
                      className={`rounded-full border p-1.5 ${on ? 'border-accent ring-2 ring-accent' : 'border-line'}`}
                    >
                      <Dots p={p} />
                    </button>
                  )
                })}
              </fieldset>
              <button
                type="button"
                className={btnPrimary}
                disabled={busy || !shown}
                onClick={() => shown && use(shown, { theme: palette })}
              >
                Use this design
              </button>
            </div>
            <div className="overflow-hidden rounded-lg border border-line bg-hover">
              {shown ? (
                <PreviewFrame data={shown.data} mobile={mobile} hints className="h-[70vh]" />
              ) : (
                <div className="h-[70vh]" />
              )}
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
