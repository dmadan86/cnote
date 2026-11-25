// embedUrlCases is shared test data for the strict embed-URL allow-list.
// A strict-policy test will iterate over it and assert that the validator
// accepts entries where allow === true and rejects entries where allow === false.
export const embedUrlCases: {
  url: string
  allow: boolean
  provider?: string
  why: string
}[] = [
  // ── YouTube – allowed ──────────────────────────────────────────────────────
  {
    url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
    allow: true,
    provider: 'youtube',
    why: 'canonical YouTube watch URL',
  },
  {
    url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s',
    allow: true,
    provider: 'youtube',
    why: 'YouTube watch URL with timestamp',
  },
  {
    url: 'https://youtu.be/dQw4w9WgXcQ',
    allow: true,
    provider: 'youtube',
    why: 'youtu.be short-link',
  },
  {
    url: 'https://youtu.be/dQw4w9WgXcQ?t=10',
    allow: true,
    provider: 'youtube',
    why: 'youtu.be short-link with timestamp',
  },
  {
    url: 'https://www.youtube.com/shorts/dQw4w9WgXcQ',
    allow: true,
    provider: 'youtube',
    why: 'YouTube Shorts URL',
  },
  {
    url: 'https://www.youtube.com/embed/dQw4w9WgXcQ',
    allow: true,
    provider: 'youtube',
    why: 'YouTube embed URL',
  },

  // ── Google Docs – allowed ──────────────────────────────────────────────────
  {
    url: 'https://docs.google.com/document/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/edit',
    allow: true,
    provider: 'google-docs',
    why: 'Google Docs /edit URL',
  },
  {
    url: 'https://docs.google.com/document/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/view',
    allow: true,
    provider: 'google-docs',
    why: 'Google Docs /view URL',
  },
  {
    url: 'https://docs.google.com/document/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/pub',
    allow: true,
    provider: 'google-docs',
    why: 'Google Docs /pub (published) URL',
  },

  // ── Google Sheets – allowed ────────────────────────────────────────────────
  {
    url: 'https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/edit',
    allow: true,
    provider: 'google-sheets',
    why: 'Google Sheets /edit URL',
  },
  {
    url: 'https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/edit#gid=0',
    allow: true,
    provider: 'google-sheets',
    why: 'Google Sheets /edit URL with sheet anchor',
  },
  {
    url: 'https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/pub?output=csv',
    allow: true,
    provider: 'google-sheets',
    why: 'Google Sheets CSV publish URL',
  },

  // ── Google Slides – allowed ────────────────────────────────────────────────
  {
    url: 'https://docs.google.com/presentation/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/edit',
    allow: true,
    provider: 'google-slides',
    why: 'Google Slides /edit URL',
  },
  {
    url: 'https://docs.google.com/presentation/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/pub?start=false&loop=false&delayms=3000',
    allow: true,
    provider: 'google-slides',
    why: 'Google Slides /pub URL with presentation params',
  },

  // ── Figma – allowed ────────────────────────────────────────────────────────
  {
    url: 'https://www.figma.com/file/ABC123def456GHI789/My-Design',
    allow: true,
    provider: 'figma',
    why: 'Figma /file/ URL',
  },
  {
    url: 'https://www.figma.com/file/ABC123def456GHI789/My-Design?node-id=0%3A1',
    allow: true,
    provider: 'figma',
    why: 'Figma /file/ URL with node-id',
  },
  {
    url: 'https://www.figma.com/design/ABC123def456GHI789/My-Design',
    allow: true,
    provider: 'figma',
    why: 'Figma /design/ URL (new routing)',
  },
  {
    url: 'https://www.figma.com/design/ABC123def456GHI789/My-Design?node-id=1-2',
    allow: true,
    provider: 'figma',
    why: 'Figma /design/ URL with dash-style node-id',
  },
  {
    url: 'https://www.figma.com/proto/ABC123def456GHI789/My-Design',
    allow: true,
    provider: 'figma',
    why: 'Figma /proto/ URL',
  },

  // ── Credentials in URL – refused ──────────────────────────────────────────
  {
    url: 'https://user:pass@www.youtube.com/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'credentials in YouTube URL',
  },
  {
    url: 'https://user:pass@docs.google.com/document/d/DOCID/edit',
    allow: false,
    why: 'credentials in Google Docs URL',
  },
  {
    url: 'https://user:secret@www.figma.com/file/KEY/name',
    allow: false,
    why: 'credentials in Figma URL',
  },

  // ── Non-default port – refused ─────────────────────────────────────────────
  {
    url: 'https://www.youtube.com:8443/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'non-default port 8443 on YouTube',
  },
  {
    url: 'https://docs.google.com:9000/document/d/DOCID/edit',
    allow: false,
    why: 'non-default port 9000 on Google Docs',
  },

  // ── Plain HTTP instead of HTTPS – refused ─────────────────────────────────
  {
    url: 'http://www.youtube.com/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'http scheme on YouTube',
  },
  {
    url: 'http://docs.google.com/document/d/DOCID/edit',
    allow: false,
    why: 'http scheme on Google Docs',
  },
  {
    url: 'http://www.figma.com/file/KEY/name',
    allow: false,
    why: 'http scheme on Figma',
  },

  // ── Lookalike / open-redirect hosts – refused ─────────────────────────────
  {
    url: 'https://youtube.com.evil.com/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'lookalike youtube.com.evil.com subdomain trick',
  },
  {
    url: 'https://www.youtube.com@evil.com/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: '@-trick: authority is evil.com, user-info is www.youtube.com',
  },
  {
    url: 'https://youtu.be.evil.com/dQw4w9WgXcQ',
    allow: false,
    why: 'lookalike youtu.be.evil.com',
  },
  {
    url: 'https://docs.google.com.phishing.example/document/d/DOCID/edit',
    allow: false,
    why: 'lookalike docs.google.com.phishing.example',
  },
  {
    url: 'https://www.figma.com.evil.io/file/KEY/name',
    allow: false,
    why: 'lookalike www.figma.com.evil.io',
  },

  // ── Percent-encoded host tricks – refused ─────────────────────────────────
  {
    url: 'https://www.you%74ube.com/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'percent-encoded character in youtube host',
  },
  {
    url: 'https://%77ww.youtube.com/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'percent-encoded first char of www in host',
  },

  // ── Dangerous schemes – refused ───────────────────────────────────────────
  {
    url: 'javascript:alert(document.cookie)',
    allow: false,
    why: 'javascript: scheme',
  },
  {
    url: 'javascript://www.youtube.com/watch?v=dQw4w9WgXcQ%0aalert(1)',
    allow: false,
    why: 'javascript: scheme disguised as URL',
  },
  {
    url: 'data:text/html,<h1>XSS</h1>',
    allow: false,
    why: 'data: scheme',
  },
  {
    url: 'data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==',
    allow: false,
    why: 'data: scheme base64 HTML',
  },

  // ── Uppercase / mixed-case scheme tricks – refused ────────────────────────
  {
    url: 'HTTPS://www.youtube.com/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'uppercase HTTPS scheme',
  },
  {
    url: 'JavaScript:alert(1)',
    allow: false,
    why: 'mixed-case JavaScript: scheme',
  },

  // ── Backslash in authority – refused ──────────────────────────────────────
  {
    url: 'https:www.youtube.com/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'backslash separators in authority',
  },

  // ── IDN homoglyph host – refused ──────────────────────────────────────────
  {
    url: 'https://www.yoütu.be/dQw4w9WgXcQ',
    allow: false,
    why: 'IDN homoglyph ü in youtu.be',
  },
  {
    url: 'https://ⅾocs.google.com/document/d/DOCID/edit',
    allow: false,
    why: 'IDN homoglyph ⅾ (Unicode digit d) in docs.google.com',
  },

  // ── Localhost / internal origin – refused ─────────────────────────────────
  {
    url: 'https://localhost/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'localhost host',
  },
  {
    url: 'https://127.0.0.1/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'loopback IPv4 host',
  },
  {
    url: 'https://[::1]/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: 'loopback IPv6 host',
  },
  {
    url: 'https://APP_ORIGIN/watch?v=dQw4w9WgXcQ',
    allow: false,
    why: "app's own origin placeholder",
  },
]
