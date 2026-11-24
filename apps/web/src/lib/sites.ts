// Website builder: shapes of the site.* commands (docs/design/site-builder.md section 4) and the renderer's safety helpers.
import type { CSSProperties } from 'react'
import catalog from '@/components/sites/catalog.json'
import { api, cmd } from './api'

export type Theme = { primary?: string; background?: string; text?: string; font?: string; radius?: string }
export type SiteBlock = { type: string; props: Record<string, unknown> }
export type SiteData = {
  root: { props: { title?: string; description?: string; theme?: Theme } }
  content: SiteBlock[]
}
export type Site = {
  id: string
  name: string
  template: string
  draft_version_id: string
  parent_version_id: string | null
  created_at: string
  updated_at: string
  // Phase 1 (publish). All optional so the builder compiles before the API returns them.
  subdomain?: string | null
  url?: string | null
  published_version_id?: string | null
  published_at?: string | null
  taken_down?: { reason?: string } | null
  pro?: boolean
  form_count?: number
  form_paused?: boolean
}
/** Everything site.get returns besides the document (design sections 3, 17). */
export type SiteMeta = {
  images?: { id: string; filename: string }[]
  ai_edits?: { used: number; limit: number; resets_at: string }
  can_publish?: { configured: boolean; verified: boolean }
  /** The space website enquiries land in (the site's lead space, or the oldest space before the first publish). */
  lead_space?: { id: string; name: string } | null
}
export type TurnStep = { tool: string; input: Record<string, unknown>; ok: boolean; error?: string }
export type Turn = {
  id: string
  message: string
  block_id: string | null
  status: 'queued' | 'running' | 'done' | 'failed' | 'cancelled'
  reply: string | null
  error: string | null
  summary: string | null
  base_version_id: string
  version_id: string | null
  steps: TurnStep[]
  created_at: string
}
/** too_long: contact fields whose company value did not fit the site and were left empty (never truncated). */
export type Template = { key: string; name: string; description: string; data: SiteData; too_long?: string[] }
export type EditOp = { tool: string; input: Record<string, unknown> }

/** Every write carries expected_version_id, the draft the person is looking at; a stale one answers `conflict`. */
export const siteApi = {
  templates: (ws: string) => cmd<Template[]>(ws, 'site.templates'),
  list: (ws: string) => cmd<Site[]>(ws, 'site.list'),
  create: (ws: string, template: string) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.create', { template }),
  get: (ws: string, id: string) =>
    cmd<{ site: Site; data: SiteData; turns: Turn[] } & SiteMeta>(ws, 'site.get', { id }),
  chat: (
    ws: string,
    id: string,
    expected_version_id: string,
    message: string,
    block_id?: string,
    image_ids?: string[],
  ) => cmd<{ turn_id: string }>(ws, 'site.chat', { id, expected_version_id, message, block_id, image_ids }),
  turn: (ws: string, id: string) => cmd<{ turn: Turn; data?: SiteData }>(ws, 'site.turn_get', { id }),
  cancel: (ws: string, id: string) => cmd<{ status: string }>(ws, 'site.turn_cancel', { id }),
  edit: (ws: string, id: string, expected_version_id: string, ops: EditOp[]) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.edit', { id, expected_version_id, ops }),
  subdomainCheck: (ws: string, id: string, subdomain: string) =>
    cmd<{ subdomain: string; available: boolean; reason?: 'invalid' | 'reserved' | 'taken' }>(
      ws,
      'site.subdomain_check',
      { id, subdomain },
    ),
  publish: (ws: string, id: string, expected_version_id: string, subdomain?: string) =>
    cmd<{ site: Site; url: string }>(ws, 'site.publish', { id, expected_version_id, subdomain }),
  unpublish: (ws: string, id: string) => cmd<{ site: Site }>(ws, 'site.unpublish', { id }),
  // Assumed command (design 17.6: "site admins can pause the form"); see the hand-off notes.
  formPause: (ws: string, id: string, paused: boolean) =>
    cmd<{ site: Site }>(ws, 'site.form_pause', { id, paused }),
  imageUpload: (ws: string, id: string, filename: string, data_base64: string) =>
    cmd<{ image: { id: string; filename: string } }>(ws, 'site.image_upload', { id, filename, data_base64 }),
  verifyRequest: () => api<{ ok?: boolean }>('/api/auth/verify/request', { method: 'POST', body: {} }),
  verifyConfirm: (code: string) => api<{ ok?: boolean }>('/api/auth/verify/confirm', { body: { code } }),
  // Undo, redo and "restore to here" all move the draft pointer.
  restore: (ws: string, id: string, expected_version_id: string, version_id: string) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.restore', { id, expected_version_id, version_id }),
}

