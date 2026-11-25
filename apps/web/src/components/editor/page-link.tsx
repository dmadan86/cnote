'use client'
import { computePosition, flip, offset, shift } from '@floating-ui/dom'
import { PluginKey } from '@tiptap/pm/state'
import { type Editor, Extension, ReactRenderer } from '@tiptap/react'
import Suggestion, { type SuggestionProps } from '@tiptap/suggestion'
import { FileText } from 'lucide-react'
import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react'
import { cmd } from '@/lib/api'
import { noteMine } from '@/lib/docs'
import { useShell } from '../shell'
import { btn, btnPrimary, Field, inputCls, Modal } from '../ui'

// docs-parity-b 5.3: a link to a page is an ordinary link, `[Title](/w/<ws>/n/<id>)`. Both entries (typing `[[` and
// the link dialog) search with search.notes title_only, which already filters by the writer's access, so a restricted
// page is never offered. The title is copied when the link is made.

export type PageHit = { id: string; title: string }
export const pageHref = (ws: string, id: string) => `/w/${ws}/n/${id}`

/** Pages whose title matches; an empty query lists the pages the writer opened last. */
export async function searchPages(ws: string, q: string): Promise<PageHit[]> {
  const s = q.trim()
  if (!s) return (await noteMine(ws, 'recent', 8)).map((p) => ({ id: p.id, title: p.title }))
  return cmd<PageHit[]>(ws, 'search.notes', { q: s, title_only: true, limit: 8 })
}

type ListHandle = { onKeyDown: (e: KeyboardEvent) => boolean }

export function PageList({
  items,
  pick,
  active,
  setActive,
  label,
}: {
  items: PageHit[]
  pick: (p: PageHit) => void
  active: number
  setActive: (i: number) => void
  label: string
}) {
  return (
    <div
      role="listbox"
      aria-label={label}
      className="max-h-72 w-72 overflow-y-auto rounded-lg border border-line bg-canvas p-1 text-fg shadow-xl"
    >
      {items.length === 0 && <p className="px-2 py-1.5 text-[13px] text-muted">No pages found</p>}
      {items.map((p, i) => (
        <button
          key={p.id}
          type="button"
          role="option"
          aria-selected={i === active}
          onMouseEnter={() => setActive(i)}
          onMouseDown={(e) => e.preventDefault()} // keep editor focus
          onClick={() => pick(p)}
          className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] aria-selected:bg-hover"
        >
          <FileText size={14} className="shrink-0 text-muted" />
          <span className="truncate">{p.title || 'Untitled'}</span>
        </button>
      ))}
    </div>
  )
}

const Picker = forwardRef<ListHandle, SuggestionProps<PageHit>>(({ items, command }, ref) => {
  const [active, setActive] = useState(0)
  // biome-ignore lint/correctness/useExhaustiveDependencies: reset the highlight whenever the results change
  useEffect(() => setActive(0), [items])
  useImperativeHandle(ref, () => ({
    onKeyDown: (e) => {
      if (!items.length) return false
      if (e.key === 'ArrowDown') setActive((a) => (a + 1) % items.length)
      else if (e.key === 'ArrowUp') setActive((a) => (a + items.length - 1) % items.length)
      else if (e.key === 'Enter' || e.key === 'Tab') command(items[active])
      else return false
      return true
    },
  }))
  return (
    <PageList items={items} pick={command} active={active} setActive={setActive} label="Link to a page" />
  )
})
Picker.displayName = 'PageLinkPicker'

