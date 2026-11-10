'use client'
import { useCallback, useEffect, useState } from 'react'
import { cmd } from './api'

/** Tiny fetch hook: runs a command, re-runs on key change or reload(). */
export function useCmd<T>(ws: string, name: string, input: unknown = {}, enabled = true) {
  const [data, setData] = useState<T>()
  const [error, setError] = useState<Error>()
  const [tick, setTick] = useState(0)
  const key = JSON.stringify(input)
  // biome-ignore lint/correctness/useExhaustiveDependencies: key stands in for input
  useEffect(() => {
    if (!enabled) return
    let live = true
    cmd<T>(ws, name, JSON.parse(key))
      .then((d) => {
        if (!live) return
        setData(d)
        setError(undefined)
      })
      .catch((e) => live && setError(e))
    return () => {
      live = false
    }
  }, [ws, name, key, tick, enabled])
  const reload = useCallback(() => setTick((t) => t + 1), [])
  return { data, error, reload, loading: !data && !error }
}

export const ago = (iso: string) => {
  const s = (Date.now() - new Date(iso).getTime()) / 1000
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

export const initials = (name?: string | null) =>
  (name ?? '?')
    .split(/\s+/)
    .map((p) => p[0])
    .slice(0, 2)
    .join('')
    .toUpperCase()
