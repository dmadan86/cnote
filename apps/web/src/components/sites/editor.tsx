'use client'
import { type Config, type Field, FieldLabel, Puck } from '@puckeditor/core'
import '@puckeditor/core/no-external.css'
import { Check, ImagePlus, X } from 'lucide-react'
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { CmdFailure } from '@/lib/api'
import { type Site, type SiteBlock, type SiteData, siteApi, type Theme, themeStyle } from '@/lib/sites'
import { BLOCKS } from './blocks'
import catalog from './catalog.json'
import { uploadImage } from './upload'

// Design section 6 and 17.11. Fields come from catalog.json; Go stays the only validator.

type Def = {
  kind: string
  label?: string
  max?: number
  values?: string[]
  labels?: string[]
  props?: Record<string, Def>
}
type Img = { id: string; filename: string }
type Ctx = { ws: string; siteId: string; assetBase: string; images: Img[]; addImage: (i: Img) => void }
type Obj = Record<string, unknown>

const inputCls =
  'w-full rounded-md border border-[var(--puck-color-grey-08,#ccc)] bg-white px-2 py-1.5 text-sm text-black'
const label = (k: string, d: Def) => d.label ?? k

/** Variant picker: the first field of every block, as a button group (wireframe thumbnails are a later polish). */
function VariantField({
  name,
  d,
  value,
  onChange,
}: {
  name: string
  d: Def
  value: unknown
  onChange: (v: string) => void
}) {
  const cur = typeof value === 'string' && value ? value : d.values?.[0]
  return (
    <FieldLabel label={label(name, d)}>
      <fieldset aria-label={label(name, d)} className="m-0 flex flex-wrap gap-2 border-0 p-0">
        {d.values?.map((v, i) => (
          <button
            key={v}
            type="button"
            aria-pressed={cur === v}
            onClick={() => onChange(v)}
            className={`flex min-h-10 items-center gap-1 rounded-md border px-3 text-sm ${cur === v ? 'border-blue-600 bg-blue-50 font-semibold text-blue-700' : 'border-gray-300 bg-white text-black'}`}
          >
            {cur === v && <Check size={14} aria-hidden="true" />}
            {d.labels?.[i] ?? v}
          </button>
        ))}
      </fieldset>
    </FieldLabel>
  )
}

function ImageField({
  name,
  d,
  value,
  onChange,
  ctx,
}: {
  name: string
  d: Def
  value: unknown
  onChange: (v: string) => void
  ctx: Ctx
}) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const id = typeof value === 'string' ? value : ''
  const pick = async (f?: File) => {
    if (!f) return
    setBusy(true)
    setErr('')
    try {
      const img = await uploadImage(ctx.ws, ctx.siteId, f)
      ctx.addImage(img)
      onChange(img.id)
    } catch (e) {
      setErr(e instanceof Error ? e.message : "That photo couldn't be uploaded")
    } finally {
      setBusy(false)
    }
  }
  return (
    <FieldLabel label={label(name, d)}>
      <div className="flex flex-col gap-2">
        {id && (
          // biome-ignore lint/performance/noImgElement: a site image served by the API
          <img src={`${ctx.assetBase}/${id}`} alt="" className="size-16 rounded-md object-cover" />
        )}
        {ctx.images.length > 0 && (
          <select
            aria-label={`${label(name, d)}: choose one of your photos`}
            className={inputCls}
            value={id}
            onChange={(e) => onChange(e.target.value)}
          >
            <option value="">No photo</option>
            {ctx.images.map((i) => (
              <option key={i.id} value={i.id}>
                {i.filename}
              </option>
            ))}
          </select>
        )}
        <div className="flex gap-2">
          <label className="inline-flex min-h-10 cursor-pointer items-center gap-1 rounded-md border border-gray-300 bg-white px-3 text-sm text-black">
            <ImagePlus size={14} aria-hidden="true" /> {busy ? 'Uploading…' : 'Upload a photo'}
            <input
              type="file"
              accept="image/jpeg,image/png"
              className="sr-only"
              disabled={busy}
              onChange={(e) => {
                pick(e.target.files?.[0])
                e.target.value = ''
              }}
            />
          </label>
          {id && (
            <button
              type="button"
              onClick={() => onChange('')}
              className="inline-flex min-h-10 items-center gap-1 rounded-md px-2 text-sm text-black underline"
            >
              <X size={14} aria-hidden="true" /> Remove
            </button>
          )}
        </div>
        {err && (
          <p role="alert" className="text-sm text-red-700">
            {err}
          </p>
        )}
      </div>
    </FieldLabel>
  )
}

