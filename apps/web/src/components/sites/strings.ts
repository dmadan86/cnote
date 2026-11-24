import strings from './strings.json'

type Table = Record<string, Record<string, string>>
const table = strings as Table

/** Visitor-facing text: the language's entry, else English, else the key. `{name}` placeholders come from `vars`. */
export function t(lang: string, key: string, vars?: Record<string, string | number>): string {
  const s = table[lang]?.[key] ?? table.en[key] ?? key
  return vars ? s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m)) : s
}
