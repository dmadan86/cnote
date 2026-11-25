package notes

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

// The one function behind "a mention notifies exactly once" (docs-parity-b 13.2.10). Two paths reach it: note.update on the
// API (the command passes the body it replaced) and the worker, on note.updated events, which is the only signal a gateway
// snapshot gives (co-edited pages never go through note.update). Both end here, under a per-note advisory lock, and the page's
// last handled version lives in core.note_mention_state, so the second path to arrive finds the version done and does nothing.
//
// What is notified: people in the page's current body who were not in the set stored at the last handled version (or, with no
// stored set, in `baseline` or the notifications they already have for this page), minus the actor, minus anyone who cannot
// read the page. A mention removed and typed again later notifies again, as the old before/after diff did.

// NotifyPageMentions handles the page's current version. baseline is the body that was replaced, when the caller has it
// (note.update); the worker passes nil. It is safe to call twice for the same version and from two processes at once.
func NotifyPageMentions(c *command.Ctx, noteID string, baseline *string) error {
	if _, err := c.Tx.Exec(c.Context, "select pg_advisory_xact_lock(hashtextextended('notes.mentions:' || $1::text, 0))", noteID); err != nil {
		return command.DB(err)
	}
	var title, body string
	var version int
	err := c.Tx.QueryRow(c.Context, "select title, content_md, version from core.notes where id = $1::uuid and deleted_at is null", noteID).Scan(&title, &body, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return command.DB(err)
	}
	ws := strings.ToLower(c.WorkspaceID())
	var done int
	var stored []string
	err = c.Tx.QueryRow(c.Context, "select processed_version, mentioned::text[] from core.note_mention_state where note_id = $1::uuid", noteID).Scan(&done, &stored)
	have := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return command.DB(err)
	}
	if have && done >= version {
		return nil // this version was handled by the other path
	}
	seen := map[string]bool{}
	switch {
	case have:
		for _, u := range stored {
			seen[strings.ToLower(u)] = true
		}
	case baseline != nil:
		for _, u := range mentions(*baseline, ws) {
			seen[u] = true
		}
	default:
		// No stored set yet (a page nobody has seeded; the worker's seeding sweep and note.create keep this rare): whoever was
		// already told about this page counts as seen, so the first co-edit of an old page does not replay its history. The
		// latest page version is no use as a baseline here: the gateway writes it in the same transaction as the body.
		rows, err := c.Tx.Query(c.Context, "select user_id::text from core.notifications where note_id = $1::uuid and kind = 'mention'", noteID)
		if err != nil {
			return command.DB(err)
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return command.DB(err)
		}
		for _, u := range users {
			seen[strings.ToLower(u)] = true
		}
	}
	now := mentions(body, ws)
	var fresh []string
	for _, u := range now {
		if !seen[u] {
			fresh = append(fresh, u)
		}
	}
	// The state is written first, in this transaction: a failure below rolls both back and the event is retried whole.
	if _, err := c.Tx.Exec(c.Context,
		`insert into core.note_mention_state (workspace_id, note_id, processed_version, mentioned) values ($1::uuid, $2::uuid, $3, $4::text[]::uuid[])
		 on conflict (note_id) do update set processed_version = excluded.processed_version, mentioned = excluded.mentioned, updated_at = now()`,
		ws, noteID, version, nonNil(now)); err != nil {
		return command.DB(err)
	}
	return notifyUsers(c, noteID, title, fresh, fmt.Sprintf("note:%s:v%d", noteID, version))
}

// SeedMentionState records a page's current mentions as handled, without notifying anyone: for a page that has no stored set
// (new page, or the worker's seeding sweep over pages that predate the table). It never overwrites an existing row.
func SeedMentionState(c *command.Ctx, noteID, ws, body string, version int32) error {
	if len(mentions(body, strings.ToLower(ws))) == 0 {
		return nil // no row is the same as an empty set for a page nobody has mentioned anyone in
	}
	_, err := c.Tx.Exec(c.Context,
		`insert into core.note_mention_state (workspace_id, note_id, processed_version, mentioned) values ($1::uuid, $2::uuid, $3, $4::text[]::uuid[])
		 on conflict (note_id) do nothing`, ws, noteID, version, nonNil(mentions(body, strings.ToLower(ws))))
	return command.DB(err)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
