package files

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cnote/api/internal/command"
)

// Phase A2 (docs-parity 7.9): note.duplicate copies a page's attachments through this internal command, never by touching
// core.attachments itself (module boundary). Internal: only Ctx.Call reaches it; HTTP and MCP answer not found.
// Input {from_note_id, to_note_id}. Output [{from_id, to_id}], one entry per file copied, in the source's creation order.
// L1 implements it in this file (replace copyForNote), including the object-store copy when store != nil.
func copyCommands() []command.Command {
	c := command.NewCommand("file.copy_for_note", command.GroupWriteNotes, copyForNote)
	c.Internal = true
	c.Description = "Internal. Copy every attachment of one page to another page of the same workspace as new rows and new object copies. Returns [{from_id, to_id}]."
	c.Schema = json.RawMessage(`{"type":"object","properties":{"from_note_id":{"type":"string"},"to_note_id":{"type":"string"}},"required":["from_note_id","to_note_id"]}`)
	return []command.Command{c}
}

// copyForNote copies every attachment of one page to another as new rows with new bytes. The source is checked with the file read
// guard and the destination with the write guard, so a restricted or finance page never leaks through a copy. With object storage each
// new object follows the Tables lifecycle (core.file_objects): an intent is committed first (recordIntent), the object is written,
// and it becomes live in this transaction; a rollback leaves the intent behind and RecordSweep collects the object later.
// shortcut: the object is copied with Get then Put through this process (10 MB cap); upgrade to an S3 server-side copy if files grow.
func copyForNote(c *command.Ctx, input json.RawMessage) (any, error) {
	var v struct {
		From string `json:"from_note_id"`
		To   string `json:"to_note_id"`
	}
	if err := json.Unmarshal(input, &v); err != nil || !uuidRe.MatchString(v.From) || !uuidRe.MatchString(v.To) {
		return nil, command.ErrInvalid("from_note_id and to_note_id must be UUIDs")
	}
	v.From, v.To = strings.ToLower(v.From), strings.ToLower(v.To)
	if err := guard(c, v.From, false); err != nil {
		return nil, err
	}
	if err := guard(c, v.To, true); err != nil {
		return nil, err
	}
	type src struct {
		id, name, ct, key string
		size              int64
		data              []byte
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, filename, content_type, size_bytes, storage_key, data from core.attachments
		 where note_id = $1::uuid and (data is not null or storage_key = 's3') order by created_at, id`, v.From)
	if err != nil {
		return nil, command.DB(err)
	}
	var files []src
	for rows.Next() {
		var f src
		if err := rows.Scan(&f.id, &f.name, &f.ct, &f.size, &f.key, &f.data); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		files = append(files, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	var have int
	if err := c.Tx.QueryRow(c.Context, "select count(*) from core.attachments where note_id = $1::uuid and (data is not null or storage_key = 's3')", v.To).Scan(&have); err != nil {
		return nil, command.DB(err)
	}
	if have+len(files) > maxPerNote {
		return nil, command.ErrConflict("a page can hold at most 10 files")
	}
	ws := c.WorkspaceID()
	out := []map[string]string{}
	for _, f := range files {
		bytes, key, newID := f.data, "pg", ""
		if f.key == "s3" || store != nil {
			if store == nil {
				return nil, fmt.Errorf("file is in object storage but FILES_S3_* is not configured")
			}
			if f.key == "s3" {
				b, err := store.Get(c.Context, ws+"/"+f.id)
				if err != nil {
					return nil, fmt.Errorf("file storage: %w", err)
				}
				bytes = b
			}
			it, err := recordIntent(c, nil)
			if err != nil {
				return nil, err
			}
			newID = it.(map[string]any)["object_id"].(string)
			key = "s3"
		}
		var id string
		var stored []byte
		if key == "pg" {
			stored = bytes
		}
		if err := c.Tx.QueryRow(c.Context,
			`insert into core.attachments (id, workspace_id, note_id, filename, content_type, size_bytes, storage_key, data, created_by)
			 values (coalesce(nullif($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9::uuid) returning id::text`,
			newID, ws, v.To, f.name, f.ct, f.size, key, stored, c.Principal.UserID).Scan(&id); err != nil {
			return nil, command.DB(err)
		}
		if key == "s3" {
			if err := store.Put(c.Context, ws+"/"+id, bytes); err != nil {
				return nil, fmt.Errorf("file storage: %w", err)
			}
			if _, err := c.Tx.Exec(c.Context, `update core.file_objects set state = 'live' where workspace_id = $1::uuid and key = $2`, ws, ws+"/"+id); err != nil {
				return nil, command.DB(err)
			}
		}
		out = append(out, map[string]string{"from_id": f.id, "to_id": id})
	}
	return out, nil
}
