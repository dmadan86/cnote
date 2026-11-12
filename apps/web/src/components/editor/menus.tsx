'use client'
import type { Editor } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import { Bold, Code, Italic, Link2, MessageSquarePlus, Strikethrough, Underline } from 'lucide-react'
import { selectionAnchor } from './inline-comments'
import { EmojiPicker, ImageDialog } from './inserts'
import { TableControls, TablePicker } from './table-controls'

const btn =
  'grid h-7 min-w-7 place-items-center rounded px-1.5 text-[12px] hover:bg-hover disabled:opacity-40 disabled:hover:bg-transparent aria-pressed:bg-hover aria-pressed:text-accent'
const bar = 'flex items-center gap-0.5 rounded-lg border border-line bg-canvas p-1 text-fg shadow-xl'

export function Tool({
  label,
  on,
  popup,
  disabled,
  run,
  children,
}: {
  label: string
  on?: boolean
  popup?: boolean
  disabled?: boolean
  run: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={popup ? undefined : on}
      aria-haspopup={popup ? 'dialog' : undefined}
      aria-expanded={popup ? on : undefined}
      disabled={disabled}
      onMouseDown={(e) => e.preventDefault()} // keep the selection
      onClick={run}
      className={btn}
    >
      {children}
    </button>
  )
}

/** Prompts for a URL and sets/removes the link on the selection. */
export function promptLink(editor: Editor) {
  const prev = editor.getAttributes('link').href as string | undefined
  const url = window.prompt('Link URL (leave empty to remove)', prev ?? 'https://')
  if (url === null) return
  const chain = editor.chain().focus().extendMarkRange('link')
  // The Link extension also rejects javascript: and other unsafe schemes.
  if (!url.trim()) chain.unsetLink().run()
  else chain.setLink({ href: url.trim() }).run()
}

/** Formatting toolbar over selected text, table handles/toolbar, and the table size picker. */
type OnComment = (anchor: { anchor_text: string; anchor_prefix: string }) => void

/** "Comment" on the selected text: only for a selection inside one paragraph (that is what an anchor can quote). */
function CommentTool({ editor, onComment }: { editor: Editor; onComment: OnComment }) {
  // Read the selection at click time: the bubble does not re-render on every selection change.
  return (
    <Tool
      label="Comment"
      on={false}
      run={() => {
        const anchor = selectionAnchor(editor)
        if (anchor) onComment(anchor)
        else window.alert('Select text within one paragraph to comment on it.')
      }}
    >
      <MessageSquarePlus size={15} />
    </Tool>
  )
}

/** Read-only pages: selecting text offers only "Comment". */
export function CommentBubble({ editor, onComment }: { editor: Editor; onComment: OnComment }) {
  return (
    <BubbleMenu editor={editor} className="z-40" shouldShow={({ state }) => !state.selection.empty}>
      <div role="toolbar" aria-label="Text actions" className={bar}>
        <CommentTool editor={editor} onComment={onComment} />
      </div>
    </BubbleMenu>
  )
}

export function EditorMenus({ editor, onComment }: { editor: Editor; onComment?: OnComment }) {
  // Buttons prevent mousedown, so the editor keeps focus; .focus() would restore the old selection a frame later.
  const c = () => editor.chain()
  return (
    <>
      {/* z-40: above the sticky Editor toolbar (z-20) when the selection is on the first line. */}
      <BubbleMenu
        editor={editor}
        className="z-40"
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
          <Tool label="Link" on={editor.isActive('link')} run={() => promptLink(editor)}>
            <Link2 size={14} />
          </Tool>
          {onComment && <CommentTool editor={editor} onComment={onComment} />}
        </div>
      </BubbleMenu>
      <TableControls editor={editor} />
      <TablePicker editor={editor} />
      <EmojiPicker editor={editor} />
      <ImageDialog editor={editor} />
    </>
  )
}
