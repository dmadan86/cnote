import type { Metadata } from 'next'
import { notFound, redirect } from 'next/navigation'
import { cache } from 'react'
import {
  canonicalHost,
  dataOfPage,
  type PublicSite,
  publicGet,
  reportHref,
  siteSummary,
  themeStyle,
  waHref,
} from '@/lib/sites'
import { Beacon } from './beacon'
import { BLOCKS, SiteBadge } from './blocks'
import { ContactFormLive } from './contact-form'

/**
 * The published page: the same BLOCKS and theme variables as the preview, no selection chrome, no cookies.
 * A server component; the only client code is the live contact form. Block ids stay as anchors (#ContactForm-…).
 */
export function Published({ site, slug = '' }: { site: PublicSite; slug?: string }) {
  const data = dataOfPage(site.data, slug)
  const sum = siteSummary(site.data)
  const assetBase = `/api/sites/img/${site.site_id}`
  return (
    <div style={themeStyle(data.root?.props?.theme)} className="min-h-screen">
      {(data.content ?? [])
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
              />
            ) : undefined
          return (
            <div key={id || b.type} id={id}>
              <C {...b.props} assetBase={assetBase} form={form} />
            </div>
          )
        })}
      <Beacon siteId={site.site_id} />
      <SiteBadge pro={site.pro} home={process.env.PUBLIC_BASE_URL ?? '/'} report={reportHref(site.url)} />
    </div>
  )
}

const load = cache(publicGet)
const originOf = (site: PublicSite) => new URL(site.url).origin
const pathOf = (slug: string) => (slug ? `/${slug}` : '/')

/** A request on any host but the canonical one goes to the canonical host (design 17.6): stored host plus a known page path. */
export function canonicalRedirect(site: PublicSite, host: string, path: string) {
  if (host === canonicalHost(site)) return null
  return Response.redirect(`${originOf(site)}${path}`, 302)
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
  const image = s.heroImage ? `${site.url}/api/sites/img/${site.site_id}/${s.heroImage}` : undefined
  return {
    metadataBase: new URL(site.url),
    title,
    description,
    alternates: { canonical: url },
    openGraph: { type: 'website', url, title, description, images: image ? [image] : undefined },
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
    url: site.url,
    telephone: s.phone || undefined,
    email: s.email || undefined,
    address: s.address || undefined,
    openingHours: s.hours.length ? s.hours : undefined,
    image: s.heroImage ? `${site.url}/api/sites/img/${site.site_id}/${s.heroImage}` : undefined,
  }
  return (
    <>
      <Published site={site} slug={slug} />
      {!slug && (
        <script
          type="application/ld+json"
          // biome-ignore lint/security/noDangerouslySetInnerHtml: JSON-LD, with < > & escaped so no value can close the tag
          dangerouslySetInnerHTML={{
            __html: JSON.stringify(ld)
              .replace(/</g, '\\u003c')
              .replace(/>/g, '\\u003e')
              .replace(/&/g, '\\u0026'),
          }}
        />
      )}
    </>
  )
}
