import { ImageIcon, MessageCircle } from 'lucide-react'
import type { ComponentType, ReactNode } from 'react'
import {
  type CatalogueView as CatalogueData,
  embedLinks,
  imageId,
  mapsHref,
  safeHref,
  telHref,
  waHref,
} from '@/lib/sites'
import type catalog from './catalog.json'
import { CatalogueView } from './catalogue-sheet'
import { EmbedFrame } from './embed'

/**
 * Block context (design section 8): every block gets `assetBase` (an image renders as `${assetBase}/${id}`),
 * `editing` (the builder: empty slots show a dashed placeholder; the published page collapses them) and, for
 * ContactForm, `form` (the published page's live form; the preview keeps the inert one).
 * Props are typed loosely on purpose: the catalog is the source of truth and is validated in Go before render.
 */
type Ctx = { assetBase?: string; editing?: boolean; form?: ReactNode; blocked?: ReadonlySet<string> }
type P = Record<string, unknown> & Ctx
type Obj = Record<string, unknown>

export type BlockType = keyof typeof catalog.blocks
const ALL = [
  'Header',
  'Hero',
  'Services',
  'About',
  'Gallery',
  'Pricing',
  'Catalogue',
  'FAQ',
  'CallToAction',
  'MapHours',
  'WhatsAppButton',
  'Testimonials',
  'ContactForm',
  'Footer',
] as const

const str = (v: unknown) => (typeof v === 'string' ? v.trim() : '')
const list = (v: unknown): Obj[] =>
  Array.isArray(v) ? v.filter((x): x is Obj => !!x && typeof x === 'object' && !Array.isArray(x)) : []

const btnCls =
  'inline-flex min-h-12 items-center justify-center rounded-[var(--site-radius)] bg-[var(--site-primary)] px-5 text-[15px] font-semibold text-[var(--site-on-primary)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-text)]'
const ghostCls =
  'inline-flex min-h-12 items-center justify-center rounded-[var(--site-radius)] border border-[var(--site-border)] px-5 text-[15px] font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-primary)]'
const linkCls =
  'rounded-sm underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-primary)]'
const sec = 'mx-auto w-full max-w-[1120px] px-5 py-14 md:px-8 md:py-24'
const h2 = 'text-2xl font-bold leading-tight md:text-4xl'
const card = 'rounded-[var(--site-radius)] border border-[var(--site-border)]'

/** A link only for a safe href; anything else stays inert text. */
function Link({
  href,
  className,
  children,
  blocked,
}: {
  href: unknown
  className?: string
  children: ReactNode
  blocked?: ReadonlySet<string>
}) {
  const h = safeHref(href, blocked)
  return h ? (
    <a href={h} className={className}>
      {children}
    </a>
  ) : (
    <span className={className}>{children}</span>
  )
}

/** Builder only: an empty slot the owner can fill. Published pages never render it. */
const Empty = ({ label, className = '' }: { label: string; className?: string }) => (
  <div
    className={`flex items-center justify-center gap-2 rounded-[var(--site-radius)] border border-dashed border-[var(--site-primary)] bg-[var(--site-tint)] p-6 text-[14px] ${className}`}
  >
    <ImageIcon size={16} aria-hidden="true" /> {label}
  </div>
)

function Img({
  id,
  p,
  alt,
  className,
  placeholder,
}: {
  id: unknown
  p: Ctx
  alt: string
  className: string
  placeholder?: string
}) {
  const i = imageId(id)
  if (!i || !p.assetBase)
    return p.editing && placeholder ? <Empty label={placeholder} className={className} /> : null
  // biome-ignore lint/performance/noImgElement: a site image served by the API, not an app asset
  return <img src={`${p.assetBase}/${i}`} alt={alt} loading="lazy" className={className} />
}

const Contact = ({
  phone,
  email,
  address,
  blocked,
}: {
  phone: string
  email: string
  address: string
  blocked?: ReadonlySet<string>
}) => (
  <>
    {phone && (
      <li>
        <Link blocked={blocked} href={telHref(phone)} className={linkCls}>
          {phone}
        </Link>
      </li>
    )}
    {email && (
      <li>
        <Link blocked={blocked} href={`mailto:${email}`} className={linkCls}>
          {email}
        </Link>
      </li>
    )}
    {address && <li className="whitespace-pre-line">{address}</li>}
  </>
)

