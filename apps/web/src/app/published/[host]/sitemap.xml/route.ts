import { canonicalRedirect } from '@/components/sites/published'
import { langPath, type PublicPostSummary, publicGet, publicPostsIn, shownLangs } from '@/lib/sites'

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
  // Every page of the published version, once per shown language with hreflang alternates (design 18.6).
  const codes = ['en', ...shownLangs(site)]
  const loc = (c: string, path: string) => esc(site.url + langPath(c, path || '/'))
  const alt = (path: string, have = codes) =>
    have.length > 1
      ? have.map((c) => `<xhtml:link rel="alternate" hreflang="${c}" href="${loc(c, path)}"/>`).join('') +
        `<xhtml:link rel="alternate" hreflang="x-default" href="${loc('en', path)}"/>`
      : ''
  const paths = ['', ...(site.data.pages ?? []).map((p) => `/${p.slug}`)].flatMap((path) =>
    codes.map((c) => `<url><loc>${loc(c, path)}</loc>${last}${alt(path)}</url>`),
  )
  // /blog and every published post of each language, newest first (design 3); at most 50 pages of posts per language.
  const blog: string[] = []
  const lastmod = (d?: string) => (d && !Number.isNaN(Date.parse(d)) ? `<lastmod>${esc(d)}</lastmod>` : '')
  const withPosts: string[] = []
  const byLang: Record<string, PublicPostSummary[]> = {}
  for (const c of codes) {
    const posts: PublicPostSummary[] = []
    for (let n = 1; n <= 50; n++) {
      const r = await publicPostsIn(host, n, c)
      if (!r) break
      posts.push(...r.posts)
      if (n * r.per_page >= r.total) break
    }
    if (posts.length) {
      withPosts.push(c)
      byLang[c] = posts
    }
  }
  for (const c of withPosts) {
    const posts = byLang[c]
    blog.push(
      `<url><loc>${loc(c, '/blog')}</loc>${lastmod(posts[0].updated_at ?? posts[0].published_at)}${alt('/blog', withPosts)}</url>`,
      ...posts.map(
        (p) => `<url><loc>${loc(c, `/blog/${p.slug}`)}</loc>${lastmod(p.updated_at ?? p.published_at)}</url>`,
      ),
    )
  }
  const urls = [...paths, ...blog].join('')
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">${urls}</urlset>\n`,
    { headers: { 'content-type': 'application/xml; charset=utf-8', 'cache-control': 'public, max-age=60' } },
  )
}
