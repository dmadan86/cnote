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
export type NoteChrome = { status: PageStatus | null; icon: string | null; wide: boolean; cover?: Cover }
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
  /** When retention deletes it for good; null when the workspace keeps trash forever. */
  purge_at: string | null
  can_purge: boolean
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

// ---------------------------------------------------------------------------------------------------------------------
// Docs parity Phase B1 (docs/design/docs-parity-b.md sections 4 and 13). Mirrors the output schemas in
// apps/api/internal/modules/{notes,publiclinks,notifications}/b1_commands.go (B1OutputSchema). Keep both in step. Every
// command answers "not implemented" until its package (P1 to P5, section 13.4) lands.

/** Notification kinds that can reach an inbox (13.2.5); `shared` and `access_request` are new in B. */
export type MailKind = 'mention' | 'assigned' | 'shared' | 'access_request' | 'comment' | 'page_updated'
/** The message shown when a write is refused in an archived space (API ArchivedMessage, gateway ARCHIVED_MESSAGE). */
export const ARCHIVED_MESSAGE = 'This space is archived'

export type SpaceRole = 'admin' | 'editor' | 'viewer'
export type WorkspaceRole = 'owner' | 'admin' | 'member' | 'guest'
/** space.list, space.update and space.archive. */
export type SpaceB1 = {
  id: string
  key: string
  name: string
  template: string
  visibility: 'open' | 'private'
  config: Record<string, unknown>
  description: string | null
  archived: boolean
  /** false only when a space admin turned page links off */
  public_links: boolean
  /** null for a guest outside the space */
  my_role: SpaceRole | null
}
export type SpaceMember = {
  user_id: string
  name: string
  email: string
  role: SpaceRole
  workspace_role: WorkspaceRole
}
export type SpaceMembers = { visibility: 'open' | 'private'; members: SpaceMember[]; can_manage: boolean }
export type SpaceUpdate = {
  id: string
  name?: string
  description?: string | null
  visibility?: 'open' | 'private'
  public_links?: boolean
}

export const spaceMembers = (ws: string, space_id: string) =>
  cmd<SpaceMembers>(ws, 'space.members', { space_id })
export const spaceMemberSet = (ws: string, space_id: string, user_id: string, role: SpaceRole | null) =>
  cmd<SpaceMember | { removed: true }>(ws, 'space.member_set', { space_id, user_id, role })
export const spaceUpdate = (ws: string, input: SpaceUpdate) => cmd<SpaceB1>(ws, 'space.update', input)
export const spaceArchive = (ws: string, id: string, archived: boolean) =>
  cmd<SpaceB1>(ws, 'space.archive', { id, archived })

/** page_link.get. The token is never returned: only page_link.create returns the address, once. */
export type PageLink = {
  id: string
  status: 'active' | 'off'
  created_by_name: string | null
  created_at: string
  expires_at: string | null
  view_count: number
  last_viewed_at: string | null
}
export type PageLinkGet = { link: PageLink | null; allowed: boolean; reason: string | null }
export type PageLinkRow = {
  note_id: string
  title: string
  created_by_name: string | null
  created_at: string
  expires_at: string | null
  view_count: number
}
/** What GET /api/public/page/<token> returns; a golden test pins this key set. */
export type PublicPage = {
  title: string
  icon: string | null
  wide: boolean
  body_md: string
  updated_on: string
  workspace_name: string
  cover_url?: string | null
  cover_y?: number | null
}

export const pageLinkGet = (ws: string, note_id: string) => cmd<PageLinkGet>(ws, 'page_link.get', { note_id })
export const pageLinkCreate = (ws: string, note_id: string, expires_in_days?: number | null) =>
  cmd<{ link: PageLink; url: string }>(ws, 'page_link.create', { note_id, expires_in_days })
export const pageLinkRevoke = (ws: string, note_id: string) =>
  cmd<{ revoked: number }>(ws, 'page_link.revoke', { note_id })
export const pageLinkList = (ws: string, space_id: string) =>
  cmd<PageLinkRow[]>(ws, 'page_link.list', { space_id })

export type EmailMode = 'smart' | 'digest' | 'off'
export type NotifyPrefs = {
  email_mode: EmailMode
  /** hour of the daily digest, IST */
  digest_hour: number
  email_configured: boolean
  direct_off: boolean
  digest_off: boolean
}
export const notifyPrefsGet = (ws: string) => cmd<NotifyPrefs>(ws, 'notify_prefs.get', {})
export const notifyPrefsSet = (
  ws: string,
  input: Partial<Pick<NotifyPrefs, 'email_mode' | 'digest_hour' | 'direct_off' | 'digest_off'>>,
) => cmd<NotifyPrefs>(ws, 'notify_prefs.set', input)

