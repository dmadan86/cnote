'use client'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { cmd, type Note } from '@/lib/api'
import { revealInTree, TREE_EVENT, type TreeLevel, type TreeRow, treeLevel } from '@/lib/docs'
import { isShared, kidKey } from './tree-model'

// The lazy page tree (docs-parity C 5.4). Levels load one at a time as nodes open; the store keeps every loaded row and, per
// level, the ids in order with the cursors for more rows each way. Level keys are kidKey values, so tree-model works on them.

export const PAGE = 100
/** A level the store holds: loaded ids in sibling order, and cursors for the rows beyond either end. */
export type Level = {
  ids: string[]
  next: string | null
  prev: string | null
  busy?: boolean
  failed?: boolean
}
type Data = { rows: Map<string, TreeRow>; levels: Map<string, Level> }

/** Where a key's rows come from: the space (the key does not carry it) and the parent as the API names it. */
type Where = { space: string; parent: string | null }
const apiParent = (p: string | null) => (isShared(p) ? 'shared' : p)

export type TreeStore = {
  /** Loaded rows by id. */
  byId: Map<string, TreeRow>
  /** Loaded sibling lists in order, keyed by kidKey. */
  kids: Map<string, TreeRow[]>
  levels: Map<string, Level>
  /** The level key (kidKey) each loaded row sits in. */
  keyOf: Map<string, string>
  /** Load the first page of a level unless it is loaded or loading. */
  ensure: (space: string, parent: string | null) => void
  /** Load the next page, or the earlier rows of a windowed level; resolves to the rows added. */
  more: (space: string, parent: string | null, dir: 'next' | 'prev') => Promise<TreeRow[]>
  /** Rows just past the loaded ones (limit rows from `next`) or just before them (`prev`), without keeping them. */
  peek: (space: string, parent: string | null, dir: 'next' | 'prev', limit: number) => Promise<TreeRow[]>
  /** Load the readable path to a page: each ancestor's level, cut around the path. */
  reveal: (id: string) => Promise<void>
  /** Reload every loaded level in place after a move, delete, restore or rename. */
  refresh: () => Promise<void>
}

const merge = (d: Data, key: string, lv: TreeLevel, mode: 'replace' | 'append' | 'prepend'): Data => {
  const rows = new Map(d.rows)
  for (const r of lv.rows) rows.set(r.id, r)
  const old = d.levels.get(key)
  const add = lv.rows.map((r) => r.id)
  let ids = add
  let next = lv.next
  let prev = lv.prev ?? null
  if (old && mode === 'append') {
    ids = [...old.ids, ...add.filter((i) => !old.ids.includes(i))]
    prev = old.prev
  } else if (old && mode === 'prepend') {
    ids = [...add.filter((i) => !old.ids.includes(i)), ...old.ids]
    next = old.next
  }
  const levels = new Map(d.levels)
  levels.set(key, { ids, next, prev })
  return { rows, levels }
}

