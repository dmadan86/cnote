'use client'
import { type Editor, useEditorState } from '@tiptap/react'
import {
  AlignCenter,
  AlignJustify,
  AlignLeft,
  AlignRight,
  AtSign,
  Baseline,
  Bold,
  ChevronDown,
  Code,
  ImageIcon,
  Italic,
  Link2,
  List,
  ListChecks,
  ListOrdered,
  Minus,
  Redo2,
  Smile,
  Strikethrough,
  Table,
  Underline,
  Undo2,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { promptLink, Tool } from './menus'

type Style = { label: string; key: string; size: string; keys: string; run: (e: Editor) => void }
const H = (n: 1 | 2 | 3 | 4 | 5 | 6, size: string): Style => ({
  label: `Heading ${n}`,
  key: `h${n}`,
  size,
  keys: `Alt+${n}`,
  run: (e) => e.chain().focus().setHeading({ level: n }).run(),
})
// Shortcuts are Tiptap's defaults (extension-paragraph/heading/blockquote/code-block).
const STYLES: Style[] = [
  {
    label: 'Normal text',
    key: 'p',
    size: 'text-[14px]',
    keys: 'Alt+0',
    run: (e) => e.chain().focus().setParagraph().run(),
  },
  H(1, 'text-[22px] font-bold'),
  H(2, 'text-[19px] font-bold'),
  H(3, 'text-[17px] font-semibold'),
  H(4, 'text-[15px] font-semibold'),
  H(5, 'text-[14px] font-semibold'),
  H(6, 'text-[13px] font-semibold'),
  {
    label: 'Quote',
    key: 'quote',
    size: 'text-[14px] italic',
    keys: 'Shift+B',
    run: (e) => e.chain().focus().setBlockquote().run(),
  },
  {
    label: 'Code block',
    key: 'code',
    size: 'font-mono text-[13px]',
    keys: 'Alt+C',
    run: (e) => e.chain().focus().setCodeBlock().run(),
  },
]

const isMac = () => typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
const hint = (k: string) =>
  (isMac() ? '⌘' : 'Ctrl+') + (isMac() ? k.replace('Alt+', '⌥').replace('Shift+', '⇧') : k)

function currentKey(e: Editor) {
  if (e.isActive('codeBlock')) return 'code'
  if (e.isActive('blockquote')) return 'quote'
  for (let n = 1; n <= 6; n++) if (e.isActive('heading', { level: n })) return `h${n}`
  return 'p'
}

function StyleMenu({ editor, active }: { editor: Editor; active: string }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const items = useRef<(HTMLButtonElement | null)[]>([])
  const cur = STYLES.find((s) => s.key === active) ?? STYLES[0]
  const idx = STYLES.indexOf(cur)

  // biome-ignore lint/correctness/useExhaustiveDependencies: focus the active item only when the menu opens
  useEffect(() => {
    if (!open) return
    items.current[idx]?.focus()
    const away = (ev: MouseEvent) => !root.current?.contains(ev.target as Node) && setOpen(false)
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [open])

  const pick = (s: Style) => {
    setOpen(false)
    s.run(editor)
  }
  const onKey = (ev: React.KeyboardEvent) => {
    const at = items.current.indexOf(document.activeElement as HTMLButtonElement)
    const go = (i: number) => {
      ev.preventDefault()
      items.current[(i + STYLES.length) % STYLES.length]?.focus()
    }
    if (ev.key === 'ArrowDown') go(at + 1)
    else if (ev.key === 'ArrowUp') go(at - 1)
    else if (ev.key === 'Home') go(0)
    else if (ev.key === 'End') go(STYLES.length - 1)
    else if (ev.key === 'Escape') {
      ev.preventDefault()
      setOpen(false)
      editor.commands.focus()
    } else if (ev.key === 'Tab') setOpen(false)
  }

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: delegates keys from the focused button and menu items
    <div ref={root} className="relative" onKeyDown={onKey}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Text style: ${cur.label}`}
        title="Text style"
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => setOpen((o) => !o)}
        className="flex h-7 w-32 items-center justify-between gap-1 rounded px-2 text-[13px] hover:bg-hover"
      >
        <span className="truncate">{cur.label}</span>
        <ChevronDown size={14} className="shrink-0 text-muted" />
      </button>
      {open && (
        <div
          role="menu"
          aria-label="Text style"
          className="absolute left-0 top-full z-30 mt-1 w-64 rounded-lg border border-line bg-canvas p-1 text-fg shadow-xl"
        >
          {STYLES.map((s, i) => (
            <button
              key={s.key}
              ref={(el) => {
                items.current[i] = el
              }}
              type="button"
              role="menuitemradio"
              aria-checked={s.key === active}
              tabIndex={-1}
              onClick={() => pick(s)}
              className={`flex w-full items-center justify-between rounded px-2 py-1 text-left hover:bg-hover focus:bg-hover focus:outline-none ${s.key === active ? 'text-accent' : ''}`}
            >
              <span className={s.size}>{s.label}</span>
              <span className="text-[11px] text-muted">{hint(s.keys)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

const COLORS: [string, string][] = [
  ['Red', '#dc2626'],
  ['Orange', '#ea580c'],
  ['Yellow', '#ca8a04'],
  ['Green', '#16a34a'],
  ['Blue', '#2563eb'],
  ['Purple', '#9333ea'],
  ['Pink', '#db2777'],
  ['Gray', '#6b7280'],
]
const ALIGNS = [
  ['left', 'Align left', AlignLeft],
  ['center', 'Align center', AlignCenter],
  ['right', 'Align right', AlignRight],
  ['justify', 'Align justify', AlignJustify],
] as const

/** Toolbar button that opens a small panel; Escape or an outside click closes it, Tab walks the options. */
function Pop({
  editor,
  label,
  trigger,
  children,
}: {
  editor: Editor
  label: string
  trigger: React.ReactNode
  children: (close: () => void) => React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    root.current?.querySelector<HTMLElement>('[role=dialog] button')?.focus()
    const away = (ev: MouseEvent) => !root.current?.contains(ev.target as Node) && setOpen(false)
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [open])
  const close = () => {
    setOpen(false)
    editor.commands.focus()
  }
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: Escape from any focused option
    <div
      ref={root}
      className="relative"
      onKeyDown={(e) => {
        if (e.key === 'Escape' && open) close()
      }}
    >
      <Tool label={label} on={open} run={() => setOpen((o) => !o)} popup>
        {trigger}
      </Tool>
      {open && (
        <div
          role="dialog"
          aria-label={label}
          className="absolute left-0 top-full z-30 mt-1 flex gap-0.5 rounded-lg border border-line bg-canvas p-1 shadow-xl"
        >
          {children(close)}
        </div>
      )}
    </div>
  )
}

const opt =
  'grid size-7 shrink-0 place-items-center rounded hover:bg-hover focus:bg-hover focus:outline-none aria-pressed:text-accent'

const Sep = () => <span aria-hidden className="mx-1 h-4 w-px bg-line" />

/** Confluence-style fixed formatting toolbar; the bubble menu in menus.tsx stays for selections. */
export function FixedToolbar({ editor }: { editor: Editor }) {
  const s = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      style: currentKey(e),
      bold: e.isActive('bold'),
      underline: e.isActive('underline'),
      italic: e.isActive('italic'),
      strike: e.isActive('strike'),
      code: e.isActive('code'),
      bullet: e.isActive('bulletList'),
      ordered: e.isActive('orderedList'),
      task: e.isActive('taskList'),
      link: e.isActive('link'),
      color: (e.getAttributes('textStyle').color as string | undefined) ?? '',
      align: ALIGNS.find(([a]) => e.isActive({ textAlign: a }))?.[0] ?? 'left',
      undo: e.can().undo(),
      redo: e.can().redo(),
    }),
  })
  const c = () => editor.chain().focus()
  const sz = 15
  const emit = (ev: string) => {
    editor.commands.focus()
    editor.view.dom.dispatchEvent(new Event(ev))
  }
  return (
    <div
      role="toolbar"
      aria-label="Editor toolbar"
      className="sticky top-0 z-20 mb-3 flex flex-wrap items-center gap-0.5 border-b border-line bg-canvas py-1 text-fg"
    >
      <StyleMenu editor={editor} active={s.style} />
      <Sep />
      <Tool label="Bold" on={s.bold} run={() => c().toggleBold().run()}>
        <Bold size={sz} />
      </Tool>
      <Tool label="Underline" on={s.underline} run={() => c().toggleUnderline().run()}>
        <Underline size={sz} />
      </Tool>
      <Tool label="Italic" on={s.italic} run={() => c().toggleItalic().run()}>
        <Italic size={sz} />
      </Tool>
      <Tool label="Strikethrough" on={s.strike} run={() => c().toggleStrike().run()}>
        <Strikethrough size={sz} />
      </Tool>
      <Tool label="Inline code" on={s.code} run={() => c().toggleCode().run()}>
        <Code size={sz} />
      </Tool>
      <Pop
        editor={editor}
        label="Text colour"
        trigger={
          <span className="grid place-items-center">
            <Baseline size={sz} />
            <span
              className="-mt-0.5 h-0.5 w-3.5 rounded bg-current"
              style={s.color ? { color: s.color } : undefined}
            />
          </span>
        }
      >
        {(close) => (
          <>
            {COLORS.map(([name, hex]) => (
              <button
                key={name}
                type="button"
                aria-label={name}
                title={name}
                aria-pressed={s.color === hex}
                onClick={() => {
                  close()
                  c().setColor(hex).run()
                }}
                className={opt}
              >
                <span className="size-4 rounded-full border border-line" style={{ background: hex }} />
              </button>
            ))}
            <button
              type="button"
              aria-label="Default colour"
              title="Default colour"
              onClick={() => {
                close()
                c().unsetColor().run()
              }}
              className={`${opt} text-[11px]`}
            >
              Aa
            </button>
          </>
        )}
      </Pop>
      <Pop
        editor={editor}
        label="Alignment"
        trigger={(() => {
          const I = ALIGNS.find(([a]) => a === s.align)?.[2] ?? AlignLeft
          return <I size={sz} />
        })()}
      >
        {(close) =>
          ALIGNS.map(([a, name, I]) => (
            <button
              key={a}
              type="button"
              aria-label={name}
              title={name}
              aria-pressed={s.align === a}
              onClick={() => {
                close()
                c().setTextAlign(a).run()
              }}
              className={opt}
            >
              <I size={sz} />
            </button>
          ))
        }
      </Pop>
      <Sep />
      <Tool label="Bulleted list" on={s.bullet} run={() => c().toggleBulletList().run()}>
        <List size={sz} />
      </Tool>
      <Tool label="Numbered list" on={s.ordered} run={() => c().toggleOrderedList().run()}>
        <ListOrdered size={sz} />
      </Tool>
      <Tool label="To-do list" on={s.task} run={() => c().toggleTaskList().run()}>
        <ListChecks size={sz} />
      </Tool>
      <Sep />
      <Tool label="Link" on={s.link} run={() => promptLink(editor)}>
        <Link2 size={sz} />
      </Tool>
      <Tool
        label="Table"
        run={() => {
          editor.commands.focus()
          editor.view.dom.dispatchEvent(new Event('cn:table-picker'))
        }}
      >
        <Table size={sz} />
      </Tool>
      <Tool label="Image" run={() => emit('cn:image-dialog')}>
        <ImageIcon size={sz} />
      </Tool>
      <Tool label="Emoji" run={() => emit('cn:emoji-picker')}>
        <Smile size={sz} />
      </Tool>
      <Tool label="Mention" run={() => c().insertContent('@').run()}>
        <AtSign size={sz} />
      </Tool>
      <Tool label="Divider" run={() => c().setHorizontalRule().run()}>
        <Minus size={sz} />
      </Tool>
      <Sep />
      <Tool label="Undo" disabled={!s.undo} run={() => c().undo().run()}>
        <Undo2 size={sz} />
      </Tool>
      <Tool label="Redo" disabled={!s.redo} run={() => c().redo().run()}>
        <Redo2 size={sz} />
      </Tool>
    </div>
  )
}