function Header(p: P) {
  const links = list(p.links).filter((l) => str(l.label))
  const cta = str(p.cta_label)
  const brand = str(p.brand)
  return (
    <header
      className={`mx-auto flex w-full max-w-[1120px] flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3 md:px-8 md:py-4 ${str(p.variant) === 'centered' ? 'flex-col justify-center text-center' : 'justify-between'}`}
    >
      <p className="flex items-center gap-2.5 text-lg font-bold">
        <Img id={p.logo} p={p} alt={brand} className="h-9 w-auto max-w-[140px] object-contain" />
        {brand}
      </p>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
        {links.length > 0 && (
          <nav aria-label="Site">
            <ul className="flex flex-wrap gap-x-5 gap-y-1 text-[15px]">
              {links.map((l, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
                <li key={i}>
                  <Link blocked={p.blocked} href={l.href} className={linkCls}>
                    {str(l.label)}
                  </Link>
                </li>
              ))}
            </ul>
          </nav>
        )}
        {cta && (
          <Link blocked={p.blocked} href={p.cta_href} className={btnCls}>
            {cta}
          </Link>
        )}
      </div>
    </header>
  )
}

function Hero(p: P) {
  const v = str(p.variant)
  const hasImg = !!imageId(p.image) && !!p.assetBase
  const cover = v === 'cover' && hasImg
  const center = v === 'centered' || v === 'cover' || (!v && p.align === 'center')
  const cta = str(p.cta_label)
  if (cover)
    return (
      <section className="relative isolate overflow-hidden text-white">
        <Img
          id={p.image}
          p={p}
          alt={str(p.image_alt)}
          className="absolute inset-0 -z-10 size-full object-cover"
        />
        <div aria-hidden="true" className="absolute inset-0 -z-10 bg-black/55" />
        <div className="mx-auto flex w-full max-w-[1120px] flex-col items-center gap-4 px-5 py-20 text-center md:px-8 md:py-32">
          <h1 className="max-w-[20ch] text-[32px] font-bold leading-[38px] md:text-[56px] md:leading-[60px]">
            {str(p.title)}
          </h1>
          {str(p.subtitle) && <p className="max-w-[56ch] text-lg opacity-90">{str(p.subtitle)}</p>}
          {cta && (
            <Link blocked={p.blocked} href={p.cta_href} className={`${btnCls} max-md:w-full`}>
              {cta}
            </Link>
          )}
        </div>
      </section>
    )
  return (
    <section
      className={`mx-auto grid w-full max-w-[1120px] gap-8 px-5 py-14 md:px-8 md:py-24 ${hasImg && !center && v !== 'centered' ? 'md:grid-cols-2 md:items-center' : ''}`}
    >
      <div className={`flex flex-col gap-4 ${center ? 'items-center text-center' : 'items-start'}`}>
        <h1 className="max-w-[20ch] text-[32px] font-bold leading-[38px] md:text-[56px] md:leading-[60px]">
          {str(p.title)}
        </h1>
        {str(p.subtitle) && (
          <p className="max-w-[56ch] text-lg text-[var(--site-muted)]">{str(p.subtitle)}</p>
        )}
        {cta && (
          <Link blocked={p.blocked} href={p.cta_href} className={`${btnCls} max-md:w-full`}>
            {cta}
          </Link>
        )}
      </div>
      {v !== 'centered' && (
        <Img
          id={p.image}
          p={p}
          alt={str(p.image_alt)}
          placeholder="Add a photo"
          className="aspect-[4/3] w-full rounded-[var(--site-radius)] object-cover"
        />
      )}
    </section>
  )
}

