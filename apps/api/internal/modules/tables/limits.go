package tables

import (
	"context"
	"fmt"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

// Plan limits (design 10 and 17.1). The per-plan numbers are placeholders until pricing is set: change them here only.
type planLimits struct {
	BasesPerWorkspace int
	TablesPerBase     int
	RecordsPerBase    int
	// AttachmentBytesPerBase is the storage of record files, pending uploads included (design 18.1, a proposal).
	AttachmentBytesPerBase int64
}

var planTable = map[string]planLimits{
	"free":       {3, 5, 1000, 100 << 20},
	"team":       {50, 20, 25000, 2 << 30},
	"business":   {200, 50, 100000, 20 << 30},
	"enterprise": {1000, 50, 250000, 100 << 30},
}

// Fixed on every plan.
const (
	fieldsPerTable  = 100
	recordsPerTable = 100000
	viewsPerTable   = 20
)

func limitsFor(plan string) planLimits {
	if l, ok := planTable[plan]; ok {
		return l
	}
	return planTable["free"]
}

func planName(plan string) string {
	if _, ok := planTable[plan]; !ok {
		plan = "free"
	}
	return string(plan[0]-32) + plan[1:]
}

func workspacePlan(ctx context.Context, tx pgx.Tx, workspaceID string) (string, error) {
	var plan string
	err := tx.QueryRow(ctx, `select plan from core.workspaces where id=$1::uuid`, workspaceID).Scan(&plan)
	return plan, command.DB(err)
}

func num(n int) string { // 25000 -> 25,000
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// checkRecordLimit refuses (conflict) when adding records would pass the plan's records-per-base number or the
// hard cap per table. It reads bases.record_count, which the record writers keep; the writer still does the
// atomic `update ... where record_count + n <= cap` itself, this gives the readable message first.
func checkRecordLimit(ctx context.Context, tx pgx.Tx, baseID, tableID string, adding int) error {
	var plan string
	var count int
	var nextSeq int64
	err := tx.QueryRow(ctx, `select w.plan, b.record_count, t.next_seq from tables.bases b
		join core.workspaces w on w.id = b.workspace_id
		join tables.tables t on t.workspace_id = b.workspace_id and t.base_id = b.id
		where b.id = $1::uuid and t.id = $2::uuid`, baseID, tableID).Scan(&plan, &count, &nextSeq)
	if err != nil {
		return command.DB(err)
	}
	if max := limitsFor(plan).RecordsPerBase; count+adding > max {
		return command.ErrConflict(fmt.Sprintf("This base has reached %s records on the %s plan", num(max), planName(plan)))
	}
	if int(nextSeq-1)+adding > recordsPerTable { // seq only grows, so count exactly only when it could matter
		var live int
		if err := tx.QueryRow(ctx, `select count(*) from tables.records where table_id=$1::uuid and deleted_at is null`, tableID).Scan(&live); err != nil {
			return command.DB(err)
		}
		if live+adding > recordsPerTable {
			return command.ErrConflict(fmt.Sprintf("This table has reached %s records, the most a table can hold", num(recordsPerTable)))
		}
	}
	return nil
}