export type MyTask = {
  note_id: string
  note_title: string
  icon: string | null
  space_id: string
  ord: number
  text: string
  due: string | null
  done: boolean
}
export const taskMine = (ws: string, opts: { done?: boolean; limit?: number } = {}) =>
  cmd<MyTask[]>(ws, 'task.mine', opts)

export const REACTIONS = ['👍', '❤️', '😂', '🎉', '👀', '✅'] as const
export type Reaction = (typeof REACTIONS)[number]
export type ReactionCount = { emoji: Reaction; count: number; mine: boolean }
/** The fields comment.list adds to each comment in B1: a deleted top-level comment with replies comes back as a tombstone (empty body). */
export type CommentB1 = { edited_at: string | null; deleted: boolean; reactions: ReactionCount[] }

export const commentEdit = (ws: string, id: string, body: string) =>
  cmd<CommentB1 & { id: string; body: string }>(ws, 'comment.edit', { id, body })
export const commentDelete = (ws: string, id: string) => cmd<{ deleted: true }>(ws, 'comment.delete', { id })
export const commentReact = (ws: string, id: string, emoji: Reaction, on: boolean) =>
  cmd<{ id: string; reactions: ReactionCount[] }>(ws, 'comment.react', { id, emoji, on })

// B2 command contracts.
export type PageViewer = { user_id: string; name: string; last_viewed_at: string }
export type PageViewers = { count: number; viewers?: PageViewer[] }
export type DocxImportResult = { id: string; title: string; warnings: string[] }
export type TemplateAnswers = Record<string, string>
export const requestAccess = (ws: string, note_id: string, message?: string) =>
  cmd<{ ok: true }>(ws, 'note.request_access', { note_id, message })
export const noteViewers = (ws: string, note_id: string) => cmd<PageViewers>(ws, 'note.viewers', { note_id })
export const docArchive = (ws: string, id: string) => cmd<{ archived: number }>(ws, 'doc.archive', { id })
export const importDocx = (
  ws: string,
  input: { space_id: string; parent_id?: string; filename: string; data_base64: string },
) => cmd<DocxImportResult>(ws, 'import.docx', input)

/** A readable page in a lazily loaded tree level; bodies are fetched separately. */
export type TreeRow = {
  id: string
  space_id: string
  parent_id: string | null
  title: string
  icon: string | null
  position: number
  status: PageStatus | null
  restriction: 'none' | 'view' | 'edit'
  freshness: string
  verify_by: string | null
  updated_at: string
  has_children: boolean
}
/** parent_id is a page id, null for the space root, or "shared" for the synthetic "Shared with you" root. */
export type TreeLevel = {
  parent_id: string | null
  rows: TreeRow[]
  next: string | null
  /** Cursor for the rows before this page (`before`), or the first row itself (`from`) to refresh a window. */
  prev?: string | null
  shared?: boolean
}
export type Cover = { file_id: string; y?: number } | null
export type TrashRetention = { days: number; purge_from: string | null }

export const treeLevel = (
  ws: string,
  input: {
    space_id: string
    parent_id?: string | null
    after?: string
    before?: string
    from?: string
    limit?: number
  },
) => cmd<TreeLevel>(ws, 'note.tree', input)
export const revealInTree = (ws: string, noteId: string) =>
  cmd<{ levels: TreeLevel[] }>(ws, 'note.tree', { reveal: noteId })
export const setCover = (ws: string, noteId: string, cover: Cover) =>
  cmd<{ cover: Cover }>(ws, 'note.cover_set', {
    note_id: noteId,
    file_id: cover?.file_id ?? null,
    y: cover?.y,
  })
export const purgeDeletion = (ws: string, deletionId: string) =>
  cmd<{ purged: boolean; pages?: number; files?: number; bytes?: number }>(ws, 'note.purge', {
    deletion_id: deletionId,
  })
export const getTrashRetention = (ws: string) => cmd<TrashRetention>(ws, 'trash.retention_get', {})
export const setTrashRetention = (ws: string, days: 0 | 30 | 90 | 365) =>
  cmd<TrashRetention>(ws, 'trash.retention_set', { days })
