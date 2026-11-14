// Package sharing ports crates/cnote-core/src/sharing.rs: page restrictions and per-person sharing on top
// of core.note_access (note.permissions, note.restrict, note.share, note.unshare, note.shared_with_me).
package sharing

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

func Commands() []command.Command {
	return []command.Command{
		command.NewCommand("note.permissions", command.GroupRead, permissions),
		command.NewCommand("note.restrict", command.GroupWriteNotes, restrict),
		command.NewCommand("note.share", command.GroupWriteNotes, share),
		command.NewCommand("note.unshare", command.GroupWriteNotes, unshare),
		command.NewCommand("note.shared_with_me", command.GroupRead, sharedWithMe),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// fields decodes a JSON object into exact-case keys (serde is case sensitive, encoding/json is not).
// Anything serde would reject returns ErrFallback so Rust produces the error text.
type fields map[string]json.RawMessage

func decode(input json.RawMessage) (fields, error) {
	var f fields
	if json.Unmarshal(input, &f) != nil || f == nil {
		return nil, command.ErrFallback
	}
	return f, nil
}

func (f fields) uuid(key string, optional bool) (*string, error) {
	raw, ok := f[key]
	if !ok || string(raw) == "null" {
		if optional {
			return nil, nil
		}
		return nil, command.ErrFallback
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || !uuidRe.MatchString(s) {
		return nil, command.ErrFallback
	}
	s = strings.ToLower(s)
	return &s, nil
}

func (f fields) str(key string, optional bool) (*string, error) {
	raw, ok := f[key]
	if !ok || string(raw) == "null" {
		if optional {
			return nil, nil
		}
		return nil, command.ErrFallback
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return nil, command.ErrFallback
	}
	return &s, nil
}

// ---------- note.permissions ----------

type Person struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Level  string `json:"level"`
}

type InheritedFrom struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Restriction string `json:"restriction"`
}

type Permissions struct {
	Restriction   string         `json:"restriction"`
	InheritedFrom *InheritedFrom `json:"inherited_from"`
	People        []Person       `json:"people"`
	CanManage     bool           `json:"can_manage"`
}

func permissions(c *command.Ctx, input json.RawMessage) (any, error) {
	f, err := decode(input)
	if err != nil {
		return nil, err
	}
	noteID, err := f.uuid("note_id", false)
	if err != nil {
		return nil, err
	}
	id := *noteID
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	_, mgErr := access.AssertNoteAccess(c, id, true)
	var restriction string
	if err := c.Tx.QueryRow(c.Context, "select restriction from core.notes where id = $1::uuid", id).Scan(&restriction); err != nil {
		return nil, command.DB(err)
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	out := Permissions{Restriction: restriction, People: []Person{}, CanManage: mgErr == nil}
	rows, err := c.Tx.Query(c.Context,
		`with recursive up as (
           select p.id, p.parent_id, p.title, p.restriction, 1 d from core.notes n join core.notes p on p.id = n.parent_id where n.id = $1::uuid
           union all
           select p.id, p.parent_id, p.title, p.restriction, up.d + 1 from up join core.notes p on p.id = up.parent_id where up.d < 50)
         select id::text, case when core.note_access(id, $3::uuid, $4, $2::text[]::uuid[]) = 'none' then '' else title end as title, restriction
         from up where restriction <> 'none' order by d limit 1`, id, spaces, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	for rows.Next() {
		var h InheritedFrom
		if err := rows.Scan(&h.ID, &h.Title, &h.Restriction); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		out.InheritedFrom = &h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	prows, err := c.Tx.Query(c.Context,
		`select p.user_id::text, u.name, u.email, p.level from core.note_permissions p join core.users u on u.id = p.user_id
         where p.note_id = $1::uuid order by u.name`, id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer prows.Close()
	for prows.Next() {
		var p Person
		if err := prows.Scan(&p.UserID, &p.Name, &p.Email, &p.Level); err != nil {
			return nil, command.DB(err)
		}
		out.People = append(out.People, p)
	}
	return out, command.DB(prows.Err())
}

// ---------- note.restrict / share / unshare ----------

func upsert(c *command.Ctx, noteID, user, level string) error {
	_, err := c.Tx.Exec(c.Context,
		`insert into core.note_permissions (note_id, workspace_id, user_id, level, granted_by) values ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid)
         on conflict (note_id, user_id) do update set level = excluded.level, granted_by = excluded.granted_by`,
		noteID, c.WorkspaceID(), user, level, c.Principal.UserID)
	return command.DB(err)
}

// A restricted page must always keep someone who can edit it.
func assertEditorLeft(c *command.Ctx, noteID string) error {
	var ok bool
	if err := c.Tx.QueryRow(c.Context,
		`select n.restriction = 'none' or exists(select 1 from core.note_permissions p where p.note_id = n.id and p.level = 'edit')
         from core.notes n where n.id = $1::uuid`, noteID).Scan(&ok); err != nil {
		return command.DB(err)
	}
	if !ok {
		return command.ErrConflict("a restricted page needs at least one person who can edit it")
	}
	return nil
}

func restrict(c *command.Ctx, input json.RawMessage) (any, error) {
	f, err := decode(input)
	if err != nil {
		return nil, err
	}
	noteID, err := f.uuid("note_id", false)
	if err != nil {
		return nil, err
	}
	rs, err := f.str("restriction", false)
	if err != nil {
		return nil, err
	}
	id, r := *noteID, *rs
	if r != "none" && r != "edit" && r != "view" {
		return nil, command.ErrInvalid("restriction must be none, edit or view")
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update core.notes set restriction = $2 where id = $1::uuid", id, r); err != nil {
		return nil, command.DB(err)
	}
	// The person restricting keeps edit access, so nobody locks themselves out.
	if r != "none" && c.Principal.UserID != nil {
		if err := upsert(c, id, *c.Principal.UserID, "edit"); err != nil {
			return nil, err
		}
	}
	if _, err := c.Emit("note.restricted", &id, map[string]any{"restriction": r}); err != nil {
		return nil, err
	}
	return map[string]any{"restriction": r}, nil
}

func share(c *command.Ctx, input json.RawMessage) (any, error) {
	f, err := decode(input)
	if err != nil {
		return nil, err
	}
	noteID, err := f.uuid("note_id", false)
	if err != nil {
		return nil, err
	}
	userID, err := f.uuid("user_id", true)
	if err != nil {
		return nil, err
	}
	email, err := f.str("email", true)
	if err != nil {
		return nil, err
	}
	lv, err := f.str("level", false)
	if err != nil {
		return nil, err
	}
	id, level := *noteID, *lv
	if level != "view" && level != "edit" {
		return nil, command.ErrInvalid("level must be view or edit")
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	var user *string
	switch {
	case userID != nil:
		err = c.Tx.QueryRow(c.Context, "select user_id::text from core.memberships where user_id = $1::uuid", *userID).Scan(&user)
	case email != nil:
		err = c.Tx.QueryRow(c.Context,
			"select m.user_id::text from core.memberships m join core.users u on u.id = m.user_id where lower(u.email) = lower($1)",
			strings.TrimSpace(*email)).Scan(&user)
	default:
		return nil, command.ErrInvalid("give user_id or email")
	}
	if err != nil && command.DB(err).(*command.Error).Code != command.NotFound {
		return nil, command.DB(err)
	}
	if err != nil || user == nil {
		return nil, command.ErrInvalid("not a member of this workspace")
	}
	if err := upsert(c, id, *user, level); err != nil {
		return nil, err
	}
	if err := assertEditorLeft(c, id); err != nil {
		return nil, err
	}
	out := map[string]any{"user_id": *user, "level": level}
	if _, err := c.Emit("note.shared", &id, out); err != nil {
		return nil, err
	}
	return out, nil
}

func unshare(c *command.Ctx, input json.RawMessage) (any, error) {
	f, err := decode(input)
	if err != nil {
		return nil, err
	}
	noteID, err := f.uuid("note_id", false)
	if err != nil {
		return nil, err
	}
	userID, err := f.uuid("user_id", false)
	if err != nil {
		return nil, err
	}
	id := *noteID
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "delete from core.note_permissions where note_id = $1::uuid and user_id = $2::uuid", id, *userID); err != nil {
		return nil, command.DB(err)
	}
	if err := assertEditorLeft(c, id); err != nil {
		return nil, err
	}
	if _, err := c.Emit("note.unshared", &id, map[string]any{"user_id": *userID}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ---------- note.shared_with_me ----------

type SharedNote struct {
	notes.Note
	Level string `json:"level"`
}

// Pages shared to the caller that sit in spaces the caller cannot otherwise read.
func sharedWithMe(c *command.Ctx, input json.RawMessage) (any, error) {
	if _, err := decode(input); err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select id::text, space_id::text, parent_id::text, title, types, content_md, owner_id::text, verify_by::text,
	verified_at, freshness, source_system, source_id, source_url, created_by::text, created_at, updated_at,
	version, restriction, collab_epoch,
	(select p.level from core.note_permissions p where p.note_id = notes.id and p.user_id = $1::uuid) as level
         from core.notes
         where deleted_at is null and space_id <> all($2::text[]::uuid[])
           and exists (select 1 from core.note_permissions p where p.note_id = notes.id and p.user_id = $1::uuid)
           and core.note_access(id, $1::uuid, $3, $2::text[]::uuid[]) <> 'none'
         order by updated_at desc`, uid, spaces, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []SharedNote{}
	for rows.Next() {
		var n SharedNote
		var verifiedAt *time.Time
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&n.ID, &n.SpaceID, &n.ParentID, &n.Title, &n.Types, &n.ContentMD, &n.OwnerID, &n.VerifyBy,
			&verifiedAt, &n.Freshness, &n.SourceSystem, &n.SourceID, &n.SourceURL, &n.CreatedBy, &createdAt, &updatedAt,
			&n.Version, &n.Restriction, &n.CollabEpoch, &n.Level); err != nil {
			return nil, command.DB(err)
		}
		n.VerifiedAt, n.CreatedAt, n.UpdatedAt = command.TsPtr(verifiedAt), command.Ts(createdAt), command.Ts(updatedAt)
		if n.Types == nil {
			n.Types = []string{}
		}
		out = append(out, n)
	}
	return out, command.DB(rows.Err())
}
