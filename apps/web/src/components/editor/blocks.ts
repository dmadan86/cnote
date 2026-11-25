import { CodeBlock } from '@tiptap/extension-code-block'
import { Plugin } from '@tiptap/pm/state'
import { type Editor, Extension, Mark, mergeAttributes, Node } from '@tiptap/react'
import { DOC_SCHEMA } from '../../lib/doc-schema'

// docs-parity 5.1: the A1 blocks and the Markdown forms they are stored as. The Markdown parser of the
// gateway (apps/collab/src/md.ts) implements the same forms; apps/collab/test/fixtures/blocks.json holds both to them.

export const STATUS_COLORS = ['grey', 'blue', 'green', 'yellow', 'red', 'purple'] as const
export const PANEL_KINDS = ['info', 'note', 'success', 'warning', 'error'] as const
export type PanelKind = (typeof PANEL_KINDS)[number]
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/
const isStatus = (c: unknown): c is string => STATUS_COLORS.includes(c as never)
const isPanel = (k: unknown): k is PanelKind => PANEL_KINDS.includes(k as never)

// ---------- the rollout fence (7.3) ----------

let gatewayMin = 0
/** The gateway's minimum schema. Until it is known to be at least DOC_SCHEMA the new blocks cannot be inserted:
 * a tab that predates them would delete them from the shared document. note-editor.tsx sets it from the gateway's GET /min-schema. */
export const setGatewayMinSchema = (n: number) => {
  gatewayMin = n
}
export const blocksEnabled = () => gatewayMin >= DOC_SCHEMA

// ---------- shared helpers ----------

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const e = document.createElement(tag)
  if (cls) e.className = cls
  if (text !== undefined) e.textContent = text
  return e
}
const unesc = (s: string) =>
  s
    .replaceAll('&lt;', '<')
    .replaceAll('&gt;', '>')
    .replaceAll('&quot;', '"')
    .replaceAll('&#39;', "'")
    .replaceAll('&amp;', '&')
export const escHtml = (s: string) =>
  s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;')

type Tok = { type: string; raw: string; [k: string]: unknown }
const SUMMARY = /^<details>\s*<summary>(.*)<\/summary>$/i
const fenceRun = (t: string) => {
  let n = 0
  while (t[n] === '`') n++
  return n
}

