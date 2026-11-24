import type { ComponentType, ReactNode } from 'react'
import { safeHref, safeSrc, telHref } from '@/lib/sites'
import type catalog from './catalog.json'

type Blocks = typeof catalog.blocks
export type BlockType = keyof Blocks
/** A misspelt prop name fails tsc: keys come from the catalog. */
type Props<T extends BlockType> = { [K in keyof Blocks[T]['props']]?: unknown }
type Obj = Record<string, unknown>

const str = (v: unknown) => (typeof v === 'string' ? v.trim() : '')
const list = (v: unknown): Obj[] =>
  Array.isArray(v) ? v.filter((x): x is Obj => !!x && typeof x === 'object' && !Array.isArray(x)) : []

const btnCls =
  'inline-flex items-center justify-center rounded-[var(--site-radius)] bg-[var(--site-primary)] px-5 py-2.5 text-[15px] font-semibold text-[var(--site-on-primary)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-text)]'
const linkCls =
  'rounded-sm underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-primary)]'

/** A link only for a safe href; anything else stays inert text. */
function Link({ href, className, children }: { href: unknown; className?: string; children: ReactNode }) {
  const h = safeHref(href)
  return h ? (
    <a href={h} className={className}>
      {children}
    </a>
  ) : (
    <span className={className}>{children}</span>
  )
}

const Contact = ({ phone, email, address }: { phone: string; email: string; address: string }) => (
  <>
    {phone && (
      <li>
        <Link href={telHref(phone)} className={linkCls}>
          {phone}
        </Link>
      </li>
    )}
    {email && (
      <li>
        <Link href={`mailto:${email}`} className={linkCls}>
          {email}
        </Link>
      </li>
    )}
    {address && <li className="whitespace-pre-line">{address}</li>}
  </>
)

function Header(p: Props<'Header'>) {
  const links = list(p.links).filter((l) => str(l.label))
  const cta = str(p.cta_label)
  return (
    <header className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-5 py-4 md:px-10">
      <p className="text-lg font-bold">{str(p.brand)}</p>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
        {links.length > 0 && (
          <nav aria-label="Site">
            <ul className="flex flex-wrap gap-x-5 gap-y-1 text-[15px]">
              {links.map((l, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
                <li key={i}>
                  <Link href={l.href} className={linkCls}>
                    {str(l.label)}
                  </Link>
                </li>
              ))}
            </ul>
          </nav>
        )}
        {cta && (
          <Link href={p.cta_href} className={btnCls}>
            {cta}
          </Link>
        )}
      </div>
    </header>
  )
}

function Hero(p: Props<'Hero'>) {
  const center = p.align === 'center'
  const img = safeSrc(p.image)
  const cta = str(p.cta_label)
  return (
    <section
      className={`flex flex-col gap-4 px-5 py-14 md:px-10 md:py-20 ${center ? 'items-center text-center' : 'items-start'}`}
    >
      <h1 className="max-w-[20ch] text-4xl font-bold leading-tight md:text-5xl">{str(p.title)}</h1>
      {str(p.subtitle) && <p className="max-w-[56ch] text-lg opacity-80">{str(p.subtitle)}</p>}
      {cta && (
        <Link href={p.cta_href} className={btnCls}>
          {cta}
        </Link>
      )}
      {img && (
        // biome-ignore lint/performance/noImgElement: a user-chosen external URL, not an app asset
        <img src={img} alt="" className="mt-4 max-h-80 w-full rounded-[var(--site-radius)] object-cover" />
      )}
    </section>
  )
}

function Services(p: Props<'Services'>) {
  const items = list(p.items).filter((i) => str(i.name))
  return (
    <section className="px-5 py-12 md:px-10">
      {str(p.title) && <h2 className="mb-6 text-2xl font-bold">{str(p.title)}</h2>}
      {items.length > 0 && (
        <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {items.map((it, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
            <li key={i} className="rounded-[var(--site-radius)] border border-[var(--site-border)] p-5">
              <h3 className="text-lg font-semibold">{str(it.name)}</h3>
              {str(it.description) && <p className="mt-1 text-[15px] opacity-80">{str(it.description)}</p>}
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

const fieldCls =
  'w-full rounded-[var(--site-radius)] border border-[var(--site-border)] bg-transparent px-3 py-2 text-[15px] focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-[var(--site-primary)]'

function ContactForm(p: Props<'ContactForm'>) {
  const [phone, email, address] = [str(p.phone), str(p.email), str(p.address)]
  return (
    <section className="grid gap-8 px-5 py-12 md:grid-cols-2 md:px-10">
      <div className="space-y-3">
        {str(p.title) && <h2 className="text-2xl font-bold">{str(p.title)}</h2>}
        {str(p.intro) && <p className="opacity-80">{str(p.intro)}</p>}
        {(phone || email || address) && (
          <ul className="space-y-1 text-[15px]">
            <Contact phone={phone} email={email} address={address} />
          </ul>
        )}
      </div>
      {/* Phase 0: the form is inert, nothing is sent. */}
      <form className="space-y-3" onSubmit={(e) => e.preventDefault()}>
        <div>
          <label htmlFor="cf-name" className="mb-1 block text-sm font-medium">
            Your name
          </label>
          <input id="cf-name" name="name" autoComplete="off" className={fieldCls} />
        </div>
        <div>
          <label htmlFor="cf-contact" className="mb-1 block text-sm font-medium">
            Phone or email
          </label>
          <input id="cf-contact" name="contact" autoComplete="off" className={fieldCls} />
        </div>
        <div>
          <label htmlFor="cf-msg" className="mb-1 block text-sm font-medium">
            Message
          </label>
          <textarea id="cf-msg" name="message" rows={4} className={fieldCls} />
        </div>
        <button type="submit" className={btnCls}>
          {str(p.submit_label) || 'Send'}
        </button>
        <p className="text-[13px] opacity-70">Preview only: the form starts working when you publish</p>
      </form>
    </section>
  )
}

function Footer(p: Props<'Footer'>) {
  const links = list(p.links).filter((l) => str(l.label))
  const [phone, email, address] = [str(p.phone), str(p.email), str(p.address)]
  return (
    <footer className="space-y-3 border-t border-[var(--site-border)] px-5 py-8 text-[14px] md:px-10">
      {str(p.text) && <p>{str(p.text)}</p>}
      {(phone || email || address) && (
        <ul className="flex flex-wrap gap-x-6 gap-y-1">
          <Contact phone={phone} email={email} address={address} />
        </ul>
      )}
      {links.length > 0 && (
        <ul className="flex flex-wrap gap-x-5 gap-y-1">
          {links.map((l, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: list items have no ids
            <li key={i}>
              <Link href={l.href} className={linkCls}>
                {str(l.label)}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </footer>
  )
}

/** A block added to the catalog without a component fails tsc. */
export const BLOCKS: { [T in BlockType]: ComponentType<Props<T>> } = {
  Header,
  Hero,
  Services,
  ContactForm,
  Footer,
}
