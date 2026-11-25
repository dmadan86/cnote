package worker

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules/notes"
)

// mentionsOnNoteUpdated is the gateway's mention path (docs-parity-b 13.2.10). A co-edited page is saved by the collab
// gateway, which writes the row and emits note.updated but never runs note.update, so nothing notified the people typed
// into it. Every note.updated that changed the body comes here; notes.NotifyPageMentions is the same function note.update
// calls, it takes the per-note lock and remembers the version it handled, so a mention notifies once whichever path is first.
// Registered before pageNotifyOnEvent, whose "already told by this edit" rule looks for the mention row.
func mentionsOnNoteUpdated(c *command.Ctx, ev jobs.Event) error {
	if ev.NoteID == nil {
		return nil
	}
	var p struct {
		Changed []string `json:"changed"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if !slices.Contains(p.Changed, "content") {
		return nil
	}
	return notes.NotifyPageMentions(c, *ev.NoteID, nil)
}

const mentionSeedBatch = 500

// MentionSeedSweep gives pages that predate core.note_mention_state a stored mention set without notifying anyone, so the first
// co-edit of an old page notifies only what was typed since. SQL only; one small transaction per batch, a minute apart.
func MentionSeedSweep(ctx context.Context, w *Worker) error {
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return err
	}
	for _, ws := range ids {
		for range 20 {
			n, err := mentionSeedBatchRun(ctx, w, ws, mentionSeedBatch)
			if err != nil {
				return err
			}
			if n < mentionSeedBatch {
				break
			}
		}
	}
	return nil
}

func mentionSeedBatchRun(ctx context.Context, w *Worker, ws string, batch int) (int64, error) {
	tx, err := db.BeginTenant(ctx, w.Pool, ws)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	n, err := mentionSeedRun(ctx, tx, batch)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// pageEventPendingSQL is true while a note.created or note.updated event of page n has an event.dispatch job that has not finished
// (queued, or running and so holding the page's locks). Such an event may still carry a mention or an assignment that only its
// handler can announce, so a sweep that "records the page as already handled" must not touch the page until it is gone. The event
// queue is the exact signal; a page-age window would be a guess. Judged under the page's lock, so the handler that is running
// has committed (job done, state written) or not yet started (job still queued).
const pageEventPendingSQL = `exists (select 1 from core.jobs j join core.events e on e.id = (j.payload->>'event_id')::bigint
	where j.kind = 'event.dispatch' and j.status in ('queued', 'running') and e.note_id = n.id and e.kind in ('note.created', 'note.updated'))`

// mentionSeedRun seeds up to batch pages of the transaction's workspace and returns how many it looked at. A page with a pending
// note.created or note.updated event is left alone (its handler does the diff) and takes the per-note mention lock like the handler.
func mentionSeedRun(ctx context.Context, tx pgx.Tx, batch int) (int64, error) {
	rows, err := tx.Query(ctx,
		`select n.id::text from core.notes n
		 where n.deleted_at is null and n.content_md like '%/people/%'
		   and not exists (select 1 from core.note_mention_state s where s.note_id = n.id) and not `+pageEventPendingSQL+`
		 order by n.id limit $1`, batch)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		// The same per-note lock the handler takes, then the checks again: a handler or an event may have arrived since the list.
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtextextended('notes.mentions:' || $1::text, 0))", id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx,
			`insert into core.note_mention_state (workspace_id, note_id, processed_version, mentioned)
			 select n.workspace_id, n.id, n.version,
			        array(select distinct lower(m[1])::uuid from regexp_matches(n.content_md,
			          '\]\(/w/' || n.workspace_id::text || '/people/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\)', 'gi') m)
			 from core.notes n
			 where n.id = $1::uuid and n.deleted_at is null and not exists (select 1 from core.note_mention_state s where s.note_id = n.id)
			   and not `+pageEventPendingSQL+`
			 on conflict (note_id) do nothing`, id); err != nil {
			return 0, err
		}
	}
	return int64(len(ids)), nil
}
