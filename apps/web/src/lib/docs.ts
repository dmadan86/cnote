import { cmd } from './api'

// Docs parity A1 (docs/design/docs-parity.md 5.4, 7.2, 7.6). Mirrors the output schemas in
// apps/api/internal/modules/notes/a1_commands.go (A1OutputSchema). Keep both in step. The commands answer
// "not implemented" until their work package (7.8) lands.

export type PageStatus = 'draft' | 'in_review' | 'final'
export type NoteListKind = 'recent' | 'starred' | 'watching'

/** A row of note.mine; `at` is when the caller opened, starred or began watching the page. */
export type PageRef = {
  id: string
  space_id: string
  title: string
  icon: string | null
  status: PageStatus | null
  updated_at: string
  at: string
}
export type LabelCount = { label: string; count: number }

/** Fields note.update gains in A1. A body save also needs expected_epoch and schema (DOC_SCHEMA, lib/doc-schema.ts). */
export type NoteUpdateA1 = { status?: PageStatus | null; icon?: string | null; wide?: boolean }
/** Filters search.notes gains in A1 (W2); `q` may be empty when one is given. */
export type SearchFiltersA1 = {
  space_id?: string
  owner_id?: string
  label?: string
  updated_after?: string
  title_only?: boolean
  status?: PageStatus
}

export const labelSet = (ws: string, note_id: string, labels: string[]) =>
  cmd<{ note_id: string; labels: string[] }>(ws, 'label.set', { note_id, labels })
export const labelList = (ws: string, prefix?: string, limit?: number) =>
  cmd<LabelCount[]>(ws, 'label.list', { prefix, limit })
export const noteStar = (ws: string, note_id: string, starred: boolean) =>
  cmd<{ note_id: string; starred: boolean }>(ws, 'note.star', { note_id, starred })
export const noteWatch = (ws: string, note_id: string, watching: boolean) =>
  cmd<{ note_id: string; watching: boolean }>(ws, 'note.watch', { note_id, watching })
export const noteViewed = (ws: string, note_id: string) =>
  cmd<{ note_id: string; at: string }>(ws, 'note.viewed', { note_id })
export const noteMine = (ws: string, list: NoteListKind, limit?: number) =>
  cmd<PageRef[]>(ws, 'note.mine', { list, limit })

/** The A1 fields note.get and note.list return beside the Note type in lib/api.ts. */
export type NoteChrome = { status: PageStatus | null; icon: string | null; wide: boolean }
export const STATUS_LABEL: Record<PageStatus, string> = {
  draft: 'Draft',
  in_review: 'In review',
  final: 'Final',
}
export const STATUS_TONE: Record<PageStatus, 'neutral' | 'warn' | 'ok'> = {
  draft: 'neutral',
  in_review: 'warn',
  final: 'ok',
}
/** Sidebar lists and the home page listen for this to reload after a star or an opening. */
export const MINE_EVENT = 'cnote:mine'
