'use client'
import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react'
import { useShell } from '@/components/shell'
import { cmd } from './api'

/**
 * Per-user, per-workspace screen config (docs/design/home-dashboard-widgets.md, sections 4 and 16).
 * Every backend call lives in `server` below, so a change to the command shapes touches one place.
 *
 * Commands: ui_config.get {screen} -> {config: object|null, updated_at}
 *           ui_config.set {screen, config} -> {updated_at}
 *           ui_config.reset {screen} -> {}
 */
const server = {
  get: (ws: string, screen: string) => cmd<{ config: unknown | null }>(ws, 'ui_config.get', { screen }),
  set: (ws: string, screen: string, config: unknown) => cmd(ws, 'ui_config.set', { screen, config }),
  reset: (ws: string, screen: string) => cmd(ws, 'ui_config.reset', { screen }),
}

const SAVE_DELAY_MS = 500

type Waiter = { resolve: () => void; reject: (e: Error) => void }
type Entry = {
  ws: string
  screen: string
  config?: unknown // parsed config being shown (optimistic)
  saved?: unknown // last config the server confirmed
  customised: boolean
  locked: boolean // stored config is from a newer app version: shown as default, never overwritten
  ready: boolean
  saving: boolean
  error?: Error
  seq: number // write sequence: a newer save or reset supersedes older ones
  timer?: ReturnType<typeof setTimeout>
  pending?: unknown
  waiters: Waiter[]
  chain: Promise<unknown> // serialises writes for this screen
  listeners: Set<() => void>
  loading?: boolean
  rev: number
}

// Caches are keyed by user, workspace, role, finance flag and screen, and dropped when the user or workspace changes.
const entries = new Map<string, Entry>()
const dependants: Map<string, unknown>[] = []
let scopeNow = ''
/** Other module-level caches (widget data) register here so they are cleared with the config cache. */
export const registerScopedCache = (m: Map<string, unknown>) => dependants.push(m)
function guardScope(userWs: string) {
  if (scopeNow === userWs) return
  scopeNow = userWs
  entries.clear()
  for (const m of dependants) m.clear()
}

const emit = (e: Entry) => {
  e.rev++
  for (const l of e.listeners) l()
}
const settle = (e: Entry, err?: Error) => {
  const w = e.waiters
  e.waiters = []
  for (const x of w) err ? x.reject(err) : x.resolve()
}

function writeNow(e: Entry) {
  if (e.timer) clearTimeout(e.timer)
  e.timer = undefined
  if (e.pending === undefined) return
  const cfg = e.pending
  e.pending = undefined
  const mine = e.seq
  const waiters = e.waiters
  e.waiters = []
  e.saving = true
  emit(e)
  e.chain = e.chain
    .catch(() => {})
    .then(() => server.set(e.ws, e.screen, cfg))
    .then(
      () => {
        e.customised = true
        e.saved = cfg
        if (mine === e.seq) {
          e.saving = false
          e.error = undefined
        }
        emit(e)
        for (const w of waiters) w.resolve()
      },
      (err: Error) => {
        // a late failure never rolls back a newer save
        if (mine === e.seq) {
          e.saving = false
          e.config = e.saved
          e.error = err
        }
        emit(e)
        for (const w of waiters) w.reject(err)
      },
    )
}

function load<T extends { v: number }>(e: Entry, defaults: T, parse: (raw: unknown, defaults: T) => T) {
  if (e.ready || e.loading) return
  e.loading = true
  server.get(e.ws, e.screen).then(
    (r) => {
      e.loading = false
      if (e.seq > 0) {
        // the user already changed something while loading: keep their change
        e.ready = true
        return emit(e)
      }
      const raw = r.config
      const newer =
        !!raw &&
        typeof raw === 'object' &&
        typeof (raw as { v?: unknown }).v === 'number' &&
        (raw as { v: number }).v > defaults.v
      e.locked = newer
      e.customised = raw != null
      e.saved = e.config = raw == null || newer ? defaults : parse(raw, defaults)
      e.ready = true
      emit(e)
    },
    (err: Error) => {
      e.loading = false
      e.error = err
      e.saved = e.config = defaults // show the default rather than a blank screen
      e.ready = true
      emit(e)
    },
  )
}

