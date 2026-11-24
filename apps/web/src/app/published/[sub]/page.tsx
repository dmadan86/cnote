import type { Metadata } from 'next'
import { notFound } from 'next/navigation'
import { cache } from 'react'
import { Published } from '@/components/sites/published'
import { publicGet, siteSummary } from '@/lib/sites'

// Rendered per request from one API read (design 17.3); the proxy adds Cache-Control: public, max-age=60.
export const dynamic = 'force-dynamic'

const load = cache(publicGet)
type Params = { params: Promise<{ sub: string }> }

export async function generateMetadata({ params }: Params): Promise<Metadata> {
  const site = await load((await params).sub)
  if (!site) return {}
  const s = siteSummary(site.data)
  const image = s.heroImage ? `${site.url}/api/sites/img/${site.site_id}/${s.heroImage}` : undefined
  return {
    metadataBase: new URL(site.url),
    title: s.title || s.name,
    description: s.description || undefined,
    alternates: { canonical: site.url },
    openGraph: {
      type: 'website',
      url: site.url,
      title: s.title || s.name,
      description: s.description || undefined,
      images: image ? [image] : undefined,
    },
  }
}

export default async function Page({ params }: Params) {
  const site = await load((await params).sub)
  if (!site) notFound()
  const s = siteSummary(site.data)
  // Built from the page itself, never from the company profile: what the owner chose to publish.
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
      <Published site={site} />
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
    </>
  )
}
