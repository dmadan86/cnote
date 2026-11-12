'use client'
import { computePosition, flip, offset, shift } from '@floating-ui/dom'
import { PluginKey } from '@tiptap/pm/state'
import { Extension, ReactRenderer } from '@tiptap/react'
import Suggestion, { type SuggestionProps } from '@tiptap/suggestion'
import { forwardRef, useEffect, useImperativeHandle, useState } from 'react'
import { cmd, type Member } from '@/lib/api'
import { Avatar } from '../ui'

type ListHandle = { onKeyDown: (e: KeyboardEvent) => boolean }

const MentionList = forwardRef<ListHandle, SuggestionProps<Member>>(({ items, command }, ref) => {
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
    <div
      role="listbox"
      aria-label="Mention someone"
      className="max-h-72 w-64 overflow-y-auto rounded-lg border border-line bg-canvas p-1 text-fg shadow-xl"
    >
      {items.length === 0 && <p className="px-2 py-1.5 text-[13px] text-muted">No people found</p>}
      {items.map((m, i) => (
        <button
          key={m.user_id}
          type="button"
          role="option"
          aria-selected={i === active}
          onMouseEnter={() => setActive(i)}
          onMouseDown={(e) => e.preventDefault()} // keep editor focus
          onClick={() => command(m)}
          className="flex w-full items-center gap-2.5 rounded px-2 py-1.5 text-left aria-selected:bg-hover"
        >
          <Avatar name={m.name} />
          <span className="min-w-0">
            <span className="block truncate text-[13px] font-medium">{m.name}</span>
            <span className="block truncate text-[12px] text-muted">{m.email}</span>
          </span>
        </button>
      ))}
    </div>
  )
})
MentionList.displayName = 'MentionList'

/** "@" people picker. Inserts "@Name" as a normal link to /w/<ws>/people/<user>, so Markdown stays `[@Name](url)`. */
export const MentionPicker = Extension.create<{ ws: string }>({
  name: 'mentionPicker',
  addOptions: () => ({ ws: '' }),
  addProseMirrorPlugins() {
    const ws = this.options.ws
    let members: Promise<Member[]> | undefined
    return [
      Suggestion<Member>({
        editor: this.editor,
        char: '@',
        allowSpaces: true, // "@Ravi Ku" keeps filtering by full name
        pluginKey: new PluginKey('mentionSuggestion'),
        allow: ({ state }) => !state.selection.$from.parent.type.spec.code,
        items: async ({ query }) => {
          members ??= cmd<Member[]>(ws, 'member.list')
          const q = query.toLowerCase()
          return (await members).filter((m) => `${m.name} ${m.email}`.toLowerCase().includes(q)).slice(0, 8)
        },
        command: ({ editor, range, props }) => {
          editor
            .chain()
            .focus()
            .insertContentAt(range, [
              {
                type: 'text',
                text: `@${props.name}`,
                marks: [{ type: 'link', attrs: { href: `/w/${ws}/people/${props.user_id}` } }],
              },
              { type: 'text', text: ' ' },
            ])
            .run()
        },
        render: () => {
          let r: ReactRenderer<ListHandle, SuggestionProps<Member>> | null = null
          const close = () => {
            r?.element.remove()
            r?.destroy()
            r = null
          }
          const place = (p: SuggestionProps<Member>) => {
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
              r = new ReactRenderer(MentionList, { props: p, editor: p.editor })
              const el = r.element as HTMLElement
              Object.assign(el.style, { position: 'fixed', left: '0px', top: '0px', zIndex: '50' })
              document.body.appendChild(el)
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
