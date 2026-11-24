package inventory

import (
	"encoding/json"
	"strings"

	"github.com/cnote/api/internal/command"
)

type publicItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Unit       string `json:"unit"`
	PriceMinor int64  `json:"price_minor"`
	GSTRateBP  int32  `json:"gst_rate_bp"`
}
type publicCursor struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func publicList(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	var ids []string
	byIDs := o.has("ids")
	if byIDs {
		if json.Unmarshal(o["ids"], &ids) != nil || len(ids) > 200 {
			return nil, command.ErrInvalid("ids must be an array of at most 200 ids")
		}
		for i, id := range ids {
			ids[i] = strings.ToLower(id)
			if !uuidRe.MatchString(ids[i]) {
				return nil, command.ErrInvalid("invalid item id")
			}
		}
	}
	q, err := o.str("q", 80)
	if err != nil {
		return nil, err
	}
	limit, ok, err := o.int("limit", 1, 100)
	if err != nil {
		return nil, err
	}
	if !ok {
		limit = 100
	}
	var cursor publicCursor
	if o.has("cursor") {
		if json.Unmarshal(o["cursor"], &cursor) != nil || len(cursor.Name) > 120 || !uuidRe.MatchString(cursor.ID) {
			return nil, command.ErrInvalid("invalid cursor")
		}
	}
	// ID lookup is independent of search and pagination, including when ids is empty.
	if byIDs {
		limit = 200
		q = ""
		cursor = publicCursor{}
	}
	rows, err := c.Tx.Query(c.Context, `select id::text, name, unit, price_minor, gst_rate_bp from inventory.items
 where workspace_id = $1::uuid and active and (not $2::boolean or id = any($3::uuid[]))
 and ($4 = '' or name ilike '%' || $4 || '%')
 and ($5 = '' or (lower(name), id) > ($5, $6::uuid))
 order by lower(name), id limit $7`, c.WorkspaceID(), byIDs, ids, q, cursor.Name, nullableCursorID(cursor.ID), limit+1)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	items := []publicItem{}
	for rows.Next() {
		var x publicItem
		if err := rows.Scan(&x.ID, &x.Name, &x.Unit, &x.PriceMinor, &x.GSTRateBP); err != nil {
			return nil, command.DB(err)
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	var next *publicCursor
	if int64(len(items)) > limit {
		items = items[:limit]
		x := items[len(items)-1]
		next = &publicCursor{Name: strings.ToLower(x.Name), ID: x.ID}
	}
	return map[string]any{"items": items, "next_cursor": next}, nil
}
func nullableCursorID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
