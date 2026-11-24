// Website builder: shapes of the site.* commands (docs/design/site-builder.md section 4) and the renderer's safety helpers.
import type { CSSProperties } from 'react'
import catalog from '@/components/sites/catalog.json'
import { api, cmd } from './api'

export type Theme = { primary?: string; background?: string; text?: string; font?: string; radius?: string }
export type SiteBlock = { type: string; props: Record<string, unknown> }
export type SiteData = {
  root: { props: { title?: string; description?: string; theme?: Theme } }
  content: SiteBlock[]
  pages?: SitePage[]
}
export type SitePage = { slug: string; title: string; description?: string; content: SiteBlock[] }
export type SiteVersion = {
  id: string
  seq: number
  source: 'template' | 'chat' | 'edit' | 'editor' | 'restore'
  summary: string
  created_by: { id: string; name: string }
  created_at: string
  is_draft: boolean
  is_published: boolean
}
export type SiteDomain = {
  hostname: string
  status: 'pending' | 'verified' | 'lost'
  txt_token: string
  verified_at?: string | null
}
export type SiteDomainCheck = {
  domain: SiteDomain
  records: { type: string; name: string; value: string; found: string[]; ok: boolean }[]
  verified: boolean
}
export type SiteStats = {
  days: { day: string; visits: number; views: number }[]
  pages: { slug: string; views: number }[]
  referrers: { domain: string; views: number }[]
  enquiries: number
}
export type SetContentOp = { tool: 'set_content'; input: { page: string; content: SiteBlock[] } }
export type Site = {
  id: string
  name: string
  template: string
  draft_version_id: string
  rev: number
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
  // Phase 3a command interfaces; implementations arrive with the catalogue and blog backend.
  catalogGet: (ws: string, id: string) => cmd<CatalogueState>(ws, 'site.catalog_get', { id }),
  catalogSet: (ws: string, id: string, price_mode: CataloguePriceMode, items: CatalogueEntry[]) =>
    cmd<CatalogueState>(ws, 'site.catalog_set', { id, price_mode, items }),
  catalogSync: (ws: string, id: string) =>
    cmd<{ synced_at: string; count: number }>(ws, 'site.catalog_sync', { id }),
  postList: (ws: string, id: string) => cmd<SitePost[]>(ws, 'site.post_list', { id }),
  postGet: (ws: string, id: string, post_id: string) => cmd<SitePost>(ws, 'site.post_get', { id, post_id }),
  postSave: (ws: string, input: PostSaveInput) => cmd<SitePost>(ws, 'site.post_save', input),
  postPublish: (ws: string, id: string, post_id: string, expected_rev: number, publish: boolean) =>
    cmd<SitePost>(ws, 'site.post_publish', { id, post_id, expected_rev, publish }),
  postDelete: (ws: string, id: string, post_id: string, expected_rev: number) =>
    cmd<void>(ws, 'site.post_delete', { id, post_id, expected_rev }),
  addPage: (
    ws: string,
    id: string,
    expected_version_id: string,
    title: string,
    slug?: string,
    template?: string,
  ) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.edit', {
      id,
      expected_version_id,
      ops: [{ tool: 'add_page', input: { title, slug, template } }],
    }),
  removePage: (ws: string, id: string, expected_version_id: string, page: string) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.edit', {
      id,
      expected_version_id,
      ops: [{ tool: 'remove_page', input: { page } }],
    }),
  setPage: (
    ws: string,
    id: string,
    expected_version_id: string,
    input: { page?: string; title?: string; description?: string; slug?: string },
  ) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.edit', {
      id,
      expected_version_id,
      ops: [{ tool: 'set_page', input }],
    }),
  setContent: (
    ws: string,
    id: string,
    expected_version_id: string,
    expected_rev: number,
    page: string,
    content: SiteBlock[],
  ) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.edit', {
      id,
      expected_version_id,
      expected_rev,
      source: 'editor',
      ops: [{ tool: 'set_content', input: { page, content } }],
    }),
  versions: (ws: string, id: string, before_seq?: number, limit?: number) =>
    cmd<SiteVersion[]>(ws, 'site.versions', { id, before_seq, limit }),
  domainAdd: (ws: string, id: string, domain: string) =>
    cmd<SiteDomainCheck>(ws, 'site.domain_set', { id, domain }),
  domainCheck: (ws: string, id: string) => cmd<SiteDomainCheck>(ws, 'site.domain_check', { id }),
  domainRemove: (ws: string, id: string, domain: string) =>
    cmd<{ site: Site }>(ws, 'site.domain_remove', { id, domain }),
  stats: (ws: string, id: string) => cmd<SiteStats>(ws, 'site.stats', { id }),
  exportUrl: (ws: string, id: string) =>
    `/api/w/${encodeURIComponent(ws)}/sites/${encodeURIComponent(id)}/export`,
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
    page?: string,
  ) =>
    cmd<{ turn_id: string }>(ws, 'site.chat', {
      id,
      expected_version_id,
      message,
      block_id,
      image_ids,
      page,
    }),
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
  if (/^\/([a-z0-9]+(-[a-z0-9]+)*)?(#[A-Za-z]+-[0-9a-f]{8})?$/.test(v)) return v // a page of this site
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
  /** The one host this site is served from (a verified custom domain, else the subdomain). Absent on older servers. */
  canonical_host?: string
  form_paused?: boolean
}
const SUB = /^[a-z0-9]+(-[a-z0-9]+)*$/
export const validSub = (v: string) => v.length >= 3 && v.length <= 40 && SUB.test(v)

/**
 * Server only. The API call is the assumed shape of `site.public_get {host}` (internal command, server token):
 * POST {API_URL}/api/sites/public_get with header x-sites-token, body {host}; 404 for unknown, unpublished or taken down.
 * `host` is a site host without port: `<sub>.<SITES_DOMAIN host>` or a verified custom domain (design 17.5).
 */
export async function publicGet(host: string): Promise<PublicSite | null> {
  const domain = process.env.SITES_DOMAIN
  if (!domain || !DNS_HOST.test(host)) return null
  const [dHost, dPort] = domain.toLowerCase().split(':')
  // Site subdomains keep the port of SITES_DOMAIN, as Phase 1 sent them.
  const sent = host.endsWith(`.${dHost}`) ? `${host}${dPort ? `:${dPort}` : ''}` : host
  const api = process.env.API_URL ?? 'http://localhost:4000'
  const r = await fetch(`${api}/api/sites/public_get`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-sites-token': process.env.SITES_SERVER_TOKEN ?? '' },
    body: JSON.stringify({ host: sent }),
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

// ---- Host routing: the proxy's decision as a pure function (design 17.2, 17.5, 17.10) ----

export type Route =
  | { kind: 'next' } // not a site host and nothing to hide: carry on
  | { kind: 'notfound' }
  // host: the site host without port (a subdomain host or a custom domain); rewrite null = pass to the API (images, form, beacon)
  | { kind: 'site'; host: string; custom: boolean; rewrite: string | null }

const UUID = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'
const IMG = new RegExp(`^/api/sites/img/${UUID}/${UUID}$`)
const FORM = new RegExp(`^/api/sites/form/${UUID}$`)
const HIT = new RegExp(`^/api/sites/hit/${UUID}$`)
/** A page address: the same rule as Go (design 17.10). Reserved names never match a page, so they 404. */
export const PAGE_SLUG = /^[a-z0-9]+(-[a-z0-9]+)*$/
export const RESERVED_SLUGS = [
  'blog',
  'api',
  'internal',
  '_next',
  'robots.txt',
  'sitemap.xml',
  'favicon.ico',
  'assets',
  'img',
  'published',
]
export const validSlug = (v: string) =>
  v.length >= 1 && v.length <= 40 && PAGE_SLUG.test(v) && !RESERVED_SLUGS.includes(v)
/** A syntactically valid public DNS name with an alphabetic top label: never an IP literal or a single label. */
export const DNS_HOST = /^(?=.{4,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/

/**
 * `domain` is SITES_DOMAIN (`host[:port]`); `rsc` says the request asked for an RSC/data payload.
 * `custom` switches custom-domain hosts on (SITES_CUSTOM_DOMAINS=1) and lists the app's own hosts, which always
 * reach the app (PUBLIC_BASE_URL host, APP_HOSTS). Without it a host outside SITES_DOMAIN is the app, as in Phase 1.
 */
export function route(
  hostHeader: string | null,
  pathname: string,
  method: string,
  domain: string | undefined,
  rsc = false,
  custom?: { appHosts: string[] },
): Route {
  const d = domain?.trim().toLowerCase().replace(/\.$/, '') ?? ''
  const hide = pathname === '/published' || pathname.startsWith('/published/')
  const pass: Route = hide ? { kind: 'notfound' } : { kind: 'next' }
  if (!d) return pass
  const [dHost, dPort = ''] = d.split(':')
  const m = /^([^:]*)(?::(\d+))?$/.exec((hostHeader ?? '').trim().toLowerCase())
  if (!m) return pass
  const host = m[1].replace(/\.$/, '')
  const port = m[2] ?? ''
  const own = host === dHost || host.endsWith(`.${dHost}`)
  const isCustom = !own && !!custom && DNS_HOST.test(host) && !custom.appHosts.includes(host)
  if (!own && !isCustom) return pass
  // From here the request is for a site host: one label under the sites domain or a custom domain, the allowed
  // port and a handful of paths get through. The bare sites domain is never a site; in local dev (localhost) it
  // is also the app's own host.
  if (host === dHost) return dHost === 'localhost' ? pass : { kind: 'notfound' }
  if (port !== dPort) return { kind: 'notfound' }
  if (own && !validSub(host.slice(0, -dHost.length - 1))) return { kind: 'notfound' }
  if (rsc) return { kind: 'notfound' }
  const site = (rewrite: string | null): Route => ({ kind: 'site', host, custom: isCustom, rewrite })
  const read = method === 'GET' || method === 'HEAD'
  if (read && pathname === '/') return site(`/published/${host}`)
  if (read && (pathname === '/robots.txt' || pathname === '/sitemap.xml'))
    return site(`/published/${host}${pathname}`)
  if (read && validSlug(pathname.slice(1))) return site(`/published/${host}${pathname}`)
  if (read && IMG.test(pathname)) return site(null)
  if (method === 'POST' && (FORM.test(pathname) || HIT.test(pathname))) return site(null)
  return { kind: 'notfound' }
}

// ---- Editor, history and embeds (package B1) ----

/** Embed ids are validated like Go does (design 17.7): only a clean id yields a URL, else nothing renders. */
export function embedLinks(provider: string, id: string): { page: string; frame: string } | undefined {
  const q = encodeURIComponent(id)
  switch (provider) {
    case 'youtube':
      return /^[\w-]{6,20}$/.test(id)
        ? {
            page: `https://www.youtube.com/watch?v=${id}`,
            frame: `https://www.youtube-nocookie.com/embed/${id}`,
          }
        : undefined
    case 'google_maps':
      return id && id.length <= 200 && !CTRL.test(id)
        ? {
            page: `https://www.google.com/maps?q=${q}`,
            frame: `https://www.google.com/maps?q=${q}&output=embed`,
          }
        : undefined
    case 'google_forms':
      return /^[\w-]{10,200}$/.test(id)
        ? {
            page: `https://docs.google.com/forms/d/e/${id}/viewform`,
            frame: `https://docs.google.com/forms/d/e/${id}/viewform?embedded=true`,
          }
        : undefined
    case 'calendly':
      return /^[\w.-]+(\/[\w.-]+){0,2}$/.test(id)
        ? { page: `https://calendly.com/${id}`, frame: `https://calendly.com/${id}` }
        : undefined
  }
}

/** A past version's document, read-only (site.get with version_id). */
export const getVersion = (ws: string, id: string, version_id: string) =>
  cmd<{ site: Site; data: SiteData }>(ws, 'site.get', { id, version_id })
/** Restore makes a new draft version on top; nothing is replaced (design 17.7 of Phase 2 section 7). */
export const restoreAsNew = (ws: string, id: string, expected_version_id: string, version_id: string) =>
  cmd<{ site: Site; data: SiteData }>(ws, 'site.restore', {
    id,
    expected_version_id,
    version_id,
    as_new: true,
  })

// ---- Pages, domains and stats helpers (package B2) ----

/** Extra fields site.get returns for custom domains (design 17.1, 17.3): the hostnames of this site and the feature gate. */
export type SiteDomainMeta = { domains?: SiteDomain[]; domains_available?: boolean }
/** Home plus every other page, in menu order; Home has the empty slug. */
export const pageList = (data: SiteData) => [
  { slug: '', title: 'Home' },
  ...(data.pages ?? []).map((p) => ({ slug: p.slug, title: p.title })),
]
const isShared = (b: SiteBlock) => (blockDefs[b.type] as { shared?: boolean } | undefined)?.shared === true
/**
 * The blocks one page shows (design section 2): Home is `content`; any other page is the Header, its own blocks,
 * then the other shared blocks (Footer, WhatsAppButton) in Home order. Null for an unknown slug.
 */
export function viewOfPage(data: SiteData, slug: string): SiteBlock[] | null {
  if (!slug) return data.content ?? []
  const p = data.pages?.find((x) => x.slug === slug)
  if (!p) return null
  const shared = (data.content ?? []).filter(isShared)
  return [
    ...shared.filter((b) => b.type === 'Header'),
    ...p.content,
    ...shared.filter((b) => b.type !== 'Header'),
  ]
}
/** The document as the renderer wants it for one page: its blocks and its own title and description. */
export function dataOfPage(data: SiteData, slug: string): SiteData {
  const blocks = viewOfPage(data, slug)
  const p = data.pages?.find((x) => x.slug === slug)
  if (!blocks || !p) return data
  return {
    ...data,
    content: blocks,
    root: { props: { ...data.root.props, title: p.title, description: p.description } },
  }
}
/** A page address from its name: lowercase words joined by hyphens, never a reserved one. */
export function slugify(title: string) {
  const s = title
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
    .replace(/-+$/, '')
  return validSlug(s) ? s : ''
}
/** The page templates of the add-page dialog; `legal` ones carry the "not legal advice" label. */
export const PAGE_TEMPLATES = [
  { key: 'about', name: 'About us', title: 'About us', legal: false },
  { key: 'services', name: 'Services', title: 'Services', legal: false },
  { key: 'contact', name: 'Contact', title: 'Contact', legal: false },
  { key: 'privacy', name: 'Privacy policy', title: 'Privacy policy', legal: true },
  { key: 'terms', name: 'Terms', title: 'Terms', legal: true },
  { key: '', name: 'Blank page', title: '', legal: false },
] as const
/** The host this site is served from, without a port; falls back to the host of `url`. */
export function canonicalHost(site: Pick<PublicSite, 'url' | 'canonical_host'>) {
  const h = site.canonical_host || new URL(site.url).host
  return h.replace(/:\d+$/, '').toLowerCase()
}
/** One hostname's DNS check (design 17.1). `domain` names the hostname; each is verified on its own. */
export const checkHostname = (ws: string, id: string, domain: string) =>
  cmd<SiteDomainCheck>(ws, 'site.domain_check', { id, domain })
/** The visitor's referrer host, only when it is another site; never a path or query. */
export function externalReferrerHost(referrer: string, own: string) {
  try {
    const h = new URL(referrer).host
    return h && h !== own ? h.slice(0, 100) : ''
  } catch {
    return ''
  }
}

// ---- Phase 3a catalogue and blog scaffolding ----
export type CataloguePriceMode = 'show_incl' | 'show_plus' | 'hide'
export type CatalogueEntry = {
  item_id: string
  category: string
  description: string
  image_id?: string
  position: number
}
export type CatalogueSnapshotItem = CatalogueEntry & {
  name: string
  unit: string
  price_minor?: number
  mode: CataloguePriceMode
}
export type CatalogueState = {
  price_mode: CataloguePriceMode
  items: CatalogueEntry[]
  synced_at: string | null
}
export type PostBodyDocument = { type: 'doc'; content?: PostNode[] }
export type PostNode = {
  type: string
  attrs?: Record<string, unknown>
  content?: PostNode[]
  text?: string
  marks?: { type: string; attrs?: Record<string, unknown> }[]
}
export type SitePost = {
  id: string
  site_id: string
  workspace_id: string
  slug: string
  title: string
  description: string
  cover_image_id: string | null
  og_image_id: string | null
  body: PostBodyDocument
  pending: Record<string, unknown> | null
  status: 'draft' | 'published'
  published_at: string | null
  rev: number
  created_by: string
  created_at: string
  updated_at: string
}
export type PostSaveInput = {
  id: string
  post_id?: string
  expected_rev?: number
  slug: string
  title: string
  description?: string
  cover_image_id?: string | null
  body: PostBodyDocument
}
