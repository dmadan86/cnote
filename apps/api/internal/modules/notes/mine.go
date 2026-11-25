package notes

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// note.star, note.viewed and note.mine (docs-parity 5.4, 7.6). They touch only the caller's own rows. note.mine
// applies the access predicate in the query, before the limit, so a page restricted or deleted later disappears.

func init() {
	a1Handlers["note.star"] = noteStar
	a1Handlers["note.viewed"] = noteViewed
	a1Handlers["note.mine"] = noteMine
}

func noteIDInput(input json.RawMessage, extra any) (string, error) {
	o, err := parseObj(input)
	if err != nil {
		return "", command.ErrInvalid("note_id is required")
	}
	raw, _ := o.get("note_id")
	var id string
	if json.Unmarshal(raw, &id) != nil || !uuidRe.MatchString(id) {
		return "", command.ErrInvalid("note_id must be a UUID")
	}
	if extra != nil {
		if raw, ok := o.get("starred"); !ok || json.Unmarshal(raw, extra) != nil {
			return "", command.ErrInvalid("starred must be true or false")
		}
	}
	return strings.ToLower(id), nil
}

func requireUser(c *command.Ctx) (string, error) {
	if c.Principal.UserID == nil {
		return "", command.ErrForbidden("this needs a signed-in person")
	}
	return *c.Principal.UserID, nil
}

func noteStar(c *command.Ctx, input json.RawMessage) (any, error) {
	var starred bool
	id, err := noteIDInput(input, &starred)
	if err != nil {
		return nil, err
	}
	uid, err := requireUser(c)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	if starred {
		_, err = c.Tx.Exec(c.Context,
			`insert into core.note_stars (workspace_id, user_id, note_id) values ($1::uuid, $2::uuid, $3::uuid) on conflict do nothing`,
			c.WorkspaceID(), uid, id)
	} else {
		_, err = c.Tx.Exec(c.Context, `delete from core.note_stars where user_id = $1::uuid and note_id = $2::uuid`, uid, id)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"note_id": id, "starred": starred}, nil
}

func noteViewed(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := noteIDInput(input, nil)
	if err != nil {
		return nil, err
	}
	uid, err := requireUser(c)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	var at time.Time
	if err := c.Tx.QueryRow(c.Context,
		`insert into core.note_views (workspace_id, user_id, note_id) values ($1::uuid, $2::uuid, $3::uuid)
		 on conflict (user_id, note_id) do update set last_viewed_at = now() returning last_viewed_at`,
		c.WorkspaceID(), uid, id).Scan(&at); err != nil {
		return nil, err
	}
	return map[string]any{"note_id": id, "at": command.Ts(at)}, nil
}

type pageRef struct {
	ID        string     `json:"id"`
	SpaceID   string     `json:"space_id"`
	Title     string     `json:"title"`
	Icon      *string    `json:"icon"`
	Status    *string    `json:"status"`
	UpdatedAt command.Ts `json:"updated_at"`
	At        command.Ts `json:"at"`
}

func noteMine(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		List  string `json:"list"`
		Limit *int64 `json:"limit"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil, command.ErrInvalid("list must be recent, starred or watching")
	}
	// Fixed text per list: the table name never comes from input.
	var from, col string
	switch in.List {
	case "recent":
		from, col = "core.note_views", "last_viewed_at"
	case "starred":
		from, col = "core.note_stars", "starred_at"
	case "watching":
		from, col = "core.note_watches", "created_at"
	default:
		return nil, command.ErrInvalid("list must be recent, starred or watching")
	}
	uid, err := requireUser(c)
	if err != nil {
		return nil, err
	}
	limit := int64(20)
	if in.Limit != nil {
		limit = min(max(*in.Limit, 1), 100)
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	_, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select n.id::text, n.space_id::text, n.title, n.icon, n.page_status, n.updated_at, s.`+col+`
		 from `+from+` s join core.notes n on n.id = s.note_id and n.workspace_id = s.workspace_id
		 where s.user_id = $1::uuid and n.deleted_at is null
		   and core.note_access(n.id, $1::uuid, $3::bool, $2::text[]::uuid[]) <> 'none'
		 order by s.`+col+` desc limit $4::int8`, uid, spaces, admin, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pageRef{}
	for rows.Next() {
		var r pageRef
		var upd, at time.Time
		if err := rows.Scan(&r.ID, &r.SpaceID, &r.Title, &r.Icon, &r.Status, &upd, &at); err != nil {
			return nil, err
		}
		r.UpdatedAt, r.At = command.Ts(upd), command.Ts(at)
		out = append(out, r)
	}
	return out, rows.Err()
}
