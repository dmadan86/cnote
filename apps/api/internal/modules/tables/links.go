package tables

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

// Links (17.4). A link field reads tables.links rows stored under the FORWARD field id; a field with inverse_of set
// reads the same rows from the other end. Writes check, under row locks, that the field is a link field of the
// source table, every target is a live record of the target table in the same base, and single cardinality holds.
// Soft-deleted targets are hidden from chips and counts and reappear on restore.

const maxLinksPerCell = 100

type linkChip struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type linkSide struct {
	field, fwd     Field
	selfCol, other string // links column of this record / of the other end
	otherTable     string
	multiple       bool // this field's own cardinality
	otherSingle    bool // the other end may hold only one link (forward field is single)
	inverse        bool
}

func linkCfg(f Field) LinkConfig {
	var cfg LinkConfig
	_ = json.Unmarshal(f.Config, &cfg)
	return cfg
}

// resolveLink finds how a link field of tc reads tables.links.
func resolveLink(c *command.Ctx, tc *openedTable, fieldID string) (linkSide, error) {
	f, ok := tc.byID[fieldID]
	if !ok || f.Type != Link {
		return linkSide{}, command.ErrInvalid("field " + fieldID + " is not a link field of this table")
	}
	cfg := linkCfg(f)
	s := linkSide{field: f, fwd: f, selfCol: "from_record", other: "to_record", otherTable: cfg.TableID, multiple: cfg.Multiple}
	if cfg.InverseOf != "" {
		var fwd Field
		err := c.Tx.QueryRow(c.Context, `select id::text, workspace_id::text, table_id::text, name, type, config, position, locked from tables.fields where workspace_id=$1::uuid and id=$2::uuid and deleted_at is null`,
			c.WorkspaceID(), cfg.InverseOf).Scan(&fwd.ID, &fwd.WorkspaceID, &fwd.TableID, &fwd.Name, &fwd.Type, &fwd.Config, &fwd.Position, &fwd.Locked)
		if err != nil {
			return linkSide{}, command.DB(err)
		}
		s.fwd, s.inverse, s.selfCol, s.other, s.otherTable = fwd, true, "to_record", "from_record", fwd.TableID
		s.otherSingle = !linkCfg(fwd).Multiple
	}
	return s, nil
}