export type BlockDef = { label: string; max?: number; props?: Record<string, { kind: string }> }
export const blockDefs = catalog.blocks as Record<string, BlockDef>
export const blockLabel = (type: string) => blockDefs[type]?.label ?? type
/** Block ids are `<Type>-<6 hex>`. */
export const blockType = (id: string) => id.split('-')[0]

// One URL rule, identical to Go (design section 14.8): parsed, never prefix-matched; anything else is dropped.
// biome-ignore lint/suspicious/noControlCharactersInRegex: rejecting control characters is the point
const CTRL = /[\u0000-\u001f\u007f]/
const web = (v: string) => {
  try {
    const u = new URL(v)
    return (u.protocol === 'https:' || u.protocol === 'http:') && !!u.hostname && !u.username && !u.password
  } catch {
    return false
  }
}
export function safeHref(v: unknown): string | undefined {
  if (typeof v !== 'string' || !v || v.length > 300 || CTRL.test(v)) return undefined
  if (/^tel:[\d+\- ]+$/.test(v)) return v
  if (/\s/.test(v)) return undefined
  if (/^#[A-Za-z]+-[0-9a-f]{8}$/.test(v)) return v
  if (/^mailto:[^@,;?]+@[^@,;?]+$/.test(v)) return v
  return /^https?:/i.test(v) && web(v) ? v : undefined
}
/** Image props hold an uploaded image's lowercase UUID, never a URL (design P5). */
export const imageId = (v: unknown) =>
  typeof v === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(v)
    ? v
    : undefined
/** The WhatsApp link for a phone: ten digits get 91 in front; anything not 8 to 15 digits gives nothing. */
export function waHref(phone: unknown, message?: string) {
  if (typeof phone !== 'string') return undefined
  const d = phone.replace(/\D/g, '')
  const n = d.length === 10 ? `91${d}` : d
  if (!/^\d{8,15}$/.test(n)) return undefined
  return `https://wa.me/${n}${message ? `?text=${encodeURIComponent(message)}` : ''}`
}
export const mapsHref = (address: string) =>
  `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(address)}`
export const telHref = (v: string) => {
  const d = v.replace(/[\s().-]/g, '')
  return /^\+\d{6,15}$/.test(d) ? `tel:${d}` : undefined
}

export const hex = (v: unknown, fallback: string) =>
  typeof v === 'string' && /^#[0-9a-f]{6}$/.test(v) ? v : fallback
/** Black or white, whichever reads better on the colour. */
export function onColor(h: string) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = Number.parseInt(h.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.179 ? '#000000' : '#ffffff'
}

const FONTS: Record<string, string> = {
  sans: 'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans", sans-serif',
  serif: 'ui-serif, Georgia, Cambria, "Times New Roman", "Noto Serif", serif',
  rounded: 'ui-rounded, "Hiragino Maru Gothic ProN", "Nunito", system-ui, sans-serif',
}
const RADII: Record<string, string> = { none: '0px', sm: '4px', md: '8px', lg: '16px' }

/** The --site-* variables every block reads; shared by the builder preview and the published page. */
export function themeStyle(theme?: Theme): CSSProperties {
  const t = theme ?? {}
  const primary = hex(t.primary, '#4f46e5')
  const text = hex(t.text, '#111827')
  return {
    '--site-primary': primary,
    '--site-on-primary': onColor(primary),
    '--site-text': text,
    '--site-bg': hex(t.background, '#ffffff'),
    '--site-border': `color-mix(in srgb, ${text} 18%, transparent)`,
    '--site-tint': `color-mix(in srgb, ${primary} 7%, transparent)`,
    '--site-muted': `color-mix(in srgb, ${text} 70%, transparent)`,
    '--site-radius': RADII[t.radius ?? ''] ?? RADII.md,
    '--site-font': FONTS[t.font ?? ''] ?? FONTS.sans,
    background: 'var(--site-bg)',
    color: 'var(--site-text)',
    fontFamily: 'var(--site-font)',
  } as CSSProperties
}

const s = (v: unknown) => (typeof v === 'string' ? v.trim() : '')
const items = (v: unknown): Record<string, unknown>[] =>
  Array.isArray(v) ? v.filter((x): x is Record<string, unknown> => !!x && typeof x === 'object') : []

/** What the page says about itself: the one source for metadata, JSON-LD and the form's business name. */
export function siteSummary(data: SiteData) {
  const block = (t: string) => data.content?.find((b) => b.type === t)?.props ?? {}
  const header = block('Header')
  const hero = block('Hero')
  const form = block('ContactForm')
  const footer = block('Footer')
  const map = block('MapHours')
  const title = s(data.root?.props?.title)
  const pick = (k: string) => s(form[k]) || s(footer[k])
  return {
    title,
    name: s(header.brand) || title,
    description: s(data.root?.props?.description) || s(hero.subtitle),
    phone: pick('phone'),
    email: pick('email'),
    address: pick('address') || s(map.address),
    hours: items(map.hours)
      .map((h) => `${s(h.days)} ${s(h.time)}`.trim())
      .filter(Boolean),
    heroImage: imageId(hero.image),
    hasForm: !!data.content?.some((b) => b.type === 'ContactForm'),
  }
}

// ---- Published-site data: one server-side read per request (design 17.3, 17.4) ----

export type PublicSite = {
  site_id: string
  data: SiteData
  pro: boolean
  published_at: string
  url: string
  form_paused?: boolean
}
const SUB = /^[a-z0-9]+(-[a-z0-9]+)*$/
export const validSub = (v: string) => v.length >= 3 && v.length <= 40 && SUB.test(v)

/**
 * Server only. The API call is the assumed shape of `site.public_get {host}` (internal command, server token):
 * POST {API_URL}/api/sites/public_get with header x-sites-token, body {host}; 404 for unknown, unpublished or taken down.
 */
export async function publicGet(sub: string): Promise<PublicSite | null> {
  const domain = process.env.SITES_DOMAIN
  if (!domain || !validSub(sub)) return null
  const api = process.env.API_URL ?? 'http://localhost:4000'
  const r = await fetch(`${api}/api/sites/public_get`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-sites-token': process.env.SITES_SERVER_TOKEN ?? '' },
    body: JSON.stringify({ host: `${sub}.${domain}` }),
    cache: 'no-store',
  }).catch(() => null)
  if (!r?.ok) return null
  const j = (await r.json().catch(() => null)) as PublicSite | null
  return j && Array.isArray(j.data?.content) ? j : null
}

