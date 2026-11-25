package notes

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// Comment edit, delete and reactions (docs-parity-b 4.5). Every command resolves the comment to its page and calls
// AssertNoteAccess first, so a person without access gets "comment not found" and cannot tell whether it exists.

var reactionSet = []string{"👍", "❤️", "😂", "🎉", "👀", "✅"}

type Reaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

func init() {
	b1Handlers["comment.edit"] = commentEdit
	b1Handlers["comment.delete"] = commentDelete
	b1Handlers["comment.react"] = commentReact
}

// reactionsFor counts reactions for the given comments. Callers have already passed page access for all of them (7.6).
func reactionsFor(c *command.Ctx, ids []string) (map[string][]Reaction, error) {
	out := map[string][]Reaction{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.Tx.Query(c.Context,
		`select comment_id::text, emoji, count(*), coalesce(bool_or(user_id = $2::uuid), false)
		 from core.comment_reactions where comment_id = any($1::uuid[]) group by comment_id, emoji`, ids, c.Principal.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var r Reaction
		if err := rows.Scan(&id, &r.Emoji, &r.Count, &r.Mine); err != nil {
			return nil, err
		}
		out[id] = append(out[id], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id, rs := range out { // the fixed order of the six, not the database's
		ordered := make([]Reaction, 0, len(rs))
		for _, e := range reactionSet {
			for _, r := range rs {
				if r.Emoji == e {
					ordered = append(ordered, r)
				}
			}
		}
		out[id] = ordered
	}
	return out, nil
}

// commentPage resolves a comment to its page, checks access and the archived gate, and says whether it is deleted.
func commentPage(c *command.Ctx, id string) (note string, author *string, deleted bool, err error) {
	err = c.Tx.QueryRow(c.Context, "select note_id::text, author_id::text, deleted_at is not null from core.comments where id = $1::uuid", id).
		Scan(&note, &author, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, command.ErrNotFound("comment")
	}
	if err != nil {
		return "", nil, false, err
	}
	space, err := access.AssertNoteAccess(c, note, false)
	if err != nil {
		return "", nil, false, err
	}
	return note, author, deleted, access.AssertSpaceWritable(c, space)
}

func commentEdit(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	body, err := o.reqStr("body")
	if err != nil {
		return nil, err
	}
	note, author, deleted, err := commentPage(c, id)
	if err != nil {
		return nil, err
	}
	if deleted {
		return nil, command.ErrNotFound("comment")
	}
	if author == nil || c.Principal.UserID == nil || !strings.EqualFold(*author, *c.Principal.UserID) {
		return nil, command.ErrForbidden("only the author can edit this comment")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, command.ErrInvalid("comment body is empty")
	}
	var old, title string
	if err := c.Tx.QueryRow(c.Context,
		`update core.comments set body = $2, edited_at = now() where id = $1::uuid
		 returning (select body from core.comments where id = $1::uuid), (select title from core.notes where id = note_id)`, id, body).
		Scan(&old, &title); err != nil {
		return nil, err
	}
	if _, err := c.Emit("comment.edited", &note, map[string]any{"comment_id": id}); err != nil {
		return nil, err
	}
	// Only people newly mentioned are told; the event key is the comment's own, so nobody is told twice.
	if err := notifyMentions(c, note, title, old, body, "comment:"+id); err != nil {
		return nil, err
	}
	return commentByID(c, id)
}

func commentDelete(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	note, author, deleted, err := commentPage(c, id)
	if err != nil {
		return nil, err
	}
	if deleted {
		return map[string]any{"deleted": true}, nil
	}
	byAuthor := author != nil && c.Principal.UserID != nil && strings.EqualFold(*author, *c.Principal.UserID)
	if !byAuthor {
		var mod bool // the page owner, a space admin or a workspace admin
		if err := c.Tx.QueryRow(c.Context,
			`select $2::bool or coalesce(n.owner_id, n.created_by) = $3::uuid
			    or exists(select 1 from core.space_members m where m.space_id = n.space_id and m.user_id = $3::uuid and m.role = 'admin')
			 from core.notes n where n.id = $1::uuid`, note, c.Principal.Role >= command.Admin, c.Principal.UserID).Scan(&mod); err != nil {
			return nil, err
		}
		if !mod {
			return nil, command.ErrForbidden("only the author, the page owner or a space admin can delete this comment")
		}
	}
	if _, err := c.Tx.Exec(c.Context,
		"update core.comments set body = '', deleted_at = now(), deleted_by = $2::uuid where id = $1::uuid", id, c.Principal.UserID); err != nil {
		return nil, err
	}
	if _, err := c.Emit("comment.deleted", &note, map[string]any{"comment_id": id, "by_author": byAuthor}); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func commentReact(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	emoji, err := o.reqStr("emoji")
	if err != nil {
		return nil, err
	}
	on, err := o.boolDef("on", true)
	if err != nil {
		return nil, err
	}
	known := false
	for _, e := range reactionSet {
		known = known || e == emoji
	}
	if !known {
		return nil, command.ErrInvalid("emoji must be one of " + strings.Join(reactionSet, " "))
	}
	if c.Principal.UserID == nil {
		return nil, command.ErrForbidden("a reaction belongs to a person, not to an API key")
	}
	_, _, deleted, err := commentPage(c, id)
	if err != nil {
		return nil, err
	}
	if deleted {
		return nil, command.ErrNotFound("comment")
	}
	q := `insert into core.comment_reactions (workspace_id, comment_id, user_id, emoji) values ($1::uuid,$2::uuid,$3::uuid,$4)
	      on conflict do nothing` // a second tap of the same emoji changes nothing
	if !on {
		q = "delete from core.comment_reactions where workspace_id = $1::uuid and comment_id = $2::uuid and user_id = $3::uuid and emoji = $4"
	}
	if _, err := c.Tx.Exec(c.Context, q, c.WorkspaceID(), id, c.Principal.UserID, emoji); err != nil {
		return nil, err
	}
	rs, err := reactionsFor(c, []string{id})
	if err != nil {
		return nil, err
	}
	out := rs[id]
	if out == nil {
		out = []Reaction{}
	}
	return map[string]any{"id": id, "reactions": out}, nil
}
