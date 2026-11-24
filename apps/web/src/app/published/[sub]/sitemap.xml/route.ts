import { publicGet } from '@/lib/sites'

export const dynamic = 'force-dynamic'
const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

export async function GET(_: Request, { params }: { params: Promise<{ sub: string }> }) {
  const site = await publicGet((await params).sub)
  if (!site) return new Response('Not found', { status: 404 })
  const last = Number.isNaN(Date.parse(site.published_at))
    ? ''
    : `<lastmod>${esc(site.published_at)}</lastmod>`
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>${esc(site.url)}</loc>${last}</url></urlset>\n`,
    { headers: { 'content-type': 'application/xml; charset=utf-8', 'cache-control': 'public, max-age=60' } },
  )
}
