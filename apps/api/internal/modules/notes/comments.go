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
)

// Comment commands, split out of write.go in docs-parity step 0 so the watch and notification package (W3, 7.8) owns them.

// ---------- comments ----------

type Comment struct {
	ID             string      `json:"id"`
	NoteID         string      `json:"note_id"`
	AuthorID       *string     `json:"author_id"`
	AuthorName     *string     `json:"author_name"`
	Body           string      `json:"body"`
	CreatedAt      command.Ts  `json:"created_at"`
	AnchorText     *string     `json:"anchor_text"`
	AnchorPrefix   *string     `json:"anchor_prefix"`
	ParentID       *string     `json:"parent_id"`
	ResolvedAt     *command.Ts `json:"resolved_at"`
	ResolvedByName *string     `json:"resolved_by_name"`
	EditedAt       *command.Ts `json:"edited_at"`
	Deleted        bool        `json:"deleted"`
	Reactions      []Reaction  `json:"reactions"`
}

// A deleted comment (a tombstone) shows no body, author or anchor.
const commentCols = `c.id::text, c.note_id::text, c.author_id::text, c.author_name, c.body, c.created_at, c.anchor_text,
	c.anchor_prefix, c.parent_id::text, c.resolved_at, (select name from core.users where id = c.resolved_by) as resolved_by_name,
	c.edited_at, c.deleted_at is not null`

func scanComment(row pgx.Row) (Comment, error) {
	var m Comment
	var created time.Time
	var resolved, edited *time.Time
	err := row.Scan(&m.ID, &m.NoteID, &m.AuthorID, &m.AuthorName, &m.Body, &created, &m.AnchorText,
		&m.AnchorPrefix, &m.ParentID, &resolved, &m.ResolvedByName, &edited, &m.Deleted)
	m.CreatedAt, m.ResolvedAt, m.EditedAt = command.Ts(created), command.TsPtr(resolved), command.TsPtr(edited)
	m.Reactions = []Reaction{}
	if m.Deleted {
		m.Body, m.AuthorID, m.AuthorName, m.AnchorText, m.AnchorPrefix = "", nil, nil, nil, nil
	}
	return m, err
}

func commentByID(c *command.Ctx, id string) (Comment, error) {
	m, err := scanComment(c.Tx.QueryRow(c.Context, "select "+commentCols+" from core.comments c where c.id = $1::uuid", id))
	if err != nil {
		return m, err
	}
	rs, err := reactionsFor(c, []string{id})
	if rs[id] != nil {
		m.Reactions = rs[id]
	}
	return m, err
}

func commentAdd(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	noteID, err := o.reqUUID("note_id")
	if err != nil {
		return nil, err
	}
	body, err := o.reqStr("body")
	if err != nil {
		return nil, err
	}
	anchor, err := o.optStr("anchor_text")
	if err != nil {
		return nil, err
	}
	prefix, err := o.optStr("anchor_prefix")
	if err != nil {
		return nil, err
	}
	parent, err := o.optUUID("parent_id")
	if err != nil {
		return nil, err
	}

	space, err := access.AssertNoteAccess(c, noteID, false)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceWritable(c, space); err != nil { // a comment is a write: an archived space refuses it (13.2.2)
		return nil, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, command.ErrInvalid("comment body is empty")
	}
	if anchor != nil && strings.TrimSpace(*anchor) == "" {
		anchor = nil
	}
	if anchor != nil && utf8.RuneCountInString(*anchor) > 500 || prefix != nil && utf8.RuneCountInString(*prefix) > 64 {
		return nil, command.ErrInvalid("anchor_text is at most 500 and anchor_prefix at most 64 characters")
	}
	if parent != nil {
		if anchor != nil {
			return nil, command.ErrInvalid("a reply cannot have its own anchor")
		}
		var top *bool
		err := c.Tx.QueryRow(c.Context, "select parent_id is null from core.comments where id = $1::uuid and note_id = $2::uuid and deleted_at is null", *parent, noteID).Scan(&top)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if top == nil || !*top {
			return nil, command.ErrInvalid("parent_id must be a top-level comment on this note")
		}
	}
	if anchor == nil {
		prefix = nil
	}
	var id string
	if err := c.Tx.QueryRow(c.Context,
		`insert into core.comments (workspace_id, note_id, author_id, author_name, body, anchor_text, anchor_prefix, parent_id)
		 values ($1::uuid,$2::uuid,$3::uuid,(select name from core.users where id = $3::uuid),$4,$5,$6,$7::uuid) returning id::text`,
		c.WorkspaceID(), noteID, c.Principal.UserID, body, anchor, prefix, parent).Scan(&id); err != nil {
		return nil, err
	}
	if _, err := c.Emit("comment.added", &noteID, map[string]any{"comment_id": id, "inline": anchor != nil, "reply": parent != nil}); err != nil {
		return nil, err
	}
	// @mentions in a comment use the page format and notify the same way.
	var title string
	if err := c.Tx.QueryRow(c.Context, "select title from core.notes where id = $1::uuid", noteID).Scan(&title); err != nil {
		return nil, err
	}
	if err := notifyMentions(c, noteID, title, "", body, "comment:"+id); err != nil {
		return nil, err
	}
	return commentByID(c, id)
}

// Anyone who can read the page can comment, so anyone who can read it can resolve or reopen a thread.
func commentResolve(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.reqUUID("id")
	if err != nil {
		return nil, err
	}
	resolved, err := o.boolDef("resolved", true)
	if err != nil {
		return nil, err
	}
	var note string
	err = c.Tx.QueryRow(c.Context, "select note_id::text from core.comments where id = $1::uuid and parent_id is null", id).Scan(&note)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("comment")
	}
	if err != nil {
		return nil, err
	}
	space, err := access.AssertNoteAccess(c, note, false)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceWritable(c, space); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`update core.comments set resolved_at = case when $2::bool then now() end,
		        resolved_by = case when $2::bool then $3::uuid end where id = $1::uuid`, id, resolved, c.Principal.UserID); err != nil {
		return nil, err
	}
	kind := "comment.reopened"
	if resolved {
		kind = "comment.resolved"
	}
	if _, err := c.Emit(kind, &note, map[string]any{"comment_id": id}); err != nil {
		return nil, err
	}
	return commentByID(c, id)
}

func commentList(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	noteID, err := o.reqUUID("note_id")
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, noteID, false); err != nil {
		return nil, err
	}
	// A deleted comment stays only as the head of a thread that still has a live reply.
	rows, err := c.Tx.Query(c.Context, "select "+commentCols+` from core.comments c where c.note_id = $1::uuid
		and (c.deleted_at is null or (c.parent_id is null and exists(select 1 from core.comments r where r.parent_id = c.id and r.deleted_at is null)))
		order by c.created_at`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		m, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out))
	for _, m := range out {
		if !m.Deleted {
			ids = append(ids, m.ID)
		}
	}
	rs, err := reactionsFor(c, ids) // after the page-access check above, so a reader of the page only (7.6)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if r := rs[out[i].ID]; r != nil {
			out[i].Reactions = r
		}
	}
	return out, nil
}
