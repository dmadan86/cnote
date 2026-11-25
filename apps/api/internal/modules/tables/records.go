package tables

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

// Record writes and reads (package 2). Every handler opens its table through openTable, which resolves the base
// role (17.3) first, so nothing below runs for a caller who cannot see the base.

func init() {
	handlers["record.get"] = recordGet
	handlers["record.create"] = recordCreate
	handlers["record.create_many"] = recordCreateMany
	handlers["record.update"] = recordUpdate
	handlers["record.bulk_update"] = recordBulkUpdate
	handlers["record.delete"] = recordDelete
	handlers["record.restore"] = recordRestore
}

const (
	maxRecordBytes = 65536
)

type recRow struct {
	ID        string          `json:"id"`
	TableID   string          `json:"table_id"`
	Seq       int64           `json:"seq"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	CreatedBy *string         `json:"created_by"`
	UpdatedBy *string         `json:"updated_by"`
}
type recWithLinks struct {
	recRow
	Links map[string][]linkChip `json:"links"`
}

const recCols = `r.id::text, r.table_id::text, r.seq, r.data, r.created_at, r.updated_at, r.created_by::text, r.updated_by::text`

func scanRec(row pgx.Row, extra ...any) (recRow, error) {
	var r recRow
	err := row.Scan(append([]any{&r.ID, &r.TableID, &r.Seq, &r.Data, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy, &r.UpdatedBy}, extra...)...)
	return r, err
}

// openedTable is a live table with its base, the caller's role there and its live fields.
type openedTable struct {
	Table  Table
	Base   Base
	Role   Role
	Fields []Field
	byID   map[string]Field
}

// openTable loads a table and checks the caller has at least min in its base. No access and no table look the same.
func openTable(c *command.Ctx, tableID string, min Role) (*openedTable, error) {
	t, err := loadTable(c, tableID)
	if err != nil {
		return nil, err
	}
	b, err := loadBase(c, t.BaseID)
	if err != nil {
		return nil, err
	}
	role, err := spaceRole(c, b.SpaceID)
	if err != nil {
		return nil, err
	}
	if role == RoleNone {
		return nil, command.ErrNotFound("table")
	}
	if !role.AtLeast(min) {
		return nil, command.ErrForbidden("you do not have permission for this change in this base")
	}
	if min.AtLeast(RoleWrite) {
		// Locking: a type conversion takes its field row FOR UPDATE (convertField); every writer takes the table's
		// field rows FOR SHARE here, before it reads the field types. Writers do not block each other, but a
		// writer and a conversion exclude one another, so a write validates against the type that will be committed
		// and the conversion sees every record that write left behind. Rows locked by a waiting conversion are
		// re-read after it commits (read committed), so loadFields below returns the new type.
		if _, err := c.Tx.Exec(c.Context, `select 1 from tables.fields where workspace_id=$1::uuid and table_id=$2::uuid and deleted_at is null for share`, c.WorkspaceID(), t.ID); err != nil {
			return nil, command.DB(err)
		}
	}
	fields, err := loadFields(c, t.ID)
	if err != nil {
		return nil, err
	}
	ot := &openedTable{Table: t, Base: b, Role: role, Fields: fields, byID: map[string]Field{}}
	for _, f := range fields {
		ot.byID[f.ID] = f
	}
	return ot, nil
}

// openRecordTable finds the table of a live record, then opens it.
func openRecordTable(c *command.Ctx, recordID string, min Role) (*openedTable, error) {
	var tid string
	err := c.Tx.QueryRow(c.Context, `select table_id::text from tables.records where workspace_id=$1::uuid and id=$2::uuid and deleted_at is null`,
		c.WorkspaceID(), recordID).Scan(&tid)
	if err != nil {
		return nil, command.DB(err)
	}
	return openTable(c, tid, min)
}

func newRecordID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func jsonEq(a, b json.RawMessage) bool {
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	return reflect.DeepEqual(x, y)
}

func rawOrNull(m map[string]json.RawMessage, k string) any {
	v, ok := m[k]
	if !ok {
		return nil
	}
	var x any
	_ = json.Unmarshal(v, &x)
	return x
}

// prepared is a write to one record, after validation.
type prepared struct {
	set        map[string]any
	unset      []string
	links      map[string][]string
	clearLinks []string
}

// prepareWrite validates values and clear against the table's fields. A nil value or a value Validate maps to
// "empty" clears the field; a locked field needs manage.
func prepareWrite(tc *openedTable, values map[string]any, clear []string, links map[string][]string) (prepared, error) {
	p := prepared{set: map[string]any{}, links: links}
	check := func(id string) (Field, error) {
		f, ok := tc.byID[id]
		if !ok {
			return f, command.ErrInvalid("unknown field " + id)
		}
		if f.Locked && !tc.Role.AtLeast(RoleManage) {
			return f, command.ErrForbidden(fmt.Sprintf("field %q is locked: ask a manager", f.Name))
		}
		return f, nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f, err := check(k)
		if err != nil {
			return p, err
		}
		if values[k] == nil {
			p.unset = append(p.unset, k)
			continue
		}
		v, err := Validate(f, values[k])
		if err != nil {
			return p, err
		}
		if v == nil {
			p.unset = append(p.unset, k)
		} else {
			p.set[k] = v
		}
	}
	for id := range links {
		f, err := check(id)
		if err != nil {
			return p, err
		}
		if f.Type != Link {
			return p, command.ErrInvalid(fmt.Sprintf("field %q is not a link field", f.Name))
		}
	}
	for _, id := range clear {
		f, err := check(id)
		if err != nil {
			return p, err
		}
		if f.Type == Link {
			p.clearLinks = append(p.clearLinks, id)
		} else if _, set := p.set[id]; !set {
			p.unset = append(p.unset, id)
		}
	}
	return p, nil
}

// upd is one record's change.
type upd struct {
	ID string
	prepared
}

// applyUpdates locks the records, applies each change, writes history and returns the records that really
// changed. A record whose values and links come out identical is left alone (no history, no event, no updated_at).
func applyUpdates(c *command.Ctx, tc *openedTable, ups []upd) ([]evRec, error) {
	idSet := map[string]bool{}
	for _, u := range ups {
		if idSet[u.ID] {
			return nil, command.ErrInvalid("record " + u.ID + " appears twice")
		}
		idSet[u.ID] = true
	}
	ids := make([]string, 0, len(ups))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows, err := c.Tx.Query(c.Context, `select id::text, data from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and id = any($3::uuid[]) and deleted_at is null order by id for update`,
		c.WorkspaceID(), tc.Table.ID, ids)
	if err != nil {
		return nil, command.DB(err)
	}
	cur := map[string]json.RawMessage{}
	for rows.Next() {
		var id string
		var d json.RawMessage
		if err := rows.Scan(&id, &d); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		cur[id] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if len(cur) != len(ids) {
		return nil, command.ErrNotFound("record")
	}
	sets := make([]newRec, len(ups))
	for i, u := range ups {
		sets[i] = newRec{set: u.set}
	}
	if err := checkUserValues(c, tc, sets); err != nil {
		return nil, err
	}
	atype, aid, _ := c.Principal.Actor()
	sides := map[string]linkSide{}
	var changed []evRec
	batch := &pgx.Batch{}
	for _, u := range ups {
		old := map[string]json.RawMessage{}
		_ = json.Unmarshal(cur[u.ID], &old)
		nw := map[string]json.RawMessage{}
		for k, v := range old {
			nw[k] = v
		}
		change := map[string][2]any{}
		for k, v := range u.set {
			b, _ := json.Marshal(v)
			nw[k] = b
		}
		for _, k := range u.unset {
			delete(nw, k)
		}
		var fieldIDs []string
		for k := range nw {
			if !jsonEq(nw[k], old[k]) || old[k] == nil {
				change[k] = [2]any{rawOrNull(old, k), rawOrNull(nw, k)}
			}
		}
		for k := range old {
			if _, ok := nw[k]; !ok {
				change[k] = [2]any{rawOrNull(old, k), nil}
			}
		}
		for k := range change {
			fieldIDs = append(fieldIDs, k)
		}
		linkSet := map[string][]string{}
		for k, v := range u.links {
			linkSet[k] = v
		}
		for _, k := range u.clearLinks {
			linkSet[k] = []string{}
		}
		for _, fid := range sortedKeys(linkSet) {
			side, ok := sides[fid]
			if !ok {
				if side, err = resolveLink(c, tc, fid); err != nil {
					return nil, err
				}
				sides[fid] = side
			}
			oldIDs, newIDs, did, err := setLinks(c, tc, u.ID, side, linkSet[fid])
			if err != nil {
				return nil, err
			}
			if did {
				change[fid] = [2]any{oldIDs, newIDs}
				fieldIDs = append(fieldIDs, fid)
			}
		}
		if len(change) == 0 {
			continue
		}
		data, _ := json.Marshal(nw)
		if len(data) > maxRecordBytes {
			return nil, command.ErrInvalid("a record can hold at most 64 KB of data")
		}
		batch.Queue(`update tables.records set data=$3::jsonb, updated_at=now(), updated_by=$4::uuid where workspace_id=$1::uuid and id=$2::uuid`,
			c.WorkspaceID(), u.ID, string(data), c.Principal.UserID)
		queueHistory(batch, c, tc.Table.ID, u.ID, atype, aid, change)
		sort.Strings(fieldIDs)
		changed = append(changed, evRec{ID: u.ID, Fields: fieldIDs})
	}
	if err := sendBatch(c, batch); err != nil {
		return nil, err
	}
	return changed, nil
}

func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func sendBatch(c *command.Ctx, b *pgx.Batch) error {
	if b.Len() == 0 {
		return nil
	}
	br := c.Tx.SendBatch(c.Context, b)
	for i := 0; i < b.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return command.DB(err)
		}
	}
	return command.DB(br.Close())
}

// newRec is a record to insert; values were checked by prepareWrite.
type newRec struct {
	set   map[string]any
	links map[string][]string
}

// insertRecords adds records to a table: limit check, seq block, rows, links, record_count. It returns the new ids
// in input order. Events are the caller's job (emitRecordEvents) so a caller can group them.
func insertRecords(c *command.Ctx, tc *openedTable, news []newRec) ([]string, error) {
	n := len(news)
	if n == 0 {
		return []string{}, nil
	}
	if err := checkUserValues(c, tc, news); err != nil {
		return nil, err
	}
	// The next_seq update takes the table row lock, so the per-table cap check below cannot race another writer.
	var next int64
	if err := c.Tx.QueryRow(c.Context, `update tables.tables set next_seq = next_seq + $3 where workspace_id=$1::uuid and id=$2::uuid returning next_seq - $3`,
		c.WorkspaceID(), tc.Table.ID, int64(n)).Scan(&next); err != nil {
		return nil, command.DB(err)
	}
	if err := checkRecordLimit(c.Context, c.Tx, tc.Base.ID, tc.Table.ID, n); err != nil {
		return nil, err
	}
	if err := bumpRecordCount(c, tc.Base.ID, n); err != nil {
		return nil, err
	}
	ids := make([]string, n)
	batch := &pgx.Batch{}
	for i, r := range news {
		ids[i] = newRecordID()
		data := map[string]any{}
		for k, v := range r.set {
			data[k] = v
		}
		b, _ := json.Marshal(data)
		if len(b) > maxRecordBytes {
			return nil, command.ErrInvalid("a record can hold at most 64 KB of data")
		}
		batch.Queue(`insert into tables.records (id, workspace_id, table_id, seq, data, created_by, updated_by) values ($1::uuid,$2::uuid,$3::uuid,$4,$5::jsonb,$6::uuid,$6::uuid)`,
			ids[i], c.WorkspaceID(), tc.Table.ID, next+int64(i), string(b), c.Principal.UserID)
	}
	if err := sendBatch(c, batch); err != nil {
		return nil, err
	}
	sides := map[string]linkSide{}
	for i, r := range news {
		for _, fid := range sortedKeys(r.links) {
			side, ok := sides[fid]
			if !ok {
				var err error
				if side, err = resolveLink(c, tc, fid); err != nil {
					return nil, err
				}
				sides[fid] = side
			}
			if _, _, _, err := setLinks(c, tc, ids[i], side, r.links[fid]); err != nil {
				return nil, err
			}
		}
	}
	return ids, nil
}

// bumpRecordCount keeps bases.record_count (what limits and base.list read) in step. Adding is atomic against the cap.
func bumpRecordCount(c *command.Ctx, baseID string, delta int) error {
	if delta > 0 {
		plan, err := workspacePlan(c.Context, c.Tx, c.WorkspaceID())
		if err != nil {
			return err
		}
		max := limitsFor(plan).RecordsPerBase
		tag, err := c.Tx.Exec(c.Context, `update tables.bases set record_count = record_count + $3 where workspace_id=$1::uuid and id=$2::uuid and record_count + $3 <= $4`,
			c.WorkspaceID(), baseID, delta, max)
		if err != nil {
			return command.DB(err)
		}
		if tag.RowsAffected() == 0 {
			return command.ErrConflict(fmt.Sprintf("This base has reached %s records on the %s plan", num(max), planName(plan)))
		}
		return nil
	}
	_, err := c.Tx.Exec(c.Context, `update tables.bases set record_count = greatest(record_count + $3, 0) where workspace_id=$1::uuid and id=$2::uuid`,
		c.WorkspaceID(), baseID, delta)
	return command.DB(err)
}

// ---- commands ----

type writeIn struct {
	Values map[string]any      `json:"values"`
	Links  map[string][]string `json:"links"`
}

func recordCreate(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		TableID string `json:"table_id"`
		writeIn
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	tc, err := openTable(c, a.TableID, RoleWrite)
	if err != nil {
		return nil, err
	}
	p, err := prepareWrite(tc, a.Values, nil, a.Links)
	if err != nil {
		return nil, err
	}
	ids, err := insertRecords(c, tc, []newRec{{set: p.set, links: p.links}})
	if err != nil {
		return nil, err
	}
	if _, err := emitRecordEvents(c, tc, "created", []evRec{{ID: ids[0], Fields: sortedKeys(p.set)}}); err != nil {
		return nil, err
	}
	return recordFull(c, tc, ids[0])
}

func recordCreateMany(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		TableID string    `json:"table_id"`
		Records []writeIn `json:"records"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	if len(a.Records) > 500 {
		return nil, command.ErrInvalid("at most 500 records per call")
	}
	tc, err := openTable(c, a.TableID, RoleWrite)
	if err != nil {
		return nil, err
	}
	news := make([]newRec, len(a.Records))
	for i, r := range a.Records {
		p, err := prepareWrite(tc, r.Values, nil, r.Links)
		if err != nil {
			return nil, command.ErrInvalid(fmt.Sprintf("record %d: %s", i+1, err.Error()))
		}
		news[i] = newRec{set: p.set, links: p.links}
	}
	ids, err := insertRecords(c, tc, news)
	if err != nil {
		return nil, err
	}
	evs := make([]evRec, len(ids))
	for i, id := range ids {
		evs[i] = evRec{ID: id, Fields: sortedKeys(news[i].set)}
	}
	batchID, err := emitRecordEvents(c, tc, "created", evs)
	if err != nil {
		return nil, err
	}
	return map[string]any{"count": len(ids), "ids": ids, "batch_id": batchID}, nil
}

