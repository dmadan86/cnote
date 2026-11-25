// Port of crates/cnote-collab/src/md.rs: Markdown <-> the Tiptap/ProseMirror Y.XmlFragment named "default".
// Indices are UTF-16 string indices throughout (only ASCII delimiters are ever matched, so results equal Rust's
// byte indices); Rust's Unicode predicates are emulated explicitly (White_Space trim, Alphabetic|N, Cc).
// shortcut: to_markdown does not backslash-escape literal `*`/`_` (the browser does not either), so `\*` seeded
// as `*` comes back unescaped; a link spanning differently-marked runs renders one link per run.
import * as Y from 'yjs'

export const FRAGMENT = 'default'

interface Marks {
  bold: boolean
  italic: boolean
  strike: boolean
  code: boolean
  link: string | null
  color: string | null
  status: string | null
  date: string | null
}
type Node = { t: 'text'; s: string; m: Marks } | { t: 'br' }
type Inl = Node[]
interface Cell {
  header: boolean
  colspan: number
  rowspan: number
  colwidth: number[] | null
  paras: Inl[]
}
interface Item {
  text: Inl
  nested: Block[]
}
type Block =
  | { k: 'para'; inl: Inl; align: string | null }
  | { k: 'heading'; level: number; inl: Inl; align: string | null }
  | { k: 'code'; lang: string | null; code: string }
  | { k: 'quote'; paras: Inl[] }
  | { k: 'list'; ordered: boolean; items: Item[] }
  | { k: 'table'; rows: Cell[][] }
  | { k: 'tasks'; items: [boolean, Inl][] }
  | { k: 'image'; src: string; alt: string; title: string | null }
  | { k: 'panel'; kind: string; blocks: Block[] }
  | { k: 'expand'; title: string; blocks: Block[] }
  | { k: 'toc' }

// docs-parity 5.1: the fixed lists every converter validates against.
const STATUS_COLORS = ['grey', 'blue', 'green', 'yellow', 'red', 'purple']
const PANEL_KINDS = ['info', 'note', 'success', 'warning', 'error']
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/

const defMarks = (): Marks => ({
  bold: false,
  italic: false,
  strike: false,
  code: false,
  link: null,
  color: null,
  status: null,
  date: null,
})
const marksEq = (a: Marks, b: Marks) =>
  a.bold === b.bold &&
  a.italic === b.italic &&
  a.strike === b.strike &&
  a.code === b.code &&
  a.link === b.link &&
  a.color === b.color &&
  a.status === b.status &&
  a.date === b.date

// ---------- Rust string semantics ----------

const WS = '\t\n\v\f\r \u0085                 　'
const isWs = (c: string | undefined) => c !== undefined && c !== '' && WS.includes(c)
const isAsciiWs = (c: string | undefined) => c === ' ' || c === '\t' || c === '\n' || c === '\f' || c === '\r'
const trimStart = (s: string) => {
  let i = 0
  while (i < s.length && isWs(s[i])) i++
  return s.slice(i)
}
const trimEnd = (s: string) => {
  let e = s.length
  while (e > 0 && isWs(s[e - 1])) e--
  return s.slice(0, e)
}
const trim = (s: string) => trimEnd(trimStart(s))
const lower = (s: string) => s.replace(/[A-Z]/g, (c) => c.toLowerCase())
const isAlnum = (cp: string | undefined) => cp !== undefined && /^[\p{Alphabetic}\p{N}]$/u.test(cp)
const isAsciiPunct = (c: string | undefined) => c !== undefined && /^[!-/:-@[-`{-~]$/.test(c)
const isDigit = (c: string | undefined) => c !== undefined && c >= '0' && c <= '9'
const firstCp = (s: string) => (s.length ? String.fromCodePoint(s.codePointAt(0)!) : undefined)
const lastCp = (s: string) => {
  if (!s.length) return undefined
  const n = s.length
  if (n >= 2 && /[\uDC00-\uDFFF]/.test(s[n - 1]) && /[\uD800-\uDBFF]/.test(s[n - 2])) return s.slice(n - 2)
  return s[n - 1]
}
const splitWs = (s: string) => {
  const out: string[] = []
  let cur = ''
  for (const ch of s) {
    if (isWs(ch)) {
      if (cur) out.push(cur)
      cur = ''
    } else cur += ch
  }
  if (cur) out.push(cur)
  return out
}
/** Rust str::lines(): split on \n, strip \r only from a \n-terminated line. */
const rlines = (s: string): string[] => {
  const parts = s.split('\n')
  const last = parts.pop()!
  const res = parts.map((l) => (l.endsWith('\r') ? l.slice(0, -1) : l))
  if (last !== '') res.push(last)
  return res
}
const i64 = (s: string): number | null => {
  if (!/^[+-]?\d+$/.test(s)) return null
  const b = BigInt(s)
  if (b > 9223372036854775807n || b < -9223372036854775808n) return null
  return Number(b)
}

// ---------- HTML helpers ----------

const esc = (s: string) =>
  s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;')
const unesc = (s: string) =>
  s
    .replaceAll('&lt;', '<')
    .replaceAll('&gt;', '>')
    .replaceAll('&quot;', '"')
    .replaceAll('&#39;', "'")
    .replaceAll('&apos;', "'")
    .replaceAll('&amp;', '&')

interface Tag {
  name: string
  closing: boolean
  attrs: [string, string][]
  len: number
}
const attrOf = (t: Tag, k: string): string | null => t.attrs.find((a) => a[0] === k)?.[1] ?? null

function tagAt(s: string): Tag | null {
  if (s[0] !== '<') return null
  let i = 1
  const closing = s[i] === '/'
  if (closing) i++
  const st = i
  while (i < s.length && /[A-Za-z0-9]/.test(s[i])) i++
  if (i === st) return null
  const name = lower(s.slice(st, i))
  const attrs: [string, string][] = []
  for (;;) {
    while (i < s.length && (isAsciiWs(s[i]) || s[i] === '/')) i++
    if (i >= s.length) return null
    if (s[i] === '>') {
      i++
      break
    }
    const ks = i
    while (i < s.length && s[i] !== '=' && s[i] !== '>' && s[i] !== '/' && !isAsciiWs(s[i])) i++
    if (i === ks) return null
    const key = lower(s.slice(ks, i))
    let val = ''
    if (s[i] === '=') {
      i++
      if (i >= s.length) return null
      const q = s[i]
      if (q === '"' || q === "'") {
        const e = s.indexOf(q, i + 1)
        if (e < 0) return null
        val = unesc(s.slice(i + 1, e))
        i = e + 1
      } else {
        const vs = i
        while (i < s.length && !isAsciiWs(s[i]) && s[i] !== '>') i++
        val = unesc(s.slice(vs, i))
      }
    }
    attrs.push([key, val])
  }
  return { name, closing, attrs, len: i }
}

function styleProp(style: string, key: string): string | null {
  for (const d of style.split(';')) {
    const p = d.indexOf(':')
    if (p < 0) continue
    if (lower(trim(d.slice(0, p))) === lower(key)) return lower(trim(d.slice(p + 1)))
  }
  return null
}

function normColor(c0: string): string | null {
  const c = lower(trim(c0))
  if (c.startsWith('#')) {
    const h = c.slice(1)
    if (!/^[0-9a-f]*$/.test(h)) return null
    if (h.length === 6) return c
    if (h.length === 3) return `#${[...h].map((x) => x + x).join('')}`
    return null
  }
  let inner: string
  if (c.startsWith('rgba(')) inner = c.slice(5)
  else if (c.startsWith('rgb(')) inner = c.slice(4)
  else return null
  if (!inner.endsWith(')')) return null
  inner = inner.slice(0, -1)
  const v = inner.split(/[, /]/).filter((x) => x !== '')
  if (v.length < 3 || v.length > 4) return null
  let out = '#'
  for (const x of v.slice(0, 3)) {
    if (!/^\+?\d+$/.test(x)) return null
    const n = Number(x)
    if (n > 255) return null
    out += n.toString(16).padStart(2, '0')
  }
  return out
}