function Services(p: P) {
  const items = list(p.items).filter((i) => str(i.name))
  return (
    <section className={sec}>
      {str(p.title) && <h2 className={`${h2} mb-8`}>{str(p.title)}</h2>}
      {items.length > 0 && (
        <ul
          className={
            str(p.variant) === 'list'
              ? 'max-w-[720px] divide-y divide-[var(--site-border)] border-y border-[var(--site-border)]'
              : 'grid gap-4 sm:grid-cols-2 lg:grid-cols-3 lg:gap-6'
          }
        >
          {items.map((it, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
            <li key={i} className={str(p.variant) === 'list' ? 'py-4' : `${card} p-5`}>
              <h3 className="text-lg font-semibold">{str(it.name)}</h3>
              {str(it.description) && (
                <p className="mt-1 text-[15px] text-[var(--site-muted)]">{str(it.description)}</p>
              )}
              {str(it.price) && (
                <p className="mt-3 font-semibold text-[var(--site-primary)]">{str(it.price)}</p>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function About(p: P) {
  const hasImg = !!imageId(p.image) && !!p.assetBase
  const v = str(p.variant)
  return (
    <section
      className={`${sec} grid gap-8 ${(hasImg || p.editing) && v !== 'stacked' ? `${v === 'image-right' ? 'md:grid-cols-[7fr_5fr]' : 'md:grid-cols-[5fr_7fr]'} md:items-center md:gap-14` : ''}`}
    >
      <Img
        id={p.image}
        p={p}
        alt={str(p.image_alt)}
        placeholder="Add a photo"
        className={`aspect-[4/3] w-full rounded-[var(--site-radius)] object-cover ${v === 'image-right' ? 'md:order-2' : ''}`}
      />
      <div className="max-w-[60ch]">
        {str(p.title) && <h2 className={`${h2} mb-4`}>{str(p.title)}</h2>}
        <p className="whitespace-pre-line text-[17px] leading-7 text-[var(--site-muted)]">{str(p.body)}</p>
      </div>
    </section>
  )
}

function Gallery(p: P) {
  const items = list(p.items).filter((i) => imageId(i.image))
  if (!items.length)
    return p.editing ? (
      <section className={sec}>
        <Empty label="Add photos: ask for a gallery, or attach photos in the chat" className="min-h-40" />
      </section>
    ) : null
  return (
    <section className={sec}>
      {str(p.title) && <h2 className={`${h2} mb-8`}>{str(p.title)}</h2>}
      <ul
        className={
          str(p.variant) === 'strip'
            ? 'flex snap-x gap-3 overflow-x-auto pb-2'
            : 'grid grid-cols-2 gap-2 md:grid-cols-3 md:gap-4'
        }
      >
        {items.map((it, i) => (
          <li
            // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
            key={i}
            className={str(p.variant) === 'strip' ? 'w-[70%] shrink-0 snap-start md:w-[32%]' : undefined}
          >
            <figure>
              <Img
                id={it.image}
                p={p}
                alt={str(it.caption)}
                className="aspect-square w-full rounded-[var(--site-radius)] object-cover"
              />
              {str(it.caption) && (
                <figcaption className="mt-1.5 text-[14px] text-[var(--site-muted)]">
                  {str(it.caption)}
                </figcaption>
              )}
            </figure>
          </li>
        ))}
      </ul>
    </section>
  )
}

function Pricing(p: P) {
  const items = list(p.items).filter((i) => str(i.name) && str(i.price))
  return (
    <section className={sec}>
      {str(p.title) && <h2 className={`${h2} mb-8`}>{str(p.title)}</h2>}
      {items.length > 0 && (
        <ul
          className={
            str(p.variant) === 'cards'
              ? 'grid gap-4 sm:grid-cols-2 lg:grid-cols-3'
              : `${card} max-w-[720px] divide-y divide-[var(--site-border)] p-2 md:p-4`
          }
        >
          {items.map((it, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
            <li key={i} className={str(p.variant) === 'cards' ? `${card} p-5` : 'px-2 py-3'}>
              <div className="flex items-baseline gap-2">
                <span className="font-semibold">{str(it.name)}</span>
                <span
                  aria-hidden="true"
                  className="min-w-4 flex-1 border-b border-dotted border-[var(--site-border)]"
                />
                <span className="font-semibold tabular-nums">{str(it.price)}</span>
              </div>
              {str(it.description) && (
                <p className="mt-0.5 text-[14px] text-[var(--site-muted)]">{str(it.description)}</p>
              )}
            </li>
          ))}
        </ul>
      )}
      {str(p.note) && <p className="mt-4 text-[14px] text-[var(--site-muted)]">{str(p.note)}</p>}
    </section>
  )
}

function FAQ(p: P) {
  const items = list(p.items).filter((i) => str(i.question) && str(i.answer))
  return (
    <section className={sec}>
      <div className="max-w-[720px]">
        {str(p.title) && <h2 className={`${h2} mb-6`}>{str(p.title)}</h2>}
        <div className="divide-y divide-[var(--site-border)] border-y border-[var(--site-border)]">
          {items.map((it, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
            <details key={i} open={str(p.variant) === 'open' || (!p.variant && i === 0)} className="group">
              <summary className="flex min-h-14 cursor-pointer list-none items-center justify-between gap-4 py-3 font-semibold [&::-webkit-details-marker]:hidden">
                {str(it.question)}
                <span aria-hidden="true" className="text-xl leading-none group-open:rotate-45">
                  +
                </span>
              </summary>
              <p className="whitespace-pre-line pb-4 text-[var(--site-muted)]">{str(it.answer)}</p>
            </details>
          ))}
        </div>
      </div>
    </section>
  )
}

function CallToAction(p: P) {
  const label = str(p.cta_label)
  return (
    <section
      className={
        str(p.variant) === 'card'
          ? 'mx-auto w-full max-w-[1120px] px-5 py-10 md:px-8'
          : 'bg-[var(--site-primary)] text-[var(--site-on-primary)]'
      }
    >
      <div
        className={`mx-auto flex w-full max-w-[1120px] flex-col items-center gap-4 px-5 py-12 text-center md:flex-row md:justify-between md:px-8 md:text-left ${str(p.variant) === 'card' ? 'rounded-[var(--site-radius)] bg-[var(--site-primary)] text-[var(--site-on-primary)]' : ''}`}
      >
        <div>
          <h2 className="text-2xl font-bold md:text-3xl">{str(p.title)}</h2>
          {str(p.text) && <p className="mt-1 opacity-90">{str(p.text)}</p>}
        </div>
        {label && (
          <Link
            blocked={p.blocked}
            href={p.cta_href}
            className="inline-flex min-h-12 items-center justify-center rounded-[var(--site-radius)] bg-[var(--site-bg)] px-5 text-[15px] font-semibold text-[var(--site-text)] max-md:w-full"
          >
            {label}
          </Link>
        )}
      </div>
    </section>
  )
}

function MapHours(p: P) {
  const address = str(p.address)
  const hours = list(p.hours).filter((h) => str(h.days) && str(h.time))
  const maps =
    safeHref(p.map_url, p.blocked) ?? (address ? safeHref(mapsHref(address), p.blocked) : undefined)
  return (
    <section className="bg-[var(--site-tint)]">
      <div className={sec}>
        {str(p.title) && <h2 className={`${h2} mb-8`}>{str(p.title)}</h2>}
        <div className={`grid gap-8 ${str(p.variant) === 'stacked' ? '' : 'md:grid-cols-2'}`}>
          {address && (
            <div>
              <h3 className="mb-2 text-lg font-semibold">Find us</h3>
              <p className="mb-4 whitespace-pre-line text-[var(--site-muted)]">{address}</p>
              {maps && (
                <a href={maps} rel="noopener noreferrer" className={ghostCls}>
                  Get directions
                </a>
              )}
            </div>
          )}
          {hours.length > 0 && (
            <div>
              <h3 className="mb-2 text-lg font-semibold">Opening hours</h3>
              <table className="w-full max-w-sm text-[15px]">
                <tbody>
                  {hours.map((h, i) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
                    <tr key={i} className="border-b border-[var(--site-border)]">
                      <th scope="row" className="py-2 pr-4 text-left font-medium">
                        {str(h.days)}
                      </th>
                      <td className="py-2 text-right tabular-nums">{str(h.time)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </section>
  )
}

function WhatsAppButton(p: P) {
  const href = safeHref(waHref(p.phone, str(p.message) || 'Hi, I found you on your website'), p.blocked)
  if (!href)
    return p.editing ? (
      <div className="px-5 py-4 md:px-8">
        <Empty label="Add your WhatsApp number to show the chat button" />
      </div>
    ) : null
  const label = str(p.label) || 'Chat on WhatsApp'
  return (
    <a
      href={href}
      rel="noopener noreferrer"
      aria-label={label}
      title={label}
      className="group fixed bottom-4 right-4 z-20 flex size-14 items-center justify-center rounded-full bg-[#25D366] text-white shadow-lg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-text)] md:bottom-6 md:right-6"
      style={{ marginBottom: 'env(safe-area-inset-bottom)' }}
    >
      <MessageCircle size={28} aria-hidden="true" />
      <span className="pointer-events-none absolute right-full mr-3 hidden whitespace-nowrap rounded bg-[var(--site-text)] px-2 py-1 text-[13px] text-[var(--site-bg)] group-hover:md:block">
        {label}
      </span>
    </a>
  )
}

function Testimonials(p: P) {
  const items = list(p.items).filter((i) => str(i.quote) && str(i.name))
  if (!items.length)
    return p.editing ? (
      <section className={sec}>
        <Empty label="Paste a customer's exact words in the chat to add a testimonial" />
      </section>
    ) : null
  return (
    <section className="bg-[var(--site-tint)]">
      <div className={sec}>
        {str(p.title) && <h2 className={`${h2} mb-8`}>{str(p.title)}</h2>}
        <ul className="flex snap-x gap-4 overflow-x-auto pb-2 md:grid md:grid-cols-2 md:overflow-visible lg:grid-cols-3">
          {items.map((it, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
            <li key={i} className={`${card} min-w-[85%] snap-start bg-[var(--site-bg)] p-5 md:min-w-0`}>
              <blockquote>
                <p aria-hidden="true" className="text-3xl font-bold leading-none text-[var(--site-primary)]">
                  “
                </p>
                <p className="mt-1 whitespace-pre-line">{str(it.quote)}</p>
                <footer className="mt-4 text-[14px]">
                  <span className="font-semibold">{str(it.name)}</span>
                  {str(it.role) && <span className="text-[var(--site-muted)]">, {str(it.role)}</span>}
                </footer>
              </blockquote>
            </li>
          ))}
        </ul>
      </div>
    </section>
  )
}

const fieldCls =
  'min-h-12 w-full rounded-[var(--site-radius)] border border-[var(--site-border)] bg-transparent px-3 py-2 text-[16px] focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-[var(--site-primary)]'
export const siteField = fieldCls
export const siteBtn = btnCls

/** The builder's inert form: same fields as the live one, nothing is sent. */
function InertForm({ label }: { label: string }) {
  return (
    <form className="space-y-3" onSubmit={(e) => e.preventDefault()}>
      {['Your name', 'Mobile number', 'Email (optional)'].map((l) => (
        <div key={l}>
          <label className="mb-1 block text-sm font-medium">
            {l}
            <input name={l} autoComplete="off" className={`${fieldCls} mt-1 font-normal`} />
          </label>
        </div>
      ))}
      <label className="block text-sm font-medium">
        Message
        <textarea name="message" rows={3} className={`${fieldCls} mt-1 font-normal`} />
      </label>
      <button type="submit" className={`${btnCls} w-full`}>
        {label}
      </button>
      <p className="text-[13px] text-[var(--site-muted)]">
        Preview only: the form starts working when you publish
      </p>
    </form>
  )
}

function ContactForm(p: P) {
  const [phone, email, address] = [str(p.phone), str(p.email), str(p.address)]
  const wa = safeHref(waHref(p.phone), p.blocked)
  return (
    <section
      className={`${sec} grid gap-8 ${str(p.variant) === 'stacked' ? 'max-w-[720px]' : 'md:grid-cols-[5fr_7fr] md:gap-14'}`}
    >
      <div className="space-y-3">
        {str(p.title) && <h2 className={h2}>{str(p.title)}</h2>}
        {str(p.intro) && <p className="text-[var(--site-muted)]">{str(p.intro)}</p>}
        {(phone || email || address) && (
          <ul className="space-y-1 text-[15px]">
            <Contact phone={phone} email={email} address={address} blocked={p.blocked} />
            {wa && (
              <li>
                <a href={wa} rel="noopener noreferrer" className={linkCls}>
                  Chat on WhatsApp
                </a>
              </li>
            )}
          </ul>
        )}
      </div>
      <div className={`${card} p-5 md:p-6`}>
        {p.form ?? <InertForm label={str(p.submit_label) || 'Send enquiry'} />}
      </div>
    </section>
  )
}

function Footer(p: P) {
  const links = list(p.links).filter((l) => str(l.label))
  const [phone, email, address] = [str(p.phone), str(p.email), str(p.address)]
  return (
    <footer className="space-y-3 border-t border-[var(--site-border)] px-5 py-8 pb-20 text-[14px] md:px-8">
      <div
        className={`mx-auto w-full max-w-[1120px] ${str(p.variant) === 'columns' ? 'grid gap-6 md:grid-cols-3' : 'space-y-3'}`}
      >
        {str(p.text) && <p>{str(p.text)}</p>}
        {(phone || email || address) && (
          <ul className={str(p.variant) === 'columns' ? 'space-y-1' : 'flex flex-wrap gap-x-6 gap-y-1'}>
            <Contact phone={phone} email={email} address={address} blocked={p.blocked} />
          </ul>
        )}
        {links.length > 0 && (
          <ul className={str(p.variant) === 'columns' ? 'space-y-1' : 'flex flex-wrap gap-x-5 gap-y-1'}>
            {links.map((l, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
              <li key={i}>
                <Link blocked={p.blocked} href={l.href} className={linkCls}>
                  {str(l.label)}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
    </footer>
  )
}

/** The plan badge and the abuse link. Not in the document, so chat cannot remove them. */
export function SiteBadge({ pro, home, report }: { pro: boolean; home: string; report?: string }) {
  return (
    <div className="flex flex-wrap items-center justify-center gap-x-4 gap-y-1 border-t border-[var(--site-border)] px-5 py-4 pb-20 text-[12px] text-[var(--site-muted)]">
      {!pro && (
        <a
          href={home}
          rel="noopener noreferrer"
          className="rounded-full border border-[var(--site-border)] px-2.5 py-1"
        >
          Made with C Note
        </a>
      )}
      {report && (
        <a href={report} rel="noopener noreferrer" className="underline-offset-4 hover:underline">
          Report this site
        </a>
      )}
    </div>
  )
}

export function Text(p: P) {
  if (!str(p.title) && !str(p.body))
    return p.editing ? (
      <section className={sec}>
        <Empty label="Add a title and some text" />
      </section>
    ) : null
  return (
    <section className={sec}>
      <div className="max-w-[60ch]">
        {str(p.title) && <h2 className={`${h2} mb-4`}>{str(p.title)}</h2>}
        {str(p.body) && (
          <p className="whitespace-pre-line text-[17px] leading-7 text-[var(--site-muted)]">{str(p.body)}</p>
        )}
      </div>
    </section>
  )
}

/** Click-to-load (design 17.7): a local placeholder, no third-party request until the visitor clicks. */
export function Embed(p: P) {
  const provider = str(p.provider)
  const id = str(p.source)
  const href = embedLinks(provider, id)
  if (!href || !safeHref(href.page, p.blocked))
    return p.editing ? (
      <section className={sec}>
        <Empty label="Choose a video, map, form or calendar and add its address or ID" />
      </section>
    ) : null
  return (
    <section className={sec}>
      {str(p.title) && <h2 className={`${h2} mb-6`}>{str(p.title)}</h2>}
      <EmbedFrame provider={provider} href={href.page} src={href.frame} title={str(p.title)} />
    </section>
  )
}

/**
 * The catalogue block (design 2). `catalog` is the published snapshot (the builder passes its own items); with no
 * items the published page renders nothing and the builder shows a dashed placeholder. `phone`, `askPath` and
 * `formId` come from the page: the WhatsApp number and where the ContactForm lives.
 */
export function Catalogue(p: P) {
  const cat = p.catalog as CatalogueData | undefined
  if (!cat?.items.length)
    return p.editing ? (
      <section className={sec}>
        <div className="rounded-[var(--site-radius)] border border-dashed border-[var(--site-border)] p-10 text-center text-[var(--site-muted)]">
          Your products will appear here
        </div>
      </section>
    ) : null
  return (
    <section className={sec}>
      {str(p.title) && <h2 className={`${h2} mb-3`}>{str(p.title)}</h2>}
      {str(p.intro) && <p className="mb-8 max-w-[640px] text-[var(--site-muted)]">{str(p.intro)}</p>}
      <CatalogueView
        items={cat.items}
        mode={cat.mode}
        variant={str(p.variant)}
        enquire={str(p.enquire)}
        assetBase={p.assetBase}
        blocked={p.blocked}
        phone={str(p.phone) || undefined}
        askPath={str(p.askPath) || undefined}
        formId={str(p.formId) || undefined}
      />
    </section>
  )
}

/** A block added to the catalog without a component fails tsc (BlockType is the catalog's keys). */
export const BLOCKS: Record<BlockType | (typeof ALL)[number], ComponentType<P>> = {
  Header,
  Hero,
  Services,
  About,
  Gallery,
  Pricing,
  Catalogue,
  FAQ,
  CallToAction,
  MapHours,
  WhatsAppButton,
  Testimonials,
  ContactForm,
  Footer,
  Text,
  Embed,
}
