package notes

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/notify"
)

// Phase B1 P1: space members and settings (docs-parity-b 4.1, 13.2.1, 13.2.2, 13.3 rule 2).
// A space admin manages members and settings of the space and nothing else: no handler here reads or writes a page, and a page
// restriction (core.note_access) is independent of space membership, so opening a space, adding someone or promoting someone
// never reveals a restricted page. The admin commands use access.AssertSpaceAdmin, which never checks the archive, so an
// archived space can be unarchived.
func init() {
	b1Handlers["space.members"] = spaceMembers
	b1Handlers["space.member_set"] = spaceMemberSet
	b1Handlers["space.update"] = spaceUpdate
	b1Handlers["space.archive"] = spaceArchive
}

// errRestrictedIn is the refusal of a subtree command (move, reorder, delete, duplicate, undelete). In an archived space it
// gives the real reason to a caller who can read the space, who knows the space is archived anyway; everyone else, and every
// other case, gets the generic text that names nothing.
func errRestrictedIn(c *command.Ctx, space string) error {
	if access.AssertSpaceWritable(c, space) != nil && access.AssertSpaceAccess(c, space, false) == nil {
		return command.ErrForbidden(access.ArchivedMessage)
	}
	return errRestricted()
}

type spaceMember struct {
	UserID        string `json:"user_id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	WorkspaceRole string `json:"workspace_role"`
}

const memberSelect = `select u.id::text, u.name, u.email,
	       case when m.role in ('owner','admin') then 'admin' else coalesce(sm.role, 'editor') end, m.role
	  from core.memberships m join core.users u on u.id = m.user_id
	  left join core.space_members sm on sm.space_id = $1::uuid and sm.user_id = m.user_id
	 where m.workspace_id = $2::uuid`

func spaceID(input json.RawMessage, key string) (string, map[string]json.RawMessage, error) {
	var o map[string]json.RawMessage
	if json.Unmarshal(input, &o) != nil || o == nil {
		return "", nil, command.ErrInvalid("input must be an object")
	}
	var id string
	if json.Unmarshal(o[key], &id) != nil || !uuidRe.MatchString(id) {
		return "", nil, command.ErrInvalid(key + " must be a UUID")
	}
	return strings.ToLower(id), o, nil
}

func spaceMembers(c *command.Ctx, input json.RawMessage) (any, error) {
	id, _, err := spaceID(input, "space_id")
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, id, false); err != nil {
		return nil, err
	}
	var vis string
	if err := c.Tx.QueryRow(c.Context, "select visibility from core.spaces where id = $1::uuid", id).Scan(&vis); err != nil {
		return nil, command.DB(err)
	}
	// Listed people, plus workspace admins and owners (admin of every space). In an open space an unlisted member is an
	// implicit editor and is not listed: the web says so in words.
	rows, err := c.Tx.Query(c.Context, memberSelect+` and (sm.user_id is not null or m.role in ('owner','admin')) order by u.name, u.email`, id, c.WorkspaceID())
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	members := []spaceMember{}
	for rows.Next() {
		var m spaceMember
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &m.WorkspaceRole); err != nil {
			return nil, command.DB(err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"visibility": vis, "members": members, "can_manage": access.AssertSpaceAdmin(c, id) == nil}, nil
}

// lockSpace serialises every membership and settings writer of one space (the last-admin rule is checked under it).
func lockSpace(c *command.Ctx, id string) error {
	var x int
	err := c.Tx.QueryRow(c.Context, "select 1 from core.spaces where id = $1::uuid for update", id).Scan(&x)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("space")
	}
	return command.DB(err)
}

func spaceMemberSet(c *command.Ctx, input json.RawMessage) (any, error) {
	id, o, err := spaceID(input, "space_id")
	if err != nil {
		return nil, err
	}
	var user string
	if json.Unmarshal(o["user_id"], &user) != nil || !uuidRe.MatchString(user) {
		return nil, command.ErrInvalid("user_id must be a UUID")
	}
	user = strings.ToLower(user)
	var role *string
	raw, ok := o["role"]
	if !ok || json.Unmarshal(raw, &role) != nil || (role != nil && *role != "admin" && *role != "editor" && *role != "viewer") {
		return nil, command.ErrInvalid("role must be admin, editor, viewer or null")
	}
	if err := access.AssertSpaceAdmin(c, id); err != nil {
		return nil, err
	}
	if err := lockSpace(c, id); err != nil {
		return nil, err
	}
	var wsRole string
	err = c.Tx.QueryRow(c.Context, "select role from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid", c.WorkspaceID(), user).Scan(&wsRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("member")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	// Workspace admins and owners are admin of every space; only a workspace admin may touch their row.
	if (wsRole == "admin" || wsRole == "owner") && c.Principal.Role < command.Admin {
		return nil, command.ErrForbidden("only a workspace admin can change a workspace admin")
	}
	if role != nil && wsRole == "guest" {
		return nil, command.ErrInvalid("Guests cannot be added to a whole space. Share a page with them instead.")
	}
	var previous *string
	err = c.Tx.QueryRow(c.Context, "select role from core.space_members where space_id = $1::uuid and user_id = $2::uuid", id, user).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, command.DB(err)
	}
	if role == nil {
		_, err = c.Tx.Exec(c.Context, "delete from core.space_members where space_id = $1::uuid and user_id = $2::uuid", id, user)
	} else {
		_, err = c.Tx.Exec(c.Context,
			`insert into core.space_members (workspace_id, space_id, user_id, role) values ($1::uuid,$2::uuid,$3::uuid,$4)
			 on conflict (space_id, user_id) do update set role = excluded.role`, c.WorkspaceID(), id, user, *role)
	}
	if err != nil {
		return nil, command.DB(err)
	}
	var admins int
	var name, vis string
	if err := c.Tx.QueryRow(c.Context,
		`select s.name, s.visibility, (select count(*) from core.space_members where space_id = s.id and role = 'admin') from core.spaces s where s.id = $1::uuid`,
		id).Scan(&name, &vis, &admins); err != nil {
		return nil, command.DB(err)
	}
	if vis == "private" && admins == 0 { // checked after the write, under the space lock
		return nil, command.ErrConflict("A private space needs at least one admin")
	}
	if _, err := c.Emit("space.member_changed", nil, map[string]any{"space_id": id, "user_id": user, "role": role, "previous": previous}); err != nil {
		return nil, err
	}
	if role == nil {
		return map[string]any{"removed": true}, nil
	}
	if previous == nil && (c.Principal.UserID == nil || *c.Principal.UserID != user) {
		today := time.Now().UTC().Format("2006-01-02")
		src := notify.SpaceSource(id)
		if _, err := notify.Send(c.Context, c.Tx, notify.Notification{WorkspaceID: c.WorkspaceID(), UserID: user, Kind: notify.KindShared,
			Title: name, ActorUserID: c.Principal.UserID, Source: &src, SourceDay: &today}); err != nil {
			return nil, command.DB(err)
		}
	}
	var m spaceMember
	err = c.Tx.QueryRow(c.Context, memberSelect+" and m.user_id = $3::uuid", id, c.WorkspaceID(), user).Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &m.WorkspaceRole)
	return m, command.DB(err)
}

func spaceByID(c *command.Ctx, id string) (Space, error) {
	var s Space
	var cfg []byte
	err := c.Tx.QueryRow(c.Context,
		`select s.id::text, s.key, s.name, s.template, s.visibility, s.config, s.description, s.archived_at is not null, coalesce(s.public_links, true),
		        coalesce(case when $3::bool then 'admin' end, sm.role, case when s.visibility = 'open' and not $4::bool then 'editor' end)
		 from core.spaces s left join core.space_members sm on sm.space_id = s.id and sm.user_id = $2::uuid where s.id = $1::uuid`,
		id, c.Principal.UserID, c.Principal.Role >= command.Admin, c.Principal.Role == command.Guest).
		Scan(&s.ID, &s.Key, &s.Name, &s.Template, &s.Visibility, &cfg, &s.Description, &s.Archived, &s.PublicLinks, &s.MyRole)
	if err != nil {
		return s, command.DB(err)
	}
	s.Config, err = command.Norm(cfg)
	return s, err
}

func spaceUpdate(c *command.Ctx, input json.RawMessage) (any, error) {
	id, o, err := spaceID(input, "id")
	if err != nil {
		return nil, err
	}
	var name, vis *string
	var desc *string
	var links *bool
	var changed []string
	if raw, ok := o["name"]; ok {
		if json.Unmarshal(raw, &name) != nil || name == nil {
			return nil, command.ErrInvalid("name must be text")
		}
		t := strings.TrimSpace(*name)
		if n := utf8.RuneCountInString(t); n < 1 || n > 80 {
			return nil, command.ErrInvalid("name must be 1 to 80 characters")
		}
		name = &t
		changed = append(changed, "name")
	}
	if raw, ok := o["description"]; ok {
		if json.Unmarshal(raw, &desc) != nil {
			return nil, command.ErrInvalid("description must be text or null")
		}
		if desc != nil {
			t := strings.TrimSpace(*desc)
			if utf8.RuneCountInString(t) > 500 {
				return nil, command.ErrInvalid("description must be at most 500 characters")
			}
			if desc = &t; t == "" {
				desc = nil
			}
		}
		changed = append(changed, "description")
	}
	if raw, ok := o["visibility"]; ok {
		if json.Unmarshal(raw, &vis) != nil || vis == nil || (*vis != "open" && *vis != "private") {
			return nil, command.ErrInvalid("visibility must be open or private")
		}
		changed = append(changed, "visibility")
	}
	if raw, ok := o["public_links"]; ok {
		if json.Unmarshal(raw, &links) != nil || links == nil {
			return nil, command.ErrInvalid("public_links must be true or false")
		}
		changed = append(changed, "public_links")
	}
	if err := access.AssertSpaceAdmin(c, id); err != nil {
		return nil, err
	}
	if err := lockSpace(c, id); err != nil {
		return nil, err
	}
	var before string
	var linksBefore bool
	if err := c.Tx.QueryRow(c.Context, "select visibility, coalesce(public_links, true) from core.spaces where id = $1::uuid", id).Scan(&before, &linksBefore); err != nil {
		return nil, command.DB(err)
	}
	_, hasDesc := o["description"]
	if _, err := c.Tx.Exec(c.Context,
		`update core.spaces set name = coalesce($2, name), description = case when $3::bool then $4 else description end,
		        visibility = coalesce($5, visibility), public_links = coalesce($6, public_links) where id = $1::uuid`,
		id, name, hasDesc, desc, vis, links); err != nil {
		return nil, command.DB(err)
	}
	// Going private keeps the listed members and makes the caller an admin, so the space is never left without one.
	if vis != nil && *vis == "private" && before == "open" && c.Principal.UserID != nil {
		if _, err := c.Tx.Exec(c.Context,
			`insert into core.space_members (workspace_id, space_id, user_id, role) values ($1::uuid,$2::uuid,$3::uuid,'admin')
			 on conflict (space_id, user_id) do update set role = 'admin'`, c.WorkspaceID(), id, *c.Principal.UserID); err != nil {
			return nil, command.DB(err)
		}
	}
	if vis != nil && *vis == "private" {
		var admins int
		if err := c.Tx.QueryRow(c.Context, "select count(*) from core.space_members where space_id = $1::uuid and role = 'admin'", id).Scan(&admins); err != nil {
			return nil, command.DB(err)
		}
		if admins == 0 {
			return nil, command.ErrConflict("A private space needs at least one admin")
		}
	}
	if links != nil && !*links && linksBefore { // every live link dies in this transaction; turning it back on revives none
		if _, err := c.Call("page_link.revoke_space", map[string]any{"space_id": id}); err != nil {
			return nil, err
		}
	}
	if len(changed) > 0 {
		if _, err := c.Emit("space.updated", nil, map[string]any{"space_id": id, "changed": changed}); err != nil {
			return nil, err
		}
	}
	return spaceByID(c, id)
}

func spaceArchive(c *command.Ctx, input json.RawMessage) (any, error) {
	id, o, err := spaceID(input, "id")
	if err != nil {
		return nil, err
	}
	var archived bool
	if json.Unmarshal(o["archived"], &archived) != nil {
		return nil, command.ErrInvalid("archived must be true or false")
	}
	if err := access.AssertSpaceAdmin(c, id); err != nil {
		return nil, err
	}
	if err := lockSpace(c, id); err != nil {
		return nil, err
	}
	var was bool
	if err := c.Tx.QueryRow(c.Context,
		`with p as (select archived_at is not null a from core.spaces where id = $1::uuid for update),
		      u as (update core.spaces set archived_at = case when $2::bool then coalesce(archived_at, now()) end where id = $1::uuid)
		 select a from p`, id, archived).Scan(&was); err != nil {
		return nil, command.DB(err)
	}
	if was != archived {
		kind := "space.unarchived"
		if archived {
			kind = "space.archived"
		}
		if _, err := c.Emit(kind, nil, map[string]any{"space_id": id}); err != nil {
			return nil, err
		}
	}
	return spaceByID(c, id)
}
