'use client'
import { useRouter } from 'next/navigation'
import { Fragment, type ReactNode, useEffect, useMemo, useRef, useState } from 'react'
import { DocxImportButton } from '@/components/import/docx-button'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd, type Note } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

export type Prompt = { key: string; label: string }
export type GalleryTpl = {
  id: string
  prompts?: Prompt[]
  name: string
  builtin?: boolean
  category?: string
  description?: string
  content_md?: string
}

const CATEGORIES = ['Meetings', 'Operations', 'HR', 'Sales', 'Projects']
const YOURS = 'Your templates'
const catOf = (t: GalleryTpl) => (t.builtin ? (t.category ?? 'Operations') : YOURS)

// ---------- preview: stored Markdown to React elements, never to HTML ----------

const PANEL: Record<string, [string, string]> = {
  INFO: ['Info', 'bg-hover'],
  NOTE: ['Note', 'bg-hover'],
  SUCCESS: ['Success', 'bg-ok-bg'],
  WARNING: ['Warning', 'bg-warn-bg'],
  ERROR: ['Error', 'bg-bad-bg'],
}
const TONE: Record<string, 'ok' | 'warn' | 'bad' | 'neutral'> = { green: 'ok', yellow: 'warn', red: 'bad' }
const INLINE = /(\*\*[^*]+\*\*|<span data-status="(?:grey|blue|green|yellow|red|purple)">[^<]*<\/span>)/

/** Keys for static, never-reordered lists. */
const seq = () => {
  let n = 0
  return () => n++
}

function inline(s: string): ReactNode[] {
  const nk = seq()
  return s.split(INLINE).map((p) => {
    const st = /^<span data-status="(\w+)">([^<]*)<\/span>$/.exec(p)
    if (st) {
      return (
        <Pill key={nk()} tone={TONE[st[1]] ?? 'neutral'}>
          {st[2]}
        </Pill>
      )
    }
    if (p.startsWith('**') && p.endsWith('**') && p.length > 4)
      return <strong key={nk()}>{p.slice(2, -2)}</strong>
    return <Fragment key={nk()}>{p}</Fragment>
  })
}

const cells = (l: string) =>
  l
    .trim()
    .replace(/^\||\|$/g, '')
    .split('|')
    .map((c) => c.trim())

