// Package notes ports space.list, note.get and note.history from crates/cnote-core/src/notes.rs.
// Rust serializes handler output through serde_json::Value, so response keys come out sorted; the
// command layer reproduces that (command.serialize). Embedded structs inline like #[serde(flatten)].
package notes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

func Commands() []command.Command {
	return []command.Command{
		command.NewCommand("space.list", command.GroupRead, spaceList),
		command.NewCommand("note.get", command.GroupRead, noteGet),
		command.NewCommand("note.history", command.GroupRead, noteHistory),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// byID decodes `{ "id": <uuid> }`. Anything serde would reject (or spell differently) falls back to Rust
// so error messages stay identical.
func byID(input json.RawMessage) (string, error) {
	var in struct {
		ID *string `json:"id"`
	}
	if json.Unmarshal(input, &in) != nil || in.ID == nil || !uuidRe.MatchString(*in.ID) {
		return "", command.ErrFallback
	}
	return strings.ToLower(*in.ID), nil
}

// ---------- space.list ----------

type Space struct {
	ID         string          `json:"id"`
	Key        string          `json:"key"`
	Name       string          `json:"name"`
	Template   string          `json:"template"`
	Visibility string          `json:"visibility"`
	Config     json.RawMessage `json:"config"`
}

func spaceList(c *command.Ctx, input json.RawMessage) (any, error) {
	var obj map[string]json.RawMessage // `Empty {}` accepts any object and ignores fields
	if json.Unmarshal(input, &obj) != nil || obj == nil {
		return nil, command.ErrFallback
	}
	ids, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		"select id::text, key, name, template, visibility, config from core.spaces where id = any($1::text[]::uuid[]) order by name", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Space{}
	for rows.Next() {
		var s Space
		var cfg []byte
		if err := rows.Scan(&s.ID, &s.Key, &s.Name, &s.Template, &s.Visibility, &cfg); err != nil {
			return nil, err
		}
		if s.Config, err = command.Norm(cfg); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------- note.get ----------

type Note struct {
	ID           string      `json:"id"`
	SpaceID      string      `json:"space_id"`
	ParentID     *string     `json:"parent_id"`
	Title        string      `json:"title"`
	Types        []string    `json:"types"`
	ContentMD    string      `json:"content_md"`
	OwnerID      *string     `json:"owner_id"`
	VerifyBy     *string     `json:"verify_by"`
	VerifiedAt   *command.Ts `json:"verified_at"`
	Freshness    string      `json:"freshness"`
	SourceSystem *string     `json:"source_system"`
	SourceID     *string     `json:"source_id"`
	SourceURL    *string     `json:"source_url"`
	CreatedBy    *string     `json:"created_by"`
	CreatedAt    command.Ts  `json:"created_at"`
	UpdatedAt    command.Ts  `json:"updated_at"`
	Version      int32       `json:"version"`
	Restriction  string      `json:"restriction"`
	CollabEpoch  int32       `json:"collab_epoch"`
}

type Relation struct {
	ID          string  `json:"id"`
	FromID      string  `json:"from_id"`
	ToID        string  `json:"to_id"`
	Kind        string  `json:"kind"`
	ExternalRef *string `json:"external_ref"`
	OtherTitle  *string `json:"other_title"`
}

type NoteDetail struct {
	Note
	Relations []Relation `json:"relations"`
	URI       string     `json:"uri"`
	Level     string     `json:"level"`
}

const noteCols = `id::text, space_id::text, parent_id::text, title, types, content_md, owner_id::text, verify_by::text,
	verified_at, freshness, source_system, source_id, source_url, created_by::text, created_at, updated_at,
	version, restriction, collab_epoch`

func loadNote(c *command.Ctx, id string) (Note, error) {
	var n Note
	var verifiedAt *time.Time
	var createdAt, updatedAt time.Time
	err := c.Tx.QueryRow(c.Context, "select "+noteCols+" from core.notes where id = $1::uuid and deleted_at is null", id).Scan(
		&n.ID, &n.SpaceID, &n.ParentID, &n.Title, &n.Types, &n.ContentMD, &n.OwnerID, &n.VerifyBy,
		&verifiedAt, &n.Freshness, &n.SourceSystem, &n.SourceID, &n.SourceURL, &n.CreatedBy, &createdAt, &updatedAt,
		&n.Version, &n.Restriction, &n.CollabEpoch)
	if err == pgx.ErrNoRows {
		return n, command.ErrNotFound("note")
	}
	n.VerifiedAt, n.CreatedAt, n.UpdatedAt = command.TsPtr(verifiedAt), command.Ts(createdAt), command.Ts(updatedAt)
	if n.Types == nil {
		n.Types = []string{}
	}
	return n, err
}

// Relations whose other end the caller can read (no titles leak from private spaces).
func listRelations(c *command.Ctx, id string) ([]Relation, error) {
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select r.id::text, r.from_id::text, r.to_id::text, r.kind, r.external_ref, o.title as other_title
		 from core.relations r
		 join core.notes o on o.id = case when r.from_id = $1::uuid then r.to_id else r.from_id end
		 where (r.from_id = $1::uuid or r.to_id = $1::uuid) and o.deleted_at is null and o.space_id = any($2::text[]::uuid[])
		   and core.note_access(o.id, $3::uuid, $4, $2::text[]::uuid[]) <> 'none'
		 order by r.created_at`, id, spaces, uid, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Relation{}
	for rows.Next() {
		var r Relation
		if err := rows.Scan(&r.ID, &r.FromID, &r.ToID, &r.Kind, &r.ExternalRef, &r.OtherTitle); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func noteGet(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	level := "view"
	if _, err := access.AssertNoteAccess(c, id, true); err == nil {
		level = "edit"
	}
	note, err := loadNote(c, id)
	if err != nil {
		return nil, err
	}
	rels, err := listRelations(c, id)
	if err != nil {
		return nil, err
	}
	return NoteDetail{note, rels, fmt.Sprintf("cnote://ws/%s/note/%s", c.WorkspaceID(), note.ID), level}, nil
}

// ---------- note.history ----------

type Event struct {
	ID          int64           `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	NoteID      *string         `json:"note_id"`
	Kind        string          `json:"kind"`
	ActorType   string          `json:"actor_type"`
	ActorID     *string         `json:"actor_id"`
	OnBehalfOf  *string         `json:"on_behalf_of"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   command.Ts      `json:"created_at"`
}

type HistoryOut struct {
	Event
	ActorName   *string `json:"actor_name"`
	SubjectName *string `json:"subject_name"`
}

func noteHistory(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select e.id, e.workspace_id::text, e.note_id::text, e.kind, e.actor_type, e.actor_id, e.on_behalf_of::text, e.payload, e.created_at,
		   coalesce(u.name, a.name) as actor_name,
		   (select s.name from core.users s
		     where s.id::text = coalesce(e.payload->>'user_id', e.payload->>'assignee_id',
		             case when e.kind = 'doc.owner_changed' then e.payload->>'to' end,
		             (select n.user_id::text from core.notifications n where n.id::text = e.payload->>'notification_id'))
		   ) as subject_name
		 from core.events e
		 left join core.users u on e.actor_type = 'user' and u.id::text = e.actor_id
		 left join core.agents a on e.actor_type = 'agent' and a.id::text = e.actor_id
		 where e.note_id = $1::uuid order by e.id desc limit 200`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryOut{}
	for rows.Next() {
		var h HistoryOut
		var payload []byte
		var created time.Time
		if err := rows.Scan(&h.ID, &h.WorkspaceID, &h.NoteID, &h.Kind, &h.ActorType, &h.ActorID, &h.OnBehalfOf,
			&payload, &created, &h.ActorName, &h.SubjectName); err != nil {
			return nil, err
		}
		h.CreatedAt = command.Ts(created)
		if h.Payload, err = command.Norm(payload); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
