package notes

import (
	"bytes"
	"encoding/json"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// note.cover_set (docs-parity-c 5.3, 13.6): set, reposition or clear the page cover. The file must be an image attached to this page.
// AssertNoteAccess(write) carries the role, guest, archived page and archived space rules, as for note.update.
func init() { c1Handlers["note.cover_set"] = noteCoverSet }

func noteCoverSet(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("note_id")
	if err != nil {
		return nil, err
	}
	var file *string
	if raw, ok := o["file_id"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var s string
		if json.Unmarshal(raw, &s) != nil || !uuidRe.MatchString(s) {
			return nil, command.ErrInvalid("file_id must be a UUID or null")
		}
		file = &s
	}
	y := 50
	if raw, ok := o["y"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if json.Unmarshal(raw, &y) != nil || y < 0 || y > 100 {
			return nil, command.ErrInvalid("y must be a whole number from 0 to 100")
		}
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	if file != nil {
		var ok bool
		if err := c.Tx.QueryRow(c.Context,
			`select exists(select 1 from core.attachments where id = $1::uuid and note_id = $2::uuid
			   and content_type in ('image/jpeg','image/png','image/gif','image/webp') and (data is not null or storage_key = 's3'))`,
			*file, id).Scan(&ok); err != nil {
			return nil, command.DB(err)
		}
		if !ok {
			return nil, command.ErrInvalid("the cover must be an image attached to this page")
		}
	}
	var cover *Cover
	var yArg *int
	if file != nil {
		cover, yArg = &Cover{FileID: *file, Y: y}, &y
	}
	if _, err := c.Tx.Exec(c.Context,
		`update core.notes set cover_file_id = $2::uuid, cover_y = $3::smallint, updated_at = now() where id = $1::uuid`, id, file, yArg); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("note.updated", &id, map[string]any{"changed": []string{"cover"}}); err != nil {
		return nil, err
	}
	return map[string]any{"cover": cover}, nil
}