// setLinks replaces the live links of record recID in one link field with ids. It returns the previous and new
// live ids and whether they differ. The caller holds the source record's row lock (applyUpdates, or the record is new).
func setLinks(c *command.Ctx, tc *openedTable, recID string, s linkSide, ids []string) (oldIDs, newIDs []string, changed bool, err error) {
	ids = slices.Compact(sortedCopy(ids))
	if len(ids) > maxLinksPerCell {
		return nil, nil, false, command.ErrInvalid(fmt.Sprintf("%q holds at most %d links", s.field.Name, maxLinksPerCell))
	}
	if !s.multiple && len(ids) > 1 {
		return nil, nil, false, command.ErrInvalid(fmt.Sprintf("%q links to one record at most", s.field.Name))
	}
	ws := c.WorkspaceID()
	if len(ids) > 0 {
		// Lock the targets so a concurrent delete or a competing single-link claim waits for this transaction.
		// Exclusive when the other end may hold only one link (the claim must be serialised), shared otherwise.
		lock := "for share"
		if s.otherSingle {
			lock = "for update"
		}
		rows, err := c.Tx.Query(c.Context, `select r.id::text from tables.records r join tables.tables t on t.workspace_id=r.workspace_id and t.id=r.table_id
			where r.workspace_id=$1::uuid and r.id = any($2::uuid[]) and r.table_id=$3::uuid and t.base_id=$4::uuid and t.deleted_at is null and r.deleted_at is null order by r.id `+lock+` of r`,
			ws, ids, s.otherTable, tc.Base.ID)
		if err != nil {
			return nil, nil, false, command.DB(err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, false, command.DB(err)
		}
		if n != len(ids) {
			return nil, nil, false, command.ErrInvalid(fmt.Sprintf("%q can only link to records of its linked table that exist", s.field.Name))
		}
		if s.otherSingle { // each target may be linked from one record only: count hidden (trashed) links too
			var taken bool
			if err := c.Tx.QueryRow(c.Context, `select exists (select 1 from tables.links where workspace_id=$1::uuid and field_id=$2::uuid and `+s.other+` = any($3::uuid[]) and `+s.selfCol+` <> $4::uuid)`,
				ws, s.fwd.ID, ids, recID).Scan(&taken); err != nil {
				return nil, nil, false, command.DB(err)
			}
			if taken {
				return nil, nil, false, command.ErrConflict(fmt.Sprintf("%q: one of these records is already linked to another record", s.field.Name))
			}
		}
	}
	oldIDs, err = liveLinks(c, s, recID)
	if err != nil {
		return nil, nil, false, err
	}
	changed = !slices.Equal(sortedCopy(oldIDs), ids)
	if !changed {
		return oldIDs, ids, false, nil
	}
	// A single-link side keeps no hidden link next to the new one; a multi side keeps links to trashed records.
	del := `delete from tables.links l where l.workspace_id=$1::uuid and l.field_id=$2::uuid and l.` + s.selfCol + `=$3::uuid and not (l.` + s.other + ` = any($4::uuid[]))`
	if s.multiple {
		del += ` and exists (select 1 from tables.records o where o.workspace_id=l.workspace_id and o.id=l.` + s.other + ` and o.deleted_at is null)`
	}
	if _, err := c.Tx.Exec(c.Context, del, ws, s.fwd.ID, recID, ids); err != nil {
		return nil, nil, false, command.DB(err)
	}
	if len(ids) > 0 {
		ins := `insert into tables.links (workspace_id, base_id, field_id, from_record, to_record) select $1::uuid,$2::uuid,$3::uuid,$5::uuid,x from unnest($4::uuid[]) x on conflict do nothing`
		if s.inverse {
			ins = `insert into tables.links (workspace_id, base_id, field_id, from_record, to_record) select $1::uuid,$2::uuid,$3::uuid,x,$5::uuid from unnest($4::uuid[]) x on conflict do nothing`
		}
		if _, err := c.Tx.Exec(c.Context, ins, ws, tc.Base.ID, s.fwd.ID, ids, recID); err != nil {
			return nil, nil, false, command.DB(err)
		}
	}
	return oldIDs, ids, true, nil
}

func liveLinks(c *command.Ctx, s linkSide, recID string) ([]string, error) {
	rows, err := c.Tx.Query(c.Context, `select l.`+s.other+`::text from tables.links l join tables.records o on o.workspace_id=l.workspace_id and o.id=l.`+s.other+` and o.deleted_at is null
		where l.workspace_id=$1::uuid and l.field_id=$2::uuid and l.`+s.selfCol+`=$3::uuid`, c.WorkspaceID(), s.fwd.ID, recID)
	if err != nil {
		return nil, command.DB(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if ids == nil {
		ids = []string{}
	}
	return ids, command.DB(err)
}

// recTitle is the name of a record in a chip: its primary field's value (an autonumber shows prefix + seq).
func recTitle(pType FieldType, pConfig json.RawMessage, seq int64, val *string) string {
	if pType == Autonumber {
		var cfg AutonumberConfig
		_ = json.Unmarshal(pConfig, &cfg)
		return cfg.Prefix + strconv.FormatInt(seq, 10)
	}
	if val == nil {
		return ""
	}
	return *val
}

// linkChips returns, per record id, per link field id, the live linked records ([{id,title}], by seq). Only
// non-empty cells appear.
func linkChips(c *command.Ctx, tc *openedTable, recIDs []string) (map[string]map[string][]linkChip, error) {
	out := map[string]map[string][]linkChip{}
	if len(recIDs) == 0 {
		return out, nil
	}
	for _, f := range tc.Fields {
		if f.Type != Link {
			continue
		}
		s, err := resolveLink(c, tc, f.ID)
		if err != nil {
			return nil, err
		}
		rows, err := c.Tx.Query(c.Context, `select l.`+s.selfCol+`::text, o.id::text, o.seq, o.data ->> pf.id::text, pf.type, pf.config
			from tables.links l
			join tables.records o on o.workspace_id=l.workspace_id and o.id=l.`+s.other+` and o.deleted_at is null
			join tables.tables ot on ot.workspace_id=o.workspace_id and ot.id=o.table_id and ot.deleted_at is null
			left join tables.fields pf on pf.workspace_id=ot.workspace_id and pf.id=ot.primary_field_id
			where l.workspace_id=$1::uuid and l.field_id=$2::uuid and l.`+s.selfCol+` = any($3::uuid[]) order by l.`+s.selfCol+`, o.seq`,
			c.WorkspaceID(), s.fwd.ID, recIDs)
		if err != nil {
			return nil, command.DB(err)
		}
		for rows.Next() {
			var rid, oid string
			var seq int64
			var val, ptype *string
			var pcfg json.RawMessage
			if err := rows.Scan(&rid, &oid, &seq, &val, &ptype, &pcfg); err != nil {
				rows.Close()
				return nil, command.DB(err)
			}
			pt := Text
			if ptype != nil {
				pt = FieldType(*ptype)
			}
			if out[rid] == nil {
				out[rid] = map[string][]linkChip{}
			}
			out[rid][f.ID] = append(out[rid][f.ID], linkChip{ID: oid, Title: recTitle(pt, pcfg, seq, val)})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, command.DB(err)
		}
	}
	return out, nil
}
