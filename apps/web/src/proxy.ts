import { type NextRequest, NextResponse } from 'next/server'
import { route } from '@/lib/sites'

// Host routing for published sites (design 17.2). SITES_DOMAIN is read per request, never frozen at build.
// Never read X-Forwarded-Host: only the Host the server itself received counts.

const headersFor = (https: boolean) => ({
  'Content-Security-Policy':
    "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'",
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'strict-origin-when-cross-origin',
  'X-Frame-Options': 'DENY',
  'Cross-Origin-Opener-Policy': 'same-origin',
  'Permissions-Policy': 'camera=(), microphone=(), geolocation=(), payment=()',
  ...(https ? { 'Strict-Transport-Security': 'max-age=31536000; includeSubDomains' } : {}),
})

export function proxy(req: NextRequest) {
  const domain = process.env.SITES_DOMAIN
  const r = route(
    req.headers.get('host'),
    req.nextUrl.pathname,
    req.method,
    domain,
    req.headers.has('rsc') || req.nextUrl.searchParams.has('_rsc'),
  )
  if (r.kind === 'next') return NextResponse.next()
  const https = !/^localhost(:|$)/i.test(domain ?? '')
  const sec = headersFor(https)
  if (r.kind === 'notfound') return new NextResponse('Not found', { status: 404, headers: sec })
  // The site origin carries no cookie and trusts no forwarded host: strip both before anything downstream sees them.
  const h = new Headers(req.headers)
  h.delete('cookie')
  h.delete('x-forwarded-host')
  const res = r.rewrite
    ? NextResponse.rewrite(new URL(r.rewrite, req.url), { request: { headers: h } })
    : NextResponse.next({ request: { headers: h } })
  for (const [k, v] of Object.entries(sec)) res.headers.set(k, v)
  if (r.rewrite) res.headers.set('Cache-Control', 'public, max-age=60')
  return res
}

// Everything except Next's static assets (served to site hosts as they are).
export const config = { matcher: ['/((?!_next/static).*)'] }
