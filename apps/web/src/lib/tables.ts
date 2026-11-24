import { cmd } from './api'

// Mirrors the output schemas in apps/api/internal/modules/tables/tables.go (OutputSchema). Keep both in step.

export type FieldType =
  | 'text'
  | 'long_text'
  | 'number'
  | 'percent'
  | 'currency'
  | 'date'
  | 'checkbox'
  | 'single_select'
  | 'multi_select'
  | 'phone'
  | 'email'
  | 'url'
  | 'user'
  | 'attachment'
  | 'link'
  | 'autonumber'
  | 'created_time'
  | 'modified_time'
  | 'created_by'
  | 'modified_by'
export type Role = 'read' | 'write' | 'manage'
export type SelectOption = { id: string; name: string; color: string }
// Per-type config, stored on the field: number/percent/currency {decimals}, selects {options}, user {multiple},
// link {table_id, multiple, inverse_of?}, autonumber {prefix}.
export type FieldConfig = {
  decimals?: number
  options?: SelectOption[]
  multiple?: boolean
  table_id?: string
  inverse_of?: string
  prefix?: string
}
export type Field = {
  id: string
  table_id: string
  name: string
  type: FieldType
  config: FieldConfig
  position: number
  locked: boolean
}
export type NewField = { name: string; type: FieldType; config?: FieldConfig }

export type RuleOp = string // see design section 4; one set per field type
export type Rule = { field: string; op: RuleOp; value?: unknown }
export type Filter = { match: 'and' | 'or'; rules: Rule[] }
export type Sort = { field: string; desc?: boolean }
export type ViewConfig = {
  v?: 1
  filters?: Filter
  sorts?: Sort[]
  group?: { field: string } | null
  order?: string[]
  hidden?: string[]
  widths?: Record<string, number>
  row_height?: 'short' | 'medium' | 'tall'
  kanban?: { stack: string; card_fields: string[] }
}
export type TableView = {
  id: string
  table_id: string
  name: string
  type: 'grid' | 'kanban'
  config: ViewConfig
  position: number
}
export type Table = {
  id: string
  base_id: string
  name: string
  position: number
  primary_field_id: string
  fields: Field[]
  views: TableView[]
}
export type Base = {
  id: string
  space_id: string
  name: string
  icon: string | null
  template: string | null
  record_count: number
  table_count: number
  created_at: string
  updated_at: string
}
export type BaseLimits = {
  records_per_base: number
  tables_per_base: number
  fields_per_table: number
  records_per_table: number
  views_per_table: number
}
export type BaseDetail = {
  base: Base
  tables: Table[]
  role: Role
  limits: BaseLimits
  usage: { records: number; tables: number }
}

export type LinkChip = { id: string; title: string }
export type TableRecord = {
  id: string
  table_id: string
  seq: number
  data: Record<string, unknown>
  created_at: string
  updated_at: string
  created_by: string | null
  updated_by: string | null
}
export type TableRecordWithLinks = TableRecord & { links: Record<string, LinkChip[]> }
export type RecordList = {
  records: TableRecord[]
  links: Record<string, Record<string, LinkChip[]>> // record id -> link field id -> chips
  groups?: { value: unknown; count: number }[]
  next_cursor: string | null
  total: number
}
export type BatchResult = { count: number; batch_id: string | null }
export type CellError = { row: number; col: number; reason: string }
export type HistoryEntry = {
  id: number
  actor_type: string
  actor_id: string | null
  at: string
  change: Record<string, [unknown, unknown]>
}
export type ConversionPreview = {
  kept: number
  changed: number
  cleared: number
  samples: { seq: number; from: unknown; to: unknown }[]
}
export type TableTemplate = { key: string; name: string; description: string; tables: string[] }
export type ImportColumn = {
  index: number
  header: string
  samples: string[]
  field_id: string | null
  new_field: NewField | null
}
export type ImportMapping = {
  columns: { index: number; field_id?: string; new_field?: NewField; skip?: boolean }[]
}
export type ImportPreview = {
  columns: ImportColumn[]
  ready: number
  needs_fixing: number
  errors: CellError[]
}
export type ImportJob = {
  id: string
  status: 'queued' | 'running' | 'done' | 'failed'
  counts: { created: number; skipped: number; failed: number }
  error: string | null
}

export type ListParams = {
  view_id?: string
  filters?: Filter
  sorts?: Sort[]
  group?: { field: string }
  search?: string
  cursor?: string
  limit?: number
}
export type PasteRow = { id?: string; cells: string[] }

// Bases
export const listBases = (ws: string, space_id?: string) => cmd<Base[]>(ws, 'base.list', { space_id })
export const getBase = (ws: string, id: string) => cmd<BaseDetail>(ws, 'base.get', { id })
export const createBase = (ws: string, space_id: string, name: string, template?: string) =>
  cmd<Base>(ws, 'base.create', { space_id, name, template })
export const updateBase = (ws: string, id: string, patch: { name?: string; icon?: string }) =>
  cmd<Base>(ws, 'base.update', { id, ...patch })