function styleColor(style: string): string | null {
  const p = styleProp(style, 'color')
  return p === null ? null : normColor(p)
}

function safeHref(h0: string): boolean {
  const h = lower(trim(h0))
  if (/[\u0000-\u001f\u007f-\u009f]/.test(h)) return false
  const m = h.search(/[:/?#]/)
  if (m >= 0 && h[m] === ':') return ['http', 'https', 'mailto'].includes(h.slice(0, m))
  return true
}
const U = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'
const FILE_SRC = new RegExp(`^/api/w/${U}/files/${U}$`)
/** http(s), plus the one relative form an uploaded image is stored as (docs-parity 7.4). */
const safeSrc = (s0: string) => {
  const t = trim(s0)
  const s = lower(t)
  return s.startsWith('http://') || s.startsWith('https://') || FILE_SRC.test(t)
}

function pushText(out: Inl, s: string, m: Marks) {
  const last = out[out.length - 1]
  if (last && last.t === 'text' && marksEq(last.m, m)) {
    last.s += s
    return
  }
  out.push({ t: 'text', s, m })
}

/** Inline HTML subset -> nodes; null when it holds anything outside the subset. */
function htmlInline(s: string): Inl | null {
  const out: Inl = []
  const stack: [string, Marks][] = []
  let m = defMarks()
  let i = 0
  while (i < s.length) {
    if (s[i] === '<') {
      const t = tagAt(s.slice(i))
      if (!t) return null
      i += t.len
      if (t.name === 'br' && !t.closing) out.push({ t: 'br' })
      else if (t.closing) {
        const top = stack.pop()
        if (!top || top[0] !== t.name) return null
        m = top[1]
      } else {
        stack.push([t.name, m])
        m = { ...m }
        switch (t.name) {
          case 'a': {
            const h = attrOf(t, 'href')
            if (h === null) return null
            m.link = safeHref(h) ? h : null
            break
          }
          case 'strong':
          case 'b':
            m.bold = true
            break
          case 'em':
          case 'i':
            m.italic = true
            break
          case 's':
          case 'del':
          case 'strike':
            m.strike = true
            break
          case 'code':
            m.code = true
            break
          case 'span': {
            const ds = attrOf(t, 'data-status')
            if (ds !== null) {
              m.status = STATUS_COLORS.includes(ds) ? ds : m.status
              break
            }
            const st = attrOf(t, 'style')
            m.color = st === null ? null : styleColor(st)
            break
          }
          case 'time': {
            const dt = attrOf(t, 'datetime')
            if (dt === null) return null
            m.date = DATE_RE.test(dt) ? dt : m.date
            break
          }
          default:
            return null
        }
      }
    } else {
      const p = s.indexOf('<', i)
      const e = p < 0 ? s.length : p
      pushText(out, unesc(s.slice(i, e)), m)
      i = e
    }
  }
  return stack.length === 0 ? out : null
}

// ---------- Markdown inline parser ----------

function matching(s: string, open: string, close: string): number | null {
  let d = 0
  let e = false
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (e) e = false
    else if (c === '\\') e = true
    else if (c === open) d++
    else if (c === close) {
      d--
      if (d === 0) return i
    }
  }
  return null
}

function closer(rest: string, c: string, n: number): number | null {
  let j = n
  while (j < rest.length) {
    const x = rest[j]
    if (x === '\\') j += 2
    else if (x === '`') {
      const e = rest.indexOf('`', j + 1)
      j += e < 0 ? 1 : e - (j + 1) + 2
    } else if (x === c) {
      let run = 0
      while (rest[j + run] === x) run++
      let k: number | null
      if (n === 1) k = run === 1 ? j : null
      else k = run >= 2 ? j + run - 2 : null
      if (k !== null && k > n && !isWs(lastCp(rest.slice(0, k)))) return k
      j += run
    } else j += 1
  }
  return null
}

/** Index of the </name> that closes an element whose open tag ends at `from`, counting nested ones; -1 if none. */
function closeTag(s: string, name: string, from: number): number {
  const l = lower(s)
  let depth = 1
  for (let i = from; i < l.length; i++) {
    if (l[i] !== '<') continue
    if (l.startsWith(`</${name}>`, i) && --depth === 0) return i
    if (l.startsWith(`<${name}`, i) && /[\s>]/.test(l[i + name.length + 1] ?? '')) depth++
  }
  return -1
}

function construct(rest: string, prev: string | undefined, m: Marks, br: boolean): [number, Inl] | null {
  const c = rest[0]
  switch (c) {
    case '`': {
      const e = rest.indexOf('`', 1)
      if (e < 0) return null
      const code = rest.slice(1, e)
      if (code === '') return null
      return [e + 1, [{ t: 'text', s: code, m: { ...m, code: true, color: null } }]]
    }
    case '*':
    case '_':
    case '~': {
      const n = rest[1] === c ? 2 : 1
      if ((c === '~' && n === 1) || (c === '_' && isAlnum(prev))) return null
      const nx = firstCp(rest.slice(n))
      if (nx === undefined || isWs(nx)) return null
      const k = closer(rest, c, n)
      if (k === null) return null
      if (c === '_' && isAlnum(firstCp(rest.slice(k + n)))) return null
      const mm = { ...m }
      if (c === '~') mm.strike = true
      else if (n === 2) mm.bold = true
      else mm.italic = true
      return [k + n, mdInline(rest.slice(n, k), mm, br)]
    }
    case '[': {
      if (m.link !== null) return null
      const te = matching(rest, '[', ']')
      if (te === null) return null
      const after = rest.slice(te + 1)
      if (!after.startsWith('(')) return null
      const ue = matching(after, '(', ')')
      if (ue === null) return null
      const url = trim(trim(after.slice(1, ue)).split(' "')[0])
      if (url === '') return null
      const mm = { ...m, link: safeHref(url) ? url : null }
      return [te + ue + 2, mdInline(rest.slice(1, te), mm, br)]
    }
    case '<': {
      const t = tagAt(rest)
      if (!t) return null
      if (t.name === 'br' && br && !t.closing) return [t.len, [{ t: 'br' }]]
      if ((t.name === 'span' || t.name === 'time') && !t.closing) {
        const ds = t.name === 'span' ? attrOf(t, 'data-status') : attrOf(t, 'datetime')
        if (ds !== null) {
          const e = closeTag(rest, t.name, t.len)
          if (e < 0) return null
          const mm = { ...m }
          if (t.name === 'span' && STATUS_COLORS.includes(ds)) mm.status = ds
          if (t.name === 'time' && DATE_RE.test(ds)) mm.date = ds
          return [e + t.name.length + 3, mdInline(rest.slice(t.len, e), mm, br)]
        }
        if (t.name === 'time') return null
        const st = attrOf(t, 'style')
        const color = st === null ? null : styleColor(st)
        const f = lower(rest.slice(t.len)).indexOf('</span>')
        if (f < 0) return null
        const e = f + t.len
        return [e + 7, [{ t: 'text', s: unesc(mdUnescape(rest.slice(t.len, e))), m: { ...m, color } }]]
      }
      return null
    }
    default:
      return null
  }
}

function mdInline(s: string, m: Marks, br: boolean): Inl {
  const out: Inl = []
  let lit = ''
  let i = 0
  while (i < s.length) {
    const rest = s.slice(i)
    const c = firstCp(rest)!
    if (c === '\\' && isAsciiPunct(rest[1])) {
      lit += rest[1]
      i += 2
      continue
    }
    const r = construct(rest, lastCp(s.slice(0, i)), m, br)
    if (r) {
      if (lit) {
        pushText(out, lit, m)
        lit = ''
      }
      for (const n of r[1]) {
        if (n.t === 'text') pushText(out, n.s, n.m)
        else out.push(n)
      }
      i += r[0]
    } else {
      lit += c
      i += c.length
    }
  }
  if (lit) pushText(out, lit, m)
  return out
}

const md = (s: string) => mdInline(s, defMarks(), false)

// ---------- block parser ----------

function listMarker(line: string): [number, boolean, string] | null {
  const t = trimStart(line)
  const indent = Buffer.byteLength(line.slice(0, line.length - t.length))
  for (const m of ['- ', '* ', '+ ']) if (t.startsWith(m)) return [indent, false, t.slice(2)]
  let digits = 0
  while (isDigit(t[digits])) digits++
  if (digits > 0 && digits < 10) {
    const rest = t.slice(digits)
    if (rest.startsWith('. ') || rest.startsWith(') ')) return [indent, true, rest.slice(2)]
  }
  return null
}

function heading(line: string): [number, string] | null {
  let n = 0
  while (line[n] === '#') n++
  if (n < 1 || n > 6) return null
  const r = line.slice(n)
  return r.startsWith(' ') ? [n, trim(r.slice(1))] : null
}

function task(line: string): [boolean, string] | null {
  const lm = listMarker(line)
  if (!lm) return null
  const [indent, ordered, rest] = lm
  if (indent > 0 || ordered) return null
  if (rest.startsWith('[ ] ')) return [false, rest.slice(4)]
  if (rest.startsWith('[x] ') || rest.startsWith('[X] ')) return [true, rest.slice(4)]
  return null
}

function isTableSep(line: string): boolean {
  const t = trim(line)
  return t.includes('|') && t.includes('-') && /^[ \t|:-]*$/.test(t)
}

function tableCells(line: string): string[] {
  let t = trim(line)
  if (t.startsWith('|')) t = t.slice(1)
  if (t.endsWith('|')) {
    const r = t.slice(0, -1)
    if (!r.endsWith('\\')) t = r
  }
  const cells: string[] = []
  let cur = ''
  let e = false
  for (const ch of t) {
    if (ch === '\\' && !e) e = true
    else if (ch === '|' && !e) {
      cells.push(trim(cur))
      cur = ''
    } else {
      if (e && ch !== '|') cur += '\\'
      e = false
      cur += ch
    }
  }
  cells.push(trim(cur))
  return cells
}

const startsBlock = (line: string) =>
  trimStart(line).startsWith('|') ||
  line.startsWith('```') ||
  line.startsWith('>') ||
  heading(line) !== null ||
  listMarker(line) !== null

function parseList(lines: string[], cur: { i: number }, base: number): Block {
  const ordered = listMarker(lines[cur.i])![1]
  const items: Item[] = []
  while (cur.i < lines.length) {
    const lm = listMarker(lines[cur.i])
    if (!lm) break
    const [indent, ord, text] = lm
    if (indent < base || ord !== ordered) break
    if (indent > base && items.length) {
      items[items.length - 1].nested.push(parseList(lines, cur, indent))
      continue
    }
    cur.i++
    items.push({ text: md(trim(text)), nested: [] })
  }
  return { k: 'list', ordered, items }
}

function image(line: string): Block | null {
  const t0 = trim(line)
  if (!t0.startsWith('![')) return null
  const t = t0.slice(2)
  const altEnd = t.indexOf('](')
  if (altEnd < 0) return null
  const tail = t.slice(altEnd + 2)
  if (!tail.endsWith(')')) return null
  const inner = tail.slice(0, -1)
  let src = inner
  let title: string | null = null
  const sp = inner.indexOf(' ')
  if (sp >= 0) {
    src = inner.slice(0, sp)
    let r = trim(inner.slice(sp + 1))
    if (!r.startsWith('"')) return null
    r = r.slice(1)
    if (!r.endsWith('"')) return null
    title = r.slice(0, -1)
  }
  return safeSrc(src) ? { k: 'image', src, alt: t.slice(0, altEnd), title } : null
}

function parseCell(t: Tag, body: string): Cell | null {
  const n = (k: string) => {
    const v = attrOf(t, k)
    const x = v === null ? null : i64(trim(v))
    return x !== null && x >= 1 ? x : 1
  }
  let colwidth: number[] | null = null
  const cw = attrOf(t, 'colwidth')
  if (cw !== null) {
    const v = cw.split(',').map((x) => i64(trim(x)))
    if (v.every((x) => x !== null)) {
      const w = v as number[]
      if (w.length && w.every((x) => x > 0)) colwidth = w
    }
  }
  const paras: Inl[] = []
  let rest = trim(body)
  while (rest !== '') {
    const p = tagAt(rest)
    if (p && p.name === 'p' && !p.closing) {
      const e = lower(rest).indexOf('</p>')
      if (e < 0 || p.len > e) return null
      const h = htmlInline(rest.slice(p.len, e))
      if (!h) return null
      paras.push(h)
      rest = trim(rest.slice(e + 4))
    } else {
      const h = htmlInline(rest)
      if (!h) return null
      paras.push(h)
      break
    }
  }
  return { header: t.name === 'th', colspan: n('colspan'), rowspan: n('rowspan'), colwidth, paras }
}

function tableBlock(s: string, pos0: number): Block | null {
  const lc = lower(s)
  let pos = pos0
  const rows: Cell[][] = []
  let cur: Cell[] | null = null
  for (;;) {
    pos += s.length - pos - trimStart(s.slice(pos)).length
    const t = tagAt(s.slice(pos))
    if (!t) return null
    pos += t.len
    const key = t.name
    if (key === 'tbody' || key === 'thead') {
      // ignored
    } else if (key === 'tr' && !t.closing && cur === null) cur = []
    else if (key === 'tr' && t.closing) {
      if (cur === null) return null
      rows.push(cur)
      cur = null
    } else if ((key === 'th' || key === 'td') && !t.closing) {
      const close = `</${key}>`
      const f = lc.indexOf(close, pos)
      if (f < 0 || cur === null) return null
      const c = parseCell(t, s.slice(pos, f))
      if (!c) return null
      cur.push(c)
      pos = f + close.length
    } else if (key === 'table' && t.closing) break
    else return null
  }
  return trim(s.slice(pos)) === '' && rows.length > 0 && cur === null ? { k: 'table', rows } : null
}

function htmlAt(lines: string[], i: number): [Block, number] | null {
  const first = trim(lines[i])
  const lw = lower(first)
  let s: string
  let used: number
  if (lw.startsWith('<table') && !lw.includes('</table>')) {
    let end = -1
    for (let j = i + 1; j < lines.length; j++)
      if (lower(lines[j]).includes('</table>')) {
        end = j
        break
      }
    if (end < 0) return null
    s = lines.slice(i, end + 1).join(' ')
    used = end - i + 1
  } else {
    s = first
    used = 1
  }
  const t = tagAt(s)
  if (!t) return null
  const lc = lower(s)
  if (t.closing) return null
  if (t.name === 'table') {
    const b = tableBlock(s, t.len)
    return b ? [b, used] : null
  }
  let level: number
  if (t.name === 'p') level = 0
  else if (/^h[1-6]$/.test(t.name)) level = Number(t.name[1])
  else return null
  const close = `</${t.name}>`
  if (!lc.endsWith(close)) return null
  const end = s.length - close.length
  if (t.len > end) return null
  const inl = htmlInline(s.slice(t.len, end))
  if (!inl) return null
  const st = attrOf(t, 'style')
  const a = st === null ? null : styleProp(st, 'text-align')
  const align = a !== null && ['center', 'right', 'justify'].includes(a) ? a : null
  return [level === 0 ? { k: 'para', inl, align } : { k: 'heading', level, inl, align }, used]
}

const fenceRun = (t: string) => {
  let n = 0
  while (t[n] === '`') n++
  return n
}
const SUMMARY = /^<details>\s*<summary>(.*)<\/summary>$/i
const panelKind = (l: string) => {
  const m = /^\[!(info|note|success|warning|error)\]$/i.exec(trim(l))
  return m ? lower(m[1]) : null
}

/** `<details><summary>T</summary>` ... `</details>` (nested ones and code fences counted); null when unclosed. */
function expandAt(lines: string[], i: number): [Block, number] | null {
  const m = SUMMARY.exec(trim(lines[i]))
  if (!m) return null
  let depth = 1
  let fence = 0
  for (let j = i + 1; j < lines.length; j++) {
    const t = trim(lines[j])
    const f = fenceRun(t)
    if (fence) {
      if (f >= fence && f === t.length) fence = 0
    } else if (f >= 3) fence = f
    else if (SUMMARY.test(t)) depth++
    else if (lower(t) === '</details>' && --depth === 0)
      return [{ k: 'expand', title: unesc(m[1]), blocks: parse(lines.slice(i + 1, j).join('\n')) }, j - i + 1]
  }
  return null
}

/** A quote whose first line is `[!KIND]`: the rest is parsed as blocks again. */
function panelAt(lines: string[], i: number): [Block, number] | null {
  let j = i
  while (j < lines.length && lines[j].startsWith('>')) j++
  const inner = lines.slice(i, j).map((l) => l.slice(1).replace(/^ /, ''))
  const kind = panelKind(inner[0])
  return kind === null ? null : [{ k: 'panel', kind, blocks: parse(inner.slice(1).join('\n')) }, j - i]
}

function parse(text: string): Block[] {
  const lines = rlines(text)
  const blocks: Block[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    let h: ReturnType<typeof heading>
    let hb: ReturnType<typeof htmlAt>
    let ib: Block | null
    let eb: ReturnType<typeof expandAt>
    if (trim(line) === '') i++
    else if (fenceRun(trimStart(line)) >= 3) {
      const n = fenceRun(trimStart(line))
      const l = trim(trimStart(line).slice(n))
      const code: string[] = []
      i++
      while (i < lines.length) {
        const u = trim(lines[i])
        if (fenceRun(u) >= n && fenceRun(u) === u.length) break
        code.push(lines[i++])
      }
      i++
      blocks.push({ k: 'code', lang: l === '' ? null : l, code: code.join('\n') })
    } else if ((h = heading(line))) {
      blocks.push({ k: 'heading', level: h[0], inl: md(h[1]), align: null })
      i++
    } else if (trim(line) === '[[toc]]') {
      blocks.push({ k: 'toc' })
      i++
    } else if ((eb = expandAt(lines, i)) || (line.startsWith('>') && (eb = panelAt(lines, i)))) {
      blocks.push(eb[0])
      i += eb[1]
    } else if ((hb = htmlAt(lines, i))) {
      blocks.push(hb[0])
      i += hb[1]
    } else if ((ib = image(line))) {
      blocks.push(ib)
      i++
    } else if (line.startsWith('>')) {
      const paras = ['']
      while (i < lines.length && lines[i].startsWith('>')) {
        const t = trim(lines[i].replace(/^>+/, ''))
        if (t === '') paras.push('')
        else {
          if (paras[paras.length - 1] !== '') paras[paras.length - 1] += ' '
          paras[paras.length - 1] += t
        }
        i++
      }
      blocks.push({ k: 'quote', paras: paras.filter((p) => p !== '').map(md) })
    } else if (trimStart(line).startsWith('|') && i + 1 < lines.length && isTableSep(lines[i + 1])) {
      let rows = [tableCells(line)]
      i += 2
      while (i < lines.length && trimStart(lines[i]).startsWith('|')) rows.push(tableCells(lines[i++]))
      const header = rows[0].some((c) => c !== '')
      if (!header) rows = rows.slice(1)
      const width = rows.reduce((a, r) => Math.max(a, r.length), 0)
      blocks.push({
        k: 'table',
        rows: rows.map((row, r) =>
          Array.from({ length: width }, (_, c) => ({
            header: header && r === 0,
            colspan: 1,
            rowspan: 1,
            colwidth: null,
            paras: [mdInline(row[c] ?? '', defMarks(), true)],
          })),
        ),
      })
    } else if (task(line)) {
      const items: [boolean, Inl][] = []
      let tk: ReturnType<typeof task>
      while (i < lines.length && (tk = task(lines[i]))) {
        items.push([tk[0], md(trim(tk[1]))])
        i++
      }
      blocks.push({ k: 'tasks', items })
    } else if (listMarker(line)) {
      blocks.push(
        parseList(
          lines,
          {
            get i() {
              return i
            },
            set i(v) {
              i = v
            },
          },
          listMarker(line)![0],
        ),
      )
    } else {
      let t = trim(line)
      i++
      while (i < lines.length && trim(lines[i]) !== '' && !startsBlock(lines[i])) t += ` ${trim(lines[i++])}`
      blocks.push({ k: 'para', inl: md(t), align: null })
    }
  }
  return blocks
}

// ---------- Yjs writer ----------

type Parent = Y.XmlFragment | Y.XmlElement

/** Marks as y-prosemirror stores them: one XmlText attribute per mark name. */
function markAttrs(m: Marks): Record<string, unknown> {
  const a: Record<string, unknown> = {}
  if (m.bold) a.bold = {}
  if (m.italic) a.italic = {}
  if (m.strike) a.strike = {}
  if (m.code) a.code = {}
  if (m.link !== null) a.link = { href: m.link }
  if (m.color !== null) a.textStyle = { color: m.color }
  if (m.status !== null) a.status = { color: m.status }
  if (m.date !== null) a.date = { datetime: m.date }
  return a
}

function writeInline(parent: Parent, inl: Inl) {
  let cur: Y.XmlText | null = null
  for (const n of inl) {
    if (n.t === 'br') {
      parent.push([new Y.XmlElement('hardBreak')])
      cur = null
    } else {
      if (!cur) {
        cur = new Y.XmlText()
        parent.push([cur])
      }
      cur.insert(cur.length, n.s, markAttrs(n.m))
    }
  }
}

function textEl(parent: Parent, tag: string, inl: Inl): Y.XmlElement {
  const el = new Y.XmlElement(tag)
  parent.push([el])
  writeInline(el, inl)
  return el
}

const setAlign = (el: Y.XmlElement, a: string | null) => {
  if (a !== null) el.setAttribute('textAlign', a)
}

function writeBlock(parent: Parent, b: Block) {
  switch (b.k) {
    case 'para':
      setAlign(textEl(parent, 'paragraph', b.inl), b.align)
      break
    case 'heading': {
      const el = textEl(parent, 'heading', b.inl)
      el.setAttribute('level', b.level as never)
      setAlign(el, b.align)
      break
    }
    case 'code': {
      const el = new Y.XmlElement('codeBlock')
      parent.push([el])
      if (b.code !== '') el.push([new Y.XmlText(b.code)])
      if (b.lang !== null) el.setAttribute('language', b.lang)
      break
    }
    case 'toc':
      parent.push([new Y.XmlElement('toc')])
      break
    case 'panel':
    case 'expand': {
      const el = new Y.XmlElement(b.k)
      parent.push([el])
      if (b.k === 'panel') el.setAttribute('kind', b.kind)
      else el.setAttribute('title', b.title)
      for (const n of b.blocks) writeBlock(el, n)
      if (b.blocks.length === 0) textEl(el, 'paragraph', []) // content is block+
      break
    }
    case 'quote': {
      const q = new Y.XmlElement('blockquote')
      parent.push([q])
      for (const p of b.paras) textEl(q, 'paragraph', p)
      break
    }
    case 'image': {
      const el = new Y.XmlElement('image')
      parent.push([el])
      el.setAttribute('src', b.src)
      el.setAttribute('alt', b.alt)
      if (b.title !== null) el.setAttribute('title', b.title)
      break
    }
    case 'table': {
      const table = new Y.XmlElement('table')
      parent.push([table])
      for (const row of b.rows) {
        const tr = new Y.XmlElement('tableRow')
        table.push([tr])
        for (const c of row) {
          const cell = new Y.XmlElement(c.header ? 'tableHeader' : 'tableCell')
          tr.push([cell])
          if (c.colspan > 1) cell.setAttribute('colspan', c.colspan as never)
          if (c.rowspan > 1) cell.setAttribute('rowspan', c.rowspan as never)
          if (c.colwidth) cell.setAttribute('colwidth', c.colwidth as never)
          for (const p of c.paras) textEl(cell, 'paragraph', p)
          if (c.paras.length === 0) textEl(cell, 'paragraph', [])
        }
      }
      break
    }
    case 'tasks': {
      const list = new Y.XmlElement('taskList')
      parent.push([list])
      for (const [done, t] of b.items) {
        const li = new Y.XmlElement('taskItem')
        list.push([li])
        li.setAttribute('checked', done as never)
        textEl(li, 'paragraph', t)
      }
      break
    }
    case 'list': {
      const list = new Y.XmlElement(b.ordered ? 'orderedList' : 'bulletList')
      parent.push([list])
      for (const it of b.items) {
        const li = new Y.XmlElement('listItem')
        list.push([li])
        textEl(li, 'paragraph', it.text)
        for (const n of it.nested) writeBlock(li, n)
      }
      break
    }
  }
}

/** Fill the "default" fragment of a fresh doc from Markdown. */
export function seed(doc: Y.Doc, text: string): void {
  doc.transact(() => {
    const frag = doc.getXmlFragment(FRAGMENT)
    for (const b of parse(text)) writeBlock(frag, b)
  })
}

// ---------- Markdown / HTML renderer ----------

type Mode = 'md' | 'html' | 'plain'
type Attrs = Record<string, unknown>
const isObj = (v: unknown): v is Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v)