function blocks(md: string, depth = 0): ReactNode[] {
  const L = md.split('\n')
  const out: ReactNode[] = []
  let i = 0
  while (i < L.length) {
    const l = L[i]
    const k = `${depth}-${i}`
    const j0 = i
    if (!l.trim()) {
      i++
    } else if (l.trim() === '[[toc]]') {
      out.push(
        <p key={k} className="rounded border border-dashed border-line px-2 py-1 text-[12px] text-muted">
          Table of contents
        </p>,
      )
      i++
    } else if (/^#{1,3} /.test(l)) {
      const n = l.indexOf(' ')
      out.push(
        <p key={k} className={n === 1 ? 'text-[16px] font-semibold' : 'text-[14px] font-semibold'}>
          {inline(l.slice(n + 1))}
        </p>,
      )
      i++
    } else if (l.startsWith('<details>')) {
      const title = /<summary>(.*)<\/summary>/.exec(l)?.[1] ?? ''
      const body: string[] = []
      for (i++; i < L.length && L[i] !== '</details>'; i++) body.push(L[i])
      i++
      out.push(
        <details key={k} className="rounded border border-line px-2 py-1">
          <summary className="cursor-pointer font-medium">{title}</summary>
          <div className="mt-1 space-y-2">{depth < 3 && blocks(body.join('\n'), depth + 1)}</div>
        </details>,
      )
    } else if (l.startsWith('>')) {
      const q: string[] = []
      for (; i < L.length && L[i].startsWith('>'); i++) q.push(L[i].replace(/^> ?/, ''))
      const kind = /^\[!(\w+)\]$/.exec(q[0])?.[1]
      const [label, bg] = PANEL[kind ?? ''] ?? ['', '']
      out.push(
        <div key={k} className={`rounded border border-line px-2 py-1.5 ${bg}`}>
          {label && <span className="mr-1 text-[11px] font-semibold uppercase text-muted">{label}</span>}
          {depth < 3 && blocks(q.slice(kind ? 1 : 0).join('\n'), depth + 1)}
        </div>,
      )
    } else if (l.startsWith('|')) {
      const rows: string[][] = []
      const hk = seq()
      const rk = seq()
      for (; i < L.length && L[i].startsWith('|'); i++)
        if (!/^\|[\s|:-]+\|$/.test(L[i])) rows.push(cells(L[i]))
      out.push(
        <div key={k} className="overflow-x-auto">
          <table className="w-full border-collapse text-[12px]">
            <thead>
              <tr>
                {rows[0].map((c) => (
                  <th key={hk()} scope="col" className="border border-line bg-hover px-1.5 py-1 text-left">
                    {inline(c)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.slice(1).map((r) => (
                <tr key={rk()}>
                  {r.map((c) => (
                    <td key={rk()} className="border border-line px-1.5 py-1">
                      {inline(c)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      )
    } else if (/^(- |\d+\. )/.test(l)) {
      const ordered = /^\d/.test(l)
      const items: string[] = []
      for (; i < L.length && /^(- |\d+\. )/.test(L[i]); i++) items.push(L[i].replace(/^(- |\d+\. )/, ''))
      const lk = seq()
      const Tag = ordered ? 'ol' : 'ul'
      out.push(
        <Tag key={k} className={`ml-5 space-y-0.5 ${ordered ? 'list-decimal' : 'list-disc'}`}>
          {items.map((t) => (
            <li key={lk()}>{inline(t.replace(/^\[[ x]\] /, (m) => (m === '[ ] ' ? '☐ ' : '☑ ')))}</li>
          ))}
        </Tag>,
      )
    } else {
      const p: string[] = []
      for (; i < L.length && L[i].trim() && !/^(#{1,3} |>|\||- |\d+\. |<details>)/.test(L[i]); i++)
        p.push(L[i])
      out.push(<p key={k}>{inline(p.join(' '))}</p>)
    }
    if (i === j0) i++ // a line no branch consumed must still advance
  }
  return out
}

export function Preview({ md }: { md: string }) {
  return <div className="space-y-2 text-[13px] leading-relaxed">{blocks(md)}</div>
}

// ---------- prompts ----------

/** The non-empty answers, for the prompts of the chosen template only. */
export function answersOf(prompts: Prompt[], answers: Record<string, string>) {
  const out: Record<string, string> = {}
  for (const p of prompts) {
    const v = (answers[p.key] ?? '').trim()
    if (v) out[p.key] = v
  }
  return Object.keys(out).length ? out : undefined
}

/** One text box per `{{ask.name}}` of the template. An empty box leaves the placeholder in the page. */
export function PromptFields({
  prompts,
  answers,
  onChange,
}: {
  prompts: Prompt[]
  answers: Record<string, string>
  onChange: (a: Record<string, string>) => void
}) {
  if (prompts.length === 0) return null
  return (
    <fieldset className="space-y-2 rounded-md border border-line p-2.5">
      <legend className="px-1 text-[12px] font-semibold text-muted">Fill in</legend>
      {prompts.map((p) => (
        <Field key={p.key} label={p.label}>
          {(id) => (
            <input
              id={id}
              className={inputCls}
              maxLength={500}
              value={answers[p.key] ?? ''}
              onChange={(e) => onChange({ ...answers, [p.key]: e.target.value.replace(/[\r\n]+/g, ' ') })}
            />
          )}
        </Field>
      ))}
    </fieldset>
  )
}

// ---------- the gallery ----------

/**
 * Template gallery for pages: categories, search, preview and "Use template".
 * The page lands under `parentId` when given, otherwise in `spaceId` or the space picked in the dialog.
 */
export function TemplateGallery({
  open,
  onClose,
  spaceId,
  parentId,
}: {
  open: boolean
  onClose: () => void
  spaceId?: string
  parentId?: string
}) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const list = useCmd<GalleryTpl[]>(
    ws,
    'template.list',
    { kind: 'page', source_kind: 'none', include_content: true },
    open,
  )
  const [cat, setCat] = useState('All')
  const [q, setQ] = useState('')
  const [sel, setSel] = useState<string>()
  const [space, setSpace] = useState(spaceId ?? '')
  const [title, setTitle] = useState('')
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const cards = useRef<(HTMLButtonElement | null)[]>([])

  const all = list.data ?? []
  const cats = useMemo(() => {
    const present = new Set(all.map(catOf))
    return ['All', ...CATEGORIES, YOURS].filter((c) => c === 'All' || present.has(c))
  }, [all])
  const shown = useMemo(() => {
    const n = q.trim().toLowerCase()
    return all.filter(
      (t) =>
        (cat === 'All' || catOf(t) === cat) &&
        (!n || `${t.name} ${t.description ?? ''} ${catOf(t)}`.toLowerCase().includes(n)),
    )
  }, [all, cat, q])
  const current = shown.find((t) => t.id === sel) ?? shown[0]
  const prompts = current?.prompts ?? []
  const spaceVal = space || spaces[0]?.id || ''

  useEffect(() => {
    if (!open) setErr(undefined)
  }, [open])

  // Arrow keys move through the grid, Home and End jump; the focused card is the selected one.
  function onKey(e: React.KeyboardEvent) {
    const idx = cards.current.indexOf(document.activeElement as HTMLButtonElement)
    if (idx < 0) return
    const top = cards.current[0]?.offsetTop
    const cols = Math.max(1, cards.current.filter((c) => c && c.offsetTop === top).length)
    const step: Record<string, number> = { ArrowRight: 1, ArrowLeft: -1, ArrowDown: cols, ArrowUp: -cols }
    let to = idx
    if (e.key in step) to = Math.min(shown.length - 1, Math.max(0, idx + step[e.key]))
    else if (e.key === 'Home') to = 0
    else if (e.key === 'End') to = shown.length - 1
    else return
    e.preventDefault()
    setSel(shown[to].id)
    cards.current[to]?.focus()
  }

  async function use() {
    if (!current) return
    setBusy(true)
    try {
      const r = await cmd<{ note: Note }>(ws, 'template.render', {
        template_id: current.id,
        ...(parentId ? { parent_id: parentId } : { space_id: spaceVal }),
        title: title.trim() || undefined,
        answers: answersOf(prompts, answers),
      })
      router.push(`/w/${ws}/n/${r.note.id}`)
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Template gallery"
      className="max-w-5xl max-h-[92vh] overflow-y-auto"
    >
      <ErrorText error={list.error} />
      <div className="grid gap-3 md:grid-cols-[150px_minmax(0,1fr)_minmax(0,1.1fr)]">
        <nav aria-label="Template categories" className="flex gap-1 overflow-x-auto md:flex-col">
          {cats.map((c) => (
            <button
              key={c}
              type="button"
              aria-current={cat === c ? 'true' : undefined}
              onClick={() => setCat(c)}
              className={`whitespace-nowrap rounded-md px-2.5 py-1.5 text-left text-[13px] hover:bg-hover ${
                cat === c ? 'bg-hover font-semibold' : ''
              }`}
            >
              {c}
            </button>
          ))}
        </nav>

        <div className="min-w-0 space-y-2">
          <input
            type="search"
            aria-label="Search templates"
            placeholder="Search templates"
            className={inputCls}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <p role="status" className="sr-only">
            {shown.length} {shown.length === 1 ? 'template' : 'templates'}
          </p>
          {shown.length === 0 ? (
            <p className="text-[13px] text-muted">
              {list.data ? 'No template matches.' : 'Loading templates.'}
            </p>
          ) : (
            <div
              role="listbox"
              aria-label="Templates"
              onKeyDown={onKey}
              className="grid max-h-[55vh] gap-2 overflow-y-auto p-0.5 sm:grid-cols-2"
            >
              {shown.map((t, i) => {
                const on = t.id === current?.id
                return (
                  <button
                    key={t.id}
                    ref={(el) => {
                      cards.current[i] = el
                    }}
                    type="button"
                    role="option"
                    aria-selected={on}
                    tabIndex={on ? 0 : -1}
                    onClick={() => setSel(t.id)}
                    onDoubleClick={use}
                    className={`rounded-md border p-2.5 text-left hover:bg-hover ${
                      on ? 'border-accent ring-1 ring-accent' : 'border-line'
                    }`}
                  >
                    <span className="block text-[13px] font-semibold">{t.name}</span>
                    <span className="mt-0.5 block text-[12px] text-muted">
                      {t.description ?? 'Saved from a page'}
                    </span>
                    <span className="mt-1 block text-[11px] text-muted">{catOf(t)}</span>
                  </button>
                )
              })}
            </div>
          )}
        </div>

        <section aria-label="Preview" className="min-w-0 space-y-2">
          {current ? (
            <>
              <p role="status" className="sr-only">
                Previewing {current.name}, {catOf(current)}. {current.description ?? ''}
              </p>
              <section
                // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable preview must be reachable by keyboard
                tabIndex={0}
                aria-label={`Preview of ${current.name}`}
                className="max-h-[40vh] overflow-y-auto rounded-md border border-line p-3"
              >
                <Preview md={current.content_md ?? ''} />
              </section>
              <Field label="Space">
                {(id) => (
                  <select
                    id={id}
                    className={inputCls}
                    value={spaceVal}
                    disabled={!!parentId}
                    onChange={(e) => setSpace(e.target.value)}
                  >
                    {spaces.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
              <Field label="Title (optional)">
                {(id) => (
                  <input
                    id={id}
                    className={inputCls}
                    value={title}
                    onChange={(e) => setTitle(e.target.value)}
                    placeholder="Defaults to the template title"
                  />
                )}
              </Field>
              <PromptFields prompts={prompts} answers={answers} onChange={setAnswers} />
              <ErrorText error={err} />
              <div className="flex items-center justify-between gap-2">
                {spaceVal ? <DocxImportButton spaceId={spaceVal} parentId={parentId} /> : <span />}
                <button type="button" className={btn} onClick={onClose}>
                  Cancel
                </button>
                <button type="button" className={btnPrimary} disabled={busy || !spaceVal} onClick={use}>
                  Use template
                </button>
              </div>
            </>
          ) : (
            <p className="text-[13px] text-muted">Pick a template to preview it.</p>
          )}
        </section>
      </div>
    </Modal>
  )
}
