import type { NextConfig } from 'next'

const config: NextConfig = {
  // Published sites are reached at <name>.localhost in development and CI (Chromium resolves *.localhost).
  allowedDevOrigins: ['*.localhost'],
  async headers() {
    return [
      {
        source: '/oauth/:path*',
        headers: [
          { key: 'X-Frame-Options', value: 'DENY' },
          { key: 'Content-Security-Policy', value: "frame-ancestors 'none'" },
          { key: 'Referrer-Policy', value: 'no-referrer' },
        ],
      },
      // The form link is the credential: never leak it through a referrer.
      { source: '/tf/:path*', headers: [{ key: 'Referrer-Policy', value: 'no-referrer' }] },
      // The invitation link is a credential: never leak it through a referrer, never cache or index it.
      {
        source: '/invite/:path*',
        headers: [
          { key: 'Referrer-Policy', value: 'no-referrer' },
          { key: 'Cache-Control', value: 'no-store' },
          { key: 'X-Robots-Tag', value: 'noindex, nofollow' },
        ],
      },
      // A public page link is a credential too, and the page is not for search engines.
      {
        source: '/pg/:path*',
        headers: [
          { key: 'Referrer-Policy', value: 'no-referrer' },
          { key: 'X-Robots-Tag', value: 'noindex, nofollow' },
        ],
      },
    ]
  },
  // Same-origin API so the session cookie is first-party.
  async rewrites() {
    const api = process.env.API_URL ?? 'http://localhost:4000'
    return [
      { source: '/api/:path*', destination: `${api}/api/:path*` },
      // MCP + OAuth machine endpoints, so one public origin serves discovery, tokens and /mcp.
      { source: '/.well-known/:path*', destination: `${api}/.well-known/:path*` },
      { source: '/oauth/:ep(token|register|revoke)', destination: `${api}/oauth/:ep` },
      { source: '/mcp', destination: `${api}/mcp` },
    ]
  },
}

export default config