/** First source index that starts one of the block forms at the start of a line, for the paragraph splitter. */
const blockStart = (src: string) => {
  const m = /(?:^|\n)(?:>[ ]?\[!|<details>|\[\[toc\]\][ \t]*(?:\n|$))/i.exec(src)
  return m ? m.index + (m[0][0] === '\n' ? 1 : 0) : -1
}

/** Source text of an inline element `<tag attr="value">inner</tag>` at the start of src, nested ones counted. */
function inlineElement(src: string, tag: string, attr: string, ok: (v: string) => boolean) {
  const open = new RegExp(`^<${tag} ${attr}="([^"]*)">`).exec(src)
  if (!open || !ok(open[1])) return null
  const l = src.toLowerCase()
  let depth = 1
  for (let i = open[0].length; i < l.length; i++) {
    if (l[i] !== '<') continue
    if (l.startsWith(`</${tag}>`, i) && --depth === 0)
      return { value: open[1], inner: src.slice(open[0].length, i), raw: src.slice(0, i + tag.length + 3) }
    if (l.startsWith(`<${tag}`, i) && /[\s>]/.test(l[i + tag.length + 1] ?? '')) depth++
  }
  return null
}

// ---------- status and date: marks (5.1) ----------

export const Status = Mark.create({
  name: 'status',
  priority: 1100, // outside link, bold and the rest, as the stored form wraps them
  inclusive: false,
  addAttributes() {
    return { color: { default: 'grey', parseHTML: (e) => e.getAttribute('data-status') ?? 'grey' } }
  },
  parseHTML() {
    return [
      { tag: 'span[data-status]', getAttrs: (e) => (isStatus(e.getAttribute('data-status')) ? null : false) },
    ]
  },
  renderHTML({ mark }) {
    return [
      'span',
      { class: 'cn-status', 'data-status': isStatus(mark.attrs.color) ? mark.attrs.color : 'grey' },
      0,
    ]
  },
  markdownTokenizer: {
    name: 'status',
    level: 'inline',
    start: (src: string) => src.indexOf('<span data-status='),
    tokenize: (src, _t, lexer) => {
      const m = inlineElement(src, 'span', 'data-status', isStatus)
      return m
        ? { type: 'status', raw: m.raw, color: m.value, tokens: lexer.inlineTokens(m.inner) }
        : undefined
    },
  },
  parseMarkdown: (token, h) =>
    h.applyMark('status', h.parseInline(token.tokens ?? []), { color: token.color }),
  renderMarkdown: (node, h) =>
    `<span data-status="${isStatus(node.attrs?.color) ? node.attrs?.color : 'grey'}">${h.renderChildren(node)}</span>`,
})

export const DateMark = Mark.create({
  name: 'date',
  priority: 1090,
  inclusive: false,
  addAttributes() {
    return { datetime: { default: '', parseHTML: (e) => e.getAttribute('datetime') ?? '' } }
  },
  parseHTML() {
    return [
      {
        tag: 'time[datetime]',
        getAttrs: (e) => (DATE_RE.test(e.getAttribute('datetime') ?? '') ? null : false),
      },
    ]
  },
  renderHTML({ mark }) {
    return ['time', { class: 'cn-date', datetime: String(mark.attrs.datetime) }, 0]
  },
  markdownTokenizer: {
    name: 'date',
    level: 'inline',
    start: (src: string) => src.indexOf('<time datetime='),
    tokenize: (src, _t, lexer) => {
      const m = inlineElement(src, 'time', 'datetime', (v) => DATE_RE.test(v))
      return m
        ? { type: 'date', raw: m.raw, datetime: m.value, tokens: lexer.inlineTokens(m.inner) }
        : undefined
    },
  },
  parseMarkdown: (token, h) =>
    h.applyMark('date', h.parseInline(token.tokens ?? []), { datetime: token.datetime }),
  renderMarkdown: (node, h) =>
    DATE_RE.test(String(node.attrs?.datetime))
      ? `<time datetime="${node.attrs?.datetime}">${h.renderChildren(node)}</time>`
      : h.renderChildren(node),
})

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
/** "2026-12-01" -> "1 Dec 2026": fixed format, so the stored text does not depend on anyone's locale. */
export const formatDate = (iso: string) => {
  const [y, m, d] = iso.split('-').map(Number)
  return `${d} ${MONTHS[m - 1]} ${y}`
}
export const todayIso = () => {
  const n = new Date()
  return `${n.getFullYear()}-${String(n.getMonth() + 1).padStart(2, '0')}-${String(n.getDate()).padStart(2, '0')}`
}

// ---------- panel ----------

const PANEL_ICON: Record<PanelKind, string> = { info: 'i', note: '✎', success: '✓', warning: '!', error: '×' }
export const PANEL_LABEL: Record<PanelKind, string> = {
  info: 'Info',
  note: 'Note',
  success: 'Success',
  warning: 'Warning',
  error: 'Error',
}

export const Panel = Node.create({
  name: 'panel',
  group: 'block',
  content: 'block+',
  defining: true,
  addAttributes() {
    return { kind: { default: 'info', parseHTML: (e) => e.getAttribute('data-panel') ?? 'info' } }
  },
  parseHTML() {
    return [
      { tag: 'div[data-panel]', getAttrs: (e) => (isPanel(e.getAttribute('data-panel')) ? null : false) },
    ]
  },
  renderHTML({ node, HTMLAttributes }) {
    return ['div', mergeAttributes(HTMLAttributes, { class: 'cn-panel', 'data-panel': node.attrs.kind }), 0]
  },
  markdownTokenizer: {
    name: 'panel',
    level: 'block',
    start: blockStart,
    tokenize: (src, _t, lexer) => {
      if (!src.startsWith('>')) return undefined
      const taken: string[] = []
      for (const l of src.split('\n')) {
        if (!l.startsWith('>')) break
        taken.push(l)
      }
      const inner = taken.map((l) => l.slice(1).replace(/^ /, ''))
      const m = /^\[!(info|note|success|warning|error)\]\s*$/i.exec(inner[0])
      if (!m) return undefined
      let raw = taken.join('\n')
      if (src[raw.length] === '\n') raw += '\n'
      return {
        type: 'panel',
        raw,
        kind: m[1].toLowerCase(),
        tokens: lexer.blockTokens(inner.slice(1).join('\n')),
      }
    },
  },
  parseMarkdown: (token, h) => {
    const kids = h.parseChildren(token.tokens ?? [])
    return h.createNode('panel', { kind: token.kind }, kids.length ? kids : [{ type: 'paragraph' }])
  },
  renderMarkdown: (node, h) => {
    const k = isPanel(node.attrs?.kind) ? node.attrs?.kind : 'info'
    const body = node.content ? h.renderChildren(node.content, '\n\n') : ''
    return [`[!${k.toUpperCase()}]`, ...body.split('\n')].map((l) => (l === '' ? '>' : `> ${l}`)).join('\n')
  },
  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = el('div', 'cn-panel')
      dom.setAttribute('role', 'note')
      const head = el('div', 'cn-panel-head')
      head.contentEditable = 'false'
      const icon = el('span', 'cn-panel-icon')
      icon.setAttribute('aria-hidden', 'true')
      head.append(icon)
      let select: HTMLSelectElement | null = null
      if (editor.isEditable) {
        select = el('select', 'cn-panel-kind')
        select.setAttribute('aria-label', 'Panel type')
        for (const k of PANEL_KINDS) select.append(new Option(PANEL_LABEL[k], k))
        select.addEventListener('change', () => {
          const pos = getPos()
          if (pos !== undefined)
            editor.view.dispatch(
              editor.state.tr.setNodeAttribute(pos, 'kind', (select as HTMLSelectElement).value),
            )
        })
        head.append(select)
      }
      const body = el('div', 'cn-panel-body')
      dom.append(head, body)
      const paint = (kind: unknown) => {
        const k = isPanel(kind) ? kind : 'info'
        dom.dataset.panel = k
        dom.setAttribute('aria-label', `${PANEL_LABEL[k]} panel`)
        icon.textContent = PANEL_ICON[k]
        if (select) select.value = k
      }
      paint(node.attrs.kind)
      return {
        dom,
        contentDOM: body,
        update: (n) => {
          if (n.type !== node.type) return false
          paint(n.attrs.kind)
          return true
        },
        stopEvent: (e) => head.contains(e.target as globalThis.Node),
        ignoreMutation: (m) => (m.type === 'attributes' && m.target === dom) || head.contains(m.target),
      }
    }
  },
})

