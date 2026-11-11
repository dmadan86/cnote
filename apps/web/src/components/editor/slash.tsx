'use client'
import { computePosition, flip, offset, shift } from '@floating-ui/dom'
import { type Editor, Extension, ReactRenderer } from '@tiptap/react'
import Suggestion, { type SuggestionProps } from '@tiptap/suggestion'
import {
  Bold,
  Code,
  Heading1,
  Heading2,
  Heading3,
  Italic,
  List,
  ListChecks,
  ListOrdered,
  type LucideIcon,
  Minus,
  Pilcrow,
  Quote,
  SquareCode,
  Strikethrough,
  Table,
} from 'lucide-react'
import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react'

export type SlashItem = {
  title: string
  hint: string
  keywords: string
  icon: LucideIcon
  run: (e: Editor) => void
}

const c = (e: Editor) => e.chain().focus()

export const SLASH_ITEMS: SlashItem[] = [
  {
    title: 'Text',
    hint: 'Plain paragraph',
    keywords: 'paragraph p',
    icon: Pilcrow,
    run: (e) => c(e).setParagraph().run(),
  },
  {
    title: 'Heading 1',
    hint: 'Big section heading',
    keywords: 'h1 title',
    icon: Heading1,
    run: (e) => c(e).toggleHeading({ level: 1 }).run(),
  },
  {
    title: 'Heading 2',
    hint: 'Medium heading',
    keywords: 'h2 subtitle',
    icon: Heading2,
    run: (e) => c(e).toggleHeading({ level: 2 }).run(),
  },
  {
    title: 'Heading 3',
    hint: 'Small heading',
    keywords: 'h3',
    icon: Heading3,
    run: (e) => c(e).toggleHeading({ level: 3 }).run(),
  },
  {
    title: 'Bulleted list',
    hint: 'Simple bullet list',
    keywords: 'ul unordered',
    icon: List,
    run: (e) => c(e).toggleBulletList().run(),
  },
  {
    title: 'Numbered list',
    hint: 'List with numbers',
    keywords: 'ol ordered',
    icon: ListOrdered,
    run: (e) => c(e).toggleOrderedList().run(),
  },
  {
    title: 'To-do list',
    hint: 'Track tasks with checkboxes',
    keywords: 'task checkbox todo',
    icon: ListChecks,
    run: (e) => c(e).toggleTaskList().run(),
  },
  {
    title: 'Table',
    hint: '3 × 3 table with a header row',
    keywords: 'grid rows columns',
    icon: Table,
    run: (e) => c(e).insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run(),
  },
  {
    title: 'Quote',
    hint: 'Capture a quote',
    keywords: 'blockquote',
    icon: Quote,
    run: (e) => c(e).toggleBlockquote().run(),
  },
  {
    title: 'Code block',
    hint: 'Code snippet',
    keywords: 'pre snippet',
    icon: SquareCode,
    run: (e) => c(e).toggleCodeBlock().run(),
  },
  {
    title: 'Divider',
    hint: 'Horizontal line',
    keywords: 'hr rule separator',
    icon: Minus,
    run: (e) => c(e).setHorizontalRule().run(),
  },
  {
    title: 'Bold',
    hint: 'Make text bold (Ctrl/⌘ B)',
    keywords: 'strong b',
    icon: Bold,
    run: (e) => c(e).toggleBold().run(),
  },
  {
    title: 'Italic',
    hint: 'Italicise text (Ctrl/⌘ I)',
    keywords: 'em emphasis i',
    icon: Italic,
    run: (e) => c(e).toggleItalic().run(),
  },
  {
    title: 'Strikethrough',
    hint: 'Cross text out',
    keywords: 'strike s del',
    icon: Strikethrough,
    run: (e) => c(e).toggleStrike().run(),
  },
  {
    title: 'Inline code',
    hint: 'Monospace text',
    keywords: 'code mono',
    icon: Code,
    run: (e) => c(e).toggleCode().run(),
  },
]

export const filterSlash = (q: string) => {
  const s = q.toLowerCase().trim()
  return SLASH_ITEMS.filter((i) => `${i.title} ${i.keywords}`.toLowerCase().includes(s))
}

type ListHandle = { onKeyDown: (e: KeyboardEvent) => boolean }

const SlashList = forwardRef<ListHandle, SuggestionProps<SlashItem>>(({ items, command }, ref) => {
  const [active, setActive] = useState(0)
  const list = useRef<HTMLDivElement>(null)
  // biome-ignore lint/correctness/useExhaustiveDependencies: reset the highlight whenever the results change
  useEffect(() => setActive(0), [items])
  // biome-ignore lint/correctness/useExhaustiveDependencies: scroll when the highlight moves
  useEffect(() => {
    list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [active])
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
    <div
      ref={list}
      role="listbox"
      aria-label="Insert block"
      className="max-h-80 w-64 overflow-y-auto rounded-lg border border-line bg-bg p-1 text-fg shadow-xl"
    >
      {items.length === 0 && <p className="px-2 py-1.5 text-[13px] text-muted">No results</p>}
      {items.map((it, i) => (
        <button
          key={it.title}
          type="button"
          role="option"
          aria-selected={i === active}
          onMouseEnter={() => setActive(i)}
          onMouseDown={(e) => e.preventDefault()} // keep editor focus
          onClick={() => command(it)}
          className="flex w-full items-center gap-2.5 rounded px-2 py-1.5 text-left aria-selected:bg-hover"
        >
          <span className="grid size-8 shrink-0 place-items-center rounded border border-line bg-bg">
            <it.icon size={15} />
          </span>
          <span className="min-w-0">
            <span className="block text-[13px] font-medium">{it.title}</span>
            <span className="block truncate text-[12px] text-muted">{it.hint}</span>
          </span>
        </button>
      ))}
    </div>
  )
})
SlashList.displayName = 'SlashList'

/** Notion-style "/" menu: type / then filter, arrows to move, Enter to insert. */
export const SlashCommand = Extension.create({
  name: 'slashCommand',
  addProseMirrorPlugins() {
    return [
      Suggestion<SlashItem>({
        editor: this.editor,
        char: '/',
        // Not inside code: "/" is literal there.
        allow: ({ state }) => !state.selection.$from.parent.type.spec.code,
        items: ({ query }) => filterSlash(query),
        command: ({ editor, range, props }) => {
          editor.chain().focus().deleteRange(range).run()
          props.run(editor)
        },
        render: () => {
          let r: ReactRenderer<ListHandle, SuggestionProps<SlashItem>> | null = null
          const close = () => {
            r?.element.remove()
            r?.destroy()
            r = null
          }
          const place = (p: SuggestionProps<SlashItem>) => {
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
              r = new ReactRenderer(SlashList, { props: p, editor: p.editor })
              document.body.appendChild(r.element)
              place(p)
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