function toField(name: string, d: Def, ctx: Ctx): Field {
  const base = { label: label(name, d) }
  if (name === 'variant' && d.kind === 'enum')
    return {
      type: 'custom',
      ...base,
      render: ({ value, onChange }) => <VariantField name={name} d={d} value={value} onChange={onChange} />,
    } as Field
  switch (d.kind) {
    case 'longtext':
      return { type: 'textarea', ...base }
    case 'enum':
      return {
        type: 'select',
        ...base,
        options: (d.values ?? []).map((v, i) => ({ label: d.labels?.[i] ?? v, value: v })),
      }
    case 'image':
      return {
        type: 'custom',
        ...base,
        render: ({ value, onChange }) => (
          <ImageField name={name} d={d} value={value} onChange={onChange} ctx={ctx} />
        ),
      } as Field
    case 'list': {
      const sub = d.props ?? {}
      const first = Object.keys(sub).find((k) => sub[k].kind === 'text') ?? Object.keys(sub)[0]
      return {
        type: 'array',
        ...base,
        max: d.max,
        arrayFields: Object.fromEntries(Object.entries(sub).map(([k, v]) => [k, toField(k, v, ctx)])),
        getItemSummary: (it: Obj, i?: number) => String(it?.[first] || `Item ${(i ?? 0) + 1}`),
      } as Field
    }
    default:
      // text, longtext-like short kinds, link, phone, email, color: plain text; Go validates the value.
      return { type: 'text', ...base }
  }
}

export function puckConfig(ctx: Ctx, theme: Theme | undefined, isHome: boolean): Config {
  const components: Obj = {}
  for (const [type, def] of Object.entries(
    catalog.blocks as Record<string, { label: string; shared?: boolean; props: Record<string, Def> }>,
  )) {
    if (def.shared && !isHome) continue // shared blocks live on Home only (design 17.10)
    // The variant picker comes first, as the UX spec asks.
    const entries = Object.entries(def.props).sort(
      ([a], [b]) => Number(b === 'variant') - Number(a === 'variant'),
    )
    const C = BLOCKS[type as keyof typeof BLOCKS] as React.ComponentType<Obj>
    components[type] = {
      label: def.label,
      fields: Object.fromEntries(entries.map(([k, d]) => [k, toField(k, d, ctx)])),
      render: ({ puck: _puck, ...p }: Obj) => <C {...p} assetBase={ctx.assetBase} editing />,
    }
  }
  return {
    components,
    root: {
      render: ({ children }: { children?: ReactNode }) => (
        // The canvas is an iframe: --site-* variables must be set inside it. Links never navigate.
        <div
          style={themeStyle(theme)}
          className="min-h-screen"
          onClickCapture={(e) => {
            if ((e.target as HTMLElement).closest('a')) e.preventDefault()
          }}
          onSubmitCapture={(e) => e.preventDefault()}
        >
          {children}
        </div>
      ),
    },
  } as unknown as Config
}

const pageContent = (d: SiteData, page: string, isHome: boolean) =>
  isHome ? d.content : (d.pages?.find((p) => p.slug === page)?.content ?? [])

export type SaveState = 'saved' | 'editing' | 'saving' | 'error' | 'conflict'

/** Puck's content is already our block list: each item is `{type, props: {id, ...}}` (spike, 17.14). */
const toBlocks = (content: unknown): SiteBlock[] => (Array.isArray(content) ? (content as SiteBlock[]) : [])