// ---------- expand ----------

/** Index of the `</details>` line that closes the one opened on line 0 (nested ones and code fences counted). */
function expandEnd(lines: string[]): number {
  let depth = 1
  let fence = 0
  for (let j = 1; j < lines.length; j++) {
    const t = lines[j].trim()
    const f = fenceRun(t)
    if (fence) {
      if (f >= fence && f === t.length) fence = 0
    } else if (f >= 3) fence = f
    else if (SUMMARY.test(t)) depth++
    else if (t.toLowerCase() === '</details>' && --depth === 0) return j
  }
  return -1
}

export const Expand = Node.create({
  name: 'expand',
  group: 'block',
  content: 'block+',
  defining: true,
  addAttributes() {
    return { title: { default: '', parseHTML: (e) => e.getAttribute('data-title') ?? '' } }
  },
  parseHTML() {
    return [{ tag: 'div[data-expand]' }]
  },
  renderHTML({ node }) {
    return ['div', { class: 'cn-expand', 'data-expand': '', 'data-title': String(node.attrs.title) }, 0]
  },
  markdownTokenizer: {
    name: 'expand',
    level: 'block',
    start: blockStart,
    tokenize: (src, _t, lexer) => {
      const lines = src.split('\n')
      const m = SUMMARY.exec(lines[0].trim())
      if (!m) return undefined
      const end = expandEnd(lines)
      if (end < 0) return undefined
      let raw = lines.slice(0, end + 1).join('\n')
      if (src[raw.length] === '\n') raw += '\n'
      return {
        type: 'expand',
        raw,
        title: unesc(m[1]),
        tokens: lexer.blockTokens(lines.slice(1, end).join('\n')),
      }
    },
  },
  parseMarkdown: (token, h) => {
    const kids = h.parseChildren(token.tokens ?? [])
    return h.createNode('expand', { title: token.title }, kids.length ? kids : [{ type: 'paragraph' }])
  },
  renderMarkdown: (node, h) => {
    const title = escHtml(
      String(node.attrs?.title ?? '')
        .replace(/\s+/g, ' ')
        .trim(),
    )
    const body = node.content ? h.renderChildren(node.content, '\n\n') : ''
    return `<details><summary>${title}</summary>\n\n${body === '' ? '' : `${body}\n\n`}</details>`
  },
  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = el('div', 'cn-expand')
      const head = el('div', 'cn-expand-head')
      head.contentEditable = 'false'
      const toggle = el('button', 'cn-expand-toggle')
      toggle.type = 'button'
      toggle.setAttribute('aria-label', 'Expand or collapse')
      const title = el('input', 'cn-expand-title')
      title.type = 'text'
      title.placeholder = 'Expand title'
      title.setAttribute('aria-label', 'Expand title')
      title.readOnly = !editor.isEditable
      head.append(toggle, title)
      const body = el('div', 'cn-expand-body')
      dom.append(head, body)
      const open = (on: boolean) => {
        dom.dataset.open = String(on)
        toggle.setAttribute('aria-expanded', String(on))
        toggle.textContent = on ? '▾' : '▸'
        body.hidden = !on
      }
      open(true)
      toggle.addEventListener('click', () => open(dom.dataset.open !== 'true'))
      title.addEventListener('input', () => {
        const pos = getPos()
        if (pos !== undefined)
          editor.view.dispatch(
            editor.state.tr.setNodeAttribute(pos, 'title', title.value.replace(/\s+/g, ' ')),
          )
      })
      title.value = String(node.attrs.title)
      return {
        dom,
        contentDOM: body,
        update: (n) => {
          if (n.type !== node.type) return false
          if (title.value !== n.attrs.title) title.value = String(n.attrs.title)
          return true
        },
        stopEvent: (e) => head.contains(e.target as globalThis.Node),
        ignoreMutation: (m) =>
          (m.type === 'attributes' && m.target === dom) || head.contains(m.target) || m.target === body,
      }
    }
  },
})

