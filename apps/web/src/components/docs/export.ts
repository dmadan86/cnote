// Docs A2 single-page export (docs-parity 7.9, 7.10). Built on the client from the reader's live editor, never
// from stored HTML. The editor's own HTML is parsed into an inert document, then rebuilt through an allowlist, so
// no script, event handler or javascript: URL can reach any output.

export const MAX_IMAGE_BYTES = 25 << 20

export type ExportMeta = { title: string; status: string | null; labels: string[]; updatedAt: string }
export type Built = { root: HTMLElement; notes: string[] }
type FetchImage = (src: string) => Promise<Blob>
/** A page's current title, or null when the reader cannot see it. */
type ResolveTitle = (ws: string, id: string) => Promise<string | null>
type Opts = { fetchImage?: FetchImage; resolveTitle?: ResolveTitle }

const UPLOAD = /^\/api\/w\/[0-9a-f-]{36}\/files\/[0-9a-f-]{36}$/
const DATA_IMG = /^data:image\/(?:png|jpe?g|gif|webp);base64,[a-z0-9+/=]+$/i
const HEX_COLOR = /^(?:#[0-9a-f]{3,8}|rgba?\([\d\s,.%]+\)|[a-z]{3,20})$/i

/** `<title>.<ext>`, the title cleaned to [A-Za-z0-9 _-] and capped at 80 characters, `page` when empty. */
export function safeFileName(title: string, ext: string) {
  const t = title
    .replace(/[^A-Za-z0-9 _-]/g, '')
    .trim()
    .slice(0, 80)
    .trim()
  return `${t || 'page'}.${ext}`
}

const defaultFetch: FetchImage = async (src) => {
  const r = await fetch(src, { credentials: 'same-origin' })
  if (!r.ok) throw new Error(`HTTP ${r.status}`)
  return r.blob()
}

const PAGE_LINK = /^\/w\/([0-9a-f-]{36})\/n\/([0-9a-f-]{36})$/i

const defaultTitle: ResolveTitle = async (ws, id) => {
  try {
    const r = await fetch(`/api/w/${ws}/commands/note.get`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ id }),
    })
    return r.ok ? ((await r.json()).title ?? '') || 'Untitled' : null
  } catch {
    return null
  }
}

/** A link to a page shows that page's title where the reader can see the page, and is plain text where they cannot. */
async function resolvePageLinks(root: HTMLElement, resolve: ResolveTitle) {
  const links = Array.from(root.querySelectorAll('a[href]')).flatMap((a) => {
    let path = ''
    try {
      path = new URL(a.getAttribute('href') ?? '', location.origin).pathname
    } catch {}
    const m = PAGE_LINK.exec(path)
    return m ? [{ a, ws: m[1], id: m[2].toLowerCase() }] : []
  })
  const titles = new Map<string, string | null>()
  for (const { ws, id } of links) if (!titles.has(id)) titles.set(id, await resolve(ws, id))
  for (const { a, id } of links) {
    const t = titles.get(id)
    if (t) a.textContent = t
    else a.replaceWith(a.textContent ?? '')
  }
}

const toDataUri = (b: Blob) =>
  new Promise<string>((ok, no) => {
    const r = new FileReader()
    r.onload = () => ok(String(r.result))
    r.onerror = () => no(r.error)
    r.readAsDataURL(b)
  })

/** Fetches each uploaded image once, with the session cookie. Past the byte cap the rest are not embedded. */
async function embed(srcs: string[], opts?: Opts) {
  const get = opts?.fetchImage ?? defaultFetch
  const done = new Map<string, string>()
  const failed = new Map<string, 'fail' | 'cap'>()
  let total = 0
  for (const src of new Set(srcs)) {
    if (!UPLOAD.test(src)) continue
    try {
      const b = await get(src)
      if (!/^image\/(?:png|jpeg|gif|webp)$/.test(b.type)) throw new Error('type')
      if (total + b.size > MAX_IMAGE_BYTES) {
        failed.set(src, 'cap')
        continue
      }
      total += b.size
      done.set(src, await toDataUri(b))
    } catch {
      failed.set(src, 'fail')
    }
  }
  const notes: string[] = []
  const n = (k: string) => [...failed.values()].filter((v) => v === k).length
  if (n('fail')) notes.push(`${n('fail')} image${n('fail') > 1 ? 's' : ''} could not be embedded.`)
  if (n('cap'))
    notes.push(
      `Embedded images are limited to 25 MB in total, so ${n('cap')} image${n('cap') > 1 ? 's were' : ' was'} left out.`,
    )
  return { done, failed, notes }
}

