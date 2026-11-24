'use client'
import { Monitor, Smartphone } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { type CatalogueView, type SiteData, themeStyle } from '@/lib/sites'
import { BLOCKS, SiteBadge } from './blocks'
import catalog from './catalog.json'

/** The page. No raw HTML anywhere: every value is a React text child. Without onSelect it is read-only. */
export function Renderer({
  data,
  selectedId,
  onSelect,
  pulseId,
  assetBase,
  pro,
  catalog: items,
  lang,
}: {
  data: SiteData
  selectedId?: string | null
  onSelect?: (id: string | null) => void
  pulseId?: string | null
  assetBase?: string
  /** The catalogue items the Catalogue block shows (the builder's chosen items); none shows its placeholder. */
  catalog?: CatalogueView
  /** false shows the plan badge strip (the preview of a free site); undefined shows nothing. */
  pro?: boolean
  /** The language of the blocks' own visitor text (strings.json); English when absent. */
  lang?: string
}) {
  const style = themeStyle(data.root?.props?.theme)
  const known = (data.content ?? []).filter((b) => b.type in BLOCKS)
  // The preview never navigates (design 14.9): every click (modifier and middle too) and submit is cancelled;
  // in-page anchors scroll. Capture phase, so block selection's stopPropagation cannot let a link through.
  const stop = (e: React.SyntheticEvent) => {
    const a = (e.target as HTMLElement).closest('a')
    if (!a && e.type !== 'submit') return
    e.preventDefault()
    const h = a?.getAttribute('href')
    if (e.type === 'click' && h?.startsWith('#'))
      document.getElementById(h.slice(1))?.scrollIntoView({ behavior: 'smooth' })
  }
  // Keyboard activation of a link or a form control (Enter, Space, browser shortcuts) is cancelled too.
  const stopKey = (e: React.KeyboardEvent) => {
    if (
      (e.target as HTMLElement).closest('a, button, input, select, textarea') &&
      (e.key === 'Enter' || e.key === ' ')
    )
      e.preventDefault()
  }
  return (
    <div
      style={style}
      className="min-h-screen"
      onKeyDownCapture={stopKey}
      onClickCapture={stop}
      onAuxClickCapture={stop}
      onSubmitCapture={stop}
    >
      {known.map((b) => {
        const id = String(b.props?.id ?? '')
        const C = BLOCKS[b.type as keyof typeof BLOCKS] as React.ComponentType<Record<string, unknown>>
        const label = (catalog.blocks as Record<string, { label: string }>)[b.type]?.label ?? b.type
        const selected = !!onSelect && id === selectedId
        const select = (e: React.SyntheticEvent) => {
          e.stopPropagation()
          onSelect?.(id)
        }
        return (
          // biome-ignore lint/a11y/noStaticElementInteractions: selectable only in the builder, where it gets role=button
          // biome-ignore lint/a11y/useAriaPropsSupportedByRole: the role is set whenever the label is
          <div
            key={id || b.type}
            id={id}
            data-block={id}
            role={onSelect ? 'button' : undefined}
            tabIndex={onSelect ? 0 : undefined}
            aria-label={onSelect ? `Select ${label}` : undefined}
            aria-pressed={onSelect ? selected : undefined}
            onClick={onSelect ? select : undefined}
            onKeyDown={
              onSelect
                ? (e) => {
                    if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) {
                      e.preventDefault()
                      select(e)
                    }
                  }
                : undefined
            }
            className={`group relative ${onSelect ? 'cursor-pointer outline-offset-[-2px] hover:outline hover:outline-1 hover:outline-accent/60 focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent' : ''} ${selected ? 'outline outline-2 outline-accent' : ''} ${pulseId === id ? 'animate-pulse' : ''}`}
          >
            {onSelect && (
              <span
                aria-hidden="true"
                className="pointer-events-none absolute left-0 top-0 z-10 hidden rounded-br bg-accent px-1.5 py-0.5 font-sans text-[11px] font-medium text-accent-fg group-hover:block"
              >
                {label}
              </span>
            )}
            <C
              {...b.props}
              assetBase={assetBase}
              lang={lang}
              editing={!!onSelect}
              catalog={b.type === 'Catalogue' ? items : undefined}
            />
          </div>
        )
      })}
      {pro === false && <SiteBadge pro={false} home="#" lang={lang} />}
    </div>
  )
}

/**
 * The preview iframe (docs/design/site-builder.md section 8). Same-origin, origin checked both sides.
 * frame -> parent: site:ready, site:select {id}.  parent -> frame: site:data {data, selected}.
 */
export function PreviewFrame({
  data,
  selected,
  onSelect,
  mobile,
  className = '',
  decorative,
  assetBase,
  pro,
  catalog: items,
}: {
  data: SiteData
  selected?: string | null
  onSelect?: (id: string | null) => void
  mobile?: boolean
  className?: string
  decorative?: boolean
  assetBase?: string
  pro?: boolean
  /** The draft catalogue the Catalogue block shows in the preview. */
  catalog?: CatalogueView | null
}) {
  const ref = useRef<HTMLIFrameElement>(null)
  const [ready, setReady] = useState(false)
  // Select ids are block ids; anything else from the frame is ignored.
  const onSel = useRef(onSelect)
  onSel.current = onSelect
  useEffect(() => {
    const h = (e: MessageEvent) => {
      if (e.origin !== location.origin || e.source !== ref.current?.contentWindow) return
      if (e.data?.type === 'site:ready') setReady(true)
      else if (e.data?.type === 'site:select')
        onSel.current?.(typeof e.data.id === 'string' ? e.data.id : null)
    }
    window.addEventListener('message', h)
    return () => window.removeEventListener('message', h)
  }, [])
  useEffect(() => {
    if (ready)
      ref.current?.contentWindow?.postMessage(
        { type: 'site:data', data, selected: selected ?? null, assetBase, pro, catalog: items ?? undefined },
        location.origin,
      )
  }, [ready, data, selected, assetBase, pro, items])
  return (
    <iframe
      ref={ref}
      onLoad={() => setReady(false)} // a new document must announce itself again
      src={onSelect ? '/site-preview' : '/site-preview?readonly=1'}
      title={decorative ? '' : 'Site preview'}
      aria-hidden={decorative || undefined}
      tabIndex={decorative ? -1 : undefined}
      className={`border-0 bg-canvas ${mobile ? 'mx-auto w-[390px] max-w-full' : 'w-full'} ${className}`}
    />
  )
}

export function DeviceToggle({ mobile, onChange }: { mobile: boolean; onChange: (m: boolean) => void }) {
  const cls = (on: boolean) => `rounded p-1.5 ${on ? 'bg-hover text-fg' : 'text-muted hover:bg-hover'}`
  return (
    <fieldset className="m-0 flex min-w-0 items-center gap-0.5 border-0 p-0" aria-label="Preview size">
      <button
        type="button"
        aria-pressed={!mobile}
        aria-label="Desktop"
        className={cls(!mobile)}
        onClick={() => onChange(false)}
      >
        <Monitor size={15} />
      </button>
      <button
        type="button"
        aria-pressed={mobile}
        aria-label="Mobile"
        className={cls(mobile)}
        onClick={() => onChange(true)}
      >
        <Smartphone size={15} />
      </button>
    </fieldset>
  )
}
