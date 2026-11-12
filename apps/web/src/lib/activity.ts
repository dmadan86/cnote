// Turns raw note events (note.history) into sentences people can read: "Asha edited the content (3 times)".

export type Evt = {
  id: number
  kind: string
  actor_type: string
  actor_name?: string | null
  subject_name?: string | null
  created_at: string
  payload: Record<string, unknown>
}

/** `at` is the newest event of a merged run, `since` the oldest. */
export type Activity = { id: number; who: string; text: string; at: string; since: string; count: number }

// Bookkeeping that follows other events; it says nothing a person did.
const HIDDEN = new Set(['doc.bindings_synced', 'item.ranked', 'mention.created'])
const MERGE_WINDOW_MS = 15 * 60 * 1000

const FIELD: Record<string, string> = {
  content: 'the content',
  content_md: 'the content',
  owner: 'the owner',
  verify_by: 'the review date',
  parent: 'the location',
  title: 'the title',
  status: 'the status',
  priority: 'the priority',
  due_date: 'the due date',
  estimate: 'the estimate',
  labels: 'the labels',
  item_type: 'the type',
  sprint_id: 'the sprint',
  fix_version_id: 'the version',
  parent_id: 'the location',
  icon: 'the icon',
  description: 'the description',
  component_id: 'the component',
  custom: 'custom fields',
}

const list = (xs: string[]) =>
  xs.length <= 1 ? (xs[0] ?? '') : `${xs.slice(0, -1).join(', ')} and ${xs[xs.length - 1]}`

function who(e: Evt) {
  if (e.actor_name) return e.actor_name
  if (e.actor_type === 'system') return 'C Note'
  if (e.actor_type === 'agent') return 'An agent'
  return 'Someone'
}

export function sentence(e: Evt): string {
  const p = e.payload ?? {}
  const s = e.subject_name ?? 'someone'
  switch (e.kind) {
    case 'note.created':
      return 'created this page'
    case 'item.created':
      return 'created this item'
    case 'note.updated':
    case 'item.updated': {
      if (typeof p.field === 'string') return `changed ${FIELD[p.field] ?? p.field.replace(/_/g, ' ')}`
      const changed = (Array.isArray(p.changed) ? p.changed : []).map(String)
      if (changed.length === 1 && changed[0] === 'title') return 'renamed it'
      const named = changed.map((c) => FIELD[c] ?? c.replace(/_/g, ' '))
      return named.length ? `edited ${list(named)}` : 'edited it'
    }
    case 'note.deleted':
      return 'moved it to the trash'
    case 'doc.restored':
      return 'restored it'
    case 'comment.added':
      return 'commented'
    case 'note.shared':
      return `shared it with ${s}${p.level === 'edit' ? ' (can edit)' : p.level === 'view' ? ' (can view)' : ''}`
    case 'note.unshared':
      return `removed ${s}'s access`
    case 'note.restricted':
      return 'changed who can see it'
    case 'doc.verified':
      return 'verified it is up to date'
    case 'doc.lapsed':
    case 'doc.stale':
      return 'marked it as due for review'
    case 'doc.archived':
      return 'archived it'
    case 'doc.owner_changed':
      return `made ${s} the owner`
    case 'doc.proposal_created':
      return 'suggested an edit'
    case 'doc.proposal_decided':
      return p.status === 'accepted' ? 'accepted a suggested edit' : 'rejected a suggested edit'
    case 'doc.capture_requested':
      return 'asked for this to be documented'
    case 'doc.capture_answered':
      return 'answered a documentation request'
    case 'template.rendered':
      return 'filled it from a template'
    case 'item.transitioned':
      return p.from && p.to ? `moved it from ${p.from} to ${p.to}` : 'changed the status'
    case 'item.assigned':
      return p.assignee_id
        ? `assigned it to ${s}`
        : p.assignee_agent_id
          ? 'assigned it to an agent'
          : 'unassigned it'
    case 'item.assigned_to_agent':
      return 'handed it to an agent'
    case 'item.triage_accepted':
      return 'accepted it from triage'
    case 'relation.added':
      return `linked it (${String(p.kind ?? 'related').replace(/_/g, ' ')})`
    case 'relation.removed':
      return 'removed a link'
    case 'evidence.recorded':
      return 'linked a pull request or deployment'
    case 'note.imported':
      return 'imported it'
    default:
      return e.kind.replace('.', ': ').replace(/_/g, ' ')
  }
}

/** Newest first, as note.history returns them. Hidden kinds dropped; repeated edits by one person close together merged. */
export function activity(events: Evt[] = []): Activity[] {
  const out: Activity[] = []
  for (const e of events) {
    if (HIDDEN.has(e.kind)) continue
    const a: Activity = {
      id: e.id,
      who: who(e),
      text: sentence(e),
      at: e.created_at,
      since: e.created_at,
      count: 1,
    }
    const prev = out[out.length - 1]
    if (
      prev &&
      prev.who === a.who &&
      prev.text === a.text &&
      Date.parse(prev.since) - Date.parse(a.at) < MERGE_WINDOW_MS
    ) {
      prev.count += 1
      prev.since = a.at
      continue
    }
    out.push(a)
  }
  return out
}

export const activityText = (a: Activity) => `${a.who} ${a.text}${a.count > 1 ? ` (${a.count} times)` : ''}`
