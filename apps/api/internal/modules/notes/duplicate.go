package notes

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

func init() { a2Handlers["note.duplicate"] = noteDuplicate }

// note.duplicate (docs-parity 7.9 as revised by 7.10.3 and 7.10.4): a sibling copy with the folded content, the page's own
// restriction and permission rows (copied server side, never returned) and its attachments (file.copy_for_note, which checks the
// source with the file read guard and the new page with the write guard).
func noteDuplicate(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	if err := requireStructural(c); err != nil {
		return nil, err
	}
	space, err := access.AssertNoteAccess(c, id, false)
	if err != nil {
		return nil, err
	}
	if err := lockTree(c, space); err != nil {
		return nil, err
	}
	src, err := loadNote(c, id)
	if err != nil {
		return nil, err
	}
	if len(src.Types) > 0 {
		return nil, command.ErrInvalid("Only plain pages can be duplicated")
	}
	if ok, err := canEditParent(c, space, src.ParentID); err != nil {
		return nil, err
	} else if !ok {
		return nil, errRestrictedIn(c, space)
	}
	// A restricted source (its own restriction or an inherited one) is copied only by someone who could manage that restriction.
	_, chain, err := chainDepth(c, &id)
	if err != nil {
		return nil, err
	}
	for _, a := range chain {
		if a.Restriction == "none" {
			continue
		}
		if ok, err := canManage(c, space, a.Owner); err != nil {
			return nil, err
		} else if !ok {
			return nil, errRestrictedIn(c, space)
		}
	}
	if d, _, err := chainDepth(c, src.ParentID); err != nil {
		return nil, err
	} else if d+1 > maxTreeDepth {
		return nil, errTooDeep()
	}
	pending, err := pendingPages(c, []string{id})
	if err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		if err := foldNote(c, id, false); err != nil {
			return nil, err
		}
		if src, err = loadNote(c, id); err != nil { // the folded content
			return nil, err
		}
	}
	title := strings.TrimSpace(src.Title) + " (copy)"
	if utf8.RuneCountInString(title) > 200 {
		title = string([]rune(title)[:200])
	}
	vby := time.Now().UTC().Add(defaultVerifyDays * 24 * time.Hour).Format("2006-01-02")
	n, err := scanNote(c.Tx.QueryRow(c.Context,
		`insert into core.notes (workspace_id, space_id, parent_id, title, types, content_md, owner_id, verify_by, created_by, icon, wide, restriction)
		 select workspace_id, space_id, parent_id, $2, '{}', content_md, $3::uuid, $4::date, $3::uuid, icon, wide, restriction
		 from core.notes where id = $1::uuid returning `+noteCols, id, title, c.Principal.UserID, vby))
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into core.note_permissions (note_id, workspace_id, user_id, level, granted_by)
		 select $2::uuid, workspace_id, user_id, level, $3::uuid from core.note_permissions where note_id = $1::uuid`, id, n.ID, c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	if n.Restriction != "none" && c.Principal.UserID != nil { // nobody, the copier included, is locked out
		if _, err := c.Tx.Exec(c.Context,
			`insert into core.note_permissions (note_id, workspace_id, user_id, level, granted_by) values ($1::uuid,$2::uuid,$3::uuid,'edit',$3::uuid)
			 on conflict (note_id, user_id) do update set level = 'edit'`, n.ID, c.WorkspaceID(), *c.Principal.UserID); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into core.note_labels (workspace_id, note_id, label_id, created_by)
		 select workspace_id, $2::uuid, label_id, $3::uuid from core.note_labels where note_id = $1::uuid`, id, n.ID, c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	if n.ParentID != nil {
		if err := addRelation(c, *n.ParentID, n.ID, "child", nil); err != nil {
			return nil, err
		}
	}
	raw, err := c.Call("file.copy_for_note", map[string]string{"from_note_id": id, "to_note_id": n.ID})
	if err != nil {
		return nil, err
	}
	var pairs []struct {
		From string `json:"from_id"`
		To   string `json:"to_id"`
	}
	if err := json.Unmarshal(raw, &pairs); err != nil {
		return nil, command.NewError(command.Internal, "file copy answered something unreadable")
	}
	if len(pairs) > 0 {
		var repl []string
		for _, p := range pairs {
			repl = append(repl, "/api/w/"+c.WorkspaceID()+"/files/"+p.From, "/api/w/"+c.WorkspaceID()+"/files/"+p.To)
		}
		body := strings.NewReplacer(repl...).Replace(n.ContentMD)
		if body != n.ContentMD { // the search_plain trigger follows the body
			if _, err := c.Tx.Exec(c.Context, "update core.notes set content_md = $2 where id = $1::uuid", n.ID, body); err != nil {
				return nil, command.DB(err)
			}
			n.ContentMD = body
		}
	}
	if _, err := c.Emit("note.created", &n.ID, map[string]any{"title": n.Title, "types": []string{}, "space_id": n.SpaceID, "duplicate_of": id}); err != nil {
		return nil, err
	}
	return map[string]any{"id": n.ID, "space_id": n.SpaceID, "parent_id": n.ParentID, "title": n.Title, "position": n.Position, "attachments_copied": len(pairs)}, nil
}
