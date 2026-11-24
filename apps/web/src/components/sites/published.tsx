import type { Metadata } from 'next'
import { notFound, redirect } from 'next/navigation'
import { cache, type ReactNode } from 'react'
import {
  blockDefs,
  blockedOf,
  type CatalogueView,
  canonicalHost,
  catalogOf,
  dataOfPage,
  dateLabel,
  imageId,
  type PublicPostSummary,
  type PublicSite,
  postDescription,
  publicGet,
  publicPost,
  publicPosts,
  readingMinutes,
  reportHref,
  type SiteBlock,
  safeHref,
  siteSummary,
  themeStyle,
  viewOfPage,
  waHref,
} from '@/lib/sites'
import { Beacon } from './beacon'
import { BLOCKS, SiteBadge } from './blocks'
import { ContactFormLive } from './contact-form'
import { PostBody } from './post-body'

const isShared = (b: SiteBlock) => (blockDefs[b.type] as { shared?: boolean } | undefined)?.shared === true
/** Every block of every page, Home first. */
const allBlocks = (site: PublicSite) => [
  site.data.content ?? [],
  ...(site.data.pages ?? []).map((p) => p.content),
]

/** Where visitors reach the contact form: the page path and block id of the first ContactForm on the site. */
function formAt(site: PublicSite) {
  const paths = ['/', ...(site.data.pages ?? []).map((p) => `/${p.slug}`)]
  const lists = allBlocks(site)
  for (let i = 0; i < lists.length; i++) {
    const f = lists[i].find((b) => b.type === 'ContactForm')
    if (f) return { path: paths[i], id: String(f.props?.id ?? ''), block: f }
  }
  return null
}
/** The WhatsApp number: the WhatsAppButton's, else the ContactForm's. */
function phoneOf(site: PublicSite) {
  const blocks = allBlocks(site).flat()
  const ph = (t: string) => blocks.find((b) => b.type === t && waHref(b.props?.phone))?.props.phone
  return String(ph('WhatsAppButton') ?? ph('ContactForm') ?? '') || undefined
}

/** One block list as elements: the same BLOCKS as the preview, with the live form and the catalogue's page facts. */
function Blocks({ site, blocks }: { site: PublicSite; blocks: SiteBlock[] }) {
  const sum = siteSummary(site.data)
  const assetBase = `/api/sites/img/${site.site_id}`
  const blocked = blockedOf(site)
  const catalog = catalogOf(site)
  const at = formAt(site)
  const phone = phoneOf(site)
  const names = catalog ? Object.fromEntries(catalog.items.map((i) => [i.id, i.name])) : undefined
  return (
    <>
      {blocks
        .filter((b) => b.type in BLOCKS)
        .map((b) => {
          const id = String(b.props?.id ?? '')
          const C = BLOCKS[b.type as keyof typeof BLOCKS]
          const form =
            b.type === 'ContactForm' ? (
              <ContactFormLive
                siteId={site.site_id}
                business={sum.name}
                phone={String(b.props.phone ?? '') || undefined}
                whatsapp={waHref(b.props.phone)}
                submitLabel={String(b.props.submit_label ?? '') || undefined}
                paused={site.form_paused}
                items={names}
              />
            ) : undefined
          const extra =
            b.type === 'Catalogue' ? { catalog, phone, askPath: at?.path, formId: at?.id } : undefined
          return (
            <div key={id || b.type} id={id}>
              <C {...b.props} {...extra} assetBase={assetBase} form={form} blocked={blocked} />
            </div>
          )
        })}
    </>
  )
}

/** The page frame: theme variables, the visit beacon and the plan badge. */
function Frame({ site, children }: { site: PublicSite; children: ReactNode }) {
  return (
    <div style={themeStyle(site.data.root?.props?.theme)} className="min-h-screen">
      {children}
      <Beacon siteId={site.site_id} />
      <SiteBadge pro={site.pro} home={process.env.PUBLIC_BASE_URL ?? '/'} report={reportHref(site.url)} />
    </div>
  )
}

/**
 * The published page: the same BLOCKS and theme variables as the preview, no selection chrome, no cookies.
 * A server component; the only client code is the live contact form and the catalogue. Block ids stay as anchors.
 */
