package notes

import (
	"encoding/json"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// note.watch (docs-parity 7.8, W3). Watching is per person. Auto-watch (creator, owner, commenter) is not here: the
// worker consumer (worker/pagenotify.go) adds those rows from note.created, note.updated with owner changed and
// comment.added, so write.go stays untouched. An explicit unwatch is respected until the next comment by that person.
func init() { a1Handlers["note.watch"] = noteWatch }

func noteWatch(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	noteID, err := o.reqUUID("note_id")
	if err != nil {
		return nil, err
	}
	if _, ok := o.get("watching"); !ok {
		return nil, command.ErrInvalid("watching is required")
	}
	watching, err := o.boolDef("watching", true)
	if err != nil {
		return nil, err
	}
	if c.Principal.UserID == nil {
		return nil, command.ErrForbidden("watching belongs to people")
	}
	if _, err := access.AssertNoteAccess(c, noteID, false); err != nil {
		return nil, err
	}
	if watching {
		_, err = c.Tx.Exec(c.Context,
			`insert into core.note_watches (workspace_id, user_id, note_id, reason) values ($1::uuid,$2::uuid,$3::uuid,'manual')
			 on conflict (user_id, note_id) do nothing`, c.WorkspaceID(), *c.Principal.UserID, noteID)
	} else {
		_, err = c.Tx.Exec(c.Context, `delete from core.note_watches where user_id = $1::uuid and note_id = $2::uuid`, *c.Principal.UserID, noteID)
	}
	if err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"note_id": noteID, "watching": watching}, nil
}
