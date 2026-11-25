import type { JSONContent } from '@tiptap/react'

// Storage contract §2: the inline HTML used inside aligned blocks and HTML tables.
export const esc = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

/** Lowercase #rrggbb, or null for anything else: the value lands inside an HTML attribute, so it is validated, not escaped. */
export function hex(c: string): string | null {
  const t = c.trim().toLowerCase()
  const m = /^rgba?\(\s*(\d{1,3})[ ,]+(\d{1,3})[ ,]+(\d{1,3})/.exec(t)
  if (m) {
    const ch = [m[1], m[2], m[3]].map(Number)
    return ch.every((n) => n < 256) ? `#${ch.map((n) => n.toString(16).padStart(2, '0')).join('')}` : null
  }
  if (/^#[0-9a-f]{3}$/.test(t)) return `#${[...t.slice(1)].map((d) => d + d).join('')}`
  return /^#[0-9a-f]{6}$/.test(t) ? t : null
}

/** docs-parity C1: the seven highlight and cell tints. Anything else keeps its text and drops the tint. */
export const TINTS = ['yellow', 'green', 'blue', 'purple', 'red', 'orange', 'grey'] as const
export const tintOf = (v: unknown): string | null =>
  typeof v === 'string' && (TINTS as readonly string[]).includes(v) ? v : null
/** Text inside <mark> is stored HTML-escaped with markdown punctuation as numeric entities: a backslash escape would be read back as literal text. Same rule as the gateway. */
export const hlEsc = (s: string) => esc(s).replace(/[\\*_`~[\]]/g, (c) => `&#${c.charCodeAt(0)};`)
export const markTag = (tint: string, inner: string) => `<mark data-color="${tint}">${inner}</mark>`

export const span = (color: string, inner: string) => {
  const h = hex(color)
  return h ? `<span style="color: ${h}">${inner}</span>` : inner
}

/** http(s), mailto, relative, #fragment: never javascript:, data:, vbscript: ... */
export const safeHref = (u: string) =>
  /^(https?:|mailto:|\/|#|\.{0,2}\/)/i.test(u.trim()) || !/^[a-z][a-z0-9+.-]*:/i.test(u.trim())
const UUID = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'
const FILE_SRC = new RegExp(`^/api/w/${UUID}/files/${UUID}$`)
/** http(s), plus the one relative form an uploaded image is stored as (docs-parity 7.4): nothing else. */
export const safeSrc = (u: string) => /^https?:\/\//i.test(u.trim()) || FILE_SRC.test(u.trim())
export const posInt = (n: unknown): n is number => Number.isInteger(n) && (n as number) > 0

const STATUS = ['grey', 'blue', 'green', 'yellow', 'red', 'purple']

// outer -> inner. Status and date are outermost, as in the gateway's converter (docs-parity 5.1).
const WRAP: [string, (a: Record<string, string>, s: string) => string][] = [
  ['status', (a, s) => (STATUS.includes(a.color) ? `<span data-status="${a.color}">${s}</span>` : s)],
  [
    'date',
    (a, s) =>
      /^\d{4}-\d{2}-\d{2}$/.test(a.datetime ?? '') ? `<time datetime="${a.datetime}">${s}</time>` : s,
  ],
  ['link', (a, s) => (safeHref(a.href ?? '') ? `<a href="${esc(a.href ?? '')}">${s}</a>` : s)],
  ['bold', (_, s) => `<strong>${s}</strong>`],
  ['italic', (_, s) => `<em>${s}</em>`],
  ['strike', (_, s) => `<s>${s}</s>`],
  ['code', (_, s) => `<code>${s}</code>`],
  [
    'textStyle',
    (a, s) => {
      const t = tintOf(a.highlight)
      const c = a.color ? span(a.color, s) : s
      return t ? markTag(t, c) : c
    },
  ],
]

export function inlineHtml(content: JSONContent[] = []): string {
  return content
    .map((n) => {
      if (n.type === 'hardBreak') return '<br>'
      const ts = n.marks?.find((x) => x.type === 'textStyle')
      const hl = !!tintOf(ts?.attrs?.highlight) && !n.marks?.some((x) => x.type === 'code')
      let out = hl ? hlEsc(n.text ?? '') : esc(n.text ?? '')
      for (const [name, wrap] of [...WRAP].reverse()) {
        const m = n.marks?.find((x) => x.type === name)
        if (!m) continue
        const attrs = (m.attrs ?? {}) as Record<string, string>
        // a highlight inside inline code is left out (same as the gateway)
        const code = name === 'textStyle' && n.marks?.some((x) => x.type === 'code')
        out = wrap(code ? { ...attrs, highlight: '' } : attrs, out)
      }
      return out
    })
    .join('')
}

export const plainText = (n: JSONContent): string => n.text ?? (n.content ?? []).map(plainText).join('')
