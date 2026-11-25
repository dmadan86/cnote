package worker

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/notify"
)

// pageNotifyOnEvent is the one delivery path for page and comment notifications (docs-parity 7.6). Events come from
// Go commands and from gateway snapshots (note.updated, actor in the event row), so one consumer covers both.
//
// Auto-watch: the creator and the owner of a new page, a new owner after note.updated with "owner" changed, and
// anyone who adds a comment. Recipients: watchers plus, for comment events, the people in the same thread. The actor
// is never notified. Each recipient is checked for read access now; one who lost it is skipped and their watch is
// removed. Someone who already got an @mention notification for this event is skipped, so a mention is not doubled.
// Dedupe: at most one notification per recipient, page, actor, kind (and thread, for comments) in 10 minutes, which
// also makes a retried event harmless.
func pageNotifyOnEvent(c *command.Ctx, ev jobs.Event) error {
	if ev.NoteID == nil {
		return nil
	}
	var title string
	var owner, creator *string
	err := c.Tx.QueryRow(c.Context,
		`select title, owner_id::text, created_by::text from core.notes where id = $1::uuid and deleted_at is null`, *ev.NoteID).Scan(&title, &owner, &creator)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var p struct {
		Changed   []string `json:"changed"`
		CommentID string   `json:"comment_id"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	actor := c.Principal.UserID // dispatch sets it from the event: the user, or who an agent acts for

	watch := func(user *string, reason string) error {
		if user == nil {
			return nil
		}
		ok, err := readable(c, *user, *ev.NoteID)
		if err != nil || !ok {
			return err
		}
		_, err = c.Tx.Exec(c.Context, `insert into core.note_watches (workspace_id, user_id, note_id, reason) values ($1::uuid,$2::uuid,$3::uuid,$4)
			on conflict (user_id, note_id) do nothing`, ev.WorkspaceID, *user, *ev.NoteID, reason)
		return err
	}
	kind := ""
	switch ev.Kind {
	case "note.created":
		if err := watch(creator, "creator"); err != nil {
			return err
		}
		return watch(owner, "owner")
	case "note.updated":
		if slices.Contains(p.Changed, "owner") {
			if err := watch(owner, "owner"); err != nil {
				return err
			}
		}
		if !slices.Contains(p.Changed, "content") && !slices.Contains(p.Changed, "title") {
			return nil
		}
		kind = "page_updated"
	case "comment.added", "comment.resolved", "comment.reopened":
		if ev.Kind == "comment.added" {
			if err := watch(actor, "commenter"); err != nil {
				return err
			}
		}
		kind = "comment"
	default:
		return nil
	}

	// The thread is its top-level comment; its authors are told about replies, resolves and reopens.
	var thread *string
	if p.CommentID != "" {
		var t string
		err := c.Tx.QueryRow(c.Context, `select coalesce(parent_id, id)::text from core.comments where id = $1::uuid and note_id = $2::uuid`, p.CommentID, *ev.NoteID).Scan(&t)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			thread = &t
		}
	}
	rows, err := c.Tx.Query(c.Context,
		`select user_id::text from core.note_watches where note_id = $1::uuid
		 union select author_id::text from core.comments where $2::uuid is not null and note_id = $1::uuid and author_id is not null
		       and (id = $2::uuid or parent_id = $2::uuid)`, *ev.NoteID, thread)
	if err != nil {
		return err
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, u := range users {
		if actor != nil && u == *actor {
			continue
		}
		ok, err := readable(c, u, *ev.NoteID)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := c.Tx.Exec(c.Context, `delete from core.note_watches where user_id = $1::uuid and note_id = $2::uuid`, u, *ev.NoteID); err != nil {
				return err
			}
			continue
		}
		var skip bool
		if err := c.Tx.QueryRow(c.Context,
			`select exists(select 1 from core.notifications where user_id = $1::uuid and note_id = $2::uuid and (
			     (kind = 'mention' and created_at >= $5::timestamptz - interval '1 minute')
			  or (kind = $3 and actor_user_id is not distinct from $4::uuid and thread_id is not distinct from $6::uuid
			      and created_at > now() - interval '10 minutes')))`,
			u, *ev.NoteID, kind, actor, ev.CreatedAt, thread).Scan(&skip); err != nil {
			return err
		}
		if skip {
			continue
		}
		if _, err := notify.Send(c.Context, c.Tx, notify.Notification{WorkspaceID: ev.WorkspaceID, UserID: u, Kind: kind,
			Title: title, NoteID: ev.NoteID, ActorUserID: actor, ThreadID: thread}); err != nil {
			return err
		}
	}
	return nil
}

func readable(c *command.Ctx, user, note string) (ok bool, err error) {
	err = c.Tx.QueryRow(c.Context, "select "+notify.ReadableExpr("$1::uuid", "$2::uuid"), user, note).Scan(&ok)
	return
}
