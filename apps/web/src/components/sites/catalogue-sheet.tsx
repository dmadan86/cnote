'use client'
import { ImageIcon, MessageCircle, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { type CatalogueItemView, type CatMode, safeHref, waHref } from '@/lib/sites'
import { t } from './strings'

const STEP = 12
const btn =
  'inline-flex min-h-12 w-full items-center justify-center gap-2 rounded-[var(--site-radius)] px-5 text-[15px] font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-primary)]'
const chip =
  'min-h-10 shrink-0 rounded-full border border-[var(--site-border)] px-4 text-[14px] font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-primary)]'

/** The item id in `?item=`, read only on the client; a hash (the "Ask a question" link) means the form, not the sheet. */
function readItem(ids: Set<string>) {
  const id = new URLSearchParams(location.search).get('item')?.toLowerCase() ?? ''
  return !location.hash && ids.has(id) ? id : null
}

/**
 * The catalogue grid or list with category chips, "Show more" and the item sheet (design 2, UX section 2).
 * Rendered on the server too, so every name and shown price is in the HTML; a hidden price is never in the props.
 */
export function CatalogueView({
  items,
  mode,
  variant,
  enquire,
  assetBase,
  phone,
  askPath,
  formId,
  blocked,
  lang = 'en',
}: {
  items: CatalogueItemView[]
  mode: CatMode
  variant?: string
  enquire?: string
  assetBase?: string
  /** The phone of the WhatsAppButton or ContactForm block; none, no WhatsApp button. */
  phone?: string
  /** The page path holding the ContactForm ('/' or '/slug') and its block id; none, no "Ask a question". */
  askPath?: string
  formId?: string
  blocked?: ReadonlySet<string>
  lang?: string
}) {
  const [cat, setCat] = useState('')
  const [shown, setShown] = useState(STEP)
  const [open, setOpen] = useState<string | null>(null)
  const dlg = useRef<HTMLDialogElement>(null)
  const opener = useRef<HTMLElement | null>(null)
  const ids = useRef(new Set(items.map((i) => i.id)))
  ids.current = new Set(items.map((i) => i.id))

  useEffect(() => {
    const sync = () => setOpen(readItem(ids.current))
    sync()
    window.addEventListener('popstate', sync)
    return () => window.removeEventListener('popstate', sync)
  }, [])
  useEffect(() => {
    const d = dlg.current
    if (!d) return
    if (open && !d.open) d.showModal()
    if (!open && d.open) d.close()
  }, [open])

  const show = (id: string, from: HTMLElement) => {
    opener.current = from
    const u = new URL(location.href)
    u.searchParams.set('item', id)
    u.hash = ''
    history.pushState({ sheet: 1 }, '', u)
    setOpen(id)
  }
  const close = useCallback(() => {
    if (history.state?.sheet) history.back()
    else {
      const u = new URL(location.href)
      u.searchParams.delete('item')
      history.replaceState(null, '', u)
      setOpen(null)
    }
    opener.current?.focus()
  }, [])

  const cats = [...new Set(items.map((i) => i.category).filter(Boolean))]
  const list = cat ? items.filter((i) => i.category === cat) : items
  const visible = list.slice(0, shown)
  const cur = items.find((i) => i.id === open)
  const photo = (id?: string) =>
    id && assetBase ? (
      // biome-ignore lint/performance/noImgElement: a published site serves its own images, same origin
      <img src={`${assetBase}/${id}`} alt="" loading="lazy" className="size-full object-cover" />
    ) : null
  const note =
    mode === 'incl'
      ? t(lang, 'catalogue_prices_incl')
      : mode === 'plus'
        ? t(lang, 'catalogue_prices_plus')
        : ''
  const wa = (name: string) =>
    safeHref(waHref(phone, t(lang, 'catalogue_whatsapp_message', { name })), blocked)
  const useWa = enquire !== 'form'
  const useAsk = enquire !== 'whatsapp' && !!askPath && !!formId
  const grid = variant !== 'list'

  return (
    <div>
      {cats.length > 1 && (
        <div className="sticky top-0 z-10 -mx-5 mb-5 flex gap-2 overflow-x-auto bg-[var(--site-bg)] px-5 py-2 md:mx-0 md:px-0">
          {['', ...cats].map((c) => (
            <button
              key={c || 'all'}
              type="button"
              aria-pressed={cat === c}
              onClick={() => {
                setCat(c)
                setShown(STEP)
              }}
              className={`${chip} ${cat === c ? 'border-transparent bg-[var(--site-primary)] text-[var(--site-on-primary)]' : ''}`}
            >
              {c || t(lang, 'catalogue_all')}
            </button>
          ))}
        </div>
      )}
      <ul
        className={
          grid
            ? 'grid grid-cols-2 gap-x-4 gap-y-6 md:grid-cols-3 lg:grid-cols-4'
            : 'max-w-[720px] divide-y divide-[var(--site-border)] rounded-[var(--site-radius)] border border-[var(--site-border)]'
        }
      >
        {visible.map((it) => (
          <li key={it.id}>
            <button
              type="button"
              aria-haspopup="dialog"
              onClick={(e) => show(it.id, e.currentTarget)}
              className={`block w-full text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-primary)] ${grid ? '' : 'flex items-baseline gap-3 px-4 py-3'}`}
            >
              {grid ? (
                <>
                  <span className="mb-2 flex aspect-square w-full items-center justify-center overflow-hidden rounded-[var(--site-radius)] bg-[var(--site-tint)]">
                    {photo(it.image_id) ?? (
                      <span className="line-clamp-4 px-3 text-center text-[15px] font-semibold text-[var(--site-primary)]">
                        {it.name}
                      </span>
                    )}
                  </span>
                  <span className="line-clamp-2 block text-[15px]">{it.name}</span>
                  <span className="block text-[15px] font-bold tabular-nums">{it.price}</span>
                </>
              ) : (
                <>
                  <span className="min-w-0 flex-1 font-semibold">{it.name}</span>
                  <span className="shrink-0 font-semibold tabular-nums">{it.price}</span>
                </>
              )}
            </button>
          </li>
        ))}
      </ul>
      {note && <p className="mt-4 text-[14px] text-[var(--site-muted)]">{note}</p>}
      {list.length > shown && (
        <button
          type="button"
          onClick={() => setShown(shown + STEP)}
          className={`${btn} mt-4 max-w-xs border border-[var(--site-border)]`}
        >
          {t(lang, 'catalogue_show_more', { count: Math.min(STEP, list.length - shown) })}
        </button>
      )}
      {/* biome-ignore lint/a11y/useKeyWithClickEvents: the click only closes on the backdrop; Escape closes the dialog natively */}
      <dialog
        ref={dlg}
        aria-label={cur?.name ?? t(lang, 'catalogue_item')}
        onClose={() => open && close()}
        onClick={(e) => e.target === e.currentTarget && close()}
        className="m-0 mt-auto max-h-[92vh] w-full max-w-none overflow-y-auto rounded-t-2xl border-0 bg-[var(--site-bg)] p-0 text-[var(--site-text)] backdrop:bg-black/50 md:m-auto md:max-w-[780px] md:rounded-[var(--site-radius)]"
      >
        {cur && (
          <div className="relative grid md:grid-cols-2">
            <button
              type="button"
              onClick={close}
              aria-label={t(lang, 'catalogue_close')}
              className="absolute right-2 top-2 z-10 flex size-10 items-center justify-center rounded-full bg-[var(--site-bg)] focus-visible:outline-2 focus-visible:outline-[var(--site-primary)]"
            >
              <X size={20} aria-hidden="true" />
            </button>
            <div className="flex aspect-square w-full items-center justify-center overflow-hidden bg-[var(--site-tint)] md:aspect-auto md:min-h-[360px]">
              {photo(cur.image_id) ?? (
                <ImageIcon size={40} aria-hidden="true" className="text-[var(--site-primary)] opacity-50" />
              )}
            </div>
            <div className="flex flex-col gap-3 p-5">
              <h3 className="pr-10 text-xl font-bold md:pr-0">{cur.name}</h3>
              <p className="text-lg font-bold tabular-nums">
                {cur.price}
                {mode === 'incl' && cur.amount !== undefined && (
                  <span className="ml-2 text-[13px] font-normal text-[var(--site-muted)]">
                    {t(lang, 'catalogue_incl_gst')}
                  </span>
                )}
              </p>
              {cur.description && (
                <p className="whitespace-pre-line text-[15px] text-[var(--site-muted)]">{cur.description}</p>
              )}
              {cur.category && <p className="text-[13px] text-[var(--site-muted)]">{cur.category}</p>}
              <div className="mt-auto flex flex-col gap-2 pt-2">
                {useWa && wa(cur.name) && (
                  <a
                    href={wa(cur.name)}
                    rel="noopener noreferrer"
                    className={`${btn} bg-[var(--site-primary)] text-[var(--site-on-primary)]`}
                  >
                    <MessageCircle size={18} aria-hidden="true" />
                    {t(lang, 'catalogue_enquire_whatsapp')}
                  </a>
                )}
                {useAsk && (
                  <a
                    href={`${askPath}?item=${cur.id}#${formId}`}
                    className={`${btn} border border-[var(--site-border)]`}
                  >
                    {t(lang, 'catalogue_ask')}
                  </a>
                )}
              </div>
            </div>
          </div>
        )}
      </dialog>
    </div>
  )
}
