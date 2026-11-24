import { type NextRequest, NextResponse } from 'next/server'
import catalog from '@/components/sites/catalog.json'
import { route } from '@/lib/sites'

// Host routing for published sites (design 17.2). SITES_DOMAIN is read per request, never frozen at build.
// Never read X-Forwarded-Host: only the Host the server itself received counts.

// Exactly the embed providers' origins (design 17.7), from the catalog the renderer and Go read.
const FRAME_SRC = [...new Set(Object.values(catalog.embeds).map((e) => new URL(e.frame).origin))].join(' ')

// A custom domain's apex response must not pin the owner's other subdomains to https (design section 5).
const headersFor = (https: boolean, custom: boolean) => ({
  'Content-Security-Policy': `default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src ${FRAME_SRC}; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`,
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'strict-origin-when-cross-origin',
  'X-Frame-Options': 'DENY',
  'Cross-Origin-Opener-Policy': 'same-origin',
  'Permissions-Policy': 'camera=(), microphone=(), geolocation=(), payment=()',
  ...(https
    ? { 'Strict-Transport-Security': custom ? 'max-age=86400' : 'max-age=31536000; includeSubDomains' }
    : {}),
})

// The app's own hosts always reach the app, whatever the custom-domain switch says.
function appHosts() {
  const own = (v: string) => {
    try {
      return [new URL(v.includes('://') ? v : `https://${v}`).hostname.toLowerCase()]
    } catch {
      return []
    }
  }
  return [process.env.PUBLIC_BASE_URL ?? '', ...(process.env.APP_HOSTS ?? '').split(',')]
    .map((v) => v.trim())
    .filter(Boolean)
    .flatMap(own)
}

export function proxy(req: NextRequest) {
  const domain = process.env.SITES_DOMAIN
  const r = route(
    req.headers.get('host'),
    req.nextUrl.pathname,
    req.method,
    domain,
    req.headers.has('rsc') || req.nextUrl.searchParams.has('_rsc'),
    process.env.SITES_CUSTOM_DOMAINS === '1' ? { appHosts: appHosts() } : undefined,
  )
  if (r.kind === 'next') return NextResponse.next()
  const https = !/^localhost(:|$)/i.test(domain ?? '')
  const sec = headersFor(https, r.kind === 'site' && r.custom)
  if (r.kind === 'notfound') return new NextResponse('Not found', { status: 404, headers: sec })
  // The site origin carries no cookie and trusts no forwarded host: strip both before anything downstream sees them.
  const h = new Headers(req.headers)
  h.delete('cookie')
  h.delete('x-forwarded-host')
  const res = r.rewrite
    ? NextResponse.rewrite(new URL(r.rewrite, req.url), { request: { headers: h } })
    : NextResponse.next({ request: { headers: h } })
  for (const [k, v] of Object.entries(sec)) res.headers.set(k, v)
  res.headers.delete('set-cookie') // a site host never sets a cookie (design 17.5)
  if (r.rewrite) res.headers.set('Cache-Control', 'public, max-age=60')
  return res
}

// Everything except Next's static assets (served to site hosts as they are).
export const config = { matcher: ['/((?!_next/static).*)'] }
