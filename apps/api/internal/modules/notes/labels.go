package notes

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// label.set and label.list (docs-parity 5.4, 7.6). Counts come only from pages the caller can read: the access
// predicate is in the query, before the grouping.

func init() {
	a1Handlers["label.set"] = labelSet
	a1Handlers["label.list"] = labelList
}

var labelRe = regexp.MustCompile(`^[^\s,]{1,40}$`)

func labelSet(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		NoteID string   `json:"note_id"`
		Labels []string `json:"labels"`
	}
	if json.Unmarshal(input, &in) != nil || !uuidRe.MatchString(in.NoteID) || in.Labels == nil {
		return nil, command.ErrInvalid("note_id and labels are required")
	}
	if len(in.Labels) > 20 {
		return nil, command.ErrInvalid("a page can have at most 20 labels")
	}
	id := strings.ToLower(in.NoteID)
	seen := map[string]bool{}
	names := []string{}
	for _, l := range in.Labels {
		l = strings.ToLower(strings.TrimSpace(l))
		if !labelRe.MatchString(l) {
			return nil, command.ErrInvalid("a label is 1 to 40 characters with no spaces or commas")
		}
		if !seen[l] {
			seen[l] = true
			names = append(names, l)
		}
	}
	if _, err := access.AssertNoteAccess(c, id, true); err != nil {
		return nil, err
	}
	ws := c.WorkspaceID()
	if _, err := c.Tx.Exec(c.Context,
		`insert into core.labels (workspace_id, name, created_by) select $1::uuid, n, $3::uuid from unnest($2::text[]) n
		 on conflict (workspace_id, name) do nothing`, ws, names, c.Principal.UserID); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`delete from core.note_labels nl using core.labels l
		 where nl.note_id = $1::uuid and l.id = nl.label_id and l.workspace_id = nl.workspace_id and not (l.name = any($2::text[]))`, id, names); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into core.note_labels (workspace_id, note_id, label_id, created_by)
		 select $1::uuid, $2::uuid, l.id, $4::uuid from core.labels l where l.workspace_id = $1::uuid and l.name = any($3::text[])
		 on conflict do nothing`, ws, id, names, c.Principal.UserID); err != nil {
		return nil, err
	}
	if _, err := c.Emit("note.labels_changed", &id, map[string]any{"labels": sortedStrings(names)}); err != nil {
		return nil, err
	}
	return map[string]any{"note_id": id, "labels": sortedStrings(names)}, nil
}

func sortedStrings(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func labelList(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		Prefix string `json:"prefix"`
		Limit  *int64 `json:"limit"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil, command.ErrInvalid("prefix must be a string and limit an integer")
	}
	limit := int64(50)
	if in.Limit != nil {
		limit = min(max(*in.Limit, 1), 200)
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select l.name, count(*)::int8 from core.note_labels nl
		 join core.labels l on l.id = nl.label_id and l.workspace_id = nl.workspace_id
		 join core.notes n on n.id = nl.note_id and n.workspace_id = nl.workspace_id
		 where n.deleted_at is null and ($1::text = '' or starts_with(l.name, lower($1::text)))
		   and core.note_access(n.id, $3::uuid, $4::bool, $2::text[]::uuid[]) <> 'none'
		 group by l.name order by count(*) desc, l.name limit $5::int8`,
		strings.TrimSpace(in.Prefix), spaces, uid, admin, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"label": name, "count": n})
	}
	return out, rows.Err()
}

// noteLabels lists one page's labels; the caller has already checked access to the page.
func noteLabels(c *command.Ctx, id string) ([]string, error) {
	rows, err := c.Tx.Query(c.Context,
		`select l.name from core.note_labels nl join core.labels l on l.id = nl.label_id and l.workspace_id = nl.workspace_id
		 where nl.note_id = $1::uuid order by l.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
