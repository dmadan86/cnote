'use client'
import type { Editor } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import { Bold, Code, Italic, Link2, Strikethrough, Underline } from 'lucide-react'

const btn =
  'grid h-7 min-w-7 place-items-center rounded px-1.5 text-[12px] hover:bg-hover aria-pressed:bg-hover aria-pressed:text-accent'
const bar = 'flex items-center gap-0.5 rounded-lg border border-line bg-bg p-1 text-fg shadow-xl'

function Tool({
  label,
  on,
  run,
  children,
}: {
  label: string
  on?: boolean
  run: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={on}
      onMouseDown={(e) => e.preventDefault()} // keep the selection
      onClick={run}
      className={btn}
    >
      {children}
    </button>
  )
}

/** Formatting toolbar over selected text, plus row/column controls while the cursor is in a table. */
export function EditorMenus({ editor }: { editor: Editor }) {
  // Buttons prevent mousedown, so the editor keeps focus; .focus() would restore the old selection a frame later.
  const c = () => editor.chain()
  const link = () => {
    const prev = editor.getAttributes('link').href as string | undefined
    const url = window.prompt('Link URL (leave empty to remove)', prev ?? 'https://')
    if (url === null) return
    if (!url.trim()) return c().focus().extendMarkRange('link').unsetLink().run()
    // The Link extension also rejects javascript: and other unsafe schemes.
    c().focus().extendMarkRange('link').setLink({ href: url.trim() }).run()
  }
  return (
    <>
      <BubbleMenu
        editor={editor}
        shouldShow={({ editor: e, state }) => !state.selection.empty && !e.isActive('codeBlock')}
      >
        <div role="toolbar" aria-label="Text formatting" className={bar}>
          <Tool label="Bold" on={editor.isActive('bold')} run={() => c().toggleBold().run()}>
            <Bold size={14} />
          </Tool>
          <Tool label="Italic" on={editor.isActive('italic')} run={() => c().toggleItalic().run()}>
            <Italic size={14} />
          </Tool>
          <Tool label="Underline" on={editor.isActive('underline')} run={() => c().toggleUnderline().run()}>
            <Underline size={14} />
          </Tool>
          <Tool label="Strikethrough" on={editor.isActive('strike')} run={() => c().toggleStrike().run()}>
            <Strikethrough size={14} />
          </Tool>
          <Tool label="Inline code" on={editor.isActive('code')} run={() => c().toggleCode().run()}>
            <Code size={14} />
          </Tool>
          <Tool label="Link" on={editor.isActive('link')} run={link}>
            <Link2 size={14} />
          </Tool>
        </div>
      </BubbleMenu>
      <BubbleMenu
        editor={editor}
        pluginKey="tableMenu"
        shouldShow={({ editor: e, state }) => state.selection.empty && e.isActive('table')}
        options={{ placement: 'top' }}
      >
        <div role="toolbar" aria-label="Table" className={bar}>
          <Tool label="Add row above" run={() => c().addRowBefore().run()}>
            + Row ↑
          </Tool>
          <Tool label="Add row below" run={() => c().addRowAfter().run()}>
            + Row ↓
          </Tool>
          <Tool label="Add column left" run={() => c().addColumnBefore().run()}>
            + Col ←
          </Tool>
          <Tool label="Add column right" run={() => c().addColumnAfter().run()}>
            + Col →
          </Tool>
          <Tool label="Delete row" run={() => c().deleteRow().run()}>
            − Row
          </Tool>
          <Tool label="Delete column" run={() => c().deleteColumn().run()}>
            − Col
          </Tool>
          <Tool label="Toggle header row" run={() => c().toggleHeaderRow().run()}>
            Header
          </Tool>
          <Tool label="Delete table" run={() => c().deleteTable().run()}>
            <span className="text-red-600">Delete</span>
          </Tool>
        </div>
      </BubbleMenu>
    </>
  )
}
