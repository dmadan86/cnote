'use client'
import { useEffect, useReducer, useRef } from 'react'
import type { RunResult, Spec } from '@/components/reports/lib'
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
 * Runs the specs of every visible widget. Identical specs are sent once, calls are chunked to 12 runs, each
 * chunk resolves on its own so a card renders as soon as its own runs are back. Results are cached 60 s and
 * shown stale while a refresh runs (a changed `nonce` forces a refresh).
 */
export function useRuns(ws: string, specs: Spec[], nonce: number) {
  const [, bump] = useReducer((n: number) => n + 1, 0)
  const keys = specs.map((s) => `${ws}|${JSON.stringify(s)}`)
  const sig = keys.join('\n')
  const seen = useRef(nonce)
  // biome-ignore lint/correctness/useExhaustiveDependencies: sig stands in for specs
  useEffect(() => {
    const force = seen.current !== nonce
    seen.current = nonce
    const todo = [...new Set(keys)].filter((k) => {
      const h = cache.get(k)
      return force || !h || Date.now() - h.at > TTL_MS
    })
    let live = true
    for (let i = 0; i < todo.length; i += MAX_RUNS) {
      const chunk = todo.slice(i, i + MAX_RUNS)
      runMany(
        ws,
        chunk.map((k, j) => ({ key: `r${j}`, spec: JSON.parse(k.slice(ws.length + 1)) as Spec })),
      )
        .then((out) =>
          chunk.forEach((k, j) => {
            const r = out.results?.[`r${j}`]
            cache.set(
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
          for (const k of chunk) cache.set(k, { at: Date.now(), state: denied(e) ? 'denied' : 'failed' })
        })
        .finally(() => live && bump())
    }
    return () => {
      live = false
    }
  }, [ws, sig, nonce])
  return keys.map((k): RunState => {
    const h = cache.get(k)
    return h ? { state: h.state, result: h.data as RunResult | undefined } : { state: 'loading' }
  })
}

/** One command for a widget that is not a report run (ageing, website stats). Same cache and stale rule. */
export function useFetched<T>(ws: string, key: string, load: () => Promise<T>, nonce: number, on = true) {
  const [, bump] = useReducer((n: number) => n + 1, 0)
  const k = `${ws}|fetch|${key}`
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
      .then((data) => cache.set(k, { at: Date.now(), state: 'ok', data }))
      .catch((e) => cache.set(k, { at: Date.now(), state: denied(e) ? 'denied' : 'failed' }))
      .finally(() => live && bump())
    return () => {
      live = false
    }
  }, [k, nonce, on])
  const h = cache.get(k)
  return { state: (h?.state ?? 'loading') as RunState['state'], data: h?.data as T | undefined }
}
