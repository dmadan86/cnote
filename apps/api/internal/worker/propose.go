package worker

import (
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/ai"
)

// proposeEditJob is cnote-docs freshness.rs propose_edit_job: ask the model for a revised page and store it for
// the owner to review.
func proposeEditJob(c *command.Ctx, payload json.RawMessage) error {
	var p struct {
		FlagID string `json:"flag_id"`
	}
	if json.Unmarshal(payload, &p) != nil || !validUUID(p.FlagID) {
		return command.ErrInvalid("flag_id required")
	}
	var page string
	var source *string
	var eventID *int64
	err := c.Tx.QueryRow(c.Context,
		`select note_id::text, source_note_id::text, source_event_id from docs.freshness_flags
		 where id = $1::uuid and resolved_at is null
		   and not exists (select 1 from docs.edit_proposals where flag_id = $1::uuid)`, p.FlagID).Scan(&page, &source, &eventID)
	if err == pgx.ErrNoRows {
		return nil // resolved meanwhile, or already proposed (job retry)
	}
	if err != nil {
		return err
	}
	var title, content string
	var owner *string
	var baseVersion int32
	if err := c.Tx.QueryRow(c.Context, "select title, content_md, owner_id::text, version from core.notes where id = $1::uuid", page).
		Scan(&title, &content, &owner, &baseVersion); err != nil {
		return err
	}
	// Quote the source only if the page owner may read it (the proposal is shown to the owner).
	readable := []string{}
	admin := false
	if owner != nil {
		if err := queryStrings(c, &readable, `select s.id::text from core.spaces s
			 left join core.memberships m on m.user_id = $1::uuid
			 left join core.space_members sm on sm.space_id = s.id and sm.user_id = $1::uuid
			 where m.role in ('owner','admin') or sm.user_id is not null
			    or (s.visibility = 'open' and m.role = 'member')`, *owner); err != nil {
			return err
		}
		if err := c.Tx.QueryRow(c.Context,
			"select exists(select 1 from core.memberships where user_id = $1::uuid and role in ('owner','admin'))", *owner).Scan(&admin); err != nil {
			return err
		}
	}
	var srcTitle, srcMD string
	if source != nil {
		err := c.Tx.QueryRow(c.Context,
			`select title, content_md from core.notes where id = $1::uuid and space_id = any($2::uuid[])
			   and core.note_access(id, $3::uuid, $4, $2::uuid[]) <> 'none'`, *source, readable, owner, admin).Scan(&srcTitle, &srcMD)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
	}
	kind := "a bound note changed"
	if eventID != nil {
		var k string
		err := c.Tx.QueryRow(c.Context, "select kind from core.events where id = $1", *eventID).Scan(&k)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if err == nil {
			kind = k
		}
	}
	maxTokens := min(len(content)/3+256, 8000) // len = bytes, like Rust's String::len
	done, err := ai.ProposeEdit(c, []ai.Message{
		{Role: "system", Content: fmt.Sprintf("You maintain the wiki page \"%s\". The note \"%s\" it documents changed (%s). "+
			"Reply with the full revised page in Markdown, changing only what the new state of that note "+
			"makes wrong. Current state of the note:\n%s", title, srcTitle, kind, srcMD)},
		{Role: "user", Content: content},
	}, uint32(maxTokens))
	if err != nil {
		return err
	}
	rationale := fmt.Sprintf("\"%s\" changed (%s), so this page may be out of date. Drafted by %s/%s; review before applying.", srcTitle, kind, done.Provider, done.Model)
	var id string
	if err := c.Tx.QueryRow(c.Context,
		`insert into docs.edit_proposals (workspace_id, note_id, flag_id, proposed_md, rationale, base_version)
		 values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6) returning id::text`,
		c.WorkspaceID(), page, p.FlagID, done.Text, rationale, baseVersion).Scan(&id); err != nil {
		return err
	}
	_, err = c.Emit("doc.proposal_created", &page, map[string]any{"proposal_id": id, "flag_id": p.FlagID})
	return err
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
