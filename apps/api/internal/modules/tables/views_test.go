package tables

import (
	"testing"

	"github.com/cnote/api/internal/command"
)

func TestViewsWriterCreatesUpdatesDeletes(t *testing.T) {
	x := newTFix(t)
	tid, _ := x.mkTable(x.openBase, "T", fspec{"Name", "text", ""})
	var gridID string
	if err := x.commit(func(c *command.Ctx) error {
		g, err := rcall(viewCreate, c, map[string]any{"table_id": tid, "name": "Shared", "type": "grid"})
		if err == nil {
			gridID = g["id"].(string)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	x.as("editor", command.Member, func(c *command.Ctx) { // a writer, not a manager
		g, err := rcall(viewCreate, c, map[string]any{"table_id": tid, "name": "Second", "type": "grid"})
		if err != nil || g["position"] == nil {
			t.Fatalf("create %v %v", g, err)
		}
		k, err := rcall(viewCreate, c, map[string]any{"table_id": tid, "name": "Board", "type": "kanban", "config": map[string]any{"v": 1}})
		if err != nil {
			t.Fatal(err)
		}
		u, err := rcall(viewUpdate, c, map[string]any{"id": k["id"], "name": "Board 2", "config": map[string]any{"v": 1, "sorts": []any{}}})
		if err != nil || u["name"] != "Board 2" {
			t.Errorf("update %v %v", u, err)
		}
		if _, err := rcall(viewCreate, c, map[string]any{"table_id": tid, "name": "X", "type": "form"}); rcode(err) != command.Invalid {
			t.Errorf("form view: %v", err)
		}
		if _, err := rcall(viewUpdate, c, map[string]any{"id": k["id"], "config": []any{1}}); rcode(err) != command.Invalid {
			t.Errorf("non-object config: %v", err)
		}
		if _, err := rcall(viewDelete, c, map[string]any{"id": k["id"]}); err != nil {
			t.Errorf("delete kanban: %v", err)
		}
	})
	x.as("viewer", command.Member, func(c *command.Ctx) {
		if _, err := rcall(viewCreate, c, map[string]any{"table_id": tid, "name": "No", "type": "grid"}); rcode(err) != command.Forbidden {
			t.Errorf("viewer create: %v", err)
		}
		if _, err := rcall(viewUpdate, c, map[string]any{"id": gridID, "name": "No"}); rcode(err) != command.Forbidden {
			t.Errorf("viewer update: %v", err)
		}
	})
	x.as("owner", command.Owner, func(c *command.Ctx) {
		if _, err := rcall(viewDelete, c, map[string]any{"id": newID()}); rcode(err) != command.NotFound {
			t.Errorf("unknown view: %v", err)
		}
	})
}

// A sort or filter on a field that is not read from the record data (autonumber, created time) used to leave an
// unused query argument, which pgx refuses.
func TestListSortsByComputedFields(t *testing.T) {
	x := newTFix(t)
	tid, f := x.mkTable(x.openBase, "T", fspec{"Name", "text", ""}, fspec{"Seq", "autonumber", ""}, fspec{"At", "created_time", ""})
	x.as("owner", command.Owner, func(c *command.Ctx) {
		for _, n := range []string{"b", "a"} {
			if _, err := rcall(recordCreate, c, map[string]any{"table_id": tid, "values": map[string]any{f["Name"]: n}}); err != nil {
				t.Fatal(err)
			}
		}
		sorts := []map[string]any{{"field": f["Name"]}, {"field": f["Seq"]}, {"field": f["At"], "desc": true}}
		r, err := rcall(recordList, c, map[string]any{"table_id": tid, "sorts": sorts,
			"filters": map[string]any{"rules": []map[string]any{{"field": f["At"], "op": "today"}}}})
		if err != nil || r["total"] != 2.0 {
			t.Errorf("list %v %v", r, err)
		}
	})
}
