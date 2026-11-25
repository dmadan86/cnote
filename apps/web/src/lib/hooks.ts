'use client'
import { usePathname, useRouter, useSearchParams } from 'next/navigation'
import { useCallback, useEffect, useRef, useState } from 'react'
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

/** Finance access of the signed-in person: the server's own check, the one `invoice.create_draft` is gated by. False until known. */
export function useFinance(ws: string) {
  return useCmd<{ finance: boolean }>(ws, 'access.me').data?.finance === true
}

/** `?new=1` opens a create dialog once (setup links use it), then drops the parameter. `ready` waits for what the dialog needs. */
export function useOpenOnNew(ready: boolean, open: () => void) {
  const wanted = useSearchParams().get('new') === '1'
  const router = useRouter()
  const path = usePathname()
  const fn = useRef(open)
  fn.current = open
  useEffect(() => {
    if (!wanted || !ready) return
    fn.current()
    router.replace(path)
  }, [wanted, ready, router, path])
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
