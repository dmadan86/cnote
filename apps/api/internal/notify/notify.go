// Package notify is the single seam for in-app notifications. Modules decide WHO is notified (domain logic) and
// hand the rows to Send; only this package writes core.notifications. Today the Local sender inserts in the
// caller's transaction; a notification service can later replace Default without touching callers.
package notify

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Notification is one row for one recipient. Kind must be allowed by the notifications_kind_check constraint.
type Notification struct {
	WorkspaceID string
	UserID      string
	Kind        string
	Title       string
	NoteID      *string
	ChannelID   *string
	ActorUserID *string
	ThreadID    *string // top-level comment a comment notification points at
	Source      *string // with SourceDay: at most one per (user, source, day), e.g. daily reminders
	SourceDay   *string // YYYY-MM-DD
	EventKey    *string // the edit or comment this came from (note:<id>:v<version>, comment:<id>), to tell a mention from a page notification of the same event
}

// Sender delivers notifications. Implementations must be idempotent for rows the unique indexes guard.
type Sender interface {
	Send(ctx context.Context, tx pgx.Tx, ns []Notification) (ids []string, err error)
}

// Default is the sender used by Send. Swap it (e.g. for an HTTP/queue client) when notifications move out.
var Default Sender = Local{}

// Send delivers ns with Default and returns the ids of rows actually created (duplicates are skipped).
func Send(ctx context.Context, tx pgx.Tx, ns ...Notification) ([]string, error) {
	if len(ns) == 0 {
		return nil, nil
	}
	return Default.Send(ctx, tx, ns)
}

// Local writes rows into core.notifications inside the caller's transaction (tenant RLS applies).
type Local struct{}

func (Local) Send(ctx context.Context, tx pgx.Tx, ns []Notification) ([]string, error) {
	var ids []string
	for _, n := range ns {
		var id string
		err := tx.QueryRow(ctx, `insert into core.notifications
			(workspace_id, user_id, kind, title, note_id, channel_id, actor_user_id, source, source_day, thread_id, event_key)
			values ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::uuid, $7::uuid, $8, $9::date, $10::uuid, $11)
			on conflict do nothing returning id::text`,
			n.WorkspaceID, n.UserID, n.Kind, n.Title, n.NoteID, n.ChannelID, n.ActorUserID, n.Source, n.SourceDay, n.ThreadID, n.EventKey).Scan(&id)
		if err == pgx.ErrNoRows { // a unique index (reminder once a day, quote decided once) already has it
			continue
		}
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ReadableExpr is a SQL boolean: the person (uuid expression user) can read the page (uuid expression note), judged
// with their own role and space access, not the caller's. A deleted page or a person outside the workspace is false.
// Used at send time and when a list is read, so a person who lost access neither gets nor keeps a page notification.
func ReadableExpr(user, note string) string {
	return fmt.Sprintf(`exists(select 1 from core.memberships m where m.user_id = %[1]s and core.note_access(%[2]s, %[1]s,
		   m.role in ('admin','owner'),
		   array(select s.id from core.spaces s
		         left join core.space_members sm on sm.space_id = s.id and sm.user_id = %[1]s
		         where m.role in ('admin','owner') or sm.user_id is not null
		            or (s.visibility = 'open' and m.role <> 'guest'))) <> 'none')`, user, note)
}