func recordUpdate(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		ID    string   `json:"id"`
		Clear []string `json:"clear"`
		writeIn
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	tc, err := openRecordTable(c, a.ID, RoleWrite)
	if err != nil {
		return nil, err
	}
	p, err := prepareWrite(tc, a.Values, a.Clear, a.Links)
	if err != nil {
		return nil, err
	}
	changed, err := applyUpdates(c, tc, []upd{{ID: a.ID, prepared: p}})
	if err != nil {
		return nil, err
	}
	if _, err := emitRecordEvents(c, tc, "updated", changed); err != nil {
		return nil, err
	}
	return recordFull(c, tc, a.ID)
}

func recordBulkUpdate(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		TableID string `json:"table_id"`
		Updates []struct {
			ID    string         `json:"id"`
			Value map[string]any `json:"values"`
			Clear []string       `json:"clear"`
		} `json:"updates"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	if len(a.Updates) > 1000 {
		return nil, command.ErrInvalid("at most 1,000 updates per call")
	}
	tc, err := openTable(c, a.TableID, RoleWrite)
	if err != nil {
		return nil, err
	}
	ups := make([]upd, len(a.Updates))
	for i, u := range a.Updates {
		p, err := prepareWrite(tc, u.Value, u.Clear, nil)
		if err != nil {
			return nil, command.ErrInvalid(fmt.Sprintf("update %d: %s", i+1, err.Error()))
		}
		ups[i] = upd{ID: u.ID, prepared: p}
	}
	changed, err := applyUpdates(c, tc, ups)
	if err != nil {
		return nil, err
	}
	if _, err := emitRecordEvents(c, tc, "updated", changed); err != nil {
		return nil, err
	}
	return map[string]any{"updated": len(changed)}, nil
}

// recordFull reads one live record with its link chips.
func recordFull(c *command.Ctx, tc *openedTable, id string) (*recWithLinks, error) {
	r, err := scanRec(c.Tx.QueryRow(c.Context, `select `+recCols+` from tables.records r where r.workspace_id=$1::uuid and r.id=$2::uuid and r.deleted_at is null`, c.WorkspaceID(), id))
	if err != nil {
		return nil, command.DB(err)
	}
	chips, err := linkChips(c, tc, []string{id})
	if err != nil {
		return nil, err
	}
	out := &recWithLinks{recRow: r, Links: chips[id]}
	if out.Links == nil {
		out.Links = map[string][]linkChip{}
	}
	return out, nil
}

func recordGet(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	tc, err := openRecordTable(c, a.ID, RoleRead)
	if err != nil {
		return nil, err
	}
	return recordFull(c, tc, a.ID)
}

// ---- delete and restore ----

func recordDelete(c *command.Ctx, in json.RawMessage) (any, error)  { return recordTrash(c, in, true) }
func recordRestore(c *command.Ctx, in json.RawMessage) (any, error) { return recordTrash(c, in, false) }

func recordTrash(c *command.Ctx, in json.RawMessage, del bool) (any, error) {
	var a struct {
		IDs []string `json:"ids"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	if len(a.IDs) > 1000 {
		return nil, command.ErrInvalid("at most 1,000 records per call")
	}
	ids := slices.Compact(sortedCopy(a.IDs))
	if len(ids) == 0 {
		return map[string]any{"count": 0, "batch_id": nil}, nil
	}
	rows, err := c.Tx.Query(c.Context, `select r.id::text, r.table_id::text, t.base_id::text
		from tables.records r
		join tables.tables t on t.workspace_id=r.workspace_id and t.id=r.table_id and t.deleted_at is null
		join tables.bases b on b.workspace_id=t.workspace_id and b.id=t.base_id and b.deleted_at is null
		where r.workspace_id=$1::uuid and r.id = any($2::uuid[]) order by r.id for update of r`, c.WorkspaceID(), ids)
	if err != nil {
		return nil, command.DB(err)
	}
	type loc struct{ id, table, base string }
	var locs []loc
	for rows.Next() {
		var l loc
		if err := rows.Scan(&l.id, &l.table, &l.base); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		locs = append(locs, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if len(locs) != len(ids) {
		return nil, command.ErrNotFound("record") // a missing id and an id in a base the caller cannot see look alike
	}
	var baseIDs []string
	tables := map[string]string{} // table -> base
	for _, l := range locs {
		baseIDs = append(baseIDs, l.base)
		tables[l.table] = l.base
	}
	if err := assertBases(c, baseIDs, RoleWrite); err != nil {
		return nil, err
	}
	if len(ids) > 100 && len(tables) > 1 {
		return nil, command.ErrInvalid("more than 100 records in one call must all be in one table")
	}
	live, op := "deleted_at is null", "deleted"
	set := "deleted_at = now()"
	if !del {
		live, op, set = "deleted_at is not null", "restored", "deleted_at = null"
	}
	atype, aid, _ := c.Principal.Actor()
	total := 0
	var batchID *string
	for _, table := range sortedKeys(tables) {
		if !del { // room first: a restore adds live records back; the table row lock keeps the cap check honest
			if _, err := c.Tx.Exec(c.Context, `select 1 from tables.tables where workspace_id=$1::uuid and id=$2::uuid for update`, c.WorkspaceID(), table); err != nil {
				return nil, command.DB(err)
			}
			var n int
			if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and id = any($3::uuid[]) and deleted_at is not null`,
				c.WorkspaceID(), table, ids).Scan(&n); err != nil {
				return nil, command.DB(err)
			}
			if n > 0 {
				if err := checkRecordLimit(c.Context, c.Tx, tables[table], table, n); err != nil {
					return nil, err
				}
			}
		}
		rs, err := c.Tx.Query(c.Context, `update tables.records set `+set+`, updated_at=now(), updated_by=$4::uuid where workspace_id=$1::uuid and table_id=$2::uuid and id = any($3::uuid[]) and `+live+` returning id::text`,
			c.WorkspaceID(), table, ids, c.Principal.UserID)
		if err != nil {
			return nil, command.DB(err)
		}
		var done []string
		for rs.Next() {
			var id string
			if err := rs.Scan(&id); err != nil {
				rs.Close()
				return nil, command.DB(err)
			}
			done = append(done, id)
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return nil, command.DB(err)
		}
		if len(done) == 0 {
			continue
		}
		sort.Strings(done)
		delta := len(done)
		if del {
			delta = -delta
		}
		if err := bumpRecordCount(c, tables[table], delta); err != nil {
			return nil, err
		}
		hist := &pgx.Batch{}
		change := map[string][2]any{"_deleted": {!del, del}}
		evs := make([]evRec, len(done))
		for i, id := range done {
			queueHistory(hist, c, table, id, atype, aid, change)
			evs[i] = evRec{ID: id}
		}
		if err := sendBatch(c, hist); err != nil {
			return nil, err
		}
		tc := &openedTable{Table: Table{ID: table, BaseID: tables[table]}, Base: Base{ID: tables[table]}}
		b, err := emitRecordEvents(c, tc, op, evs)
		if err != nil {
			return nil, err
		}
		if b != nil {
			batchID = b
		}
		total += len(done)
	}
	return map[string]any{"count": total, "batch_id": batchID}, nil
}

func sortedCopy(s []string) []string {
	o := slices.Clone(s)
	sort.Strings(o)
	return o
}

func recDecode(in json.RawMessage, v any) error {
	if len(in) == 0 {
		in = json.RawMessage("{}")
	}
	if err := json.Unmarshal(in, v); err != nil {
		return command.ErrInvalid("invalid input: " + err.Error())
	}
	return nil
}

// checkUserValues refuses user-field values that name someone outside the workspace (Validate checks only the shape).
func checkUserValues(c *command.Ctx, tc *openedTable, recs []newRec) error {
	want := map[string]bool{}
	for _, r := range recs {
		for k, v := range r.set {
			if tc.byID[k].Type != User {
				continue
			}
			switch x := v.(type) {
			case []string:
				for _, id := range x {
					want[id] = true
				}
			case []any:
				for _, e := range x {
					if id, ok := e.(string); ok {
						want[id] = true
					}
				}
			}
		}
	}
	return assertMembers(c, want)
}

func assertMembers(c *command.Ctx, want map[string]bool) error {
	if len(want) == 0 {
		return nil
	}
	ids := sortedKeys(want)
	var n int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from core.memberships where workspace_id=$1::uuid and user_id = any($2::uuid[])`, c.WorkspaceID(), ids).Scan(&n); err != nil {
		return command.DB(err)
	}
	if n != len(ids) {
		return command.ErrInvalid("a person in a user field is not a member of this workspace")
	}
	return nil
}
