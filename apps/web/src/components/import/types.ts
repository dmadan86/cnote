export type Job = {
  id: string
  source: string
  status: 'queued' | 'running' | 'done' | 'failed'
  cursor: { phase?: string; offset?: number }
  report: {
    imported?: number
    updated?: number
    skipped?: number
    comments?: number
    relations?: number
    warnings?: Record<string, number>
  }
  created_at: string
}