export function Published({ site, slug = '' }: { site: PublicSite; slug?: string }) {
  const data = dataOfPage(site.data, slug)
  return (
    <Frame site={site}>
      <Blocks site={site} blocks={data.content ?? []} />
    </Frame>
  )
}

const load = cache(publicGet)

export async function hasPublishedBlogPage(host: string) {
  const site = await load(host)
  return !!site?.data.pages?.some((page) => page.slug === 'blog')
}
const originOf = (site: PublicSite) => new URL(site.url).origin
const pathOf = (slug: string) => (slug ? `/${slug}` : '/')
const img = (site: PublicSite, id: string) => `${site.url}/api/sites/img/${site.site_id}/${id}`

/** A request on any host but the canonical one goes to the canonical host (design 17.6): stored host plus a known page path. */
export function canonicalRedirect(site: PublicSite, host: string, path: string) {
  if (host === canonicalHost(site)) return null
  return Response.redirect(`${originOf(site)}${path}`, 302)
}

/**
 * The picture a shared link shows (design 17.1, section 8): the owner's own photo when set, else the picture made at
 * publish, else the hero photo. Stored ids only; nothing is rendered per request.
 */
function shareImage(site: PublicSite, slug: string) {
  const x = site as PublicSite & {
    share_image?: string | null
    share_images?: Record<string, string>
    page_og_images?: Record<string, string>
  }
  const own = slug
    ? (site.data.pages?.find((p) => p.slug === slug) as { share_image?: unknown } | undefined)?.share_image
    : (site.data.root?.props as { share_image?: unknown } | undefined)?.share_image
  // The generated ids per page: keyed by slug, Home under '' or '_home' (the API's record of the publish render).
  const made = x.page_og_images ?? x.share_images
  const id =
    imageId(own) ??
    imageId(slug ? made?.[slug] : (made?.[''] ?? made?._home ?? x.share_image)) ??
    (slug ? undefined : imageId(siteSummary(site.data).heroImage))
  return id ? img(site, id) : undefined
}

export async function publishedMetadata(host: string, slug: string): Promise<Metadata> {
  const site = await load(host)
  if (!site) return {}
  const p = site.data.pages?.find((x) => x.slug === slug)
  if (slug && !p) return {}
  const s = siteSummary(site.data)
  const title = p ? p.title : s.title || s.name
  const description = (p ? p.description : s.description) || undefined
  const url = `${site.url}${slug ? `/${slug}` : ''}`
  const image = shareImage(site, slug)
  return {
    metadataBase: new URL(site.url),
    title,
    description,
    alternates: { canonical: url },
    openGraph: { type: 'website', url, title, description, images: image ? [image] : undefined },
  }
}

/** JSON-LD with < > & escaped so no value can close the tag. */
function Ld({ data }: { data: unknown }) {
  return (
    <script
      type="application/ld+json"
      // biome-ignore lint/security/noDangerouslySetInnerHtml: JSON-LD, with < > & escaped so no value can close the tag
      dangerouslySetInnerHTML={{
        __html: JSON.stringify(data)
          .replace(/</g, '\\u003c')
          .replace(/>/g, '\\u003e')
          .replace(/&/g, '\\u0026'),
      }}
    />
  )
}

/** An ItemList of Product for the catalogue on this page; the price only when the mode shows it with GST. */
function catalogueLd(site: PublicSite, cat: CatalogueView) {
  return {
    '@context': 'https://schema.org',
    '@type': 'ItemList',
    itemListElement: cat.items.map((it, i) => ({
      '@type': 'ListItem',
      position: i + 1,
      item: {
        '@type': 'Product',
        name: it.name,
        description: it.description || undefined,
        image: it.image_id ? img(site, it.image_id) : undefined,
        offers:
          it.amount !== undefined
            ? { '@type': 'Offer', price: it.amount.toFixed(2), priceCurrency: 'INR' }
            : undefined,
      },
    })),
  }
}