export type ScreenConfig<T> = {
  config: T
  ready: boolean
  customised: boolean
  /** Stored config is from a newer app version; editing is off so it is not overwritten. */
  locked: boolean
  saving: boolean
  error?: Error
  /** Updates at once; writes after a short pause (or now with `immediate`). Resolves when the write settles. */
  save: (next: T, immediate?: boolean) => Promise<void>
  /** Back to the screen's default (deletes the stored row). */
  reset: () => Promise<void>
  clearError: () => void
}

export function useScreenConfig<T extends { v: number }>(
  screen: string,
  defaults: T,
  parse: (raw: unknown, defaults: T) => T,
  scope = '',
): ScreenConfig<T> {
  const { ws, me } = useShell()
  const role = me.workspaces.find((w) => w.id === ws)?.role ?? ''
  guardScope(`${me.user.id}|${ws}`)
  const key = `${me.user.id}|${ws}|${role}|${scope}|${screen}`
  let e = entries.get(key)
  if (!e) {
    e = {
      ws,
      screen,
      customised: false,
      locked: false,
      ready: false,
      saving: false,
      seq: 0,
      rev: 0,
      waiters: [],
      chain: Promise.resolve(),
      listeners: new Set(),
    }
    entries.set(key, e)
  }
  const entry = e
  useSyncExternalStore(
    (l) => {
      entry.listeners.add(l)
      return () => entry.listeners.delete(l)
    },
    () => entry.rev,
    () => 0,
  )
  // the load needs the latest defaults/parse; run it after mount, once per cache entry
  const d = useRef({ defaults, parse })
  d.current = { defaults, parse }
  useEffect(() => load(entry, d.current.defaults, d.current.parse), [entry])
  // leaving the screen writes what is pending; the entry outlives the component so the write still completes
  useEffect(() => () => writeNow(entry), [entry])

  const save = useCallback(
    (next: T, immediate = false) => {
      if (entry.locked) return Promise.resolve()
      entry.seq++
      entry.config = next
      entry.pending = next
      entry.error = undefined
      const p = new Promise<void>((resolve, reject) => entry.waiters.push({ resolve, reject }))
      if (entry.timer) clearTimeout(entry.timer)
      entry.timer = setTimeout(() => writeNow(entry), immediate ? 0 : SAVE_DELAY_MS)
      emit(entry)
      return p
    },
    [entry],
  )
  const reset = useCallback(async () => {
    entry.seq++
    if (entry.timer) clearTimeout(entry.timer)
    entry.timer = undefined
    entry.pending = undefined
    settle(entry) // superseded saves resolve: the reset wins
    const before = entry.config
    entry.config = d.current.defaults
    entry.error = undefined
    entry.saving = true
    emit(entry)
    const mine = entry.seq
    const run = entry.chain.catch(() => {}).then(() => server.reset(entry.ws, entry.screen))
    entry.chain = run
    try {
      await run
      entry.saved = d.current.defaults
      entry.customised = false
      entry.locked = false
    } catch (err) {
      if (mine === entry.seq) {
        entry.config = before // what is shown stays what is stored
        entry.error = err as Error
      }
      throw err
    } finally {
      if (mine === entry.seq) entry.saving = false
      emit(entry)
    }
  }, [entry])
  const clearError = useCallback(() => {
    entry.error = undefined
    emit(entry)
  }, [entry])

  return {
    config: (entry.config as T | undefined) ?? defaults,
    ready: entry.ready,
    customised: entry.customised,
    locked: entry.locked,
    saving: entry.saving,
    error: entry.error,
    save,
    reset,
    clearError,
  }
}

/**
 * Puts one widget at the top of a screen's saved layout without touching the rest, and updates any open copy of that
 * layout. Does nothing when the person has no saved layout (they see the default, which the caller owns), when the
 * widget is already in it, or when the layout is from a newer app version.
 */
export async function prependWidget(
  ws: string,
  screen: string,
  widget: { id: string; type: string; size: string },
) {
  const cfg = (await server.get(ws, screen)).config as { v?: number; widgets?: { type?: string }[] } | null
  if (!cfg || cfg.v !== 1 || !Array.isArray(cfg.widgets) || cfg.widgets.some((w) => w?.type === widget.type))
    return
  const next = { ...cfg, widgets: [widget, ...cfg.widgets] }
  await server.set(ws, screen, next)
  for (const e of entries.values()) {
    if (e.ws !== ws || e.screen !== screen) continue
    e.seq++
    e.config = e.saved = next
    e.customised = true
    emit(e)
  }
}
