'use client'
import { useState } from 'react'
import type { Editor } from 'tldraw'
import { btn, btnPrimary, ErrorText, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'

export type BrandKit = { colors: string[]; fonts: string[]; logo_url: string }

// tldraw styles are a fixed palette (no hex) and four font families, so brand values snap to the nearest.
const PALETTE: Record<string, string> = {
  black: '#1d1d1d',
  grey: '#9fa8b2',
  'light-violet': '#e085f4',
  violet: '#ae3ec9',
  blue: '#4465e9',
  'light-blue': '#4ba1f1',
  yellow: '#f1ac4b',
  orange: '#e16919',
  green: '#099268',
  'light-green': '#4cb05e',
  'light-red': '#f87777',
  red: '#e03131',
}
const rgb = (h: string) => [1, 3, 5].map((i) => Number.parseInt(h.slice(i, i + 2), 16))
function nearest(hex: string) {
  const c = rgb(hex)
  let best = 'black'
  let d = Number.POSITIVE_INFINITY
  for (const [name, p] of Object.entries(PALETTE)) {
    const dist = rgb(p).reduce((a, v, i) => a + (v - c[i]) ** 2, 0)
    if (dist < d) [best, d] = [name, dist]
  }
  return best
}
const family = (f: string) =>
  /mono|code|courier/i.test(f)
    ? 'mono'
    : /georgia|times|garamond|serif/i.test(f) && !/sans/i.test(f)
      ? 'serif'
      : 'sans'
const list = (s: string) =>
  s
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean)

export function BrandPanel({
  ws,
  editor,
  kit,
  admin,
  onSaved,
}: {
  ws: string
  editor: Editor
  kit: BrandKit
  admin: boolean
  onSaved: (k: BrandKit) => void
}) {
  const [colors, setColors] = useState(kit.colors.join(', '))
  const [fonts, setFonts] = useState(kit.fonts.join(', '))
  const [logo, setLogo] = useState(kit.logo_url)
  const [err, setErr] = useState<string>()

  async function save() {
    const next = {
      colors: list(colors).filter((c) => /^#[0-9a-f]{6}$/i.test(c)),
      fonts: list(fonts),
      logo_url: logo,
    }
    try {
      await cmd(ws, 'workspace.update', { settings: { brand_kit: next } })
      onSaved(next)
      setErr(undefined)
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  function apply(prop: 'color' | 'font', value: string) {
    editor.run(() => {
      for (const id of editor.getSelectedShapeIds()) {
        const s = editor.getShape(id)
        if (s && prop in s.props) editor.updateShape({ id, type: s.type, props: { [prop]: value } })
      }
    })
  }

  return (
    <div
      role="dialog"
      aria-label="Brand kit"
      className="absolute right-3 top-3 z-[400] w-72 space-y-3 rounded-lg border border-line bg-canvas p-3 shadow-lg"
    >
      <h2 className="text-[14px] font-semibold">Brand kit</h2>
      <p className="text-[12px] text-muted">Select shapes, then pick a colour or font to apply.</p>
      <div className="flex flex-wrap gap-1.5">
        {kit.colors.length === 0 && <span className="text-[12px] text-muted">No colours yet.</span>}
        {kit.colors.map((c) => (
          <button
            key={c}
            type="button"
            aria-label={`Apply ${c}`}
            title={c}
            onClick={() => apply('color', nearest(c))}
            style={{ background: c }}
            className="size-6 rounded border border-line"
          />
        ))}
      </div>
      <div className="flex flex-wrap gap-1.5">
        {kit.fonts.map((f) => (
          <button key={f} type="button" className={btn} onClick={() => apply('font', family(f))}>
            {f}
          </button>
        ))}
      </div>
      {/^https:\/\//i.test(kit.logo_url) && (
        <img src={kit.logo_url} referrerPolicy="no-referrer" alt="Brand logo" className="max-h-12" />
      )}
      {admin && (
        <div className="space-y-2 border-t border-line pt-2">
          <input
            aria-label="Brand colours"
            className={inputCls}
            placeholder="#4465e9, #1d1d1d"
            value={colors}
            onChange={(e) => setColors(e.target.value)}
          />
          <input
            aria-label="Brand fonts"
            className={inputCls}
            placeholder="Inter, Georgia"
            value={fonts}
            onChange={(e) => setFonts(e.target.value)}
          />
          <input
            aria-label="Logo URL"
            className={inputCls}
            placeholder="https://..."
            value={logo}
            onChange={(e) => setLogo(e.target.value)}
          />
          <ErrorText error={err} />
          <button type="button" className={btnPrimary} onClick={save}>
            Save brand kit
          </button>
        </div>
      )}
    </div>
  )
}