function color(a: Attrs): string | null {
  const ts = a.textStyle
  return isObj(ts) && typeof ts.color === 'string' ? normColor(ts.color) : null
}
function linkHref(a: Attrs): string | null {
  const l = a.link
  return isObj(l) && typeof l.href === 'string' && safeHref(l.href) ? l.href : null
}
const mdEscape = (s: string) => s.replace(/[\\*_`~[\]]/g, (c) => `\\${c}`)
function mdUnescape(s: string): string {
  let o = ''
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '\\' && isAsciiPunct(s[i + 1])) o += s[++i]
    else o += s[i]
  }
  return o
}
const span = (c: string, inner: string) => `<span style="color: ${c}">${inner}</span>`

function statusOf(a: Attrs): string | null {
  const v = a.status
  return isObj(v) && typeof v.color === 'string' && STATUS_COLORS.includes(v.color) ? v.color : null
}
function dateOf(a: Attrs): string | null {
  const v = a.date
  return isObj(v) && typeof v.datetime === 'string' && DATE_RE.test(v.datetime) ? v.datetime : null
}
/** Status and date wrap outside every other mark (docs-parity 5.1). */
function wrapChips(s: string, a: Attrs): string {
  const st = statusOf(a)
  const dt = dateOf(a)
  if (dt !== null) s = `<time datetime="${dt}">${s}</time>`
  if (st !== null) s = `<span data-status="${st}">${s}</span>`
  return s
}

/** A literal `[[toc]]` in text is escaped so it cannot be read back as a table of contents (the browser escapes
 * every `[` anyway, which the md.test contract keeps unescaped here). */
const tocEscape = (s: string) => s.replaceAll('[[toc]]', '\\[\\[toc\\]\\]')

function mark(text: string, a: Attrs | undefined, mode: Mode, literal = false): string {
  if (!a) return mode === 'md' ? (literal ? text : tocEscape(text)) : esc(text)
  const has = (k: string) => Object.hasOwn(a, k)
  const code = has('code')
  if (mode === 'md' && !code && !literal) text = tocEscape(text)
  let col = color(a)
  if (code) col = null
  if (mode === 'plain') return esc(text)
  if (mode === 'html') {
    let s = esc(text)
    if (col !== null) s = span(col, s)
    for (const [on, tag] of [
      [code, 'code'],
      [has('strike'), 's'],
      [has('italic') || has('em'), 'em'],
      [has('bold') || has('strong'), 'strong'],
    ] as [boolean, string][])
      if (on) s = `<${tag}>${s}</${tag}>`
    const h = linkHref(a)
    return wrapChips(h !== null ? `<a href="${esc(h)}">${s}</a>` : s, a)
  }
  const inner = trim(text)
  if (inner === '') return text
  const lead = text.slice(0, text.length - trimStart(text).length)
  const trail = text.slice(trimEnd(text).length)
  let s = col !== null ? span(col, esc(mdEscape(inner))) : inner
  if (code) s = `\`${s}\``
  if (has('italic') || has('em')) s = `*${s}*`
  if (has('bold') || has('strong')) s = `**${s}**`
  if (has('underline')) s = `<u>${s}</u>`
  if (has('strike')) s = `~~${s}~~`
  const h = linkHref(a)
  if (h !== null) s = `[${s}](${h})`
  return lead + wrapChips(s, a) + trail
}

function inline(el: Parent, mode: Mode, literal = false): string {
  let out = ''
  for (const c of el.toArray()) {
    if (c instanceof Y.XmlText) {
      for (const d of c.toDelta() as { insert: unknown; attributes?: Attrs }[])
        if (typeof d.insert === 'string') out += mark(d.insert, d.attributes, mode, literal)
    } else if (c instanceof Y.XmlElement) {
      if (c.nodeName === 'hardBreak') out += mode === 'md' ? '  \n' : mode === 'html' ? '<br>' : ' '
      else out += inline(c, mode, literal)
    }
  }
  return out
}

function indentRest(s: string, pad: string): string {
  return rlines(s)
    .map((l, i) => (i === 0 || l === '' ? l : pad + l))
    .join('\n')
}

const strOf = (v: unknown): string | null => (typeof v === 'string' ? v : null)
function intOf(v: unknown): number | null {
  if (typeof v === 'number') return Number.isNaN(v) ? 0 : Math.trunc(v)
  if (typeof v === 'string') return i64(v)
  return null
}
const alignOf = (el: Y.XmlElement): string | null => {
  const a = strOf(el.getAttribute('textAlign'))
  return a !== null && ['center', 'right', 'justify'].includes(a) ? a : null
}

function render(node: unknown, top: boolean): string | null {
  if (!(node instanceof Y.XmlElement)) return null
  const el = node
  const tag = el.nodeName
  const align = top ? alignOf(el) : null
  if (tag === 'heading') {
    const level = Math.min(6, Math.max(1, intOf(el.getAttribute('level')) ?? 1))
    return align !== null
      ? `<h${level} style="text-align: ${align}">${inline(el, 'html')}</h${level}>`
      : `${'#'.repeat(level)} ${trim(inline(el, 'md'))}`
  }
  if (tag === 'paragraph' && align !== null)
    return `<p style="text-align: ${align}">${inline(el, 'html')}</p>`
  if (tag === 'image') {
    const src = strOf(el.getAttribute('src')) ?? ''
    const alt = strOf(el.getAttribute('alt')) ?? ''
    if (!safeSrc(src)) return null
    const t = strOf(el.getAttribute('title'))
    return t ? `![${alt}](${src} "${t}")` : `![${alt}](${src})`
  }
  if (tag === 'codeBlock') {
    const code = inline(el, 'md', true)
    const fence = '`'.repeat(Math.max(3, ...(code.match(/`+/g) ?? []).map((r) => r.length + 1)))
    return `${fence}${strOf(el.getAttribute('language')) ?? ''}\n${code}\n${fence}`
  }
  if (tag === 'toc') return '[[toc]]'
  if (tag === 'panel') {
    const k = strOf(el.getAttribute('kind'))
    const head = `[!${(k !== null && PANEL_KINDS.includes(k) ? k : 'info').toUpperCase()}]`
    return [head, ...rlines(blocks(el, false))].map((l) => (l === '' ? '>' : `> ${l}`)).join('\n')
  }
  if (tag === 'expand') {
    const title = esc((strOf(el.getAttribute('title')) ?? '').replace(/\s+/g, ' ').trim())
    const body = blocks(el, false)
    return `<details><summary>${title}</summary>\n\n${body === '' ? '' : `${body}\n\n`}</details>`
  }
  if (tag === 'blockquote')
    return rlines(blocks(el, false))
      .map((l) => (l === '' ? '>' : `> ${l}`))
      .join('\n')
  if (tag === 'bulletList' || tag === 'orderedList' || tag === 'taskList') {
    const lines: string[] = []
    el.toArray().forEach((li, n) => {
      if (!(li instanceof Y.XmlElement)) return
      const marker =
        tag === 'bulletList'
          ? '- '
          : tag === 'orderedList'
            ? `${n + 1}. `
            : li.getAttribute('checked') === (true as never)
              ? '- [x] '
              : '- [ ] '
      const parts = li
        .toArray()
        .map((c) => render(c, false))
        .filter((x): x is string => x !== null)
      lines.push(marker + indentRest(parts.join('\n'), ' '.repeat(marker.length)))
    })
    return lines.join('\n')
  }
  if (tag === 'table')
    return cells(el)
      .flat()
      .some((c) => c[2][0] !== 1 || c[2][1] !== 1 || c[2][2] !== null)
      ? tableHtml(el)
      : tableMd(el)
  if (tag === 'horizontalRule') return '---'
  return trim(inline(el, 'md'))
}

type CellAttrs = [number, number, number[] | null]

/** Every cell as [element, is-header, [colspan, rowspan, colwidth]], row by row. */
export function cells(table: Y.XmlElement): [Y.XmlElement, boolean, CellAttrs][][] {
  const rows: [Y.XmlElement, boolean, CellAttrs][][] = []
  for (const tr of table.toArray()) {
    if (!(tr instanceof Y.XmlElement)) continue
    const row: [Y.XmlElement, boolean, CellAttrs][] = []
    for (const cell of tr.toArray()) {
      if (!(cell instanceof Y.XmlElement)) continue
      const span = (k: string) => {
        const n = intOf(cell.getAttribute(k))
        return n !== null && n >= 1 ? n : 1
      }
      let width: number[] | null = null
      const cw = cell.getAttribute('colwidth') as unknown
      if (Array.isArray(cw)) {
        const w = cw.map((x) => {
          const n = intOf(x)
          return n !== null && n > 0 ? n : null
        })
        if (w.every((x) => x !== null) && w.length) width = w as number[]
      }
      row.push([cell, cell.nodeName === 'tableHeader', [span('colspan'), span('rowspan'), width]])
    }
    rows.push(row)
  }
  return rows
}

function tableHtml(table: Y.XmlElement): string {
  let out = '<table>'
  for (const row of cells(table)) {
    out += '<tr>'
    for (const [cell, header, [cs, rs, w]] of row) {
      const tag = header ? 'th' : 'td'
      out += `<${tag}`
      if (cs > 1) out += ` colspan="${cs}"`
      if (rs > 1) out += ` rowspan="${rs}"`
      if (w) out += ` colwidth="${w.join(',')}"`
      out += '>'
      let any = false
      for (const p of cell.toArray()) {
        if (!(p instanceof Y.XmlElement)) continue
        out += `<p>${inline(p, p.nodeName === 'paragraph' ? 'html' : 'plain')}</p>`
        any = true
      }
      if (!any) out += '<p></p>'
      out += `</${tag}>`
    }
    out += '</tr>'
  }
  return `${out}</table>`
}

function tableMd(table: Y.XmlElement): string {
  const rows: [string, boolean][][] = []
  for (const tr of table.toArray()) {
    if (!(tr instanceof Y.XmlElement)) continue
    const cs: [string, boolean][] = []
    for (const cell of tr.toArray()) {
      if (!(cell instanceof Y.XmlElement)) continue
      const raw = cell
        .toArray()
        .map((c) => render(c, false))
        .filter((x): x is string => x !== null)
        .join('\n')
      const text = splitWs(raw.split('\n').map(trim).join('<br>')).join(' ')
      let e = ''
      for (let i = 0; i < text.length; i++) {
        const ch = text[i]
        if (ch === '\\') {
          e += ch
          if (i + 1 < text.length) e += text[++i]
        } else if (ch === '|') e += '\\|'
        else e += ch
      }
      cs.push([e, cell.nodeName === 'tableHeader'])
    }
    rows.push(cs)
  }
  const cols = rows.reduce((a, r) => Math.max(a, r.length), 0)
  if (cols === 0) return ''
  const widths = Array.from({ length: cols }, (_, i) =>
    Math.max(3, ...rows.filter((r) => i < r.length).map((r) => r[i][0].length)),
  )
  const ct = (r: [string, boolean][], i: number) => r[i]?.[0] ?? ''
  const line = (texts: string[]) =>
    `| ${texts.map((t, i) => t + ' '.repeat(Math.max(0, widths[i] - t.length))).join(' | ')} |`
  const hasHeader = rows[0].some((c) => c[1])
  const out = [line(Array.from({ length: cols }, (_, i) => (hasHeader ? ct(rows[0], i) : '')))]
  out.push(`| ${widths.map((w) => '-'.repeat(w)).join(' | ')} |`)
  for (const r of rows.slice(hasHeader ? 1 : 0))
    out.push(line(Array.from({ length: cols }, (_, i) => ct(r, i))))
  return out.join('\n')
}

function blocks(parent: Parent, top: boolean): string {
  return parent
    .toArray()
    .map((c) => render(c, top))
    .filter((x): x is string => x !== null)
    .join('\n\n')
}

/** Equal up to surrounding whitespace and blank-line runs. */
export function same(a: string, b: string): boolean {
  const norm = (s: string) => {
    let out = ''
    let blank = 0
    for (const l of rlines(trim(s))) {
      if (trim(l) === '') {
        blank++
        continue
      }
      if (out !== '') out += blank > 0 ? '\n\n' : '\n'
      blank = 0
      out += trimEnd(l)
    }
    return out
  }
  return norm(a) === norm(b)
}

/** Markdown snapshot of the "default" fragment (empty when there is none). */
export function toMarkdown(doc: Y.Doc): string {
  return doc.share.has(FRAGMENT) ? blocks(doc.getXmlFragment(FRAGMENT), true) : ''
}
