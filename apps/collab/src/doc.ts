import * as Y from 'yjs'
import { CmdError, type Tx } from './db.ts'
import * as md from './md.ts'

/** Deterministic client id for seeding (same derivation as the Rust gateway), so seeding the same note twice
 * yields identical Yjs items and clients that cached an earlier seed merge cleanly. */
export function seedClient(note: string): number {
  const b = Buffer.from(note.replaceAll('-', ''), 'hex')
  const n = (b[0] & 0x1f) * 2 ** 40 + b[1] * 2 ** 32 + b[2] * 2 ** 24 + b[3] * 2 ** 16 + b[4] * 2 ** 8 + b[5]
  return Math.max(n, 1)
}

/** The live document: the last snapshot (or a seed from Markdown) plus every logged update of the current epoch.
 * Returns the doc, the epoch, and the log ids it absorbed (the next snapshot deletes them). */
export async function loadDoc(tx: Tx, note: string): Promise<{ doc: Y.Doc; epoch: number; ids: string[] }> {
  const n = await tx.query(
    'select content_md, content_ydoc, collab_epoch from core.notes where id = $1 and deleted_at is null',
    [note],
  )
  if (!n.rows[0]) throw new CmdError('not_found', 'note')
  const {
    content_md: mdText,
    content_ydoc: ydoc,
    collab_epoch: epoch,
  } = n.rows[0] as {
    content_md: string
    content_ydoc: Buffer | null
    collab_epoch: number
  }
  const log = await tx.query(
    'select id, update from core.note_updates where note_id = $1 and epoch = $2 order by id',
    [note, epoch],
  )
  const ids = log.rows.map((r) => String(r.id))
  let replay = !ydoc || ydoc.length === 0 // a seed is deterministic, so updates made on it apply again
  let doc: Y.Doc | null = null
  if (ydoc && ydoc.length > 0) {
    const d = new Y.Doc()
    let ok = true
    try {
      Y.applyUpdate(d, ydoc)
    } catch {
      ok = false
    }
    // Persisting writes content_md from the doc, so a mismatch means the note was edited through the API;
    // then the API text wins and updates logged against the older snapshot are dropped.
    if (ok && md.same(md.toMarkdown(d), mdText)) {
      replay = true
      doc = d
    }
  }
  if (!doc) {
    doc = new Y.Doc()
    doc.clientID = seedClient(note)
    md.seed(doc, mdText)
  }
  if (replay) {
    for (const r of log.rows) {
      try {
        Y.applyUpdate(doc, r.update)
      } catch {}
    }
  }
  return { doc, epoch, ids }
}
