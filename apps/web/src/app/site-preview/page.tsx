'use client'
import { useEffect, useRef, useState } from 'react'
import { Renderer } from '@/components/sites/renderer'
import type { CatalogueView, SiteData } from '@/lib/sites'

/** The draft catalogue from the parent: the shape is checked, every value is rendered as text. */
function asCatalog(v: unknown): CatalogueView | undefined {
  const c = v as CatalogueView | undefined
  if (!c || !Array.isArray(c.items) || !['incl', 'plus', 'hide'].includes(c.mode)) return undefined
  const items = c.items.filter(
    (i) => i && typeof i.id === 'string' && typeof i.name === 'string' && typeof i.price === 'string',
  )
  return { mode: c.mode, items }
}

/** Rendered inside the builder's iframe, outside the workspace shell. Fetches nothing: the parent posts the data. */
export default function SitePreview() {
  const [data, setData] = useState<SiteData>()
  const [selected, setSelected] = useState<string | null>(null)
  const [pulse, setPulse] = useState<string | null>(null)
  const [ctx, setCtx] = useState<{ assetBase?: string; pro?: boolean; catalog?: CatalogueView }>({})
  const prev = useRef<Map<string, string>>(new Map())
  // The parent is the only thing that may drive this page; a standalone visit renders nothing.
  const embedded = typeof window !== 'undefined' && window.parent !== window

  useEffect(() => {
    const h = (e: MessageEvent) => {
      if (e.origin !== location.origin || e.source !== window.parent) return
      if (e.data?.type !== 'site:data') return
      const d = e.data.data as SiteData
      if (
        !Array.isArray(d?.content) ||
        d.content.some((b) => typeof b?.type !== 'string' || typeof b.props !== 'object')
      )
        return
      if (e.data.selected !== null && typeof e.data.selected !== 'string') return
      const next = new Map<string, string>()
      let changed: string | null = null
      for (const b of d?.content ?? []) {
        const id = String(b.props?.id ?? '')
        const s = JSON.stringify(b)
        next.set(id, s)
        if (prev.current.size && prev.current.get(id) !== s) changed = id
      }
      prev.current = next
      setData(d)
      // The asset base is a same-origin API path or nothing; a boolean badge flag is all else the parent sends.
      const ab = e.data.assetBase
      setCtx({
        assetBase: typeof ab === 'string' && /^\/api\/[\w/-]+$/.test(ab) ? ab : undefined,
        pro: typeof e.data.pro === 'boolean' ? e.data.pro : undefined,
        catalog: asCatalog(e.data.catalog),
      })
      setSelected(typeof e.data.selected === 'string' ? e.data.selected : null)
      if (changed) {
        setPulse(changed)
        document.getElementById(changed)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
        setTimeout(() => setPulse(null), 1500)
      }
    }
    window.addEventListener('message', h)
    const ready = () => window.parent.postMessage({ type: 'site:ready' }, location.origin)
    ready()
    // The parent resets readiness when this document finishes loading, so announce again then.
    if (document.readyState !== 'complete') window.addEventListener('load', ready, { once: true })
    return () => {
      window.removeEventListener('message', h)
      window.removeEventListener('load', ready)
    }
  }, [])

  if (!embedded || !data) return null
  const select = (id: string | null) =>
    window.parent.postMessage({ type: 'site:select', id }, location.origin)
  // ?readonly=1 (template preview, thumbnails): no outlines, nothing selectable.
  const readonly = new URLSearchParams(location.search).has('readonly')
  return (
    <Renderer
      data={data}
      selectedId={selected}
      onSelect={readonly ? undefined : select}
      pulseId={pulse}
      assetBase={ctx.assetBase}
      pro={ctx.pro}
      catalog={ctx.catalog}
    />
  )
}
