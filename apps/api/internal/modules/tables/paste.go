package tables

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cnote/api/internal/command"
)

func init() { handlers["record.paste"] = recordPaste }

type cellErr struct {
	Row    int    `json:"row"`
	Col    int    `json:"col"`
	Reason string `json:"reason"`
}

func errReason(err error) string {
	var ce *command.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

// coerceInto turns raw cells into the stored form for a table's fields. A blank cell clears. cols[i] is the field of
// cell i; a cell that cannot be stored is reported and left out.
func coerceRow(tc *openedTable, cols []Field, cells []string, row int, errs *[]cellErr) (set map[string]any, unset []string) {
	set = map[string]any{}
	for j, raw := range cells {
		if j >= len(cols) {
			break
		}
		f := cols[j]
		fail := func(err error) {
			if len(*errs) < 50 {
				*errs = append(*errs, cellErr{row, j, errReason(err)})
			}
		}
		if f.Locked && !tc.Role.AtLeast(RoleManage) {
			fail(fmt.Errorf("%q is locked", f.Name))
			continue
		}
		v, err := Coerce(f, raw)
		if err != nil {
			fail(err)
			continue
		}
		if v == nil {
			unset = append(unset, f.ID)
		} else {
			set[f.ID] = v
		}
	}
	return
}

func recordPaste(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		TableID  string   `json:"table_id"`
		FieldIDs []string `json:"field_ids"`
		Rows     []struct {
			ID    string   `json:"id"`
			Cells []string `json:"cells"`
		} `json:"rows"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	if len(a.Rows) > 1000 || len(a.FieldIDs) > 50 {
		return nil, command.ErrInvalid("a paste holds at most 1,000 rows by 50 columns: use Import for more")
	}
	tc, err := openTable(c, a.TableID, RoleWrite)
	if err != nil {
		return nil, err
	}
	cols := make([]Field, len(a.FieldIDs))
	seen := map[string]bool{}
	for i, id := range a.FieldIDs {
		f, ok := tc.byID[id]
		if !ok || seen[id] {
			return nil, command.ErrInvalid("unknown or repeated field " + id)
		}
		seen[id] = true
		cols[i] = f
	}
	errs := []cellErr{}
	var ups []upd
	var news []newRec
	byID := map[string]int{}
	for i, r := range a.Rows {
		set, unset := coerceRow(tc, cols, r.Cells, i, &errs)
		if r.ID == "" {
			if len(set) > 0 {
				news = append(news, newRec{set: set})
			}
			continue
		}
		if k, ok := byID[r.ID]; ok { // the same record twice: later cells win
			for kk, v := range set {
				ups[k].set[kk] = v
			}
			ups[k].unset = append(ups[k].unset, unset...)
			continue
		}
		byID[r.ID] = len(ups)
		ups = append(ups, upd{ID: r.ID, prepared: prepared{set: set, unset: unset}})
	}
	for ui := range ups { // a cell cannot be both set and cleared
		u := &ups[ui]
		for k := range u.set {
			for i, x := range u.unset {
				if x == k {
					u.unset = append(u.unset[:i], u.unset[i+1:]...)
					break
				}
			}
		}
	}
	changed, err := applyUpdates(c, tc, ups)
	if err != nil {
		return nil, err
	}
	if _, err := emitRecordEvents(c, tc, "updated", changed); err != nil {
		return nil, err
	}
	ids, err := insertRecords(c, tc, news)
	if err != nil {
		return nil, err
	}
	evs := make([]evRec, len(ids))
	for i, id := range ids {
		evs[i] = evRec{ID: id, Fields: sortedKeys(news[i].set)}
	}
	if _, err := emitRecordEvents(c, tc, "created", evs); err != nil {
		return nil, err
	}
	return map[string]any{"updated": len(changed), "created": len(ids), "errors": errs}, nil
}
