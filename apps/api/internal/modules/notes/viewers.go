package notes

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// note.viewers (docs-parity-b 5.6): how many current workspace members opened a page, and for editors, who and when.
func init() { b2Handlers["note.viewers"] = noteViewers }

func noteViewers(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := noteIDInput(input, nil)
	if err != nil {
		return nil, err
	}
	if _, err := requireUser(c); err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	var count int
	if err := c.Tx.QueryRow(c.Context,
		`select count(*) from core.note_views v join core.memberships m on m.user_id = v.user_id and m.workspace_id = v.workspace_id
		 where v.note_id = $1::uuid`, id).Scan(&count); err != nil {
		return nil, command.DB(err)
	}
	out := map[string]any{"count": count}
	if c.Principal.Role < command.Member { // a guest gets the number only, even with edit access to the page
		return out, nil
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		var ce *command.Error
		if errors.As(err, &ce) && (ce.Code == command.Forbidden || ce.Code == command.NotFound) {
			return out, nil
		}
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select u.id::text, u.name, v.last_viewed_at from core.note_views v
		   join core.memberships m on m.user_id = v.user_id and m.workspace_id = v.workspace_id
		   join core.users u on u.id = v.user_id
		 where v.note_id = $1::uuid order by v.last_viewed_at desc, u.id limit 100`, id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	viewers := []map[string]any{}
	for rows.Next() {
		var uid, name string
		var at time.Time
		if err := rows.Scan(&uid, &name, &at); err != nil {
			return nil, command.DB(err)
		}
		viewers = append(viewers, map[string]any{"user_id": uid, "name": name, "last_viewed_at": command.Ts(at)})
	}
	out["viewers"] = viewers
	return out, command.DB(rows.Err())
}
