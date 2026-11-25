import type { Note } from '@/lib/api'
import { bySiblingOrder } from '@/lib/docs'

/** Key of the sibling list a page sits in: its live parent, else the space root (a page whose parent is gone shows at the root). */
export const kidKey = (spaceId: string, parent: string | null) => parent ?? `root:${spaceId}`

/** Sibling lists in tree order. A page whose parent is not in the list (trashed, or hidden) goes to the space root. */
export function buildKids(notes: Note[]) {
  const ids = new Set(notes.map((n) => n.id))
  const kids = new Map<string, Note[]>()
  for (const n of notes) {
    const k = kidKey(n.space_id, n.parent_id && ids.has(n.parent_id) ? n.parent_id : null)
    kids.set(k, [...(kids.get(k) ?? []), n])
  }
  for (const l of kids.values()) l.sort(bySiblingOrder)
  return kids
}

export const liveParent = (n: Note, byId: Map<string, Note>) =>
  n.parent_id && byId.has(n.parent_id) ? n.parent_id : null

/** The page and all its descendants. */
export function subtreeIds(kids: Map<string, Note[]>, id: string) {
  const out = new Set<string>()
  const walk = (i: string) => {
    if (out.has(i)) return
    out.add(i)
    for (const c of kids.get(i) ?? []) walk(c.id)
  }
  walk(id)
  return out
}

/** Every page of a space in tree order with its depth, skipping the excluded subtree. */
export function flatten(kids: Map<string, Note[]>, spaceId: string, skip: Set<string>) {
  const out: { note: Note; depth: number }[] = []
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
