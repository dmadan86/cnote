import { canonicalRedirect } from '@/components/sites/published'
import { publicGet } from '@/lib/sites'

export const dynamic = 'force-dynamic'
const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

export async function GET(_: Request, { params }: { params: Promise<{ host: string }> }) {
  const host = (await params).host
  const site = await publicGet(host)
  if (!site) return new Response('Not found', { status: 404 })
  const moved = canonicalRedirect(site, host, '/sitemap.xml')
  if (moved) return moved
  const last = Number.isNaN(Date.parse(site.published_at))
    ? ''
    : `<lastmod>${esc(site.published_at)}</lastmod>`
  // Every page of the published version; slugs were validated by the server, and are escaped anyway.
  const urls = ['', ...(site.data.pages ?? []).map((p) => `/${p.slug}`)]
    .map((path) => `<url><loc>${esc(site.url + path)}</loc>${last}</url>`)
    .join('')
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${urls}</urlset>\n`,
    { headers: { 'content-type': 'application/xml; charset=utf-8', 'cache-control': 'public, max-age=60' } },
  )
}
