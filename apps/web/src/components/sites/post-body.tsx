import type { ReactNode } from 'react'
import { type PostBodyDocument, type PostNode, safeHref } from '@/lib/sites'

// Builds React elements from the stored tree only (design 17.2): no HTML strings, no parser. Unknown nodes and
// marks render nothing / pass their text through; the tree is validated in Go on every write and again before render.

const MAX_DEPTH = 8
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

function mark(m: { type: string; attrs?: Record<string, unknown> }, child: ReactNode): ReactNode {
  if (m.type === 'bold') return <strong>{child}</strong>
  if (m.type === 'italic') return <em>{child}</em>
  if (m.type === 'link') {
    // The href passes safeHref again at render; a link that fails it stays plain text.
    const href = safeHref(m.attrs?.href)
    return href ? (
      <a
        href={href}
        rel="noopener noreferrer nofollow ugc"
        className="underline underline-offset-2 hover:no-underline"
      >
        {child}
      </a>
    ) : (
      child
    )
  }
  return child
}

function Node({ n, assetBase, depth }: { n: PostNode; assetBase?: string; depth: number }) {
  if (depth > MAX_DEPTH) return null
  const kids = (n.content ?? []).map((c, i) => (
    // biome-ignore lint/suspicious/noArrayIndexKey: a document tree has no ids
    <Node key={i} n={c} assetBase={assetBase} depth={depth + 1} />
  ))
  switch (n.type) {
    case 'text':
      return typeof n.text === 'string'
        ? (n.marks ?? []).reduce<ReactNode>((c, m) => mark(m, c), n.text)
        : null
    case 'paragraph':
      return <p className="my-4">{kids}</p>
    case 'heading': {
      const lvl = n.attrs?.level
      return lvl === 3 ? (
        <h3 className="mb-2 mt-6 text-xl font-bold">{kids}</h3>
      ) : lvl === 2 ? (
        <h2 className="mb-3 mt-8 text-2xl font-bold">{kids}</h2>
      ) : null
    }
    case 'bulletList':
      return <ul className="my-4 list-disc space-y-1 pl-6">{kids}</ul>
    case 'orderedList':
      return <ol className="my-4 list-decimal space-y-1 pl-6">{kids}</ol>
    case 'listItem':
      return <li>{kids}</li>
    case 'blockquote':
      return (
        <blockquote className="my-6 border-l-4 border-[var(--site-primary)] pl-4 italic text-[var(--site-muted)]">
          {kids}
        </blockquote>
      )
    case 'hardBreak':
      return <br />
    case 'horizontalRule':
      return <hr className="my-8 border-[var(--site-border)]" />
    case 'siteImage':
    case 'image': {
      const id = String(n.attrs?.image_id ?? n.attrs?.id ?? '').toLowerCase()
      if (!assetBase || !UUID.test(id)) return null
      return (
        // biome-ignore lint/performance/noImgElement: a published site serves its own images, same origin
        <img
          src={`${assetBase}/${id}`}
          alt={typeof n.attrs?.alt === 'string' ? n.attrs.alt.slice(0, 120) : ''}
          loading="lazy"
          className="my-6 h-auto w-full rounded-[var(--site-radius)]"
        />
      )
    }
    default:
      return null
  }
}

export function PostBody({ body, assetBase }: { body: PostBodyDocument; assetBase?: string }) {
  if (body?.type !== 'doc') return null
  return (
    <>
      {(body.content ?? []).map((c, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: a document tree has no ids
        <Node key={i} n={c} assetBase={assetBase} depth={1} />
      ))}
    </>
  )
}
