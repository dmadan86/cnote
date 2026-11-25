package notes

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// search.notes, split out of write.go in docs-parity step 0 so the labels and search filters package (W2, 7.8) owns it.

// ---------- search.notes ----------

// Rank is a Postgres real. Rust turns f32 into a serde_json Value, which widens it to f64 and prints that.
type Rank float32

func (r Rank) MarshalJSON() ([]byte, error) {
	s := strconv.FormatFloat(float64(r), 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return []byte(s), nil
}

type SearchHit struct {
	ID         string      `json:"id"`
	SpaceID    string      `json:"space_id"`
	Title      string      `json:"title"`
	Types      []string    `json:"types"`
	Freshness  string      `json:"freshness"`
	VerifiedAt *command.Ts `json:"verified_at"`
	Snippet    string      `json:"snippet"`
	Rank       Rank        `json:"rank"`
}

func searchNotes(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	q, err := o.reqStr("q")
	if err != nil {
		return nil, err
	}
	lim, err := o.optInt("limit", 64)
	if err != nil {
		return nil, err
	}
	archived, err := o.boolDef("include_archived", false)
	if err != nil {
		return nil, err
	}
	limit := int64(20)
	if lim != nil {
		limit = min(max(*lim, 1), 100)
	}
	ids, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	// Permission filter is applied inside the query, not after (ADR-009).
	rows, err := c.Tx.Query(c.Context,
		`select id::text, space_id::text, title, types, freshness, verified_at,
		        content_md as snippet, -- replaced by Snippets below
		        (ts_rank(search, websearch_to_tsquery('english', $1::text)) + similarity(title, $1::text))::real as rank
		 from core.notes
		 where deleted_at is null and ($4::bool or freshness <> 'archived')
		   and (space_id = any($2::text[]::uuid[]) or exists (select 1 from core.note_permissions sp where sp.note_id = notes.id and sp.user_id = $5::uuid))
		   and core.note_access(id, $5::uuid, $6::bool, $2::text[]::uuid[]) <> 'none'
		   and (search @@ websearch_to_tsquery('english', $1::text) or title % $1::text or title ilike '%' || $1::text || '%')
		 order by rank desc limit $3::int8`,
		strings.TrimSpace(q), ids, limit, archived, uid, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		var verified *time.Time
		if err := rows.Scan(&h.ID, &h.SpaceID, &h.Title, &h.Types, &h.Freshness, &verified, &h.Snippet, &h.Rank); err != nil {
			return nil, err
		}
		h.VerifiedAt = command.TsPtr(verified)
		if h.Types == nil {
			h.Types = []string{}
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	bodies := make([]string, len(out))
	for i, h := range out {
		bodies[i] = h.Snippet
	}
	snips, err := Snippets(c, strings.TrimSpace(q), bodies)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Snippet = snips[i]
	}
	return out, nil
}
