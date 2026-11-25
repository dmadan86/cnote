import { cmd } from '@/lib/api'
import { applyBusinessType } from '@/lib/business-types'

/** The first-run wizard's step 3: one choice, saved as a Home ordering hint, then that flow opens. */
export type Goal = {
  id: string
  label: string
  path: string
  widgets: readonly string[]
  /** The module the flow lives in; the choice is hidden when it is uninstalled. */
  module?: string
  /** Needs finance access, like invoice.create_draft. */
  finance?: true
}
export const GOALS: readonly Goal[] = [
  {
    id: 'invoice',
    label: 'Send an invoice',
    path: '/billing?new=1',
    module: 'billing',
    finance: true,
    widgets: ['kpi.sales', 'chart.sales', 'tile.payments'],
  },
  {
    id: 'customers',
    label: 'Add customers',
    path: '/crm?new=1',
    module: 'crm',
    widgets: ['kpi.deals', 'chart.pipeline', 'tile.inquiries'],
  },
  { id: 'website', label: 'Build a website', path: '/site', module: 'site', widgets: ['kpi.site'] },
  { id: 'table', label: 'Make a table', path: '/tables?new=1', module: 'tables', widgets: [] },
  { id: 'import', label: 'Import from Tally, Excel or CSV', path: '/import', widgets: [] },
]

/** Saves that did not go through. They are kept for one tab so Home can offer "Try again". */
export type Pending = {
  type?: { id: string }
  legal_name?: string
  company?: Record<string, string>
  goal?: string
  complete?: boolean
}

const key = (ws: string) => `cnote:onboarding:${ws}`
export const readPending = (ws: string): Pending => {
  try {
    return JSON.parse(sessionStorage.getItem(key(ws)) ?? '{}')
  } catch {
    return {}
  }
}
export const writePending = (ws: string, p: Pending) => {
  try {
    if (Object.keys(p).length) sessionStorage.setItem(key(ws), JSON.stringify(p))
    else sessionStorage.removeItem(key(ws))
  } catch {}
}

/**
 * Runs every save in `p`, in order, each on its own, and returns the ones that did not complete (with the first error).
 * `signal` stops the run: an aborted request and every step after it count as not done, so they stay in `left`.
 * `onDone` is called for each step that finished while the run was still current, never after an abort, so a
 * superseded run cannot clear values that a newer run still has to save.
 */
export async function runPending(
  ws: string,
  p: Pending,
  signal?: AbortSignal,
  onDone?: (name: keyof Pending) => void,
): Promise<{ left: Pending; error?: Error }> {
  const left: Pending = {}
  let error: Error | undefined
  const run = async (name: keyof Pending, fn: () => Promise<unknown>) => {
    if (p[name] === undefined) return
    try {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
      await fn()
      if (!signal?.aborted) onDone?.(name)
    } catch (e) {
      error ??= e as Error
      Object.assign(left, { [name]: p[name] })
    }
  }
  await run('type', () => applyBusinessType(ws, (p.type as { id: string }).id, {}, signal))
  await run('legal_name', () => cmd(ws, 'company.update', { legal_name: p.legal_name }, signal))
  await run('company', () => cmd(ws, 'company.update', p.company, signal))
  await run('goal', () => cmd(ws, 'workspace.update', { settings: { first_goal: p.goal } }, signal))
  await run('complete', () => cmd(ws, 'onboarding.complete', {}, signal))
  return { left, error }
}

/** GSTIN shape from the design: 2 digits, 5 letters, 4 digits, a letter, 1-9 or a letter, Z, then a check character. */
export const GSTIN = /^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$/
