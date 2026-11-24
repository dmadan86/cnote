'use client'
import Image from '@tiptap/extension-image'
import { EditorContent, useEditor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Bold, Heading2, Heading3, ImagePlus, Link2, List, Loader2, Quote } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls, Modal } from '@/components/ui'
import type { CmdFailure } from '@/lib/api'
import {
  blogApi,
  POST_SLUG,
  type PostBodyDocument,
  type PostNode,
  postSlugify,
  type Site,
  type SitePost,
  safeHref,
} from '@/lib/sites'
import { ShareCard } from './publish-modal'
import { ACCEPT, uploadImage } from './upload'

type Img = { id: string; filename: string }
const EMPTY: PostBodyDocument = { type: 'doc', content: [{ type: 'paragraph' }] }
// The stored grammar (design 17.2): only these attributes are ever sent, whatever TipTap adds.
const NODE_ATTRS: Record<string, string[]> = { heading: ['level'], image: ['image_id', 'alt'] }
const MARK_ATTRS: Record<string, string[]> = { link: ['href'] }
const pick = (attrs: Record<string, unknown> | undefined, keys: string[] = []) => {
  const out: Record<string, unknown> = {}
  for (const k of keys) if (attrs?.[k] !== undefined && attrs[k] !== null) out[k] = attrs[k]
  return Object.keys(out).length ? out : undefined
}
/** editor.getJSON() cut down to the allowed attributes (the server rejects anything else). */
export function cleanPost(n: PostNode): PostNode {
  const out: PostNode = { type: n.type }
  const attrs = pick(n.attrs, NODE_ATTRS[n.type])
  if (attrs) out.attrs = attrs
  if (n.text !== undefined) out.text = n.text
  if (n.content?.length) out.content = n.content.map(cleanPost)
  if (n.marks?.length)
    out.marks = n.marks.map((m) => {
      const a = pick(m.attrs, MARK_ATTRS[m.type])
      return a ? { type: m.type, attrs: a } : { type: m.type }
    })
  return out
}

const extensions = (assetBase: string) => [
  StarterKit.configure({
    heading: { levels: [2, 3] },
    codeBlock: false,
    code: false,
    strike: false,
    underline: false,
    trailingNode: false,
    link: { openOnClick: false, autolink: false, linkOnPaste: false, isAllowedUri: (u) => !!safeHref(u) },
  }),
  Image.extend({
    name: 'image',
    addAttributes: () => ({ image_id: { default: null }, alt: { default: '' } }),
    addInputRules: () => [],
    parseHTML: () => [
      {
        tag: 'img[data-image-id]',
        getAttrs: (el) => ({
          image_id: (el as HTMLElement).getAttribute('data-image-id'),
          alt: (el as HTMLElement).getAttribute('alt') ?? '',
        }),
      },
    ],
    renderHTML: ({ HTMLAttributes: a }) => [
      'img',
      { 'data-image-id': a.image_id, alt: a.alt, src: `${assetBase}/${a.image_id}` },
    ],
  }),
]

/** A photo chooser: one of the site's photos, or a new upload. */
function PhotoPick({
  ws,
  siteId,
  images,
  value,
  onChange,
  onImage,
}: {
  ws: string
  siteId: string
  images: Img[]
  value: string | null
  onChange: (id: string | null) => void
  onImage: (i: Img) => void
}) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  return (
    <div className="space-y-1.5">
      {images.length > 0 && (
        <select
          aria-label="Choose one of your photos"
          className={inputCls}
          value={value ?? ''}
          onChange={(e) => onChange(e.target.value || null)}
        >
          <option value="">No photo</option>
          {images.map((i) => (
            <option key={i.id} value={i.id}>
              {i.filename}
            </option>
          ))}
        </select>
      )}
      <label className={`${btn} cursor-pointer`}>
        <ImagePlus size={14} aria-hidden="true" /> {busy ? 'Uploading…' : 'Upload a photo'}
        <input
          type="file"
          accept={ACCEPT}
          className="sr-only"
          disabled={busy}
          onChange={async (e) => {
            const f = e.target.files?.[0]
            e.target.value = ''
            if (!f) return
            setBusy(true)
            setErr('')
            try {
              const img = await uploadImage(ws, siteId, f)
              onImage(img)
              onChange(img.id)
            } catch (x) {
              setErr(x instanceof Error ? x.message : "That photo couldn't be uploaded")
            } finally {
              setBusy(false)
            }
          }}
        />
      </label>
      {err && (
        <p role="alert" className="text-[12px] text-bad">
          {err}
        </p>
      )}
    </div>
  )
}

