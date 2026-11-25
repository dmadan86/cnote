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
	qp, err := o.optStr("q")
	if err != nil {
		return nil, err
	}
	q := ""
	if qp != nil {
		q = strings.TrimSpace(*qp)
	}
	f, err := parseSearchFilters(o)
	if err != nil {
		return nil, err
	}
	if q == "" && !f.any() {
		return nil, command.ErrInvalid("q or a filter is required")
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
	// Permission filter is applied inside the query, not after (ADR-009). With no q the match clause is off and
	// results run newest first.
	rows, err := c.Tx.Query(c.Context,
		`select id::text, space_id::text, title, types, freshness, verified_at,
		        content_md as snippet, -- replaced by Snippets below
		        case when $1::text = '' then 0 else (ts_rank(search, websearch_to_tsquery('english', $1::text)) + similarity(title, $1::text))::real end as rank
		 from core.notes
		 where deleted_at is null and ($4::bool or freshness <> 'archived')
		   and (space_id = any($2::text[]::uuid[]) or exists (select 1 from core.note_permissions sp where sp.note_id = notes.id and sp.user_id = $5::uuid))
		   and core.note_access(id, $5::uuid, $6::bool, $2::text[]::uuid[]) <> 'none'
		   and ($1::text = '' or (case when $11::bool then title % $1::text or title ilike '%' || $1::text || '%'
		        else search @@ websearch_to_tsquery('english', $1::text) or title % $1::text or title ilike '%' || $1::text || '%' end))
		   and ($7::uuid is null or space_id = $7::uuid)
		   and ($8::uuid is null or owner_id = $8::uuid or created_by = $8::uuid
		        or exists (select 1 from core.note_revisions r where r.note_id = notes.id and r.created_by = $8::uuid))
		   and ($9::text is null or exists (select 1 from core.note_labels nl join core.labels l on l.id = nl.label_id and l.workspace_id = nl.workspace_id
		        where nl.note_id = notes.id and l.name = $9::text))
		   and ($10::timestamptz is null or updated_at >= $10::timestamptz)
		   and ($12::text is null or case $12::text when 'verified' then verified_at is not null and freshness = 'fresh' else page_status = $12::text end)
		 order by rank desc, updated_at desc limit $3::int8`,
		q, ids, limit, archived, uid, admin, f.space, f.owner, f.label, f.after, f.titleOnly, f.status)
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
	snips, err := Snippets(c, q, bodies)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Snippet = snips[i]
	}
	return out, nil
}

// searchFilters are the A1 filters of search.notes. status is draft, in_review, final or verified.
type searchFilters struct {
	space, owner, label, status *string
	after                       *time.Time
	titleOnly                   bool
}

func (f searchFilters) any() bool {
	return f.space != nil || f.owner != nil || f.label != nil || f.status != nil || f.after != nil
}

func parseSearchFilters(o obj) (f searchFilters, err error) {
	if f.space, err = o.optUUID("space_id"); err != nil {
		return
	}
	if f.owner, err = o.optUUID("owner_id"); err != nil {
		return
	}
	if f.label, err = o.optStr("label"); err != nil {
		return
	}
	if f.label != nil {
		l := strings.ToLower(strings.TrimSpace(*f.label))
		f.label = &l
	}
	if f.status, err = o.optStr("status"); err != nil {
		return
	}
	if f.status != nil {
		switch *f.status {
		case "draft", "in_review", "final", "verified":
		default:
			return f, command.ErrInvalid("status must be draft, in_review, final or verified")
		}
	}
	var after *string
	if after, err = o.optStr("updated_after"); err != nil {
		return
	}
	if after != nil {
		t, perr := time.Parse(time.RFC3339, *after)
		if perr != nil {
			return f, command.ErrInvalid("updated_after must be an RFC 3339 timestamp")
		}
		f.after = &t
	}
	f.titleOnly, err = o.boolDef("title_only", false)
	return
}
