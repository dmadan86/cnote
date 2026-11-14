// Package revisions ports the read side of crates/cnote-collab/src/revisions.rs: note.revisions and note.revision.
// note.restore stays in Rust: it needs the live Yjs document rendered to Markdown (yrs), which Go cannot do.
package revisions

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

func Commands() []command.Command {
	return []command.Command{
		command.NewCommand("note.revisions", command.GroupRead, list),
		command.NewCommand("note.revision", command.GroupRead, get),
	}
}

type revision struct {
	ID            string     `json:"id"`
	NoteID        string     `json:"note_id"`
	Number        int32      `json:"number"`
	Title         string     `json:"title"`
	Reason        string     `json:"reason"`
	RestoredFrom  *string    `json:"restored_from"`
	CreatedBy     *string    `json:"created_by"`
	CreatedByName *string    `json:"created_by_name"`
	CreatedAt     command.Ts `json:"created_at"`
	ContentMD     *string    `json:"content_md"` // only on note.revision
}

const cols = `r.id::text, r.note_id::text, r.number, r.title, r.reason, r.restored_from::text, r.created_by::text, u.name, r.created_at`

func list(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select `+cols+` from core.note_revisions r left join core.users u on u.id = r.created_by
		where r.note_id = $1::uuid order by r.number desc limit 200`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []revision{}
	for rows.Next() {
		var r revision
		var at time.Time
		if err := rows.Scan(&r.ID, &r.NoteID, &r.Number, &r.Title, &r.Reason, &r.RestoredFrom, &r.CreatedBy, &r.CreatedByName, &at); err != nil {
			return nil, err
		}
		r.CreatedAt = command.Ts(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

func get(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("id")
	if err != nil {
		return nil, err
	}
	var r revision
	var at time.Time
	err = c.Tx.QueryRow(c.Context, `select `+cols+`, r.content_md from core.note_revisions r left join core.users u on u.id = r.created_by
		where r.id = $1::uuid`, id).Scan(&r.ID, &r.NoteID, &r.Number, &r.Title, &r.Reason, &r.RestoredFrom, &r.CreatedBy, &r.CreatedByName, &at, &r.ContentMD)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("version")
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt = command.Ts(at)
	// same answer as a missing version when the page itself is hidden
	if _, err := access.AssertNoteAccess(c, r.NoteID, false); err != nil {
		var ce *command.Error
		if errors.As(err, &ce) && ce.Code == command.NotFound {
			return nil, command.ErrNotFound("version")
		}
		return nil, err
	}
	return r, nil
}
