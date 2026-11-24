import { publicGet } from '@/lib/sites'

export const dynamic = 'force-dynamic'

export async function GET(_: Request, { params }: { params: Promise<{ sub: string }> }) {
  const site = await publicGet((await params).sub)
  if (!site) return new Response('Not found', { status: 404 })
  return new Response(`User-agent: *\nAllow: /\nSitemap: ${site.url}/sitemap.xml\n`, {
    headers: { 'content-type': 'text/plain; charset=utf-8', 'cache-control': 'public, max-age=60' },
  })
}
