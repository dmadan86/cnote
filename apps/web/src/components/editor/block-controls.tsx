'use client'
import { type Editor, useEditorState } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import { formatDate, STATUS_COLORS } from './blocks'

const bar = 'flex items-center gap-1 rounded-lg border border-line bg-canvas p-1 text-fg shadow-xl'

/** Appears under a status or date while the caret is in it: change the colour or the date, or remove it. */
export function BlockControls({ editor }: { editor: Editor }) {
  const st = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      status: e.isActive('status') ? String(e.getAttributes('status').color) : null,
      date: e.isActive('date') ? String(e.getAttributes('date').datetime) : null,
    }),
  })
  if (!editor.isEditable) return null
  const chain = (mark: string) => editor.chain().focus().extendMarkRange(mark)
  return (
    <BubbleMenu
      editor={editor}
      className="z-40"
      options={{ placement: 'bottom' }}
      shouldShow={({ editor: e }) => e.isActive('status') || e.isActive('date')}
    >
      {st.status && (
        <div role="toolbar" aria-label="Status" className={bar}>
          {STATUS_COLORS.map((c) => (
            <button
              key={c}
              type="button"
              aria-label={`Status colour ${c}`}
              aria-pressed={st.status === c}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => chain('status').updateAttributes('status', { color: c }).run()}
              className="cn-status size-6 rounded aria-pressed:ring-2 aria-pressed:ring-[var(--accent)]"
              data-status={c}
            />
          ))}
          <button
            type="button"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => chain('status').unsetMark('status').run()}
            className="rounded px-2 py-1 text-[12px] hover:bg-hover"
          >
            Remove
          </button>
        </div>
      )}
      {st.date && !st.status && (
        <div role="toolbar" aria-label="Date" className={bar}>
          <input
            type="date"
            aria-label="Date"
            value={st.date}
            onChange={(e) => {
              const iso = e.target.value
              if (!/^\d{4}-\d{2}-\d{2}$/.test(iso)) return
              chain('date')
                .insertContent({
                  type: 'text',
                  text: formatDate(iso),
                  marks: [{ type: 'date', attrs: { datetime: iso } }],
                })
                .run()
            }}
            className="rounded border border-line bg-canvas px-1 py-0.5 text-[13px]"
          />
          <button
            type="button"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => chain('date').unsetMark('date').run()}
            className="rounded px-2 py-1 text-[12px] hover:bg-hover"
          >
            Remove
          </button>
        </div>
      )}
    </BubbleMenu>
  )
}
