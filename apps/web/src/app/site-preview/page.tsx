'use client'
import { useEffect, useRef, useState } from 'react'
import { Renderer } from '@/components/sites/renderer'
import type { SiteData } from '@/lib/sites'

/** Rendered inside the builder's iframe, outside the workspace shell. Fetches nothing: the parent posts the data. */
export default function SitePreview() {
  const [data, setData] = useState<SiteData>()
  const [selected, setSelected] = useState<string | null>(null)
  const [pulse, setPulse] = useState<string | null>(null)
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
    <Renderer data={data} selectedId={selected} onSelect={readonly ? undefined : select} pulseId={pulse} />
  )
}
