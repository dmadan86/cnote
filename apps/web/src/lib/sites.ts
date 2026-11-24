// Website builder: shapes of the site.* commands (docs/design/site-builder.md section 4) and the renderer's safety helpers.
import catalog from '@/components/sites/catalog.json'
import { cmd } from './api'

export type Theme = { primary?: string; background?: string; text?: string; font?: string; radius?: string }
export type SiteBlock = { type: string; props: Record<string, unknown> }
export type SiteData = { root: { props: { title?: string; theme?: Theme } }; content: SiteBlock[] }
export type Site = {
  id: string
  name: string
  template: string
  draft_version_id: string
  parent_version_id: string | null
  created_at: string
  updated_at: string
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
  get: (ws: string, id: string) => cmd<{ site: Site; data: SiteData; turns: Turn[] }>(ws, 'site.get', { id }),
  chat: (ws: string, id: string, expected_version_id: string, message: string, block_id?: string) =>
    cmd<{ turn_id: string }>(ws, 'site.chat', { id, expected_version_id, message, block_id }),
  turn: (ws: string, id: string) => cmd<{ turn: Turn; data?: SiteData }>(ws, 'site.turn_get', { id }),
  cancel: (ws: string, id: string) => cmd<{ status: string }>(ws, 'site.turn_cancel', { id }),
  edit: (ws: string, id: string, expected_version_id: string, ops: EditOp[]) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.edit', { id, expected_version_id, ops }),
  // Undo, redo and "restore to here" all move the draft pointer.
  restore: (ws: string, id: string, expected_version_id: string, version_id: string) =>
    cmd<{ site: Site; data: SiteData }>(ws, 'site.restore', { id, expected_version_id, version_id }),
}

export type BlockDef = { label: string; max?: number }
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
export const safeSrc = (v: unknown) =>
  typeof v === 'string' && /^https:\/\/[^\s]+$/i.test(v) ? v : undefined
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