export function useTreeStore(ws: string, enabled: boolean): TreeStore {
  const [data, setData] = useState<Data>({ rows: new Map(), levels: new Map() })
  const live = useRef(data)
  live.current = data
  const where = useRef(new Map<string, Where>())
  const busy = useRef(new Set<string>())
  const epoch = useRef(0) // bumped when the workspace changes: answers for the old one are dropped
  const gen = useRef(0) // newest refresh wins

  // A different workspace starts from nothing.
  // biome-ignore lint/correctness/useExhaustiveDependencies: reset on workspace change only
  useEffect(() => {
    epoch.current++
    where.current = new Map()
    busy.current = new Set()
    setData({ rows: new Map(), levels: new Map() })
  }, [ws])

  const fail = useCallback((key: string, failed: boolean) => {
    setData((d) => {
      const levels = new Map(d.levels)
      const l = levels.get(key) ?? { ids: [], next: null, prev: null }
      levels.set(key, { ...l, failed })
      return { ...d, levels }
    })
  }, [])

  const load = useCallback(
    async (
      space: string,
      parent: string | null,
      input: { after?: string; before?: string; from?: string; limit?: number },
    ) => {
      const key = kidKey(space, parent)
      where.current.set(key, { space, parent })
      return treeLevel(ws, { space_id: space, parent_id: apiParent(parent), ...input })
    },
    [ws],
  )

  const ensure = useCallback(
    (space: string, parent: string | null) => {
      const key = kidKey(space, parent)
      const have = live.current.levels.get(key)
      if (!enabled || (have && !have.failed) || busy.current.has(key)) return
      busy.current.add(key)
      const at = epoch.current
      load(space, parent, { limit: PAGE })
        .then((lv) => at === epoch.current && setData((d) => merge(d, key, lv, 'replace')))
        .catch(() => at === epoch.current && fail(key, true))
        .finally(() => busy.current.delete(key))
    },
    [enabled, load, fail],
  )

  const more = useCallback(
    async (space: string, parent: string | null, dir: 'next' | 'prev') => {
      const key = kidKey(space, parent)
      const l = live.current.levels.get(key)
      const cursor = dir === 'next' ? l?.next : l?.prev
      if (!l || !cursor || busy.current.has(key)) return []
      busy.current.add(key)
      const at = epoch.current
      try {
        const lv = await load(
          space,
          parent,
          dir === 'next' ? { after: cursor, limit: PAGE } : { before: cursor, limit: PAGE },
        )
        if (at !== epoch.current) return []
        setData((d) => merge(d, key, lv, dir === 'next' ? 'append' : 'prepend'))
        return lv.rows
      } catch {
        return [] // the cursor is still held: the caller says so and the reader can ask again
      } finally {
        busy.current.delete(key)
      }
    },
    [load],
  )

  const peek = useCallback(
    async (space: string, parent: string | null, dir: 'next' | 'prev', limit: number) => {
      const l = live.current.levels.get(kidKey(space, parent))
      const cursor = dir === 'next' ? l?.next : l?.prev
      if (!cursor) return []
      const lv = await load(
        space,
        parent,
        dir === 'next' ? { after: cursor, limit } : { before: cursor, limit },
      )
      return lv.rows
    },
    [load],
  )

  const reveal = useCallback(
    async (id: string) => {
      const at = epoch.current
      const { levels } = await revealInTree(ws, id)
      if (at !== epoch.current) return
      setData((d) => {
        let out = d
        levels.forEach((lv, i) => {
          const space = lv.rows[0]?.space_id
          if (!space) return
          const parent = lv.parent_id === 'shared' ? `shared:${space}` : lv.parent_id
          const key = kidKey(space, parent)
          where.current.set(key, { space, parent })
          // the level the reader already holds keeps what they opened; otherwise the window around the path replaces it
          const onPath = levels[i + 1]?.parent_id ?? id
          if (!out.levels.get(key)?.ids.includes(onPath)) out = merge(out, key, lv, 'replace')
        })
        return out
      })
    },
    [ws],
  )

  const refresh = useCallback(async () => {
    const at = ++gen.current
    const results = await Promise.all(
      [...live.current.levels.entries()].map(async ([key, l]) => {
        const w = where.current.get(key)
        if (!w) return null
        const limit = Math.min(200, Math.max(PAGE, l.ids.length))
        try {
          return { key, lv: await load(w.space, w.parent, l.prev ? { from: l.prev, limit } : { limit }) }
        } catch (e) {
          // a level whose parent is gone or no longer readable is dropped; any other failure keeps the old rows
          return { key, gone: /not found/i.test((e as Error).message) }
        }
      }),
    )
    if (at !== gen.current) return
    setData((d) => {
      const levels = new Map(d.levels)
      const fresh = new Map<string, TreeRow>()
      for (const r of results) {
        if (r?.lv) {
          levels.set(r.key, { ids: r.lv.rows.map((x) => x.id), next: r.lv.next, prev: r.lv.prev ?? null })
          for (const x of r.lv.rows) fresh.set(x.id, x)
        } else if (r?.gone) levels.delete(r.key)
      }
      // rows stay only while a level lists them, so a deleted page stops being a drag or menu target
      const rows = new Map<string, TreeRow>()
      for (const l of levels.values())
        for (const i of l.ids) {
          const row = fresh.get(i) ?? d.rows.get(i)
          if (row) rows.set(i, row)
        }
      return { rows, levels }
    })
  }, [load])

  const { kids, keyOf } = useMemo(() => {
    const kids = new Map<string, TreeRow[]>()
    const keyOf = new Map<string, string>()
    for (const [key, l] of data.levels) {
      const rows: TreeRow[] = []
      for (const i of l.ids) {
        const r = data.rows.get(i)
        if (!r) continue
        rows.push(r)
        keyOf.set(i, key)
      }
      kids.set(key, rows)
    }
    return { kids, keyOf }
  }, [data])

  return useMemo(
    () => ({ byId: data.rows, kids, keyOf, levels: data.levels, ensure, more, peek, reveal, refresh }),
    [data, kids, keyOf, ensure, more, peek, reveal, refresh],
  )
}

/** Pages in a subtree, counted level by level up to a cap: exact for the small trees people delete, "all" past it. */
export async function countSubtree(ws: string, space: string, id: string, cap = 100) {
  let total = 1
  const queue = [id]
  for (let calls = 0; queue.length; calls++) {
    const parent = queue.shift() as string
    if (calls >= 20) return { total, exact: false }
    for (let after: string | undefined; ; ) {
      const lv = await treeLevel(ws, { space_id: space, parent_id: parent, after, limit: 200 })
      total += lv.rows.length
      for (const r of lv.rows) if (r.has_children) queue.push(r.id)
      if (total > cap) return { total, exact: false }
      if (!lv.next) break
      after = lv.next
    }
  }
  return { total, exact: true }
}

/**
 * Plain pages, most recently updated first, for the places that want a flat list (Home, the capture picker) rather than the
 * tree. Bodies come with note.list, so the limit stays small.
 */
export function useRecentPages(ws: string, limit: number) {
  const [pages, setPages] = useState<Note[]>([])
  useEffect(() => {
    let live = true
    const load = () =>
      cmd<Note[]>(ws, 'note.list', { limit, type: 'page' })
        .then((r) => live && setPages(r))
        .catch(() => {})
    load()
    window.addEventListener(TREE_EVENT, load)
    return () => {
      live = false
      window.removeEventListener(TREE_EVENT, load)
    }
  }, [ws, limit])
  return pages
}
