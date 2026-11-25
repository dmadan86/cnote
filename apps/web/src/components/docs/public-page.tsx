'use client'
import { TaskItem, TaskList } from '@tiptap/extension-list'
import { TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import TextAlign from '@tiptap/extension-text-align'
import { Color } from '@tiptap/extension-text-style'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, mergeAttributes, useEditor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { useEffect, useMemo, useState } from 'react'
import type { PublicPage } from '@/lib/docs'
import { blockExtensions } from '../editor/blocks'
import { AlignHeading, AlignParagraph, ColorText, HtmlTable, SafeImage } from '../editor/formats'

type State = 'loading' | 'unavailable' | 'busy' | 'ready'

const UUID = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'
const ABUSE = process.env.NEXT_PUBLIC_ABUSE_EMAIL

/** Only this renderer accepts the public file path as an image source, and only for its own token (design 9.1, layer 3). */
const publicImage = (token: string) => {
  const own = new RegExp(`^/api/public/page/${token}/files/${UUID}$`, 'i')
  return SafeImage.extend({
    renderHTML({ HTMLAttributes }) {
      const src = String(HTMLAttributes.src ?? '')
      if (!/^https?:\/\//i.test(src) && !own.test(src)) return ['span', {}, String(HTMLAttributes.alt ?? '')]
      return ['img', mergeAttributes(HTMLAttributes, { loading: 'lazy', referrerpolicy: 'no-referrer' })]
    },
  }).configure({ inline: false, allowBase64: false })
}

/** The public page at /pg/<token>: no session, no app chrome, read-only. Same-origin API, so no CORS. */
export function PublicPageView({ token }: { token: string }) {
  const [page, setPage] = useState<PublicPage>()
  const [state, setState] = useState<State>('loading')

  useEffect(() => {
    fetch(`/api/public/page/${encodeURIComponent(token)}`, {
      cache: 'no-store',
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
    })
      .then(async (r) => {
        if (r.status === 429) return setState('busy')
        if (!r.ok) return setState('unavailable')
        const p = (await r.json()) as PublicPage
        document.title = p.title || 'Shared page'
        setPage(p)
        setState('ready')
      })
      .catch(() => setState('unavailable'))
  }, [token])

  const shell = (wide: boolean, children: React.ReactNode) => (
    <div className="min-h-screen bg-canvas text-ink">
      <main className={`mx-auto w-full px-4 py-8 ${wide ? 'max-w-[1100px]' : 'max-w-[720px]'}`}>
        {children}
      </main>
      <footer className="mx-auto w-full max-w-[720px] px-4 pb-10 text-center text-[12px] text-muted">
        <p>Shared with C Note</p>
        {page && <p>From {page.workspace_name}</p>}
        {ABUSE && (
          <p>
            <a
              className="underline"
              href={`mailto:${ABUSE}?subject=Report%20abuse`}
              rel="noopener noreferrer nofollow"
            >
              Report abuse
            </a>
          </p>
        )}
      </footer>
    </div>
  )

  if (state === 'loading') return shell(false, <p role="status">Loading...</p>)
  if (state === 'busy')
    return shell(
      false,
      <p role="status">Too many people are viewing this page right now. Please try again in a while.</p>,
    )
  if (state === 'unavailable' || !page) return shell(false, <p role="status">This page is not available.</p>)
  return shell(page.wide, <Article token={token} page={page} />)
}

function Article({ token, page }: { token: string; page: PublicPage }) {
  const extensions = useMemo(
    () => [
      StarterKit.configure({
        paragraph: false,
        heading: false,
        codeBlock: false,
        undoRedo: false,
        link: {
          openOnClick: false,
          HTMLAttributes: { rel: 'noopener noreferrer nofollow ugc', target: '_blank' },
        },
      }),
      AlignParagraph,
      AlignHeading,
      TextAlign.configure({ types: ['heading', 'paragraph'] }),
      ColorText,
      Color,
      publicImage(token),
      Markdown,
      HtmlTable.configure({ resizable: false }),
      TableRow,
      TableHeader,
      TableCell,
      TaskList,
      TaskItem.configure({ nested: true }),
      ...blockExtensions,
    ],
    [token],
  )
  const editor = useEditor({
    immediatelyRender: false,
    editable: false,
    extensions,
    content: page.body_md,
    contentType: 'markdown',
    editorProps: { attributes: { 'aria-label': 'Page content' } },
  })
  return (
    <article>
      <h1 className="mb-1 text-[30px] font-bold leading-tight">
        {page.icon && (
          <span aria-hidden="true" className="mr-2">
            {page.icon}
          </span>
        )}
        {page.title || 'Untitled'}
      </h1>
      <p className="mb-6 text-[12px] text-muted">Updated {page.updated_on}</p>
      <EditorContent editor={editor} />
    </article>
  )
}