// ---------- table of contents ----------

export const Toc = Node.create({
  name: 'toc',
  group: 'block',
  atom: true,
  selectable: true,
  parseHTML() {
    return [{ tag: 'nav[data-toc]' }]
  },
  renderHTML() {
    return ['nav', { class: 'cn-toc', 'data-toc': '' }]
  },
  markdownTokenizer: {
    name: 'toc',
    level: 'block',
    start: blockStart,
    tokenize: (src) => {
      const m = /^\[\[toc\]\][ \t]*(?:\n|$)/.exec(src)
      return m ? ({ type: 'toc', raw: m[0] } as Tok) : undefined
    },
  },
  parseMarkdown: (_token, h) => h.createNode('toc'),
  renderMarkdown: () => '[[toc]]',
  addNodeView() {
    return ({ editor }) => {
      const dom = el('nav', 'cn-toc')
      dom.contentEditable = 'false'
      dom.setAttribute('aria-label', 'Table of contents')
      const paint = () => {
        const items: { level: number; text: string; pos: number }[] = []
        editor.state.doc.descendants((n, pos) => {
          if (n.type.name === 'heading' && n.textContent.trim())
            items.push({ level: Number(n.attrs.level) || 1, text: n.textContent, pos })
        })
        dom.replaceChildren()
        if (!items.length)
          return dom.append(el('p', 'cn-toc-empty', 'Add headings to build the table of contents'))
        const ul = el('ul')
        for (const it of items) {
          const li = el('li')
          li.style.paddingLeft = `${(it.level - 1) * 14}px`
          const b = el('button', 'cn-toc-link', it.text)
          b.type = 'button'
          b.addEventListener('click', () => {
            const at = editor.view.domAtPos(it.pos + 1).node
            ;(at instanceof HTMLElement ? at : at.parentElement)?.scrollIntoView({
              block: 'start',
              behavior: 'smooth',
            })
          })
          li.append(b)
          ul.append(li)
        }
        dom.append(ul)
      }
      paint()
      editor.on('update', paint)
      return {
        dom,
        update: (n) => n.type.name === 'toc',
        stopEvent: () => true,
        ignoreMutation: () => true,
        destroy: () => editor.off('update', paint),
      }
    }
  },
})

// ---------- code language picker ----------

export const LANGUAGES = [
  'bash', 'c', 'cpp', 'csharp', 'css', 'diff', 'go', 'graphql', 'html', 'java', 'javascript', 'json', 'kotlin',
  'markdown', 'php', 'python', 'ruby', 'rust', 'sql', 'swift', 'typescript', 'xml', 'yaml',
] as const // biome-ignore format: a flat list

/** Code block with a language picker; the language is the block's existing `language` attribute. A fence is one
 * backtick longer than any run inside the code, so code that holds ``` survives (7.4). */
