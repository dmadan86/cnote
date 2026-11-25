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

// ---- Docs parity A2: page lifecycle (docs/design/docs-parity.md 7.5 and 7.9). Mirrors A2OutputSchema in
// apps/api/internal/modules/notes/a2_commands.go. ----

/** The one message every refusal of move, reorder, delete and duplicate carries; it never names a hidden page. */
export const RESTRICTED_MESSAGE = 'Some pages here are restricted'
/** Close code the collab gateway sends when the page was moved to the trash. Show "This page was moved to the trash". */
export const CLOSE_DELETED = 4411

export type RestoreTarget = 'original' | 'space_root' | 'blocked'
export type TrashEntry = {
  deletion_id: string
  space_id: string
  root: { id: string; title: string; icon: string | null }
  page_count: number
  deleted_at: string
  deleted_by: { user_id: string; name: string } | null
  restore_to: RestoreTarget
  blocked_reason: string | null
}

/** Move to a new parent (null = space root), before a sibling (omitted or null = last). Same space only. */
export const noteMove = (ws: string, id: string, parent_id: string | null, before_id?: string | null) =>
  cmd<{ id: string; space_id: string; parent_id: string | null; position: number }>(ws, 'note.move', {
    id,
    parent_id,
    before_id,
  })
/** Reorder among the current siblings; before_id null = last. */
export const noteReorder = (ws: string, id: string, before_id: string | null) =>
  cmd<{ id: string; parent_id: string | null; position: number }>(ws, 'note.reorder', { id, before_id })
export const noteDelete = (ws: string, id: string) =>
  cmd<{ deleted: true; deletion_id: string; count: number }>(ws, 'note.delete', { id })
export const noteTrash = (
  ws: string,
  o?: { space_id?: string; limit?: number; before?: string; before_id?: string },
) => cmd<TrashEntry[]>(ws, 'note.trash', o ?? {})
export const noteUndelete = (ws: string, deletion_id: string) =>
  cmd<{
    deletion_id: string
    root_id: string
    space_id: string
    count: number
    placed: 'original' | 'space_root'
  }>(ws, 'note.undelete', { deletion_id })
export const noteDuplicate = (ws: string, id: string) =>
  cmd<{
    id: string
    space_id: string
    parent_id: string | null
    title: string
    position: number
    attachments_copied: number
  }>(ws, 'note.duplicate', { id })

/** Tree order of siblings (7.9): position, then created_at, then id. A page whose parent is in the trash shows at the space root. */
export const bySiblingOrder = (
  a: { position?: number; created_at?: string; id: string },
  b: { position?: number; created_at?: string; id: string },
) =>
  (a.position ?? 0) - (b.position ?? 0) ||
  (a.created_at ?? '').localeCompare(b.created_at ?? '') ||
  a.id.localeCompare(b.id)
/** Window event: the tree, Trash and the open page reload after a move, delete, restore or duplicate. */
export const TREE_EVENT = 'cnote:tree'