export async function PublishedPage({ host, slug }: { host: string; slug: string }) {
  const site = await load(host)
  if (!site) notFound()
  const known = !slug || !!site.data.pages?.some((p) => p.slug === slug)
  // Unknown paths on a secondary host go to its Home; the target is built only from stored values.
  if (host !== canonicalHost(site)) redirect(`${originOf(site)}${known ? pathOf(slug) : '/'}`)
  if (!known) notFound()
  const s = siteSummary(site.data)
  // Built from the page itself, never from the company profile: what the owner chose to publish. Home only.
  const ld = {
    '@context': 'https://schema.org',
    '@type': 'LocalBusiness',
    name: s.name || undefined,
    url: safeHref(site.url, blockedOf(site)),
    telephone: s.phone || undefined,
    email: s.email || undefined,
    address: s.address || undefined,
    openingHours: s.hours.length ? s.hours : undefined,
    image: s.heroImage ? img(site, s.heroImage) : undefined,
  }
  const cat = catalogOf(site)
  const hasCat = !!cat && !!viewOfPage(site.data, slug)?.some((b) => b.type === 'Catalogue')
  return (
    <>
      <Published site={site} slug={slug} />
      {!slug && <Ld data={ld} />}
      {hasCat && cat && <Ld data={catalogueLd(site, cat)} />}
    </>
  )
}

// ---- Blog (design 3, 17.4) ----

/** Header first, the other shared blocks (WhatsApp button, Footer) after, as a page of this site shows them. */
const sharedOf = (site: PublicSite) => {
  const shared = (site.data.content ?? []).filter(isShared)
  return {
    top: shared.filter((b) => b.type === 'Header'),
    bottom: shared.filter((b) => b.type !== 'Header'),
  }
}
const blogPath = (n = 1) => (n > 1 ? `/blog/page/${n}` : '/blog')
const rssLink = (site: PublicSite) => ({ 'application/rss+xml': `${site.url}/blog/rss.xml` })

export async function blogMetadata(host: string, n: number): Promise<Metadata> {
  const [site, list] = await Promise.all([load(host), publicPosts(host, n)])
  if (!site || !list) return {}
  const s = siteSummary(site.data)
  const title = `Blog | ${s.name || s.title}`
  const description = s.description || undefined
  const pages = Math.ceil(list.total / list.per_page)
  const image = shareImage(site, '')
  return {
    metadataBase: new URL(site.url),
    title,
    description,
    alternates: { canonical: `${site.url}${blogPath(n)}`, types: rssLink(site) },
    pagination: {
      previous: n > 1 ? `${site.url}${blogPath(n - 1)}` : undefined,
      next: n < pages ? `${site.url}${blogPath(n + 1)}` : undefined,
    },
    openGraph: {
      type: 'website',
      url: `${site.url}${blogPath(n)}`,
      title,
      description,
      images: image ? [image] : undefined,
    },
  }
}

function PostRow({ site, p }: { site: PublicSite; p: PublicPostSummary }) {
  const cover = imageId(p.cover_image_id)
  return (
    <li className="flex gap-4 py-5">
      {cover && (
        // biome-ignore lint/performance/noImgElement: a published site serves its own images, same origin
        <img
          src={`/api/sites/img/${site.site_id}/${cover}`}
          alt=""
          loading="lazy"
          className="size-24 shrink-0 rounded-[var(--site-radius)] object-cover md:size-32"
        />
      )}
      <div className="min-w-0">
        <h2 className="text-xl font-bold leading-snug">
          <a href={`/blog/${p.slug}`} className="underline-offset-4 hover:underline">
            {p.title}
          </a>
        </h2>
        <p className="mt-1 text-[14px] text-[var(--site-muted)]">{dateLabel(p.published_at)}</p>
        {p.description && <p className="mt-2 line-clamp-2 text-[15px]">{p.description}</p>}
      </div>
    </li>
  )
}

/** Where a blog path goes on a secondary host: the canonical host, same path. Null when this is the canonical host. */
function blogRedirect(site: PublicSite, host: string, path: string) {
  if (host !== canonicalHost(site)) redirect(`${originOf(site)}${path}`)
}

