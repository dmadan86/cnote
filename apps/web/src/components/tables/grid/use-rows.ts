'use client'
import { useCallback, useEffect, useRef, useState } from 'react'
import { type LinkChip, type ListParams, listRecords, type RecordList, type TableRecord } from '@/lib/tables'

export const PAGE = 150
const MAX_REFRESH_PAGES = 10 // shortcut: a refresh keeps at most 1,500 rows, the rest reloads on scroll

type Links = Record<string, Record<string, LinkChip[]>>
type S = {
  records: TableRecord[]
  links: Links
  groups?: RecordList['groups']
  total: number
  cursor: string | null
  status: 'loading' | 'ready' | 'error'
  error?: Error
}
const EMPTY: S = { records: [], links: {}, total: 0, cursor: null, status: 'loading' }

/**
 * Keyset-paged records of one view. `key` changes (view, filters, sorts, group) restart from the first page.
 * The view's settings are sent with each request, not read from the saved view: an edit shows before its debounced save lands.
 */
export function useRows(
  ws: string,
  tableId: string,
  view: Pick<ListParams, 'filters' | 'sorts' | 'group'>,
  key: string,
) {
  const [s, setS] = useState<S>(EMPTY)
  const gen = useRef(0) // a newer load makes older responses stale
  const more = useRef(false)
  const sRef = useRef(s)
  sRef.current = s
  const viewRef = useRef(view)
  viewRef.current = view

  const fetchPages = useCallback(
    async (pages: number) => {
      const mine = ++gen.current
      more.current = false
      try {
        let cursor: string | undefined
        let out: RecordList | undefined
        const records: TableRecord[] = []
        const links: Links = {}
        for (let i = 0; i < pages; i++) {
          const r = await listRecords(ws, tableId, { ...viewRef.current, limit: PAGE, cursor })
          if (mine !== gen.current) return
          out = r
          records.push(...r.records)
          Object.assign(links, r.links)
          if (!r.next_cursor) break
          cursor = r.next_cursor
        }
        if (!out) return
        setS({
          records,
          links,
          groups: out.groups,
          total: out.total,
          cursor: out.next_cursor,
          status: 'ready',
        })
      } catch (e) {
        if (mine === gen.current) setS((p) => ({ ...p, status: 'error', error: e as Error }))
      }
    },
    [ws, tableId],
  )

  // biome-ignore lint/correctness/useExhaustiveDependencies: `key` is the restart signal
  useEffect(() => {
    setS(EMPTY)
    void fetchPages(1)
    return () => {
      gen.current++
    }
  }, [fetchPages, key])

  const loadMore = useCallback(async () => {
    const cur = sRef.current
    if (!cur.cursor || more.current || cur.status !== 'ready') return
    more.current = true
    const mine = gen.current
    try {
      const r = await listRecords(ws, tableId, { ...viewRef.current, limit: PAGE, cursor: cur.cursor })
      if (mine !== gen.current) return
      setS((p) => {
        const seen = new Set(p.records.map((x) => x.id))
        return {
          ...p,
          records: [...p.records, ...r.records.filter((x) => !seen.has(x.id))],
          links: { ...p.links, ...r.links },
          groups: r.groups ?? p.groups,
          cursor: r.next_cursor,
        }
      })
    } catch (e) {
      if (mine === gen.current) setS((p) => ({ ...p, error: e as Error, status: 'error' }))
    } finally {
      more.current = false
    }
  }, [ws, tableId])

  /** Refetch what is loaded (window focus, after a paste). */
  const refresh = useCallback(
    () => fetchPages(Math.min(MAX_REFRESH_PAGES, Math.max(1, Math.ceil(sRef.current.records.length / PAGE)))),
    [fetchPages],
  )
  useEffect(() => {
    const on = () => void refresh()
    window.addEventListener('focus', on)
    return () => window.removeEventListener('focus', on)
  }, [refresh])

  /** Own write: replace the record with the server's copy, or append a new one. */
  const put = useCallback((r: TableRecord, chips?: Record<string, LinkChip[]>, add = false) => {
    setS((p) => {
      const has = p.records.some((x) => x.id === r.id)
      return {
        ...p,
        records: has ? p.records.map((x) => (x.id === r.id ? r : x)) : [...p.records, r],
        links: chips ? { ...p.links, [r.id]: chips } : p.links,
        total: !has && add ? p.total + 1 : p.total,
      }
    })
  }, [])

  return { ...s, loadMore, refresh, put }
}
