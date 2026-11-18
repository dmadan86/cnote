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

// ---- data imports (contacts, items, opening documents, Tally): data_import.* commands ----
export type DataKind = 'contacts' | 'items' | 'opening_docs' | 'tally'
export type RowStatus = 'ready' | 'fix' | 'existing'
export type TemplateField = { key: string; label: string; required?: boolean; example?: string }
export type Template = { fields: TemplateField[]; sample_csv: string }
export type PreviewColumn = { header: string; samples: string[]; field?: string }
export type PreviewRow = {
  row_no: number
  status: RowStatus
  reason?: string
  values?: Record<string, string>
}
export type Preview = {
  columns: PreviewColumn[]
  counts: Record<RowStatus, number>
  rows: PreviewRow[]
  tally?: {
    found: Record<'customers' | 'vendors' | 'ledgers' | 'items' | 'units' | 'godowns', number>
    warnings: string[]
  }
}
export type DataJob = {
  id: string
  kind: DataKind
  status: 'preview' | 'queued' | 'running' | 'done' | 'failed' | 'undone'
  total?: number
  processed?: number
  added?: number
  skipped?: number
  failed?: number
  error?: string
  created_at: string
  undo_until?: string
}