export async function PublishedBlog({ host, n }: { host: string; n: number }) {
  const [site, list] = await Promise.all([load(host), publicPosts(host, n)])
  if (!site || !list) notFound()
  blogRedirect(site, host, blogPath(n))
  const pages = Math.ceil(list.total / list.per_page)
  const { top, bottom } = sharedOf(site)
  const nav =
    'inline-flex min-h-12 items-center rounded-[var(--site-radius)] border border-[var(--site-border)] px-5 text-[15px] font-semibold'
  return (
    <Frame site={site}>
      <Blocks site={site} blocks={top} />
      <main className="mx-auto w-full max-w-[760px] px-5 py-12 md:py-16">
        <h1 className="mb-4 text-3xl font-bold md:text-4xl">Blog</h1>
        <ul className="divide-y divide-[var(--site-border)]">
          {list.posts.map((p) => (
            <PostRow key={p.slug} site={site} p={p} />
          ))}
        </ul>
        {pages > 1 && (
          <nav aria-label="Blog pages" className="mt-6 flex justify-between gap-3">
            {n > 1 ? (
              <a rel="prev" href={blogPath(n - 1)} className={nav}>
                Newer posts
              </a>
            ) : (
              <span />
            )}
            {n < pages && (
              <a rel="next" href={blogPath(n + 1)} className={nav}>
                Older posts
              </a>
            )}
          </nav>
        )}
      </main>
      <Blocks site={site} blocks={bottom} />
    </Frame>
  )
}

export async function postMetadata(host: string, slug: string): Promise<Metadata> {
  const [site, post] = await Promise.all([load(host), publicPost(host, slug)])
  if (!site || !post) return {}
  const url = `${site.url}/blog/${post.slug}`
  const description = postDescription(post)
  const cover = imageId(post.cover_image_id) ?? imageId(post.og_image_id)
  return {
    metadataBase: new URL(site.url),
    title: post.title,
    description,
    alternates: { canonical: url, types: rssLink(site) },
    openGraph: {
      type: 'article',
      url,
      title: post.title,
      description,
      publishedTime: post.published_at,
      modifiedTime: post.updated_at,
      images: cover ? [img(site, cover)] : undefined,
    },
  }
}

export async function PublishedPost({ host, slug }: { host: string; slug: string }) {
  const [site, post, recent] = await Promise.all([load(host), publicPost(host, slug), publicPosts(host, 1)])
  if (!site || !post) notFound()
  blogRedirect(site, host, `/blog/${post.slug}`)
  const { top, bottom } = sharedOf(site)
  const name = siteSummary(site.data).name
  const cover = imageId(post.cover_image_id)
  const more = (recent?.posts ?? []).filter((p) => p.slug !== post.slug).slice(0, 3)
  const form = formAt(site)?.block
  const modified = post.updated_at || post.published_at
  const image = cover ?? imageId(post.og_image_id)
  const ld = {
    '@context': 'https://schema.org',
    '@type': 'BlogPosting',
    headline: post.title,
    description: postDescription(post),
    datePublished: post.published_at,
    dateModified: modified,
    image: image ? img(site, image) : undefined,
    mainEntityOfPage: safeHref(`${site.url}/blog/${post.slug}`, blockedOf(site)),
    author: { '@type': 'Organization', name },
    publisher: { '@type': 'Organization', name },
  }
  return (
    <>
      <Frame site={site}>
        <Blocks site={site} blocks={top} />
        <article className="mx-auto w-full max-w-[680px] px-5 py-10 text-[17px] leading-[1.6] md:py-14 md:text-[18px]">
          {cover && (
            // biome-ignore lint/performance/noImgElement: a published site serves its own images, same origin
            <img
              src={`/api/sites/img/${site.site_id}/${cover}`}
              alt=""
              className="mb-6 aspect-video w-full rounded-[var(--site-radius)] object-cover"
            />
          )}
          <h1 className="text-3xl font-bold leading-tight md:text-4xl">{post.title}</h1>
          <p className="mt-2 text-[14px] text-[var(--site-muted)]">
            <time dateTime={post.published_at}>{dateLabel(post.published_at)}</time> ·{' '}
            {readingMinutes(post.body)} min read
          </p>
          <PostBody body={post.body} assetBase={`/api/sites/img/${site.site_id}`} blocked={blockedOf(site)} />
        </article>
        {form && <Blocks site={site} blocks={[form]} />}
        {more.length > 0 && (
          <section className="mx-auto w-full max-w-[680px] px-5 pb-10">
            <h2 className="text-2xl font-bold">More posts</h2>
            <ul className="divide-y divide-[var(--site-border)]">
              {more.map((p) => (
                <PostRow key={p.slug} site={site} p={p} />
              ))}
            </ul>
            <a href="/blog" className="font-semibold underline underline-offset-4">
              All posts
            </a>
          </section>
        )}
        <Blocks site={site} blocks={bottom} />
      </Frame>
      <Ld data={ld} />
    </>
  )
}
