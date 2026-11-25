// Package notifications ports notification.list, notification.read and notification.unread_count
// from crates/cnote-core/src/notifications.rs.
//
// All three commands require the caller to be a human user (Principal.UserID != nil).
// API keys and agent keys whose on_behalf_of is nil carry no user identity and receive 403,
// matching the Rust `me()` helper: "notifications belong to people".
package notifications

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/notify"
)

func Commands() []command.Command {
	return []command.Command{
		command.NewCommand("notification.list", command.GroupRead, list),
		command.NewCommand("notification.read", command.GroupRead, read),
		command.NewCommand("notification.unread_count", command.GroupRead, unreadCount),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// me returns the authenticated user's ID, mirroring the Rust `me()` helper.
// Returns 403 when the principal carries no user identity (pure machine caller).
func me(c *command.Ctx) (string, error) {
	uid := c.Principal.UserID
	if uid == nil {
		return "", command.ErrForbidden("notifications belong to people")
	}
	return *uid, nil
}

// ---------- notification.list ----------

type NotificationOut struct {
	ID        string      `json:"id"`
	Kind      string      `json:"kind"`
	NoteID    *string     `json:"note_id"`
	ThreadID  *string     `json:"thread_id,omitempty"`  // top-level comment a comment notification points at
	ChannelID *string     `json:"channel_id,omitempty"` // only chat mentions carry one; absent keeps page notifications identical to Rust
	NoteTitle string      `json:"note_title"`
	ActorName *string     `json:"actor_name"`
	CreatedAt command.Ts  `json:"created_at"`
	ReadAt    *command.Ts `json:"read_at"`
}

func list(c *command.Ctx, input json.RawMessage) (any, error) {
	// Accept any JSON object (Empty {}); anything else falls back to Rust for identical serde errors.
	var obj map[string]json.RawMessage
	if json.Unmarshal(input, &obj) != nil || obj == nil {
		return nil, command.ErrFallback
	}
	uid, err := me(c)
	if err != nil {
		return nil, err
	}
	// Access is checked again here (docs-parity 7.6): a page the reader can no longer see drops out before the limit.
	// Watches on pages they can no longer see are removed now (lazily); trashed pages keep theirs.
	if _, err := c.Tx.Exec(c.Context,
		`delete from core.note_watches w where w.user_id = $1::uuid
		    and exists(select 1 from core.notes n where n.id = w.note_id and n.deleted_at is null)
		    and not `+notify.ReadableExpr("w.user_id", "w.note_id"), uid); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select n.id::text, n.kind, n.note_id::text, n.thread_id::text, n.channel_id::text, n.title as note_title,
		        u.name as actor_name, n.created_at, n.read_at
		   from core.notifications n
		   left join core.users u on u.id = n.actor_user_id
		  where n.user_id = $1::uuid
		    and (n.note_id is null or `+notify.ReadableExpr("n.user_id", "n.note_id")+`)
		  order by n.created_at desc limit 50`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationOut{}
	for rows.Next() {
		var n NotificationOut
		var createdAt time.Time
		var readAt *time.Time
		if err := rows.Scan(&n.ID, &n.Kind, &n.NoteID, &n.ThreadID, &n.ChannelID, &n.NoteTitle,
			&n.ActorName, &createdAt, &readAt); err != nil {
			return nil, err
		}
		n.CreatedAt = command.Ts(createdAt)
		n.ReadAt = command.TsPtr(readAt)
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---------- notification.read ----------

func read(c *command.Ctx, input json.RawMessage) (any, error) {
	// Decode `{ "id": <uuid or null> }`. Missing field means mark-all.
	// Anything serde would reject falls back to Rust.
	var in struct {
		ID *string `json:"id"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil, command.ErrFallback
	}
	// If id is provided it must be a valid UUID (Rust's serde_json rejects other spellings).
	if in.ID != nil && !uuidRe.MatchString(*in.ID) {
		return nil, command.ErrFallback
	}
	uid, err := me(c)
	if err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context,
		`update core.notifications
		    set read_at = now()
		  where user_id = $1::uuid
		    and read_at is null
		    and ($2::uuid is null or id = $2::uuid)`, uid, in.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": tag.RowsAffected()}, nil
}

// ---------- notification.unread_count ----------

func unreadCount(c *command.Ctx, input json.RawMessage) (any, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(input, &obj) != nil || obj == nil {
		return nil, command.ErrFallback
	}
	uid, err := me(c)
	if err != nil {
		return nil, err
	}
	var n int64
	if err := c.Tx.QueryRow(c.Context,
		`select count(*) from core.notifications n where n.user_id = $1::uuid and n.read_at is null
		   and (n.note_id is null or `+notify.ReadableExpr("n.user_id", "n.note_id")+`)`,
		uid).Scan(&n); err != nil {
		return nil, err
	}
	return map[string]any{"count": n}, nil
}
