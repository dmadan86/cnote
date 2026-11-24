import { canonicalRedirect } from '@/components/sites/published'
import { type PublicPostSummary, publicGet, publicPosts } from '@/lib/sites'

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
  const paths = ['', ...(site.data.pages ?? []).map((p) => `/${p.slug}`)].map(
    (path) => `<url><loc>${esc(site.url + path)}</loc>${last}</url>`,
  )
  // /blog and every published post, newest first (design 3); at most 50 pages of posts.
  const posts: PublicPostSummary[] = []
  for (let n = 1; n <= 50; n++) {
    const r = await publicPosts(host, n)
    if (!r) break
    posts.push(...r.posts)
    if (n * r.per_page >= r.total) break
  }
  const lastmod = (d?: string) => (d && !Number.isNaN(Date.parse(d)) ? `<lastmod>${esc(d)}</lastmod>` : '')
  const blog = posts.length
    ? [
        `<url><loc>${esc(`${site.url}/blog`)}</loc>${lastmod(posts[0].updated_at ?? posts[0].published_at)}</url>`,
        ...posts.map(
          (p) =>
            `<url><loc>${esc(`${site.url}/blog/${p.slug}`)}</loc>${lastmod(p.updated_at ?? p.published_at)}</url>`,
        ),
      ]
    : []
  const urls = [...paths, ...blog].join('')
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${urls}</urlset>\n`,
    { headers: { 'content-type': 'application/xml; charset=utf-8', 'cache-control': 'public, max-age=60' } },
  )
}
