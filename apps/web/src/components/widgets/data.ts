'use client'
import { useEffect, useReducer, useRef } from 'react'
import type { RunResult, Spec } from '@/components/reports/lib'
import { useShell } from '@/components/shell'
import { CmdFailure, cmd } from '@/lib/api'
import { registerScopedCache } from '@/lib/screen-config'
import type { RunState } from './types'

const TTL_MS = 60_000
const MAX_RUNS = 12 // report.run_many limit per call

type Hit = { at: number; state: RunState['state']; data?: unknown }
const cache = new Map<string, Hit>()
registerScopedCache(cache as Map<string, unknown>)

const denied = (e: unknown) =>
  e instanceof CmdFailure && (e.code === 'forbidden' || e.status === 403 || e.status === 404)

/** All call sites of report.run_many live here. */
type ManyOut = { results: Record<string, { ok: true; result: RunResult } | { ok: false; code: string }> }
const runMany = (ws: string, runs: { key: string; spec: Spec }[]) =>
  cmd<ManyOut>(ws, 'report.run_many', { runs })

/**
 * Runs the specs of every visible widget: one report.run_many call per widget (calls in parallel), so a slow report
 * delays only its own card. Identical specs inside a widget are sent once. Results are cached 60 s per user and
 * workspace and shown stale while a refresh runs (a changed `nonce` forces a refresh). A response that arrives
 * after cleanup (logout, workspace switch, a newer run) is discarded and never written to the cache.
 */
export function useRuns(ws: string, groups: Spec[][], nonce: number): RunState[][] {
  const [, bump] = useReducer((n: number) => n + 1, 0)
  const uid = useShell().me.user.id
  const pre = `${uid}|${ws}|`
  const keys = groups.map((g) => g.map((s) => `${pre}${JSON.stringify(s)}`))
  const sig = keys.map((g) => g.join('\n')).join('\n\n')
  const seen = useRef(nonce)
  // biome-ignore lint/correctness/useExhaustiveDependencies: sig stands in for groups
  useEffect(() => {
    const force = seen.current !== nonce
    seen.current = nonce
    let live = true
    for (const g of keys) {
      const todo = [...new Set(g)].filter((k) => {
        const h = cache.get(k)
        return force || !h || Date.now() - h.at > TTL_MS
      })
      for (let i = 0; i < todo.length; i += MAX_RUNS) {
        const chunk = todo.slice(i, i + MAX_RUNS)
        const put = (k: string, h: Hit) => live && cache.set(k, h)
        runMany(
          ws,
          chunk.map((k, j) => ({ key: `r${j}`, spec: JSON.parse(k.slice(pre.length)) as Spec })),
        )
          .then((out) =>
            chunk.forEach((k, j) => {
              const r = out.results?.[`r${j}`]
              put(
                k,
                !r
                  ? { at: Date.now(), state: 'failed' }
                  : r.ok && !r.result.error
                    ? { at: Date.now(), state: 'ok', data: r.result }
                    : {
                        at: Date.now(),
                        state: !r.ok && (r.code === 'denied' || r.code === 'not_found') ? 'denied' : 'failed',
                      },
              )
            }),
          )
          .catch((e) => {
            for (const k of chunk) put(k, { at: Date.now(), state: denied(e) ? 'denied' : 'failed' })
          })
          .finally(() => live && bump())
      }
    }
    return () => {
      live = false
    }
  }, [ws, sig, nonce])
  return keys.map((g) =>
    g.map((k): RunState => {
      const h = cache.get(k)
      return h ? { state: h.state, result: h.data as RunResult | undefined } : { state: 'loading' }
    }),
  )
}

/** One command for a widget that is not a report run (ageing, website stats). Same cache and stale rule. */
export function useFetched<T>(ws: string, key: string, load: () => Promise<T>, nonce: number, on = true) {
  const [, bump] = useReducer((n: number) => n + 1, 0)
  const uid = useShell().me.user.id
  const k = `${uid}|${ws}|fetch|${key}`
  const seen = useRef(nonce)
  const fn = useRef(load)
  fn.current = load
  useEffect(() => {
    if (!on) return
    const force = seen.current !== nonce
    seen.current = nonce
    const h = cache.get(k)
    if (!force && h && Date.now() - h.at <= TTL_MS) return
    let live = true
    fn.current()
      .then((data) => live && cache.set(k, { at: Date.now(), state: 'ok', data }))
      .catch((e) => live && cache.set(k, { at: Date.now(), state: denied(e) ? 'denied' : 'failed' }))
      .finally(() => live && bump())
    return () => {
      live = false
    }
  }, [k, nonce, on])
  const h = cache.get(k)
  return { state: (h?.state ?? 'loading') as RunState['state'], data: h?.data as T | undefined }
}
