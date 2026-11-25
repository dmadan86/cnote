import { Plugin } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { type Editor, Extension } from '@tiptap/react'
import { toast } from './blocks'

// docs-parity-b 5.2: an attachment in the text is an ordinary link to one of the page's files,
// `[Name.pdf](/api/w/<ws>/files/<id>)`. Nothing new is stored; a decoration draws it as a chip.

export const FILE_HREF = /^\/api\/w\/[0-9a-f-]{36}\/files\/([0-9a-f-]{36})$/i
const MAX_BYTES = 10 << 20

/** Fired on window after any upload to a page, so the Attachments panel reloads (blocks.ts fires it for images). */
export const FILES_EVENT = 'cnote:files'

export const fileHref = (ws: string, id: string) => `/api/w/${ws}/files/${id}`

/** Draws every link to a page file as a chip (icon and name). */
export const FileChips = Extension.create({
  name: 'fileChips',
  addProseMirrorPlugins() {
    return [
      new Plugin({
        props: {
          decorations: ({ doc }) => {
            const out: Decoration[] = []
            doc.descendants((n, pos) => {
              const href = n.isText ? n.marks.find((m) => m.type.name === 'link')?.attrs.href : null
              const m = typeof href === 'string' ? FILE_HREF.exec(href) : null
              if (m)
                out.push(Decoration.inline(pos, pos + n.nodeSize, { class: 'file-chip', 'data-file': m[1] }))
            })
            return DecorationSet.create(doc, out)
          },
        },
      }),
    ]
  },
})

export const FILE_TYPES = ['application/pdf', 'image/png', 'image/jpeg', 'image/gif', 'image/webp']

/** Uploads one PDF or image as a page file; resolves to its link target. */
export async function uploadFile(ws: string, noteId: string, f: File): Promise<string> {
  if (!FILE_TYPES.includes(f.type))
    throw new Error('Only PDF files and PNG, JPEG, GIF and WebP images can be attached.')
  if (f.size > MAX_BYTES) throw new Error('That file is larger than 10 MB.')
  const res = await fetch(
    `/api/w/${ws}/files?note_id=${noteId}&filename=${encodeURIComponent(f.name || 'file.pdf')}`,
    {
      method: 'POST',
      body: f,
      credentials: 'same-origin',
    },
  )
  const j = await res.json().catch(() => null)
  if (!res.ok || typeof j?.id !== 'string') throw new Error(j?.error?.message ?? 'Could not upload the file.')
  window.dispatchEvent(new Event(FILES_EVENT))
  return fileHref(ws, j.id)
}

/** A file link at `pos` (default: the selection), followed by a space. */
export function insertFileLink(editor: Editor, href: string, name: string, pos?: number) {
  editor
    .chain()
    .focus()
    .insertContentAt(pos ?? editor.state.selection.to, [
      { type: 'text', text: name, marks: [{ type: 'link', attrs: { href } }] },
      { type: 'text', text: ' ' },
    ])
    .run()
}

type Storage = { insert: (files: File[], pos?: number) => void }

/** Paste and drop of PDFs (images stay with ImageUpload), and `storage.fileUpload.insert` for the picker. */
export const FileUpload = Extension.create<{ ws: string; noteId: string }, Storage>({
  name: 'fileUpload',
  addOptions: () => ({ ws: '', noteId: '' }),
  addStorage: () => ({ insert: () => {} }),
  onCreate() {
    this.storage.insert = (files, pos) => {
      const { ws, noteId } = this.options
      for (const f of files)
        uploadFile(ws, noteId, f).then(
          (href) => !this.editor.isDestroyed && insertFileLink(this.editor, href, f.name || 'file.pdf', pos),
          (e: Error) => toast(e.message),
        )
    }
  },
  addProseMirrorPlugins() {
    const pdfs = (l?: FileList | null) => [...(l ?? [])].filter((f) => f.type === 'application/pdf')
    return [
      new Plugin({
        props: {
          handlePaste: (view, e) => {
            const files = pdfs(e.clipboardData?.files)
            if (!view.editable || !files.length) return false
            e.preventDefault()
            this.storage.insert(files)
            return true
          },
          handleDrop: (view, e) => {
            const files = pdfs(e.dataTransfer?.files)
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

/** Opens the file picker for a PDF or an image; images go through ImageUpload and become images. */
export function pickFiles(editor: Editor) {
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = FILE_TYPES.join(',')
  input.multiple = true
  input.addEventListener('change', () => {
    const all = [...(input.files ?? [])]
    const st = editor.storage as unknown as { imageUpload: Storage; fileUpload: Storage }
    st.imageUpload.insert(all.filter((f) => f.type.startsWith('image/')))
    st.fileUpload.insert(all.filter((f) => !f.type.startsWith('image/')))
  })
  input.click()
}