/** "Report this site": the support form when configured, else the support address, else the app itself. */
export function reportHref(url: string) {
  const base = process.env.PUBLIC_BASE_URL ?? 'http://localhost:3000'
  const subject = `Report website ${url}`
  if (process.env.SUPPORT_FORM_TOKEN)
    return `${base}/t/${encodeURIComponent(process.env.SUPPORT_FORM_TOKEN)}?subject=${encodeURIComponent(subject)}`
  if (process.env.SUPPORT_EMAIL)
    return `mailto:${process.env.SUPPORT_EMAIL}?subject=${encodeURIComponent(subject)}`
  return base
}

// ---- Host routing: the proxy's decision as a pure function (design 17.2) ----

export type Route =
  | { kind: 'next' } // not a site host and nothing to hide: carry on
  | { kind: 'notfound' }
  | { kind: 'site'; sub: string; rewrite: string | null } // rewrite null = pass to the API (images, form)

const UUID = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'
const IMG = new RegExp(`^/api/sites/img/${UUID}/${UUID}$`)
const FORM = new RegExp(`^/api/sites/form/${UUID}$`)

/** `domain` is SITES_DOMAIN (`host[:port]`); `rsc` says the request asked for an RSC/data payload. */
export function route(
  hostHeader: string | null,
  pathname: string,
  method: string,
  domain: string | undefined,
  rsc = false,
): Route {
  const d = domain?.trim().toLowerCase().replace(/\.$/, '') ?? ''
  const hide = pathname === '/published' || pathname.startsWith('/published/')
  if (!d) return hide ? { kind: 'notfound' } : { kind: 'next' }
  const [dHost, dPort = ''] = d.split(':')
  const m = /^([^:]*)(?::(\d+))?$/.exec((hostHeader ?? '').trim().toLowerCase())
  if (!m) return hide ? { kind: 'notfound' } : { kind: 'next' }
  const host = m[1].replace(/\.$/, '')
  const port = m[2] ?? ''
  if (host !== dHost && !host.endsWith(`.${dHost}`)) return hide ? { kind: 'notfound' } : { kind: 'next' }
  // From here the request is for the sites domain: only one label, the allowed port and four paths get through.
  // The bare sites domain is never a site; in local dev (localhost) it is also the app's own host.
  if (host === dHost)
    return dHost === 'localhost' ? (hide ? { kind: 'notfound' } : { kind: 'next' }) : { kind: 'notfound' }
  if (port !== dPort) return { kind: 'notfound' }
  const sub = host.slice(0, -dHost.length - 1)
  if (!validSub(sub) || rsc) return { kind: 'notfound' }
  const read = method === 'GET' || method === 'HEAD'
  if (read && pathname === '/') return { kind: 'site', sub, rewrite: `/published/${sub}` }
  if (read && (pathname === '/robots.txt' || pathname === '/sitemap.xml'))
    return { kind: 'site', sub, rewrite: `/published/${sub}${pathname}` }
  if (read && IMG.test(pathname)) return { kind: 'site', sub, rewrite: null }
  if (method === 'POST' && FORM.test(pathname)) return { kind: 'site', sub, rewrite: null }
  return { kind: 'notfound' }
}
