package tables

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
)

func init() { handlers["record.batch_list"] = recordBatchList }

// Events carry no values and no titles (17.3): a consumer that needs them calls record.get with its own access.

type evRec struct {
	ID     string
	Fields []string // field ids changed
}

const batchThreshold = 100

// emitRecordEvents emits record.<op> per record up to 100 records, and above that one record.batch event whose ids
// record.batch_list returns (17.5). It returns the batch id, or nil when it emitted per record.
func emitRecordEvents(c *command.Ctx, tc *openedTable, op string, recs []evRec) (*string, error) {
	if len(recs) == 0 {
		return nil, nil
	}
	if len(recs) <= batchThreshold {
		for _, r := range recs {
			fields := r.Fields
			if fields == nil {
				fields = []string{}
			}
			if _, err := c.Emit("record."+op, nil, map[string]any{"base_id": tc.Base.ID, "table_id": tc.Table.ID, "record_id": r.ID, "field_ids": fields}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	var batch string
	if err := c.Tx.QueryRow(c.Context, `insert into tables.batches (workspace_id, table_id, op, record_ids) values ($1::uuid,$2::uuid,$3,$4::uuid[]) returning id::text`,
		c.WorkspaceID(), tc.Table.ID, op, ids).Scan(&batch); err != nil {
		return nil, command.DB(err)
	}
	_, err := c.Emit("record.batch", nil, map[string]any{"op": op, "base_id": tc.Base.ID, "table_id": tc.Table.ID, "count": len(recs), "batch_id": batch})
	return &batch, err
}

func recordBatchList(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		BatchID string `json:"batch_id"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	var op, tid string
	var ids []string
	if err := c.Tx.QueryRow(c.Context, `select op, table_id::text, record_ids::text[] from tables.batches where workspace_id=$1::uuid and id=$2::uuid`,
		c.WorkspaceID(), a.BatchID).Scan(&op, &tid, &ids); err != nil {
		return nil, command.DB(err)
	}
	if _, err := openTable(c, tid, RoleRead); err != nil { // the caller's own access, as record.get applies it
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select id::text from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and id = any($3::uuid[]) order by seq`, c.WorkspaceID(), tid, ids)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, id)
	}
	return map[string]any{"op": op, "table_id": tid, "ids": out}, command.DB(rows.Err())
}