// ---------- allowlist ----------

const TAGS = new Set(
  'p h1 h2 h3 h4 h5 h6 ul ol li blockquote pre code em strong s u a br hr table thead tbody tfoot tr th td img div span details summary nav time label input sub sup'.split(
    ' ',
  ),
)
const DROP = new Set(
  'script style iframe object embed link meta base form button select textarea svg math noscript template'.split(
    ' ',
  ),
)
const PANEL = ['info', 'note', 'success', 'warning', 'error'] as const
const STATUS = ['grey', 'blue', 'green', 'yellow', 'red', 'purple'] as const
const PANEL_STYLE: Record<string, [string, string]> = {
  info: ['#e8f1fb', '#2b6cb0'],
  note: ['#f1ecfa', '#6b46c1'],
  success: ['#e9f7ee', '#2f855a'],
  warning: ['#fdf4e0', '#b7791f'],
  error: ['#fdeaea', '#c53030'],
}
const STATUS_STYLE: Record<string, [string, string]> = {
  grey: ['#e4e6ea', '#3b4350'],
  blue: ['#dbeafe', '#1e429f'],
  green: ['#d6f2df', '#14532d'],
  yellow: ['#fdf0c4', '#744210'],
  red: ['#fbd9d9', '#7f1d1d'],
  purple: ['#e9defa', '#44337a'],
}