const Counter = ({ n, max }: { n: number; max: number }) => (
  <span className={`text-[12px] ${n > max ? 'text-warn' : 'text-muted'}`}>
    {n}/{max}
  </span>
)

/** One post: title, body and settings. Saves with expected_rev; a conflict asks for a reload. */
export function PostEditor({
  ws,
  site,
  postId,
  images,
  ai,
  onImage,
  onChanged,
  onOpen,
  onClose,
}: {
  ws: string
  site: Site
  /** 'new' for a fresh draft. Mount with key={postId}. */
  postId: string
  images: Img[]
  /** Opened from an AI draft: shows the read-before-publishing banner. */
  ai?: boolean
  onImage: (i: Img) => void
  onChanged: () => void
  /** A new post got its id on the first save. */
  onOpen: (id: string) => void
  onClose: () => void
}) {
  const assetBase = `/api/w/${ws}/sites/${site.id}/img`
  const [post, setPost] = useState<(SitePost & { ai?: boolean }) | null>(null)
  const [loading, setLoading] = useState(postId !== 'new')
  const [title, setTitle] = useState('')
  const [slug, setSlug] = useState('')
  const [typedSlug, setTypedSlug] = useState(false)
  const [desc, setDesc] = useState('')
  const [cover, setCover] = useState<string | null>(null)
  const [dirty, setDirty] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const [del, setDel] = useState(false)
  const [linkOpen, setLinkOpen] = useState(false)
  const [href, setHref] = useState('')
  const [linkErr, setLinkErr] = useState('')
  const [photoOpen, setPhotoOpen] = useState(false)
  const ready = useRef(false)
  const first = useRef(postId) // a new post gets its id on the first save; the editor stays as it is
  const exts = useMemo(() => extensions(assetBase), [assetBase])
  const editor = useEditor({
    extensions: exts,
    content: EMPTY,
    immediatelyRender: false,
    shouldRerenderOnTransaction: true,
    editorProps: {
      attributes: {
        'aria-label': 'Post body',
        class:
          'min-h-[280px] rounded-md border border-line px-3 py-2 text-[15px] leading-relaxed focus:outline-none focus-visible:border-accent [&_a]:text-accent [&_a]:underline [&_blockquote]:border-l-4 [&_blockquote]:border-line [&_blockquote]:pl-3 [&_blockquote]:text-muted [&_h2]:mt-4 [&_h2]:text-xl [&_h2]:font-bold [&_h3]:mt-3 [&_h3]:text-lg [&_h3]:font-semibold [&_img]:my-2 [&_img]:max-h-64 [&_img]:rounded [&_ol]:list-decimal [&_ol]:pl-6 [&_p]:my-2 [&_ul]:list-disc [&_ul]:pl-6',
      },
    },
    onUpdate: () => ready.current && setDirty(true),
  })

  const fill = (p: SitePost & { ai?: boolean }) => {
    setPost(p)
    setTitle(p.title)
    setSlug(p.slug)
    setTypedSlug(true)
    setDesc(p.description ?? '')
    setCover(p.cover_image_id)
    editor?.commands.setContent(p.body?.content?.length ? p.body : EMPTY, { emitUpdate: false })
    setDirty(false)
    ready.current = true
  }
  // biome-ignore lint/correctness/useExhaustiveDependencies: load once per post, when the editor exists
  useEffect(() => {
    if (!editor) return
    if (first.current === 'new') {
      ready.current = true
      return
    }
    blogApi.get(ws, site.id, first.current).then(
      (p) => {
        fill(p)
        setLoading(false)
      },
      (e) => {
        setErr(e as Error)
        setLoading(false)
      },
    )
  }, [editor, ws, site.id])

  const touch =
    <T,>(set: (v: T) => void) =>
    (v: T) => {
      set(v)
      setDirty(true)
    }
  const published = post?.status === 'published'
  const live = !!site.published_version_id && !site.taken_down
  const slugOk = !slug || (POST_SLUG.test(slug) && slug.length <= 60)
  const run = async (fn: () => Promise<void>) => {
    setBusy(true)
    setErr(undefined)
    try {
      await fn()
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const save = async () => {
    if (!editor) return null
    const r = await blogApi.save(ws, {
      id: site.id,
      post_id: post?.id,
      expected_rev: post?.rev,
      title: title.trim(),
      slug: slug || undefined,
      description: desc.trim(),
      cover_image_id: cover,
      body: cleanPost(editor.getJSON() as PostNode) as PostBodyDocument,
    })
    setPost(r)
    setSlug(r.slug)
    setDirty(false)
    onChanged()
    if (!post) onOpen(r.id)
    return r
  }
  const publish = (on: boolean) =>
    run(async () => {
      const p = dirty || !post ? await save() : post
      if (!p) return
      const r = await blogApi.publish(ws, site.id, p.id, p.rev, on)
      setPost((x) => ({ ...(x ?? p), ...r }))
      onChanged()
    })
  const conflict = (err as CmdFailure | undefined)?.code === 'conflict'
  const canSave = !!title.trim() && slugOk && dirty && !busy
  const url = site.url?.replace(/^https?:\/\//, '') ?? ''
  const e = editor
  const btnTool = (on: boolean) =>
    `rounded p-1.5 hover:bg-hover disabled:opacity-40 ${on ? 'bg-hover text-accent' : ''}`

  if (loading || !e)
    return (
      <p className="flex items-center gap-2 p-4 text-[13px] text-muted" role="status">
        <Loader2 size={14} className="animate-spin" aria-hidden="true" /> Loading…
      </p>
    )
  return (
    <div className="grid min-h-0 flex-1 gap-4 overflow-y-auto p-4 text-[13px] md:grid-cols-[minmax(0,1fr)_300px]">
      <div className="min-w-0 space-y-3">
        {(ai || post?.ai) && !published && (
          <p role="status" className="rounded-md bg-warn-bg px-2.5 py-1.5 text-warn">
            Draft written by C Note AI. Please read before publishing.
          </p>
        )}
        <div
          role="toolbar"
          aria-label="Format"
          className="flex flex-wrap items-center gap-0.5 border-b border-line pb-1.5"
        >
          <button
            type="button"
            title="Heading"
            aria-label="Heading"
            aria-pressed={e.isActive('heading', { level: 2 })}
            className={btnTool(e.isActive('heading', { level: 2 }))}
            onClick={() => e.chain().focus().toggleHeading({ level: 2 }).run()}
          >
            <Heading2 size={15} />
          </button>
          <button
            type="button"
            title="Subheading"
            aria-label="Subheading"
            aria-pressed={e.isActive('heading', { level: 3 })}
            className={btnTool(e.isActive('heading', { level: 3 }))}
            onClick={() => e.chain().focus().toggleHeading({ level: 3 }).run()}
          >
            <Heading3 size={15} />
          </button>
          <button
            type="button"
            title="Bold"
            aria-label="Bold"
            aria-pressed={e.isActive('bold')}
            className={btnTool(e.isActive('bold'))}
            onClick={() => e.chain().focus().toggleBold().run()}
          >
            <Bold size={15} />
          </button>
          <button
            type="button"
            title="List"
            aria-label="List"
            aria-pressed={e.isActive('bulletList')}
            className={btnTool(e.isActive('bulletList'))}
            onClick={() => e.chain().focus().toggleBulletList().run()}
          >
            <List size={15} />
          </button>
          <button
            type="button"
            title="Link"
            aria-label="Link"
            aria-pressed={e.isActive('link')}
            className={btnTool(e.isActive('link'))}
            onClick={() => {
              setHref((e.getAttributes('link').href as string) ?? '')
              setLinkErr('')
              setPhotoOpen(false)
              setLinkOpen((o) => !o)
            }}
          >
            <Link2 size={15} />
          </button>
          <button
            type="button"
            title="Quote"
            aria-label="Quote"
            aria-pressed={e.isActive('blockquote')}
            className={btnTool(e.isActive('blockquote'))}
            onClick={() => e.chain().focus().toggleBlockquote().run()}
          >
            <Quote size={15} />
          </button>
          <button
            type="button"
            title="Photo"
            aria-label="Photo"
            aria-pressed={photoOpen}
            className={btnTool(photoOpen)}
            onClick={() => {
              setLinkOpen(false)
              setPhotoOpen((o) => !o)
            }}
          >
            <ImagePlus size={15} />
          </button>
        </div>
        {linkOpen && (
          <form
            className="flex items-start gap-2"
            onSubmit={(ev) => {
              ev.preventDefault()
              const h = href.trim()
              if (!h) {
                e.chain().focus().extendMarkRange('link').unsetLink().run()
                return setLinkOpen(false)
              }
              const ok = safeHref(h)
              if (!ok)
                return setLinkErr(
                  "That address can't be used. Start with https:// or / for a page of your site.",
                )
              e.chain().focus().extendMarkRange('link').setLink({ href: ok }).run()
              setLinkOpen(false)
            }}
          >
            <div className="min-w-0 flex-1">
              <input
                aria-label="Link address"
                placeholder="https://… or /page"
                value={href}
                onChange={(ev) => setHref(ev.target.value)}
                className={inputCls}
              />
              {linkErr && (
                <p role="alert" className="mt-1 text-[12px] text-bad">
                  {linkErr}
                </p>
              )}
            </div>
            <button type="submit" className={btn}>
              {href.trim() ? 'Apply' : 'Remove link'}
            </button>
          </form>
        )}
        {photoOpen && (
          <div className="rounded-md border border-line p-2">
            <PhotoPick
              ws={ws}
              siteId={site.id}
              images={images}
              value={null}
              onImage={onImage}
              onChange={(id) => {
                if (!id) return
                e.chain()
                  .focus()
                  .insertContent({ type: 'image', attrs: { image_id: id, alt: '' } })
                  .run()
                setPhotoOpen(false)
              }}
            />
          </div>
        )}
        {e.isActive('image') && (
          <label className="block text-[12px] font-medium text-muted">
            Describe this photo for people who can't see it
            <input
              maxLength={120}
              value={(e.getAttributes('image').alt as string) ?? ''}
              onChange={(ev) => e.chain().updateAttributes('image', { alt: ev.target.value }).run()}
              className={`${inputCls} mt-1 font-normal text-fg`}
            />
          </label>
        )}
        <input
          aria-label="Post title"
          placeholder="Post title"
          maxLength={120}
          value={title}
          onChange={(ev) => {
            const v = ev.target.value
            setTitle(v)
            setDirty(true)
            if (!typedSlug && !published) setSlug(postSlugify(v))
          }}
          className="w-full border-b border-line bg-transparent px-1 py-1.5 text-[22px] font-bold focus:border-accent focus:outline-none"
        />
        <EditorContent editor={e} />
      </div>

      <aside className="space-y-4" aria-label="Post settings">
        <div className="flex items-center gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Back to posts
          </button>
          <span className="ml-auto text-[12px] text-muted" role="status">
            {published ? (post?.pending ? 'Published · changes not live' : 'Published') : 'Draft'}
            {dirty ? ' · unsaved' : post ? ' · saved' : ''}
          </span>
        </div>
        {conflict ? (
          <p role="alert" className="flex items-center gap-2 text-bad">
            This post changed somewhere else. Reload to see the latest.
            <button
              type="button"
              className={btn}
              onClick={() => {
                setErr(undefined)
                run(async () => {
                  if (post) fill(await blogApi.get(ws, site.id, post.id))
                })
              }}
            >
              Reload
            </button>
          </p>
        ) : (
          <ErrorText error={err} />
        )}
        <div className="space-y-1">
          <span className="text-[12px] font-medium text-muted">Cover photo</span>
          {cover && (
            // biome-ignore lint/performance/noImgElement: a site image served by the API
            <img
              src={`${assetBase}/${cover}`}
              alt=""
              className="aspect-video w-full rounded-md object-cover"
            />
          )}
          <PhotoPick
            ws={ws}
            siteId={site.id}
            images={images}
            value={cover}
            onChange={touch(setCover)}
            onImage={onImage}
          />
        </div>
        <div className="space-y-1">
          <div className="flex justify-between">
            <label htmlFor="post-desc" className="text-[12px] font-medium text-muted">
              Short summary
            </label>
            <Counter n={desc.length} max={160} />
          </div>
          <textarea
            id="post-desc"
            rows={3}
            maxLength={200}
            value={desc}
            onChange={(ev) => touch(setDesc)(ev.target.value)}
            className={inputCls}
          />
          <p className="text-[12px] text-muted">Shown in the list, on Google and when the link is shared.</p>
        </div>
        <div className="space-y-1">
          <label htmlFor="post-slug" className="text-[12px] font-medium text-muted">
            Address
          </label>
          <div className="flex items-center gap-1">
            <span className="shrink-0 text-muted">/blog/</span>
            <input
              id="post-slug"
              value={slug}
              maxLength={60}
              onChange={(ev) => {
                setTypedSlug(true)
                touch(setSlug)(ev.target.value.toLowerCase())
              }}
              className={inputCls}
            />
          </div>
          {!slugOk && <p className="text-[12px] text-bad">Use lowercase letters, numbers and hyphens.</p>}
        </div>
        <figure className="rounded-md border border-line p-3">
          <p className="truncate text-[12px] text-muted">
            {url}/blog/{slug}
          </p>
          <p className="truncate text-[15px] text-accent">{title || 'Post title'}</p>
          <p className="line-clamp-2">{desc || 'No summary yet.'}</p>
          <figcaption className="mt-1 text-[11px] text-muted">How it appears on Google.</figcaption>
        </figure>
        <div className="space-y-1">
          <span className="text-[12px] font-medium text-muted">How it looks when shared</span>
          <ShareCard
            image={post?.og_image_id ? `${assetBase}/${post.og_image_id}` : undefined}
            title={title}
            description={desc}
            host={`${url}/blog/${slug}`}
          />
        </div>
        {!live && <p className="text-muted">Publish your website first. Then you can publish posts.</p>}
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className={btn}
            disabled={!canSave}
            onClick={() => run(async () => void (await save()))}
          >
            Save
          </button>
          {published ? (
            <>
              {(post?.pending || dirty) && (
                <button
                  type="button"
                  className={btnPrimary}
                  disabled={busy || !live || !title.trim() || !slugOk}
                  onClick={() => publish(true)}
                >
                  Update post
                </button>
              )}
              <button type="button" className={btn} disabled={busy || dirty} onClick={() => publish(false)}>
                Move back to drafts
              </button>
            </>
          ) : (
            <button
              type="button"
              className={btnPrimary}
              disabled={busy || !live || !title.trim() || !slugOk}
              onClick={() => publish(true)}
            >
              {busy && <Loader2 size={14} className="animate-spin" aria-hidden="true" />} Publish post
            </button>
          )}
          {post && (
            <button type="button" className="ml-auto text-bad underline" onClick={() => setDel(true)}>
              Delete
            </button>
          )}
        </div>
        <p className="text-[12px] text-muted">
          Publishing a post does not publish other changes to your website.
        </p>
      </aside>

      <Modal open={del} onClose={() => setDel(false)} title="Delete this post?">
        <div className="space-y-4 text-[13px]">
          <p>{published ? 'It is removed from your website at once. ' : ''}This can't be undone.</p>
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setDel(false)}>
              Cancel
            </button>
            <button
              type="button"
              disabled={busy}
              className="inline-flex items-center rounded-md bg-bad px-3 py-1.5 text-[13px] font-medium text-canvas disabled:opacity-50"
              onClick={() =>
                run(async () => {
                  if (!post) return
                  await blogApi.remove(ws, site.id, post.id, post.rev)
                  setDel(false)
                  onChanged()
                  onClose()
                })
              }
            >
              Delete post
            </button>
          </div>
        </div>
      </Modal>
    </div>
  )
}
