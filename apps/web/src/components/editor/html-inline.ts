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

export const span = (color: string, inner: string) => {
  const h = hex(color)
  return h ? `<span style="color: ${h}">${inner}</span>` : inner
}

/** http(s), mailto, relative, #fragment: never javascript:, data:, vbscript: ... */
export const safeHref = (u: string) =>
  /^(https?:|mailto:|\/|#|\.{0,2}\/)/i.test(u.trim()) || !/^[a-z][a-z0-9+.-]*:/i.test(u.trim())
export const safeSrc = (u: string) => /^https?:\/\//i.test(u.trim())
export const posInt = (n: unknown): n is number => Number.isInteger(n) && (n as number) > 0

// outer -> inner
const WRAP: [string, (a: Record<string, string>, s: string) => string][] = [
  ['link', (a, s) => (safeHref(a.href ?? '') ? `<a href="${esc(a.href ?? '')}">${s}</a>` : s)],
  ['bold', (_, s) => `<strong>${s}</strong>`],
  ['italic', (_, s) => `<em>${s}</em>`],
  ['strike', (_, s) => `<s>${s}</s>`],
  ['code', (_, s) => `<code>${s}</code>`],
  ['textStyle', (a, s) => (a.color ? span(a.color, s) : s)],
]

export function inlineHtml(content: JSONContent[] = []): string {
  return content
    .map((n) => {
      if (n.type === 'hardBreak') return '<br>'
      let out = esc(n.text ?? '')
      for (const [name, wrap] of [...WRAP].reverse()) {
        const m = n.marks?.find((x) => x.type === name)
        if (m) out = wrap((m.attrs ?? {}) as Record<string, string>, out)
      }
      return out
    })
    .join('')
}

export const plainText = (n: JSONContent): string => n.text ?? (n.content ?? []).map(plainText).join('')
