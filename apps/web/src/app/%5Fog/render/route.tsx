import { createHash, timingSafeEqual } from 'node:crypto'
import { ImageResponse } from 'next/og'

// Share-image renderer (design 17.1). Called only by the API at publish, with the sites server token; never public.
// The folder is %5Fog so the URL is /_og/render (a plain _og folder would be a private, unrouted folder).
export const runtime = 'nodejs'
export const dynamic = 'force-dynamic'

const W = 1200
const H = 630
const MAX_IMG = 3 * 1024 * 1024
const MAX_PNG = 600 * 1024 // WhatsApp's limit; the design aims for 300 KB
const HEX = /^#[0-9a-f]{6}$/i
const sha = (v: string) => createHash('sha256').update(v).digest()

/** Only text whose every letter is Latin is drawn: no Indic glyphs are bundled, and a box is worse than no text. */
const NON_LATIN = /[^\p{Script=Latin}\p{Script=Common}\p{Script=Inherited}]|\p{Extended_Pictographic}/u
const latin = (v: unknown, max: number) => {
  const s = typeof v === 'string' ? v.replace(/\s+/g, ' ').trim() : ''
  return s && !NON_LATIN.test(s) ? s.slice(0, max) : ''
}

/** Fetch an image to a data URI: png/jpeg/gif only, 3s, 3 MB, no redirects. Anything else means no picture. */
async function picture(u: unknown) {
  if (typeof u !== 'string' || !/^https?:\/\//i.test(u)) return undefined
  try {
    const r = await fetch(u, { signal: AbortSignal.timeout(3000), redirect: 'error', cache: 'no-store' })
    const type = r.headers.get('content-type')?.split(';')[0] ?? ''
    if (!r.ok || !/^image\/(png|jpeg|gif)$/.test(type)) return undefined
    const buf = Buffer.from(await r.arrayBuffer())
    return buf.length <= MAX_IMG ? `data:${type};base64,${buf.toString('base64')}` : undefined
  } catch {
    return undefined
  }
}

type Body = {
  kind?: string
  site_name?: string
  title?: string
  theme?: { primary?: string; background?: string; text?: string }
  logo_url?: string
  photo_url?: string
}

const render = (
  b: Body,
  c: { primary: string; background: string; text: string },
  logo?: string,
  photo?: string,
) => {
  const name = latin(b.site_name, 60)
  const title = latin(b.title, 90)
  return new ImageResponse(
    <div
      style={{
        width: W,
        height: H,
        display: 'flex',
        background: c.background,
        color: c.text,
        fontSize: 40,
      }}
    >
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'space-between',
          flex: 1,
          padding: 64,
          borderLeft: `24px solid ${c.primary}`,
        }}
      >
        {logo ? (
          // biome-ignore lint/performance/noImgElement: an ImageResponse tree, not a page
          <img src={logo} height={96} alt="" style={{ objectFit: 'contain', alignSelf: 'flex-start' }} />
        ) : (
          <div style={{ display: 'flex' }} />
        )}
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          {title && (
            <div style={{ display: 'flex', fontSize: 68, fontWeight: 700, lineHeight: 1.15 }}>{title}</div>
          )}
          {name && (
            <div style={{ display: 'flex', fontSize: 36, marginTop: 24, color: c.primary }}>{name}</div>
          )}
        </div>
      </div>
      {photo && (
        // biome-ignore lint/performance/noImgElement: an ImageResponse tree, not a page
        <img src={photo} width={420} height={H} alt="" style={{ objectFit: 'cover' }} />
      )}
    </div>,
    { width: W, height: H },
  )
}

export async function POST(req: Request) {
  const want = process.env.SITES_SERVER_TOKEN ?? ''
  const got = req.headers.get('x-sites-token') ?? ''
  if (!want || !timingSafeEqual(sha(got), sha(want))) return new Response('Not found', { status: 404 })
  if (Number(req.headers.get('content-length') ?? 0) > 8192) return new Response('Too large', { status: 413 })
  const b = (await req.json().catch(() => null)) as Body | null
  if (!b || typeof b !== 'object' || (b.kind !== 'page' && b.kind !== 'post'))
    return new Response('Bad request', { status: 400 })
  const t = b.theme ?? {}
  const colors = {
    primary: HEX.test(t.primary ?? '') ? (t.primary as string) : '#1f2937',
    background: HEX.test(t.background ?? '') ? (t.background as string) : '#ffffff',
    text: HEX.test(t.text ?? '') ? (t.text as string) : '#111827',
  }
  const [logo, photo] = await Promise.all([picture(b.logo_url), picture(b.photo_url)])
  let png = await render(b, colors, logo, photo).arrayBuffer()
  // A photo can push the PNG past the share limit: draw the card without it instead.
  if (png.byteLength > MAX_PNG && photo) png = await render(b, colors, logo).arrayBuffer()
  return new Response(png, { headers: { 'content-type': 'image/png', 'cache-control': 'no-store' } })
}