/** Typing `[[` opens a title search under the caret; choosing a page writes the link. Escape leaves the text as typed. */
export const PageLinkPicker = Extension.create<{ ws: string }>({
  name: 'pageLinkPicker',
  addOptions: () => ({ ws: '' }),
  addProseMirrorPlugins() {
    const ws = this.options.ws
    return [
      Suggestion<PageHit>({
        editor: this.editor,
        char: '[[',
        allowSpaces: true,
        pluginKey: new PluginKey('pageLinkSuggestion'),
        allow: ({ state }) =>
          !state.selection.$from.parent.type.spec.code &&
          !state.selection.$from.marks().some((m) => m.type.name === 'code'),
        items: ({ query }) => searchPages(ws, query).catch(() => []),
        command: ({ editor, range, props }) => {
          editor
            .chain()
            .focus()
            .insertContentAt(range, [
              {
                type: 'text',
                text: props.title || 'Untitled',
                marks: [{ type: 'link', attrs: { href: pageHref(ws, props.id) } }],
              },
              { type: 'text', text: ' ' },
            ])
            .run()
        },
        render: () => {
          let r: ReactRenderer<ListHandle, SuggestionProps<PageHit>> | null = null
          const close = () => {
            r?.element.remove()
            r?.destroy()
            r = null
          }
          const place = (p: SuggestionProps<PageHit>) => {
            const rect = p.clientRect?.()
            const el = r?.element as HTMLElement | undefined
            if (!rect || !el) return
            computePosition({ getBoundingClientRect: () => rect }, el, {
              strategy: 'fixed',
              placement: 'bottom-start',
              middleware: [offset(6), flip(), shift({ padding: 8 })],
            }).then(({ x, y }) =>
              Object.assign(el.style, { position: 'fixed', left: `${x}px`, top: `${y}px`, zIndex: '50' }),
            )
          }
          return {
            onStart: (p) => {
              r = new ReactRenderer(Picker, { props: p, editor: p.editor })
              Object.assign((r.element as HTMLElement).style, {
                position: 'fixed',
                left: '0px',
                top: '0px',
                zIndex: '50',
              })
              document.body.appendChild(r.element)
              requestAnimationFrame(() => place(p))
            },
            onUpdate: (p) => {
              r?.updateProps(p)
              place(p)
            },
            onKeyDown: ({ event }) => {
              if (event.key === 'Escape') {
                close()
                return true
              }
              return r?.ref?.onKeyDown(event) ?? false
            },
            onExit: close,
          }
        },
      }),
    ]
  },
})

const URLISH = /^(?:https?:|mailto:|\/|#)/i

/** The link dialog (toolbar, bubble menu): one box that takes a URL or searches pages. Empty and Remove clear the link. */
export function LinkDialog({ editor }: { editor: Editor }) {
  const { ws } = useShell()
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [hits, setHits] = useState<PageHit[]>([])
  const [active, setActive] = useState(0)
  const [had, setHad] = useState(false)
  const sel = useRef({ from: 0, to: 0 }) // the dialog takes focus, so the selection is kept and put back
  useEffect(() => {
    const dom = editor.view.dom
    const show = () => {
      sel.current = { from: editor.state.selection.from, to: editor.state.selection.to }
      const prev = editor.getAttributes('link').href as string | undefined
      setText(prev ?? '')
      setHad(!!prev)
      setActive(0)
      setOpen(true)
    }
    dom.addEventListener('cn:link-dialog', show)
    return () => dom.removeEventListener('cn:link-dialog', show)
  }, [editor])
  useEffect(() => {
    if (!open || !text.trim() || URLISH.test(text.trim())) return setHits([])
    let live = true
    const t = setTimeout(
      () =>
        searchPages(ws, text).then(
          (h) => live && setHits(h),
          () => live && setHits([]),
        ),
      150,
    )
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [open, text, ws])

  const apply = (href: string, label?: string) => {
    setOpen(false)
    const chain = editor.chain().focus().setTextSelection(sel.current).extendMarkRange('link')
    // The Link extension also rejects javascript: and other unsafe schemes.
    if (!href) return void chain.unsetLink().run()
    if (sel.current.from === sel.current.to && !had)
      chain
        .insertContent({ type: 'text', text: label || href, marks: [{ type: 'link', attrs: { href } }] })
        .run()
    else chain.setLink({ href }).run()
  }
  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    const t = text.trim()
    const h = hits[active]
    if (h && !URLISH.test(t)) apply(pageHref(ws, h.id), h.title)
    else apply(t)
  }
  return (
    <Modal open={open} onClose={() => setOpen(false)} title="Link">
      <form onSubmit={submit} className="space-y-3">
        <Field label="Paste a URL or search for a page">
          {(id) => (
            <input
              // biome-ignore lint/a11y/noAutofocus: the dialog exists only to take this link
              autoFocus
              id={id}
              value={text}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (!hits.length) return
                if (e.key === 'ArrowDown') setActive((a) => (a + 1) % hits.length)
                else if (e.key === 'ArrowUp') setActive((a) => (a + hits.length - 1) % hits.length)
                else return
                e.preventDefault()
              }}
              className={inputCls}
              placeholder="https:// or a page title"
            />
          )}
        </Field>
        {hits.length > 0 && (
          <PageList
            items={hits}
            pick={(p) => apply(pageHref(ws, p.id), p.title)}
            active={active}
            setActive={setActive}
            label="Pages"
          />
        )}
        <div className="flex items-center justify-between gap-3">
          {had ? (
            <button type="button" className={btn} onClick={() => apply('')}>
              Remove link
            </button>
          ) : (
            <span />
          )}
          <button type="submit" className={btnPrimary}>
            Apply
          </button>
        </div>
      </form>
    </Modal>
  )
}