export function SiteEditor({
  ws,
  siteId,
  page,
  isHome,
  versionId,
  revision,
  data,
  images,
  assetBase,
  readOnly,
  debounceMs = 800,
  onSaved,
  onReload,
  onState,
  children,
}: {
  ws: string
  siteId: string
  /** Page slug the blocks belong to (set_content's `page`). */
  page: string
  isHome: boolean
  /** The draft version the person is looking at. Remount (key) when it moves for any reason but our own save. */
  versionId: string
  revision: number
  data: SiteData
  images: Img[]
  assetBase: string
  /** AI turn running or a version preview open: fields cannot be changed. */
  readOnly?: boolean
  debounceMs?: number
  onSaved?: (r: { site: Site; data: SiteData }) => void
  /** Conflict: the only way out is to reload the latest draft (design 17.11). */
  onReload: () => void
  onState?: (s: SaveState, message?: string) => void
  /** Replaces the default layout; use Puck.Preview, Puck.Fields, Puck.Outline, Puck.Components inside it. */
  children?: ReactNode
}) {
  const [imgs, setImgs] = useState(images)
  const [state, setStateRaw] = useState<SaveState>('saved')
  const [msg, setMsg] = useState('')
  const ver = useRef(versionId)
  const rev = useRef(revision)
  const ids = useRef(new Map<string, string>())
  const warnRef = useRef<((e: BeforeUnloadEvent) => void) | null>(null)
  const last = useRef(JSON.stringify(pageContent(data, page, isHome)))
  const pending = useRef<SiteBlock[] | null>(null)
  const inflight = useRef(false)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const alive = useRef(true)
  const stopped = useRef(false)
  const cb = useRef({ onSaved, onState })
  cb.current = { onSaved, onState }

  const setState = useCallback((s: SaveState, m = '') => {
    if (!alive.current) return
    setStateRaw(s)
    setMsg(m)
    cb.current.onState?.(s, m)
  }, [])

  const flush = useCallback(async () => {
    if (inflight.current || stopped.current || !pending.current) return
    const content = pending.current
    pending.current = null
    inflight.current = true
    setState('saving')
    try {
      // Each save uses the version id the previous one returned.
      const r = await siteApi.setContent(ws, siteId, ver.current, rev.current, page, content)
      ver.current = r.site.draft_version_id
      rev.current = r.site.rev
      last.current = JSON.stringify(content)
      cb.current.onSaved?.(r)
      setState(pending.current ? 'editing' : 'saved')
    } catch (e) {
      if (e instanceof CmdFailure && e.code === 'conflict') {
        stopped.current = true
        setState('conflict')
      } else {
        pending.current ??= content // keep the local copy so Retry resends it
        setState('error', e instanceof Error ? e.message : 'Could not save')
        return
      }
    } finally {
      inflight.current = false
    }
    if (pending.current && !stopped.current) {
      if (alive.current) timer.current = setTimeout(flush, debounceMs)
      else void flush()
    } else if (!alive.current && !pending.current && warnRef.current)
      window.removeEventListener('beforeunload', warnRef.current)
  }, [ws, siteId, page, debounceMs, setState])

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
      clearTimeout(timer.current)
      if (pending.current && !inflight.current && !stopped.current) void flush()
    }
  }, [flush])

  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (pending.current || inflight.current) e.preventDefault()
    }
    warnRef.current = warn
    window.addEventListener('beforeunload', warn)
    return () => {
      if (!pending.current && !inflight.current) window.removeEventListener('beforeunload', warn)
    }
  }, [])

  const config = useMemo(
    () =>
      puckConfig(
        {
          ws,
          siteId,
          assetBase,
          images: imgs,
          addImage: (i) => setImgs((l) => (l.some((x) => x.id === i.id) ? l : [...l, i])),
        },
        data.root?.props?.theme,
        isHome,
      ),
    [ws, siteId, assetBase, imgs, data.root?.props?.theme, isHome],
  )
  // Puck's data is the initial value only; the parent remounts us with a new key to reset it.
  const initial = useRef({ root: { props: {} }, content: pageContent(data, page, isHome) as never }).current

  const change = (d: { content?: unknown }) => {
    if (stopped.current) return
    const content = toBlocks(d.content).map((block) => {
      const id = String(block.props.id ?? '')
      if (new RegExp(`^${block.type}-[0-9a-f]{8}$`).test(id)) return block
      let next = ids.current.get(id)
      if (!next) {
        const bytes = crypto.getRandomValues(new Uint8Array(4))
        next = `${block.type}-${Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')}`
        ids.current.set(id, next)
      }
      return { ...block, props: { ...block.props, id: next } }
    })
    if (JSON.stringify(content) === last.current && !pending.current) return
    pending.current = content
    setState('editing')
    clearTimeout(timer.current)
    timer.current = setTimeout(flush, debounceMs)
  }
  const retry = () => {
    clearTimeout(timer.current)
    flush()
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 px-3 py-2 text-sm" aria-live="polite">
        {state === 'editing' && <span className="text-gray-500">● Editing…</span>}
        {state === 'saving' && <span className="text-gray-500">Saving…</span>}
        {state === 'saved' && (
          <span className="text-gray-600">
            <Check size={14} className="mr-1 inline" aria-hidden="true" />
            Saved
          </span>
        )}
        {state === 'error' && (
          <span className="text-red-700" role="alert">
            Not saved{msg ? `: ${msg}` : ''} ·{' '}
            <button type="button" onClick={retry} className="underline">
              Retry
            </button>
          </span>
        )}
      </div>
      {state === 'conflict' && (
        <div
          role="alert"
          className="mx-3 mb-2 rounded-md border border-amber-400 bg-amber-50 p-3 text-sm text-black"
        >
          This page was changed somewhere else, so your latest edits were not saved.{' '}
          <button type="button" onClick={onReload} className="font-semibold underline">
            Reload
          </button>
        </div>
      )}
      <Puck
        config={config}
        data={initial}
        onChange={change as never}
        permissions={
          readOnly || state === 'conflict'
            ? { drag: false, delete: false, duplicate: false, insert: false, edit: false }
            : undefined
        }
      >
        {children ?? (
          <div className="grid min-h-0 flex-1 grid-cols-[320px_1fr]">
            <div className="flex min-h-0 flex-col gap-3 overflow-y-auto border-r p-3">
              <details>
                <summary className="cursor-pointer text-sm font-semibold">Add a section</summary>
                <Puck.Components />
              </details>
              <Puck.Outline />
              <Puck.Fields />
            </div>
            <Puck.Preview />
          </div>
        )}
      </Puck>
    </div>
  )
}
