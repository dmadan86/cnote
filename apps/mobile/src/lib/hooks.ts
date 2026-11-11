import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from './api'
import { useSession } from './session'

/** Run a mutation-style command; surfaces ApiError message and expires the session on 401. */
export function useRun() {
  const { api, wsId, expire } = useSession()
  return useCallback(
    async <T>(name: string, input: unknown = {}) => {
      try {
        return await api.cmd<T>(wsId as string, name, input)
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) expire()
        throw e
      }
    },
    [api, wsId, expire],
  )
}

/** Fetch a command's result; refetches when the input changes. `refresh` is for pull-to-refresh (keeps data on screen). */
export function useCmd<T>(name: string, input: unknown = {}, enabled = true) {
  const run = useRun()
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(enabled)
  const [refreshing, setRefreshing] = useState(false)
  const key = JSON.stringify(input)
  const seq = useRef(0)

  const load = useCallback(
    async (soft: boolean) => {
      const n = ++seq.current
      soft ? setRefreshing(true) : setLoading(true)
      try {
        const d = await run<T>(name, JSON.parse(key))
        if (n === seq.current) {
          setData(d)
          setError(null)
        }
      } catch (e: any) {
        if (n === seq.current) setError(e?.message ?? 'Something went wrong')
      } finally {
        if (n === seq.current) {
          setLoading(false)
          setRefreshing(false)
        }
      }
    },
    [run, name, key],
  )

  useEffect(() => {
    if (enabled) void load(false)
  }, [enabled, load])

  return { data, error, loading, refreshing, refetch: () => load(true), reload: () => load(false) }
}

export const errMsg = (e: unknown) => (e instanceof Error ? e.message : 'Something went wrong')
