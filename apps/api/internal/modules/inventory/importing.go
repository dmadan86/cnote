package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

// Import-only commands for the data importers. Opening stock is an inventory receipt whose source is the import job
// (source_type 'import'), so undoing the job reverses exactly those movements.

func importCommands() []command.Command {
	adm := func(name string, h command.Handler) command.Command {
		c := command.NewCommand(name, command.GroupAdmin, h)
		c.MinRole = command.Admin
		c.Internal = true
		return c
	}
	return []command.Command{
		adm("stock_item.import_keys", importKeys),
		adm("stock.import_opening", importOpening),
		adm("stock.import_check", importCheck),
		adm("stock.import_reverse", importReverse),
	}
}

// stock_item.import_keys {} -> {items:[{id,sku,name,active}]} for duplicate detection (SKU, then name).
func importKeys(c *command.Ctx, _ json.RawMessage) (any, error) {
	rows, err := c.Tx.Query(c.Context, "select id::text, sku, name, active from inventory.items order by created_at, id")
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, sku, name string
		var active bool
		if err := rows.Scan(&id, &sku, &name, &active); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "sku": sku, "name": name, "active": active})
	}
	return map[string]any{"items": out}, command.DB(rows.Err())
}

// stock.import_opening {job_id, item_id, qty_milli, value_minor, location?} posts opening stock for an imported item.
func importOpening(c *command.Ctx, input json.RawMessage) (any, error) {
	var v struct {
		JobID      string `json:"job_id"`
		ItemID     string `json:"item_id"`
		QtyMilli   int64  `json:"qty_milli"`
		ValueMinor int64  `json:"value_minor"`
		Location   string `json:"location"`
	}
	if json.Unmarshal(input, &v) != nil || v.JobID == "" || v.ItemID == "" || v.QtyMilli <= 0 || v.ValueMinor < 0 {
		return nil, command.ErrInvalid("job_id, item_id, a positive qty_milli and a value_minor are required")
	}
	if _, err := lockItem(c, v.ItemID); err != nil {
		return nil, err
	}
	loc, err := defaultLocation(c)
	if err != nil {
		return nil, err
	}
	if name := strings.TrimSpace(v.Location); name != "" {
		err := c.Tx.QueryRow(c.Context, "select id::text from inventory.locations where lower(name) = lower($1) and active", name).Scan(&loc)
		if errors.Is(err, pgx.ErrNoRows) {
			err = c.Tx.QueryRow(c.Context, "insert into inventory.locations (workspace_id, name) values ($1::uuid, $2) returning id::text", c.WorkspaceID(), name).Scan(&loc)
		}
		if err != nil {
			return nil, command.DB(err)
		}
	}
	id, err := insertMovement(c, posting{itemID: v.ItemID, locID: loc, kind: "receipt", qty: unmilli(v.QtyMilli), value: fmt.Sprint(v.ValueMinor),
		reason: "Opening stock (import)", srcType: "import", srcID: v.JobID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"movement_id": id}, nil
}

// stock.import_check {job_id} -> {blocked:[{item_id,sku,imported,on_hand}]}: imported stock that has been used since.
func importCheck(c *command.Ctx, input json.RawMessage) (any, error) {
	var v struct {
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(input, &v) != nil || v.JobID == "" {
		return nil, command.ErrInvalid("job_id is required")
	}
	rows, err := c.Tx.Query(c.Context,
		`select i.id::text, i.sku, m.qty::text, coalesce((select sum(x.qty) from inventory.movements x where x.item_id = m.item_id and x.location_id = m.location_id), 0)::text
		   from inventory.movements m join inventory.items i on i.id = m.item_id
		  where m.source_type = 'import' and m.source_id = $1::uuid and m.kind = 'receipt'
		    and not exists (select 1 from inventory.movements r where r.reverses = m.id)
		    and coalesce((select sum(x.qty) from inventory.movements x where x.item_id = m.item_id and x.location_id = m.location_id), 0) < m.qty`, v.JobID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, sku, imp, have string
		if err := rows.Scan(&id, &sku, &imp, &have); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]string{"item_id": id, "sku": sku, "imported": imp, "on_hand": have})
	}
	return map[string]any{"blocked": out}, command.DB(rows.Err())
}

// stock.import_reverse {job_id} reverses every opening movement of the job.
func importReverse(c *command.Ctx, input json.RawMessage) (any, error) {
	var v struct {
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(input, &v) != nil || v.JobID == "" {
		return nil, command.ErrInvalid("job_id is required")
	}
	return map[string]any{"ok": true}, Reverse(c, "import", v.JobID)
}
