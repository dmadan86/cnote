import { canonicalRedirect } from '@/components/sites/published'
import { publicGet } from '@/lib/sites'

export const dynamic = 'force-dynamic'

export async function GET(_: Request, { params }: { params: Promise<{ host: string }> }) {
  const host = (await params).host
  const site = await publicGet(host)
  if (!site) return new Response('Not found', { status: 404 })
  const moved = canonicalRedirect(site, host, '/robots.txt')
  if (moved) return moved
  return new Response(`User-agent: *\nAllow: /\nSitemap: ${site.url}/sitemap.xml\n`, {
    headers: { 'content-type': 'text/plain; charset=utf-8', 'cache-control': 'public, max-age=60' },
  })
}
