import { canonicalRedirect } from '@/components/sites/published'
import { type PublicPostSummary, publicGet, publicPosts, siteSummary, xmlEscape } from '@/lib/sites'

export const dynamic = 'force-dynamic'

/** RSS 2.0, the newest 20 posts: title, link, guid, pubDate, description. Every value goes through xmlEscape. */
export async function GET(_: Request, { params }: { params: Promise<{ host: string }> }) {
  const host = (await params).host
  const site = await publicGet(host)
  if (!site) return new Response('Not found', { status: 404 })
  const moved = canonicalRedirect(site, host, '/blog/rss.xml')
  if (moved) return moved
  const posts: PublicPostSummary[] = []
  for (let n = 1; n <= 3 && posts.length < 20; n++) {
    const r = await publicPosts(host, n)
    if (!r) break
    posts.push(...r.posts)
    if (n * r.per_page >= r.total) break
  }
  if (!posts.length) return new Response('Not found', { status: 404 })
  const s = siteSummary(site.data)
  const items = posts
    .slice(0, 20)
    .map((p) => {
      const link = `${site.url}/blog/${p.slug}`
      const d = new Date(p.published_at)
      return `<item><title>${xmlEscape(p.title)}</title><link>${xmlEscape(link)}</link><guid isPermaLink="true">${xmlEscape(link)}</guid>${Number.isNaN(d.getTime()) ? '' : `<pubDate>${d.toUTCString()}</pubDate>`}<description>${xmlEscape(p.description ?? '')}</description></item>`
    })
    .join('')
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<rss version="2.0"><channel><title>${xmlEscape(s.name || s.title)}</title><link>${xmlEscape(`${site.url}/blog`)}</link><description>${xmlEscape(s.description || s.name || s.title)}</description>${items}</channel></rss>\n`,
    {
      headers: {
        'content-type': 'application/rss+xml; charset=utf-8',
        'cache-control': 'public, max-age=60',
      },
    },
  )
}
