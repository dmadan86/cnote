'use client'
import { ChevronDown, ChevronRight, ImagePlus, Loader2, Search } from 'lucide-react'
import Link from 'next/link'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls, Modal } from '@/components/ui'
import {
  type CatalogCandidate,
  type CatalogItem,
  type CataloguePriceMode,
  type CatalogView,
  catalogApi,
  catalogueView,
  previewSnapshot,
  priceText,
  type Site,
  type SiteData,
  themeStyle,
} from '@/lib/sites'
import { Catalogue } from './blocks'
import { ACCEPT, uploadImage } from './upload'

type Row = CatalogItem & { on: boolean }
type Img = { id: string; filename: string }
const asRow = (c: CatalogCandidate): Row => ({
  item_id: c.id,
  name: c.name,
  unit: c.unit,
  price_minor: c.price_minor,
  gst_rate_bp: c.gst_rate_bp,
  category: '',
  description: '',
  image_id: null,
  on: false,
})
const MODES: { key: CataloguePriceMode; label: string; note: string }[] = [
  { key: 'show_incl', label: 'Price with GST', note: 'GST is added for you' },
  { key: 'show_plus', label: 'Price + GST', note: 'Your price, with "+ GST" after it' },
  { key: 'hide', label: "Don't show prices", note: '"Ask for price"' },
]
const picks = (rows: Row[]) =>
  rows
    .filter((r) => r.on)
    .map((r) => ({
      item_id: r.item_id,
      category: r.category.trim(),
      description: r.description.trim(),
      image_id: r.image_id ?? null,
    }))