function safeHref(h: string | null) {
  if (!h) return null
  if (/^(?:https?:|mailto:|#)/i.test(h)) return h
  if (h.startsWith('/') && !h.startsWith('//')) return `${location.origin}${h}`
  return null
}

function safeStyle(raw: string | null) {
  const out: string[] = []
  for (const part of (raw ?? '').split(';')) {
    const [k, ...v] = part.split(':')
    const key = k?.trim().toLowerCase()
    const val = v.join(':').trim()
    if ((key === 'color' || key === 'background-color') && HEX_COLOR.test(val)) out.push(`${key}: ${val}`)
    else if (key === 'text-align' && /^(?:center|right|justify)$/.test(val)) out.push(`${key}: ${val}`)
  }
  return out.join('; ')
}

/** Copies `src` into a fresh element tree, keeping only allowed tags and attributes. Unknown tags keep their children. */
function clean(src: Node, into: Node, doc: Document) {
  for (const n of Array.from(src.childNodes)) {
    if (n.nodeType === 3) {
      into.appendChild(doc.createTextNode(n.textContent ?? ''))
      continue
    }
    if (n.nodeType !== 1) continue
    const e = n as Element
    const tag = e.tagName.toLowerCase()
    if (DROP.has(tag)) continue
    const panel = e.getAttribute('data-panel')
    const isExpand = e.hasAttribute('data-expand')
    const status = e.getAttribute('data-status')
    const keepSpan =
      tag === 'span' && ((status && STATUS.includes(status as never)) || e.getAttribute('style'))
    if (!TAGS.has(tag) || (tag === 'span' && !keepSpan)) {
      clean(e, into, doc)
      continue
    }
    if (tag === 'input') {
      if (e.getAttribute('type') !== 'checkbox') continue
      const i = doc.createElement('input')
      i.type = 'checkbox'
      i.disabled = true
      i.checked = e.hasAttribute('checked')
      into.appendChild(i)
      continue
    }
    let out: HTMLElement
    if (isExpand) {
      out = doc.createElement('details')
      out.setAttribute('open', '')
      const s = doc.createElement('summary')
      s.textContent = e.getAttribute('data-title') || 'Expand'
      out.appendChild(s)
    } else if (tag === 'div' && panel && PANEL.includes(panel as never)) {
      out = doc.createElement('div')
      out.setAttribute('data-panel', panel)
      out.setAttribute('role', 'note')
      const [bg, bar] = PANEL_STYLE[panel]
      out.setAttribute(
        'style',
        `background: ${bg}; border-left: 4px solid ${bar}; padding: 8px 12px; margin: 12px 0`,
      )
      const label = doc.createElement('p')
      label.className = 'cn-panel-label'
      label.setAttribute('style', 'margin: 0 0 4px; font-weight: 700')
      label.textContent = `${panel[0].toUpperCase()}${panel.slice(1)}:`
      out.appendChild(label)
    } else if (tag === 'nav') {
      out = doc.createElement('nav')
      out.setAttribute('data-toc', '')
      out.setAttribute('aria-label', 'Table of contents')
      into.appendChild(out)
      continue // filled by resolveToc once every heading has an id
    } else if (tag === 'div' && !panel) {
      out = doc.createElement('div') // task item body, expand-less wrappers
    } else if (tag === 'div') {
      clean(e, into, doc) // a panel kind we do not know: keep the content only
      continue
    } else {
      out = doc.createElement(tag)
      if (tag === 'a') {
        const h = safeHref(e.getAttribute('href'))
        if (!h) {
          clean(e, into, doc)
          continue
        }
        out.setAttribute('href', h)
      } else if (tag === 'img') {
        const s = e.getAttribute('src') ?? ''
        if (!UPLOAD.test(s) && !DATA_IMG.test(s) && !/^https?:\/\//i.test(s)) {
          into.appendChild(doc.createTextNode(e.getAttribute('alt') ?? ''))
          continue
        }
        out.setAttribute('src', s)
        out.setAttribute('alt', e.getAttribute('alt') ?? '')
      } else if (tag === 'td' || tag === 'th') {
        for (const a of ['colspan', 'rowspan']) {
          const v = Number(e.getAttribute(a))
          if (Number.isInteger(v) && v > 1 && v < 100) out.setAttribute(a, String(v))
        }
      } else if (tag === 'time') {
        const d = e.getAttribute('datetime') ?? ''
        if (/^\d{4}-\d{2}-\d{2}$/.test(d)) out.setAttribute('datetime', d)
      } else if (tag === 'code' || tag === 'pre') {
        const c = /(?:^|\s)language-([\w+#-]{1,30})(?:\s|$)/.exec(e.getAttribute('class') ?? '')
        if (c && tag === 'code') out.setAttribute('class', `language-${c[1]}`)
      } else if (tag === 'li' && e.getAttribute('data-type') === 'taskItem') {
        out.setAttribute('data-checked', e.getAttribute('data-checked') === 'true' ? 'true' : 'false')
      } else if (tag === 'ul' && e.getAttribute('data-type') === 'taskList') {
        out.setAttribute('data-type', 'taskList')
      } else if (tag === 'span' && status && STATUS.includes(status as never)) {
        out.setAttribute('data-status', status)
        const [bg, fg] = STATUS_STYLE[status]
        out.setAttribute(
          'style',
          `background: ${bg}; color: ${fg}; border: 1px solid ${fg}; border-radius: 3px; padding: 0 5px; font-size: 0.85em; font-weight: 600; text-transform: uppercase`,
        )
      } else if (tag === 'span') {
        const st = safeStyle(e.getAttribute('style'))
        if (st) out.setAttribute('style', st)
      }
      if (['p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6'].includes(tag)) {
        const st = safeStyle(e.getAttribute('style'))
        if (st) out.setAttribute('style', st)
      }
    }
    clean(e, out, doc)
    into.appendChild(out)
  }
}

/** Gives every heading an id and fills each Toc with a nested list of anchor links to them. */
function resolveToc(root: HTMLElement, doc: Document) {
  const heads = Array.from(root.querySelectorAll<HTMLElement>('h1,h2,h3,h4,h5,h6')).filter((h) =>
    h.textContent?.trim(),
  )
  heads.forEach((h, i) => {
    h.id = `section-${i + 1}`
  })
  for (const nav of Array.from(root.querySelectorAll('nav[data-toc]'))) {
    if (!heads.length) {
      const p = doc.createElement('p')
      p.textContent = 'No headings on this page.'
      nav.appendChild(p)
      continue
    }
    const top = Math.min(...heads.map((h) => Number(h.tagName[1])))
    const rootUl = doc.createElement('ul')
    const stack: { level: number; ul: HTMLElement }[] = [{ level: top, ul: rootUl }]
    for (const h of heads) {
      const level = Number(h.tagName[1])
      while (stack.length > 1 && stack[stack.length - 1].level > level) stack.pop()
      let cur = stack[stack.length - 1]
      while (cur.level < level) {
        const parent = cur.ul.lastElementChild ?? cur.ul.appendChild(doc.createElement('li'))
        const ul = parent.appendChild(doc.createElement('ul'))
        cur = { level: cur.level + 1, ul }
        stack.push(cur)
      }
      const li = doc.createElement('li')
      const a = doc.createElement('a')
      a.setAttribute('href', `#${h.id}`)
      a.textContent = h.textContent
      li.appendChild(a)
      cur.ul.appendChild(li)
    }
    nav.appendChild(rootUl)
  }
}

/** Wraps each code block with a visible language label. */
function labelCode(root: HTMLElement, doc: Document) {
  for (const code of Array.from(root.querySelectorAll('pre > code[class^="language-"]'))) {
    const pre = code.parentElement as HTMLElement
    const lang = code.className.slice('language-'.length)
    const lab = doc.createElement('div')
    lab.className = 'cn-code-lang'
    lab.textContent = lang
    pre.before(lab)
  }
}

/** The detached, fully resolved copy used by the PDF and HTML outputs. */
export async function buildExportDom(
  editor: { getHTML(): string },
  meta: ExportMeta,
  opts?: Opts,
): Promise<Built> {
  const src = new DOMParser().parseFromString(editor.getHTML(), 'text/html') // inert: nothing runs
  const doc = document
  const root = doc.createElement('div')
  root.className = 'cn-export'

  const head = doc.createElement('header')
  const h1 = doc.createElement('h1')
  h1.textContent = meta.title || 'Untitled'
  head.appendChild(h1)
  const info: [string, string][] = []
  if (meta.status) info.push(['Status', meta.status])
  if (meta.labels.length) info.push(['Labels', meta.labels.join(', ')])
  if (meta.updatedAt) info.push(['Last updated', meta.updatedAt])
  for (const [k, v] of info) {
    const p = doc.createElement('p')
    p.className = 'cn-meta'
    p.textContent = `${k}: ${v}`
    head.appendChild(p)
  }
  root.appendChild(head)

  const body = doc.createElement('main')
  clean(src.body, body, doc)
  root.appendChild(body)

  // Title heading is the page's h1; content headings get ids after it, so the TOC lists content only.
  resolveToc(body, doc)
  labelCode(body, doc)

  await resolvePageLinks(body, opts?.resolveTitle ?? defaultTitle)

  const imgs = Array.from(body.querySelectorAll('img'))
  const { done, failed, notes } = await embed(
    imgs.map((i) => i.getAttribute('src') ?? ''),
    opts,
  )
  for (const img of imgs) {
    const s = img.getAttribute('src') ?? ''
    const uri = done.get(s)
    if (uri) img.setAttribute('src', uri)
    else if (/^https?:/i.test(s) || failed.has(s)) {
      const alt = img.getAttribute('alt') ?? ''
      const sp = doc.createElement('span')
      sp.className = 'cn-img-missing'
      sp.textContent = `[Image not embedded${alt ? `: ${alt}` : ''}]`
      img.replaceWith(sp)
    }
  }
  if (notes.length) {
    const n = doc.createElement('aside')
    n.className = 'cn-notice'
    n.setAttribute('role', 'note')
    n.textContent = notes.join(' ')
    head.appendChild(n)
  }
  return { root, notes }
}

/** The stored Markdown with each uploaded image embedded as a data URI; a failed image keeps its alt text. */
export async function buildMarkdown(editor: { getMarkdown(): string }, opts?: Opts) {
  const md = editor.getMarkdown()
  const IMG =
    /!\[((?:\\.|[^\]\\])*)\]\((\/api\/w\/[0-9a-f-]{36}\/files\/[0-9a-f-]{36})((?: "(?:\\.|[^"\\])*")?)\)/g
  const { done, failed, notes } = await embed(
    [...md.matchAll(IMG)].map((m) => m[2]),
    opts,
  )
  const text = md.replace(IMG, (all, alt: string, src: string, title: string) => {
    const uri = done.get(src)
    if (uri) return `![${alt}](${uri}${title})`
    return failed.has(src) ? `${alt.replace(/\\(.)/g, '$1')} (image not embedded)` : all
  })
  return { text, notes }
}

// ---------- stylesheet (shared by the HTML file and the print root) ----------

export const EXPORT_CSS = `
.cn-export{font:14px/1.6 system-ui,-apple-system,"Segoe UI",sans-serif;color:#111;max-width:46rem;margin:0 auto;padding:16px;background:#fff}
.cn-export h1{font-size:1.9em;margin:0 0 .3em}.cn-export h2{font-size:1.45em;margin:1.2em 0 .4em}.cn-export h3{font-size:1.2em;margin:1em 0 .3em}
.cn-export h4,.cn-export h5,.cn-export h6{font-size:1em;margin:1em 0 .3em}
.cn-export p{margin:.5em 0}.cn-export .cn-meta{margin:.1em 0;color:#444;font-size:.9em}
.cn-export .cn-notice{margin:.8em 0;padding:6px 10px;border:1px solid #b7791f;background:#fdf4e0}
.cn-export ul{list-style:disc;padding-left:1.5em;margin:.5em 0}.cn-export ol{list-style:decimal;padding-left:1.5em;margin:.5em 0}
.cn-export ul[data-type=taskList]{list-style:none;padding-left:.2em}
.cn-export li[data-type=taskItem]>label{margin-right:.5em}.cn-export li[data-type=taskItem]{display:flex}
.cn-export blockquote{margin:.8em 0;padding-left:12px;border-left:3px solid #bbb;color:#333}
.cn-export a{color:#1a56b0;text-decoration:underline}
.cn-export pre{background:#f4f5f7;border:1px solid #ddd;padding:10px;overflow:auto;white-space:pre-wrap;margin:0 0 .8em}
.cn-export code{font:12.5px ui-monospace,Menlo,Consolas,monospace}
.cn-export .cn-code-lang{margin-top:.8em;padding:2px 8px;background:#e4e6ea;border:1px solid #ddd;border-bottom:0;font:600 11px ui-monospace,Menlo,monospace;text-transform:uppercase}
.cn-export table{border-collapse:collapse;margin:.8em 0;width:100%}.cn-export th,.cn-export td{border:1px solid #bbb;padding:4px 8px;vertical-align:top;text-align:left}
.cn-export th{background:#f1f2f4}.cn-export td p,.cn-export th p{margin:0}
.cn-export details{border:1px solid #ddd;padding:6px 10px;margin:.8em 0}.cn-export summary{font-weight:600}
.cn-export nav[data-toc]{border:1px solid #ddd;padding:6px 12px;margin:.8em 0}.cn-export nav[data-toc] ul{list-style:none;padding-left:1em}
.cn-export img{max-width:100%;height:auto}.cn-export hr{border:0;border-top:1px solid #ccc;margin:1em 0}
.cn-export .cn-img-missing{font-style:italic;color:#555}
@page{size:A4;margin:18mm}
@media print{
.cn-export{max-width:none;padding:0;font-size:11pt}
.cn-export h1,.cn-export h2,.cn-export h3,.cn-export h4,.cn-export h5,.cn-export h6{break-after:avoid}
.cn-export tr,.cn-export img,.cn-export [data-panel],.cn-export pre{break-inside:avoid}
.cn-export a[href^="http"]::after{content:" (" attr(href) ")";font-size:.85em;color:#444}
}`

/** One self-contained file: inline stylesheet, data-URI images, no script. Built through the DOM so nothing is unescaped. */
export function buildHtmlFile(root: HTMLElement, meta: ExportMeta) {
  const d = document.implementation.createHTMLDocument(meta.title || 'Untitled')
  const m = d.createElement('meta')
  m.setAttribute('charset', 'utf-8')
  d.head.prepend(m)
  const v = d.createElement('meta')
  v.name = 'viewport'
  v.content = 'width=device-width, initial-scale=1'
  d.head.appendChild(v)
  const s = d.createElement('style')
  s.textContent = EXPORT_CSS
  d.head.appendChild(s)
  d.body.appendChild(d.importNode(root, true))
  return `<!doctype html>\n${d.documentElement.outerHTML}`
}

export function download(name: string, text: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** Shows the resolved DOM in `#print-root` (globals.css hides everything else when printing) and opens the print dialog. */
export async function printDom(root: HTMLElement) {
  document.getElementById('print-root')?.remove()
  const host = document.createElement('div')
  host.id = 'print-root'
  const st = document.createElement('style')
  st.textContent = EXPORT_CSS
  host.append(st, root)
  document.body.appendChild(host)
  await Promise.all(Array.from(host.querySelectorAll('img')).map((i) => i.decode().catch(() => {})))
  window.addEventListener('afterprint', () => host.remove(), { once: true })
  window.print()
}
