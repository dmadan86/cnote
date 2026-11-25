import type { TreeRow } from '@/lib/docs'

/** Key of a sibling list: a page's children, else the space root (a page whose parent is gone shows at the root). */
export const kidKey = (spaceId: string, parent: string | null) => parent ?? `root:${spaceId}`
/** The synthetic "Shared with you" level of a space: pages the reader can open although an ancestor is hidden. */
export const sharedParent = (spaceId: string) => `shared:${spaceId}`
/** The parent a level key stands for: a page id, "shared:<space>", or null for the space root. */
export const parentOfKey = (key: string) => (key.startsWith('root:') ? null : key)
export const isShared = (parent: string | null) => !!parent?.startsWith('shared:')

/** A page's parent when that parent is a loaded row, else null: a page under a trashed or hidden parent sits at the root. */
export const liveParent = (n: { parent_id: string | null }, byId: Map<string, unknown>) =>
  n.parent_id && byId.has(n.parent_id) ? n.parent_id : null

/** The page and every loaded descendant (the server refuses a move into a descendant that is not loaded). */
export function subtreeIds(kids: Map<string, { id: string }[]>, id: string) {
  const out = new Set<string>()
  const walk = (i: string) => {
    if (out.has(i)) return
    out.add(i)
    for (const c of kids.get(i) ?? []) walk(c.id)
  }
  walk(id)
  return out
}

/** Every loaded page of a space in tree order with its depth, skipping the excluded subtree. */
export function flatten(kids: Map<string, TreeRow[]>, spaceId: string, skip: Set<string>) {
  const out: { note: TreeRow; depth: number }[] = []
  const walk = (key: string, depth: number) => {
    for (const n of kids.get(key) ?? []) {
      if (skip.has(n.id)) continue
      out.push({ note: n, depth })
      walk(n.id, depth + 1)
    }
  }
  walk(kidKey(spaceId, null), 0)
  return out
}
