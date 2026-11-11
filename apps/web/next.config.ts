import type { NextConfig } from 'next'

const config: NextConfig = {
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
