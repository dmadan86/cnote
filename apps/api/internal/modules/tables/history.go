package tables

import (
	"encoding/json"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

func init() { handlers["record.history"] = recordHistory }

// historyRetention is how long record_history rows are kept; the purge sweep applies it. Placeholder until the
// plans set retention (design 3).
const historyRetention = 90

// queueHistory adds one history row to a batch: change = {field id: [old, new]}; "_deleted" marks delete/restore.
func queueHistory(b *pgx.Batch, c *command.Ctx, tableID, recordID, actorType string, actorID *string, change map[string][2]any) {
	raw, _ := json.Marshal(change)
	b.Queue(`insert into tables.record_history (workspace_id, table_id, record_id, actor_type, actor_id, change) values ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::jsonb)`,
		c.WorkspaceID(), tableID, recordID, actorType, actorID, string(raw))
}

func recordHistory(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	// history stays readable for a record in the trash, so look the table up without the live filter
	var tid string
	if err := c.Tx.QueryRow(c.Context, `select table_id::text from tables.records where workspace_id=$1::uuid and id=$2::uuid`, c.WorkspaceID(), a.ID).Scan(&tid); err != nil {
		return nil, command.DB(err)
	}
	if _, err := openTable(c, tid, RoleRead); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select id, actor_type, actor_id::text, at, change from tables.record_history where workspace_id=$1::uuid and record_id=$2::uuid order by at desc, id desc limit 100`, c.WorkspaceID(), a.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	type entry struct {
		ID        int64           `json:"id"`
		ActorType string          `json:"actor_type"`
		ActorID   *string         `json:"actor_id"`
		At        time.Time       `json:"at"`
		Change    json.RawMessage `json:"change"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.ActorType, &e.ActorID, &e.At, &e.Change); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, e)
	}
	return out, command.DB(rows.Err())
}
