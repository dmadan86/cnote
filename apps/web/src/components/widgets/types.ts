import type { ReactNode } from 'react'
import type { Summary } from '@/components/home/dashboard'
import type { RunResult, Spec } from '@/components/reports/lib'

export type Size = 's' | 'm' | 'l'
export type WidgetCfg = { id: string; type: string; size: Size }
export type GridConfig = { v: 1; widgets: WidgetCfg[] }
export const MAX_WIDGETS = 24

/** What a widget needs to know about the person and the page. Access is only a hint: the server decides. */
export type Ctx = {
  ws: string
  base: string
  role: string
  finance: boolean
  modules: string[] | null
  summary: Summary
  branch: string
}

export type RunState = { state: 'loading' | 'ok' | 'denied' | 'failed'; result?: RunResult }
export type WidgetProps = { size: Size; ctx: Ctx; runs: RunState[]; nonce: number }

export type WidgetDef = {
  type: string
  title: string
  group: 'Money' | 'Sales' | 'Work' | 'People' | 'Website' | 'Shortcuts'
  /** First is the default. */
  sizes: Size[]
  /** The exact server command(s) this widget calls (finance access is enforced there, not here). */
  command: string
  /** Gallery and render filter; hiding is cosmetic. */
  available: (c: Ctx) => boolean
  /** report.run_many entries; results arrive in `runs`, in the same order. */
  runs?: (c: Ctx) => Spec[]
  /** Skeleton height while loading. */
  height?: 'kpi' | 'chart' | 'list'
  render: (p: WidgetProps) => ReactNode
}