/** Full-height modal: choose which invoicing items appear on the website, how prices read, and see it live. */
export function ItemPicker({
  ws,
  site,
  data,
  images,
  open,
  onClose,
  onSaved,
  onImage,
}: {
  ws: string
  site: Site
  data: SiteData
  images: Img[]
  open: boolean
  onClose: () => void
  onSaved: (v: CatalogView) => void
  onImage: (i: Img) => void
}) {
  const [rows, setRows] = useState<Row[]>([])
  const [orig, setOrig] = useState<string[]>([])
  const [mode, setMode] = useState<CataloguePriceMode>('show_incl')
  const [sig0, setSig0] = useState('')
  const [limit, setLimit] = useState(0)
  const [loading, setLoading] = useState(false)
  const [more, setMore] = useState<string | null>(null)
  const [q, setQ] = useState('')
  const [filter, setFilter] = useState<'all' | 'on' | 'nophoto'>('all')
  const [shut, setShut] = useState<Set<string>>(new Set())
  const [pane, setPane] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const seq = useRef(0)
  const fileFor = useRef<string>('')
  const file = useRef<HTMLInputElement>(null)
  const assetBase = `/api/w/${ws}/sites/${site.id}/img`

  // Open: load the saved selection and the first page of items.
  // biome-ignore lint/correctness/useExhaustiveDependencies: only the open edge matters
  useEffect(() => {
    if (!open) return
    setQ('')
    setFilter('all')
    setErr(undefined)
    setLoading(true)
    catalogApi.get(ws, site.id).then(
      (v) => {
        const on = v.items.map((i): Row => ({ ...i, on: !i.missing }))
        const have = new Set(on.map((r) => r.item_id))
        setRows([...on, ...(v.candidates ?? []).filter((c) => !have.has(c.id)).map(asRow)])
        setOrig(v.items.map((i) => i.item_id))
        setMode(v.price_mode)
        setLimit(v.limit)
        setMore(v.next_cursor ?? null)
        setSig0(JSON.stringify([v.price_mode, picks(on)]))
        setLoading(false)
      },
      (e) => {
        setErr(e as Error)
        setLoading(false)
      },
    )
  }, [open])

  // Search: keyset-paged on the server; ticked rows always stay.
  const search = useCallback(
    async (text: string, cursor?: string) => {
      const mine = ++seq.current
      try {
        const v = await catalogApi.get(ws, site.id, text, cursor)
        if (mine !== seq.current) return
        setMore(v.next_cursor ?? null)
        setRows((prev) => {
          const keep = prev.filter((r) => r.on || (cursor && !r.on))
          const have = new Set(keep.map((r) => r.item_id))
          return [...keep, ...(v.candidates ?? []).filter((c) => !have.has(c.id)).map(asRow)]
        })
      } catch (e) {
        if (mine === seq.current) setErr(e as Error)
      }
    },
    [ws, site.id],
  )
  const first = useRef(true)
  // biome-ignore lint/correctness/useExhaustiveDependencies: the first run is the load above
  useEffect(() => {
    if (!open) return
    if (first.current) {
      first.current = false
      return
    }
    const t = setTimeout(() => search(q.trim()), 300)
    return () => clearTimeout(t)
  }, [q])
  useEffect(() => {
    if (!open) first.current = true
  }, [open])

  const patch = (id: string, p: Partial<Row>) =>
    setRows((rs) => rs.map((r) => (r.item_id === id ? { ...r, ...p } : r)))
  const onSet = useMemo(() => new Set(rows.filter((r) => r.on).map((r) => r.item_id)), [rows])
  const adding = [...onSet].filter((id) => !orig.includes(id)).length
  const removing = orig.filter((id) => !onSet.has(id)).length
  const noPhoto = rows.filter((r) => r.on && !r.image_id).length
  const dirty = JSON.stringify([mode, picks(rows)]) !== sig0
  const needle = q.trim().toLowerCase()
  const shown = rows.filter(
    (r) =>
      (!needle || r.name.toLowerCase().includes(needle) || r.on) &&
      (filter === 'all' || (filter === 'on' ? r.on : r.on && !r.image_id)),
  )
  const groups = useMemo(() => {
    const g = new Map<string, Row[]>()
    for (const r of shown) g.set(r.category.trim(), [...(g.get(r.category.trim()) ?? []), r])
    return [...g.entries()].sort(([a], [b]) => (a === '' ? 1 : b === '' ? -1 : 0))
  }, [shown])
  const cats = [...new Set(rows.map((r) => r.category.trim()).filter(Boolean))]
  const sample = rows.find((r) => r.on) ?? { price_minor: 100000, gst_rate_bp: 1800, unit: '' }

  const pickFile = async (f?: File) => {
    const id = fileFor.current
    if (!f || !id) return
    try {
      const img = await uploadImage(ws, site.id, f)
      onImage(img)
      patch(id, { image_id: img.id })
    } catch (e) {
      setErr(e as Error)
    }
  }
  const save = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      const v = await catalogApi.set(ws, site.id, mode, picks(rows))
      onSaved(v)
      onClose()
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }

  const block = (data.content ?? []).find((b) => b.type === 'Catalogue')?.props ?? {}
  const live = (
    <div style={themeStyle(data.root?.props?.theme)} className="min-h-full">
      <Catalogue
        {...block}
        catalog={catalogueView(
          previewSnapshot(
            rows.filter((r) => r.on),
            mode,
          ),
        )}
        assetBase={assetBase}
        editing={false}
      />
    </div>
  )
  const empty = !loading && !rows.length && !q.trim()

  return (
    <Modal open={open} onClose={onClose} title="Choose what to show on your website" className="max-w-5xl">
      <div className="flex h-[calc(90vh-5.5rem)] min-h-[360px] flex-col gap-3 text-[13px]">
        <div className="flex min-h-0 flex-1 gap-4">
          <div className={`flex min-w-0 flex-1 flex-col gap-2 ${pane ? 'max-md:hidden' : ''}`}>
            <label className="flex items-center gap-2 rounded-md border border-line px-2.5 py-1.5 focus-within:border-accent">
              <Search size={14} aria-hidden="true" className="text-muted" />
              <input
                aria-label="Search your items"
                placeholder="Search your items"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                className="min-w-0 flex-1 bg-transparent text-[14px] focus:outline-none"
              />
            </label>
            <fieldset className="m-0 flex min-w-0 flex-wrap gap-1.5 border-0 p-0" aria-label="Filter">
              {(
                [
                  ['all', 'All'],
                  ['on', `On website ${onSet.size}`],
                  ['nophoto', `No photo ${noPhoto}`],
                ] as const
              ).map(([k, l]) => (
                <button
                  key={k}
                  type="button"
                  aria-pressed={filter === k}
                  onClick={() => setFilter(k)}
                  className={`rounded-full border px-2.5 py-0.5 text-[12px] ${filter === k ? 'border-accent bg-hover font-medium' : 'border-line hover:bg-hover'}`}
                >
                  {l}
                </button>
              ))}
            </fieldset>
            <div className="min-h-0 flex-1 space-y-2 overflow-y-auto pr-1">
              {loading && (
                <p className="flex items-center gap-2 text-muted" role="status">
                  <Loader2 size={14} className="animate-spin" aria-hidden="true" /> Loading your items…
                </p>
              )}
              {empty && (
                <div className="rounded-md border border-dashed border-line p-4 text-center">
                  <p>You haven't added any items yet. Add them in Items and they'll appear here.</p>
                  <Link href={`/w/${ws}/billing/stock`} className={`${btn} mt-2`}>
                    Go to Items
                  </Link>
                </div>
              )}
              {!loading && !empty && !groups.length && <p className="text-muted">No items match.</p>}
              {groups.map(([cat, list]) => {
                const ticked = list.filter((r) => r.on).length
                const closed = shut.has(cat)
                return (
                  <section
                    key={cat || '-'}
                    aria-label={cat || 'No category'}
                    className="rounded-md border border-line"
                  >
                    <div className="flex items-center gap-2 bg-hover px-2.5 py-1.5">
                      <input
                        type="checkbox"
                        aria-label={`Select all in ${cat || 'No category'}`}
                        className="size-4"
                        checked={ticked === list.length}
                        ref={(el) => {
                          if (el) el.indeterminate = ticked > 0 && ticked < list.length
                        }}
                        onChange={(e) => {
                          const ids = new Set(list.filter((r) => !r.missing).map((r) => r.item_id))
                          setRows((rs) =>
                            rs.map((r) => (ids.has(r.item_id) ? { ...r, on: e.target.checked } : r)),
                          )
                        }}
                      />
                      <button
                        type="button"
                        aria-expanded={!closed}
                        className="flex flex-1 items-center gap-1 text-left font-medium"
                        onClick={() =>
                          setShut((s) => {
                            const n = new Set(s)
                            if (!n.delete(cat)) n.add(cat)
                            return n
                          })
                        }
                      >
                        {closed ? (
                          <ChevronRight size={14} aria-hidden="true" />
                        ) : (
                          <ChevronDown size={14} aria-hidden="true" />
                        )}
                        {cat || 'No category'}
                        <span className="ml-auto text-[12px] font-normal text-muted">
                          {ticked} of {list.length}
                        </span>
                      </button>
                    </div>
                    {!closed && (
                      <ul className="divide-y divide-line">
                        {list.map((r) => (
                          <li key={r.item_id} className="space-y-2 px-2.5 py-2">
                            <div className="flex items-center gap-2">
                              <input
                                type="checkbox"
                                aria-label={r.name}
                                className="size-4"
                                checked={r.on}
                                disabled={r.missing}
                                onChange={(e) => patch(r.item_id, { on: e.target.checked })}
                              />
                              {r.image_id ? (
                                // biome-ignore lint/performance/noImgElement: a site image served by the API
                                <img
                                  src={`${assetBase}/${r.image_id}`}
                                  alt=""
                                  className="size-9 rounded object-cover"
                                />
                              ) : (
                                <span aria-hidden="true" className="size-9 rounded bg-hover" />
                              )}
                              <span className="min-w-0 flex-1 truncate">{r.name}</span>
                              <span className="shrink-0 text-muted">
                                {priceText(mode, r.price_minor, r.gst_rate_bp, r.unit)}
                              </span>
                            </div>
                            {r.missing && (
                              <p className="pl-6 text-[12px] text-warn">
                                No longer in Items. It is removed when you save.
                              </p>
                            )}
                            {r.on && (
                              <div className="space-y-1.5 pl-6">
                                <div className="flex gap-2">
                                  <input
                                    aria-label={`Category for ${r.name}`}
                                    placeholder="Category"
                                    list="site-cats"
                                    maxLength={40}
                                    value={r.category}
                                    onChange={(e) => patch(r.item_id, { category: e.target.value })}
                                    className={`${inputCls} w-40`}
                                  />
                                  <input
                                    aria-label={`Description for ${r.name}`}
                                    placeholder="Short description"
                                    maxLength={300}
                                    value={r.description}
                                    onChange={(e) => patch(r.item_id, { description: e.target.value })}
                                    className={inputCls}
                                  />
                                </div>
                                <div className="flex flex-wrap items-center gap-2 text-[12px]">
                                  {!r.image_id && <span className="text-muted">No photo</span>}
                                  <button
                                    type="button"
                                    className="inline-flex items-center gap-1 underline"
                                    onClick={() => {
                                      fileFor.current = r.item_id
                                      file.current?.click()
                                    }}
                                  >
                                    <ImagePlus size={12} aria-hidden="true" />{' '}
                                    {r.image_id ? 'Change photo' : 'Add photo'}
                                  </button>
                                  {images.length > 0 && (
                                    <select
                                      aria-label={`Choose a photo for ${r.name}`}
                                      value={r.image_id ?? ''}
                                      onChange={(e) => patch(r.item_id, { image_id: e.target.value || null })}
                                      className="rounded border border-line bg-canvas px-1 py-0.5"
                                    >
                                      <option value="">No photo</option>
                                      {images.map((i) => (
                                        <option key={i.id} value={i.id}>
                                          {i.filename}
                                        </option>
                                      ))}
                                    </select>
                                  )}
                                </div>
                              </div>
                            )}
                          </li>
                        ))}
                      </ul>
                    )}
                  </section>
                )
              })}
              <datalist id="site-cats">
                {cats.map((c) => (
                  <option key={c} value={c} />
                ))}
              </datalist>
              {more && (
                <button type="button" className={btn} onClick={() => search(q.trim(), more)}>
                  Show more items
                </button>
              )}
              {noPhoto > 0 && filter !== 'nophoto' && (
                <p className="rounded-md bg-warn-bg px-2.5 py-1.5 text-warn">
                  {noPhoto} chosen {noPhoto === 1 ? 'item has' : 'items have'} no photo. They'll show a plain
                  tile.{' '}
                  <button type="button" className="underline" onClick={() => setFilter('nophoto')}>
                    Show them
                  </button>
                </p>
              )}
            </div>
          </div>

          <div
            className={`flex w-full min-w-0 flex-col gap-3 md:w-[340px] md:shrink-0 ${pane ? '' : 'max-md:hidden'}`}
          >
            <fieldset className="space-y-1.5">
              <legend className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted">
                Prices on your website
              </legend>
              {MODES.map((m) => (
                <label key={m.key} className="flex items-start gap-2">
                  <input
                    type="radio"
                    name="price-mode"
                    className="mt-0.5"
                    checked={mode === m.key}
                    onChange={() => setMode(m.key)}
                  />
                  <span>
                    {m.label}
                    <span className="block text-muted">
                      {priceText(m.key, sample.price_minor, sample.gst_rate_bp)}{' '}
                      <span className="text-[12px]">· {m.note}</span>
                    </span>
                  </span>
                </label>
              ))}
              <p className="text-[12px] text-muted">
                This only changes what visitors read. Items and tax stay as set in Items.
              </p>
            </fieldset>
            <div className="min-h-0 flex-1 overflow-y-auto rounded-md border border-line">
              <p className="border-b border-line px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-muted">
                Preview
              </p>
              {onSet.size ? live : <p className="p-3 text-muted">Tick items to see them here.</p>}
            </div>
          </div>
        </div>
        <ErrorText error={err} />
        <p className="text-[12px] text-muted">
          Removing an item takes it off your website. Undo does not bring it back.
        </p>
        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-3">
          <span className="min-w-0 flex-1" role="status">
            {onSet.size}
            {limit ? ` of ${limit}` : ''} items on your website · adding {adding}, removing {removing}
          </span>
          <button type="button" className={`${btn} md:hidden`} onClick={() => setPane((p) => !p)}>
            {pane ? 'Items' : 'Preview and prices'}
          </button>
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="button" className={btnPrimary} disabled={!dirty || busy || loading} onClick={save}>
            {busy && <Loader2 size={14} className="animate-spin" aria-hidden="true" />} Save
          </button>
        </div>
      </div>
      <input
        ref={file}
        type="file"
        accept={ACCEPT}
        hidden
        data-testid="item-photo-input"
        onChange={(e) => {
          const f = e.target.files?.[0]
          e.target.value = ''
          pickFile(f)
        }}
      />
    </Modal>
  )
}