export const CodeBlockLang = CodeBlock.extend({
  renderMarkdown(node, h) {
    const lang = node.attrs?.language || ''
    const code = node.content ? h.renderChildren(node.content) : ''
    const fence = '`'.repeat(Math.max(3, ...(code.match(/`+/g) ?? []).map((r) => r.length + 1)))
    return `${fence}${lang}\n${code}\n${fence}`
  },
  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = el('div', 'cn-code')
      const select = el('select', 'cn-code-lang')
      select.contentEditable = 'false'
      select.disabled = !editor.isEditable
      select.setAttribute('aria-label', 'Code language')
      const fill = (lang: string) => {
        select.replaceChildren(new Option('Plain text', ''))
        for (const l of LANGUAGES) select.append(new Option(l, l))
        if (lang && !LANGUAGES.includes(lang as never)) select.append(new Option(lang, lang))
        select.value = lang
      }
      fill(node.attrs.language ?? '')
      select.addEventListener('change', () => {
        const pos = getPos()
        if (pos !== undefined)
          editor.view.dispatch(editor.state.tr.setNodeAttribute(pos, 'language', select.value || null))
      })
      const pre = el('pre')
      const code = el('code')
      pre.append(code)
      dom.append(select, pre)
      return {
        dom,
        contentDOM: code,
        update: (n) => {
          if (n.type !== node.type) return false
          if ((n.attrs.language ?? '') !== select.value) fill(n.attrs.language ?? '')
          return true
        },
        stopEvent: (e) => e.target === select,
        ignoreMutation: (m) => select.contains(m.target),
      }
    }
  },
})

// ---------- image upload: paste, drop and the picker ----------

const IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']
const MAX_BYTES = 10 << 20

function toast(msg: string) {
  const t = el('div', 'cn-toast', msg)
  t.setAttribute('role', 'alert')
  document.body.append(t)
  setTimeout(() => t.remove(), 6000)
}

/** Uploads through the existing endpoint contract; the stored URL is the same-origin `/api/w/<ws>/files/<id>`. */
export async function uploadImage(
  ws: string,
  noteId: string,
  f: File,
): Promise<{ src: string; alt: string }> {
  if (!IMAGE_TYPES.includes(f.type)) throw new Error('Only PNG, JPEG, GIF and WebP images can be uploaded.')
  if (f.size > MAX_BYTES) throw new Error('That image is larger than 10 MB.')
  const res = await fetch(
    `/api/w/${ws}/files?note_id=${noteId}&filename=${encodeURIComponent(f.name || 'image')}`,
    {
      method: 'POST',
      body: f,
      credentials: 'same-origin',
    },
  )
  const j = await res.json().catch(() => null)
  if (!res.ok || typeof j?.id !== 'string')
    throw new Error(j?.error?.message ?? 'Could not upload the image.')
  return { src: `/api/w/${ws}/files/${j.id}`, alt: f.name.replace(/\.[^.]+$/, '') }
}

export const ImageUpload = Extension.create<
  { ws: string; noteId: string },
  { insert: (files: File[], pos?: number) => void }
>({
  name: 'imageUpload',
  addOptions: () => ({ ws: '', noteId: '' }),
  addStorage() {
    return { insert: () => {} }
  },
  onCreate() {
    this.storage.insert = (files, pos) => {
      const { ws, noteId } = this.options
      for (const f of files)
        uploadImage(ws, noteId, f).then(
          (img) => {
            if (this.editor.isDestroyed) return
            const at = pos ?? this.editor.state.selection.to
            this.editor.chain().focus().insertContentAt(at, { type: 'image', attrs: img }).run()
          },
          (e: Error) => toast(e.message),
        )
    }
  },
  addProseMirrorPlugins() {
    const images = (l?: FileList | null) => [...(l ?? [])].filter((f) => f.type.startsWith('image/'))
    return [
      new Plugin({
        props: {
          handlePaste: (view, e) => {
            const files = images(e.clipboardData?.files)
            if (!view.editable || !files.length) return false
            e.preventDefault()
            this.storage.insert(files)
            return true
          },
          handleDrop: (view, e) => {
            const files = images(e.dataTransfer?.files)
            if (!view.editable || !files.length) return false
            e.preventDefault()
            this.storage.insert(files, view.posAtCoords({ left: e.clientX, top: e.clientY })?.pos)
            return true
          },
        },
      }),
    ]
  },
})

/** Opens the file picker; used by the slash menu and the image dialog. */
export function pickImages(editor: Editor) {
  const input = el('input')
  input.type = 'file'
  input.accept = IMAGE_TYPES.join(',')
  input.multiple = true
  input.addEventListener('change', () =>
    (editor.storage as unknown as { imageUpload: { insert: (f: File[]) => void } }).imageUpload.insert([
      ...(input.files ?? []),
    ]),
  )
  input.click()
}

/** Everything the A1 blocks need besides the upload plugin (which needs the workspace and page). Pair it with
 * `StarterKit.configure({ codeBlock: false })`. */
export const blockExtensions = [Panel, Expand, Toc, Status, DateMark, CodeBlockLang]