export const deleteBase = (ws: string, id: string) => cmd<Record<string, never>>(ws, 'base.delete', { id })
export const restoreBase = (ws: string, id: string) => cmd<Record<string, never>>(ws, 'base.restore', { id })
export const listTemplates = (ws: string) => cmd<TableTemplate[]>(ws, 'table_template.list', {})

// Tables
export const getTable = (ws: string, id: string) => cmd<Table>(ws, 'table.get', { id })
export const createTable = (ws: string, base_id: string, name: string, fields?: NewField[]) =>
  cmd<Table>(ws, 'table.create', { base_id, name, fields })
export const updateTable = (ws: string, id: string, patch: { name?: string; position?: number }) =>
  cmd<Table>(ws, 'table.update', { id, ...patch })
export const deleteTable = (ws: string, id: string) => cmd<Record<string, never>>(ws, 'table.delete', { id })
export const restoreTable = (ws: string, id: string) =>
  cmd<Record<string, never>>(ws, 'table.restore', { id })

// Fields
export const createField = (
  ws: string,
  table_id: string,
  f: NewField & { position?: number; inverse?: boolean },
) => cmd<Field>(ws, 'field.create', { table_id, ...f })
export const updateField = (
  ws: string,
  id: string,
  patch: {
    name?: string
    config?: FieldConfig
    position?: number
    locked?: boolean
    type?: FieldType
    confirm?: boolean
  },
) => cmd<Field | ConversionPreview>(ws, 'field.update', { id, ...patch })
export const deleteField = (ws: string, id: string) => cmd<Record<string, never>>(ws, 'field.delete', { id })
export const restoreField = (ws: string, id: string) =>
  cmd<Record<string, never>>(ws, 'field.restore', { id })

// Records
export const listRecords = (ws: string, table_id: string, p: ListParams = {}) =>
  cmd<RecordList>(ws, 'record.list', { table_id, ...p })
export const getRecord = (ws: string, id: string) => cmd<TableRecordWithLinks>(ws, 'record.get', { id })
export const createRecord = (
  ws: string,
  table_id: string,
  values: Record<string, unknown>,
  links?: Record<string, string[]>,
) => cmd<TableRecordWithLinks>(ws, 'record.create', { table_id, values, links })
export const createRecords = (
  ws: string,
  table_id: string,
  records: { values: Record<string, unknown>; links?: Record<string, string[]> }[],
) =>
  cmd<{ count: number; ids: string[]; batch_id: string | null }>(ws, 'record.create_many', {
    table_id,
    records,
  }) // at most 500
export const updateRecord = (
  ws: string,
  id: string,
  patch: { values?: Record<string, unknown>; clear?: string[]; links?: Record<string, string[]> },
) => cmd<TableRecordWithLinks>(ws, 'record.update', { id, ...patch })
export const bulkUpdateRecords = (
  ws: string,
  table_id: string,
  updates: { id: string; values?: Record<string, unknown>; clear?: string[] }[],
) => cmd<{ updated: number }>(ws, 'record.bulk_update', { table_id, updates }) // at most 1,000
export const pasteRecords = (ws: string, table_id: string, field_ids: string[], rows: PasteRow[]) =>
  cmd<{ updated: number; created: number; errors: CellError[] }>(ws, 'record.paste', {
    table_id,
    field_ids,
    rows,
  }) // 1,000 rows x 50 columns
export const deleteRecords = (ws: string, ids: string[]) => cmd<BatchResult>(ws, 'record.delete', { ids })
export const restoreRecords = (ws: string, ids: string[]) => cmd<BatchResult>(ws, 'record.restore', { ids })
export const searchRecords = (ws: string, table_id: string, q: string, limit?: number) =>
  cmd<{ id: string; title: string; preview: string[] }[]>(ws, 'record.search', { table_id, q, limit })
export const recordHistory = (ws: string, id: string) => cmd<HistoryEntry[]>(ws, 'record.history', { id })
export const listRecordBatch = (ws: string, batch_id: string) =>
  cmd<{ op: 'created' | 'updated' | 'deleted' | 'restored'; table_id: string; ids: string[] }>(
    ws,
    'record.batch_list',
    { batch_id },
  )

// Views
export const createView = (
  ws: string,
  table_id: string,
  name: string,
  type: 'grid' | 'kanban',
  config?: ViewConfig,
) => cmd<TableView>(ws, 'table_view.create', { table_id, name, type, config })
export const updateView = (
  ws: string,
  id: string,
  patch: { name?: string; config?: ViewConfig; position?: number },
) => cmd<TableView>(ws, 'table_view.update', { id, ...patch })
export const deleteView = (ws: string, id: string) =>
  cmd<Record<string, never>>(ws, 'table_view.delete', { id })

// CSV import: the file goes to POST /api/tables/import first, which returns the handle used here.
export const previewImport = (ws: string, handle: string, mapping?: ImportMapping) =>
  cmd<ImportPreview>(ws, 'table_import.preview', { handle, mapping })
export const runImport = (ws: string, handle: string, mapping?: ImportMapping) =>
  cmd<ImportJob>(ws, 'table_import.run', { handle, mapping })
export const importStatus = (ws: string, id: string) => cmd<ImportJob>(ws, 'table_import.status', { id })
