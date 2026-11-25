package tables

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/dataimport"
	"github.com/jackc/pgx/v5"
)

// CSV import (17.6). The file arrives through POST /api/tables/import (server/tables_import.go), which stores it in
// tables.imports and returns the row id as the handle. table_import.preview shows the mapping and what would fail;
// table_import.run is idempotent per handle and queues job tables.import, which adds the ready rows in chunks of 500.
// Each chunk commits together with next_row, so a crash or retry never adds a row twice and the job resumes.

func init() {
	handlers["table_import.preview"] = importPreview
	handlers["table_import.run"] = importRun
	handlers["table_import.status"] = importStatus
}

const importChunk = 500

type colMap struct {
	Index    int       `json:"index"`
	FieldID  string    `json:"field_id,omitempty"`
	NewField *newField `json:"new_field,omitempty"`
	Skip     bool      `json:"skip,omitempty"`
}
type newField struct {
	Name   string          `json:"name"`
	Type   FieldType       `json:"type"`
	Config json.RawMessage `json:"config,omitempty"`
}
type importMapping struct {
	Columns []colMap `json:"columns"`
}

// StoreUpload saves an uploaded CSV for a table and returns the handle. Called by the HTTP endpoint inside a tenant
// transaction after the caller passed the record.create gate.
func StoreUpload(c *command.Ctx, tableID, baseID, filename string, data []byte) (map[string]any, error) {
	tc, err := openTable(c, tableID, RoleWrite)
	if err != nil {
		return nil, err
	}
	if baseID != "" && baseID != tc.Base.ID {
		return nil, command.ErrNotFound("table")
	}
	t, err := dataimport.ReadCSVKeepSpace(data)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	if len(filename) > 200 {
		filename = filename[:200]
	}
	var id string
	if err := c.Tx.QueryRow(c.Context, `insert into tables.imports (workspace_id, table_id, filename, data, created_by) values ($1::uuid,$2::uuid,$3,$4,$5::uuid) returning id::text`,
		c.WorkspaceID(), tableID, filename, data, c.Principal.UserID).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"handle": id, "rows": len(t.Rows), "headers": t.Headers}, nil
}

type importRow struct {
	ID, TableID, Status string
	Data                []byte
	Mapping             json.RawMessage
	NextRow             int
	Counts              json.RawMessage
	Error               *string
	CreatedBy           *string
}

func loadImport(c *command.Ctx, id string, lock bool) (importRow, error) {
	var r importRow
	q := `select id::text, table_id::text, status, data, mapping, next_row, counts, error, created_by::text from tables.imports where workspace_id=$1::uuid and id=$2::uuid`
	if lock {
		q += " for update"
	}
	err := c.Tx.QueryRow(c.Context, q, c.WorkspaceID(), id).Scan(&r.ID, &r.TableID, &r.Status, &r.Data, &r.Mapping, &r.NextRow, &r.Counts, &r.Error, &r.CreatedBy)
	return r, command.DB(err)
}

func importJobOut(r importRow) map[string]any {
	var counts any = map[string]int{"created": 0, "skipped": 0, "failed": 0}
	if len(r.Counts) > 0 {
		counts = r.Counts
	}
	return map[string]any{"id": r.ID, "status": r.Status, "counts": counts, "error": r.Error}
}

// target is what a file column becomes.
type target struct {
	idx     int
	header  string
	field   Field // ID is "" for a field the run will create
	spec    *newField
	skipped bool
}

var simpleNew = map[FieldType]bool{Text: true, LongText: true, Number: true, Percent: true, Currency: true, Date: true, Checkbox: true, Phone: true, Email: true, URL: true}

func guessType(samples []string) FieldType {
	var vals []string
	for _, s := range samples {
		if strings.TrimSpace(s) != "" {
			vals = append(vals, s)
		}
	}
	if len(vals) == 0 {
		return Text
	}
	all := func(f func(string) bool) bool {
		for _, v := range vals {
			if !f(v) {
				return false
			}
		}
		return true
	}
	probe := func(t FieldType) func(string) bool {
		return func(s string) bool {
			v, err := Coerce(Field{Type: t, Config: json.RawMessage(`{}`)}, s)
			return err == nil && v != nil
		}
	}
	switch {
	case all(func(s string) bool {
		_, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 64)
		return err == nil
	}):
		return Number
	case all(probe(Date)):
		return Date
	case all(func(s string) bool {
		s = strings.ToLower(strings.TrimSpace(s))
		return s == "yes" || s == "no" || s == "true" || s == "false"
	}):
		return Checkbox
	case all(probe(Email)):
		return Email
	}
	for _, v := range vals {
		if len(v) > 500 {
			return LongText
		}
	}
	return Text
}

// resolveTargets maps every file column: the caller's mapping where given, else a field with the same name, else a
// new field guessed from the samples.
func resolveTargets(tc *openedTable, t *dataimport.Table, m *importMapping) ([]target, error) {
	given := map[int]colMap{}
	if m != nil {
		for _, cm := range m.Columns {
			if cm.Index < 0 || cm.Index >= len(t.Headers) {
				return nil, command.ErrInvalid("mapping names a column that is not in the file")
			}
			given[cm.Index] = cm
		}
	}
	byName := map[string]Field{}
	names := map[string]bool{}
	for _, f := range tc.Fields {
		names[strings.ToLower(f.Name)] = true
		if !f.Type.Computed() && f.Type != Attachment {
			byName[strings.ToLower(f.Name)] = f
		}
	}
	used := map[string]bool{}
	out := make([]target, len(t.Headers))
	for i, h := range t.Headers {
		x := target{idx: i, header: h}
		cm, has := given[i]
		switch {
		case has && cm.Skip:
			x.skipped = true
		case has && cm.FieldID != "":
			f, ok := tc.byID[cm.FieldID]
			if !ok || f.Type.Computed() || f.Type == Attachment {
				return nil, command.ErrInvalid("column " + strconv.Itoa(i+1) + " is mapped to a field that cannot be imported into")
			}
			x.field = f
		case has && cm.NewField != nil:
			if !simpleNew[cm.NewField.Type] || strings.TrimSpace(cm.NewField.Name) == "" {
				return nil, command.ErrInvalid("a new field from an import must be text, number, date, checkbox, phone, email or URL")
			}
			x.spec = cm.NewField
		default:
			if f, ok := byName[strings.ToLower(strings.TrimSpace(h))]; ok && strings.TrimSpace(h) != "" {
				x.field = f
			} else if strings.TrimSpace(h) == "" {
				x.skipped = true
			} else {
				var samples []string
				for _, r := range t.Rows {
					if i < len(r) {
						samples = append(samples, r[i])
					}
					if len(samples) >= 50 {
						break
					}
				}
				name := strings.TrimSpace(h)
				for n := 2; names[strings.ToLower(name)]; n++ {
					name = fmt.Sprintf("%s (%d)", strings.TrimSpace(h), n)
				}
				names[strings.ToLower(name)] = true
				x.spec = &newField{Name: name, Type: guessType(samples)}
			}
		}
		if x.spec != nil {
			x.field = Field{Name: x.spec.Name, Type: x.spec.Type, Config: x.spec.Config}
			if len(x.field.Config) == 0 {
				x.field.Config = json.RawMessage(`{}`)
			}
		}
		if x.field.ID != "" {
			if used[x.field.ID] {
				return nil, command.ErrInvalid("two columns are mapped to " + x.field.Name)
			}
			used[x.field.ID] = true
		}
		out[i] = x
	}
	return out, nil
}

// rowValues coerces one file row. Cells keep their whitespace (Coerce decides what blank means).
func rowValues(ts []target, row []string, fileRow int, errs *[]cellErr) (map[string]any, bool) {
	set := map[string]any{}
	ok := true
	for _, x := range ts {
		if x.skipped || x.idx >= len(row) {
			continue
		}
		v, err := Coerce(x.field, row[x.idx])
		if err != nil {
			ok = false
			if len(*errs) < 50 {
				*errs = append(*errs, cellErr{fileRow, x.idx, errReason(err)})
			}
			continue
		}
		if v != nil {
			key := x.field.ID
			if key == "" {
				key = "new:" + strconv.Itoa(x.idx)
			}
			set[key] = v
		}
	}
	return set, ok
}

func importFor(c *command.Ctx, handle string, min Role, lock bool) (importRow, *openedTable, error) {
	r, err := loadImport(c, handle, lock)
	if err != nil {
		return r, nil, err
	}
	tc, err := openTable(c, r.TableID, min)
	return r, tc, err
}

func parseMapping(raw json.RawMessage) (*importMapping, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m importMapping
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, command.ErrInvalid("invalid mapping")
	}
	return &m, nil
}

func importPreview(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		Handle  string          `json:"handle"`
		Mapping json.RawMessage `json:"mapping"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	r, tc, err := importFor(c, a.Handle, RoleWrite, false)
	if err != nil {
		return nil, err
	}
	if r.Status != "uploaded" {
		return nil, command.ErrConflict("this import has already been started")
	}
	m, err := parseMapping(a.Mapping)
	if err != nil {
		return nil, err
	}
	t, err := dataimport.ReadCSVKeepSpace(r.Data)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	ts, err := resolveTargets(tc, t, m)
	if err != nil {
		return nil, err
	}
	cols := make([]map[string]any, len(ts))
	for i, x := range ts {
		samples := []string{}
		for _, row := range t.Rows {
			if x.idx < len(row) && strings.TrimSpace(row[x.idx]) != "" && !contains(samples, row[x.idx]) {
				samples = append(samples, row[x.idx])
			}
			if len(samples) == 3 {
				break
			}
		}
		col := map[string]any{"index": i, "header": x.header, "samples": samples, "field_id": nil, "new_field": nil}
		if x.field.ID != "" {
			col["field_id"] = x.field.ID
		}
		if x.spec != nil {
			col["new_field"] = x.spec
		}
		cols[i] = col
	}
	errs := []cellErr{}
	ready, bad := 0, 0
	for i, row := range t.Rows {
		if _, ok := rowValues(ts, row, t.RowNums[i], &errs); ok {
			ready++
		} else {
			bad++
		}
	}
	return map[string]any{"columns": cols, "ready": ready, "needs_fixing": bad, "errors": errs}, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func importRun(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		Handle  string          `json:"handle"`
		Mapping json.RawMessage `json:"mapping"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	r, tc, err := importFor(c, a.Handle, RoleWrite, true) // the row lock makes a second run wait and then see "queued"
	if err != nil {
		return nil, err
	}
	if r.Status != "uploaded" {
		return importJobOut(r), nil
	}
	m, err := parseMapping(a.Mapping)
	if err != nil {
		return nil, err
	}
	t, err := dataimport.ReadCSVKeepSpace(r.Data)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	ts, err := resolveTargets(tc, t, m)
	if err != nil {
		return nil, err
	}
	var scratch []cellErr
	ready := 0
	for i, row := range t.Rows {
		if _, ok := rowValues(ts, row, t.RowNums[i], &scratch); ok {
			ready++
		}
	}
	if err := checkRecordLimit(c.Context, c.Tx, tc.Base.ID, tc.Table.ID, ready); err != nil {
		return nil, err
	}
	final := importMapping{}
	for _, x := range ts {
		if x.skipped {
			final.Columns = append(final.Columns, colMap{Index: x.idx, Skip: true})
			continue
		}
		if x.spec != nil {
			out, err := c.Call("field.create", map[string]any{"table_id": tc.Table.ID, "name": x.spec.Name, "type": x.spec.Type, "config": x.spec.Config})
			if err != nil {
				return nil, err
			}
			var f struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(out, &f) != nil || f.ID == "" {
				return nil, command.NewError(command.Internal, "field.create returned no id")
			}
			x.field.ID = f.ID
		}
		final.Columns = append(final.Columns, colMap{Index: x.idx, FieldID: x.field.ID})
	}
	fm, _ := json.Marshal(final)
	if _, err := c.Tx.Exec(c.Context, `update tables.imports set status='queued', mapping=$3::jsonb where workspace_id=$1::uuid and id=$2::uuid`, c.WorkspaceID(), r.ID, string(fm)); err != nil {
		return nil, command.DB(err)
	}
	if err := enqueueImport(c, r.ID); err != nil {
		return nil, err
	}
	r.Status, r.Counts = "queued", nil
	return importJobOut(r), nil
}

func enqueueImport(c *command.Ctx, id string) error {
	p, _ := json.Marshal(map[string]string{"import_id": id})
	_, err := c.Tx.Exec(c.Context, `insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,'tables.import',$2,now())`, c.WorkspaceID(), json.RawMessage(p))
	return command.DB(err)
}

func importStatus(c *command.Ctx, in json.RawMessage) (any, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := recDecode(in, &a); err != nil {
		return nil, err
	}
	r, _, err := importFor(c, a.ID, RoleRead, false)
	if err != nil {
		return nil, err
	}
	return importJobOut(r), nil
}

// ImportJob is the worker handler of kind tables.import: one chunk, committed together with its progress.
func ImportJob(c *command.Ctx, payload json.RawMessage) error {
	var p struct {
		ImportID string `json:"import_id"`
	}
	if json.Unmarshal(payload, &p) != nil || p.ImportID == "" {
		return command.ErrInvalid("import_id missing")
	}
	r, err := loadImport(c, p.ImportID, true)
	if err != nil {
		return nil // deleted meanwhile
	}
	if r.Status != "queued" && r.Status != "running" {
		return nil
	}
	fail := func(why string) error {
		_, err := c.Tx.Exec(c.Context, `update tables.imports set status='failed', error=$3, data='' where workspace_id=$1::uuid and id=$2::uuid`, c.WorkspaceID(), r.ID, why)
		return command.DB(err)
	}
	// The rows are added as the person who started the import, through the same access checks.
	if r.CreatedBy == nil {
		return fail("the person who started this import no longer exists")
	}
	var role string
	if err := c.Tx.QueryRow(c.Context, `select role from core.memberships where workspace_id=$1::uuid and user_id=$2::uuid`, c.WorkspaceID(), *r.CreatedBy).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fail("the person who started this import is no longer in the workspace")
		}
		return command.DB(err)
	}
	saved := c.Principal
	defer func() { c.Principal = saved }()
	c.Principal = command.Principal{Kind: command.KindUser, UserID: r.CreatedBy, WorkspaceID: c.WorkspaceID(), Role: command.ParseRole(role)}
	tc, err := openTable(c, r.TableID, RoleWrite)
	if err != nil {
		return fail("you no longer have access to this table")
	}
	m, err := parseMapping(r.Mapping)
	if err != nil || m == nil {
		return fail("the import has no mapping")
	}
	t, err := dataimport.ReadCSVKeepSpace(r.Data)
	if err != nil {
		return fail(err.Error())
	}
	var ts []target
	for _, cm := range m.Columns {
		x := target{idx: cm.Index, skipped: cm.Skip}
		if !cm.Skip {
			f, ok := tc.byID[cm.FieldID]
			if !ok {
				return fail("a mapped field was deleted")
			}
			x.field = f
		}
		ts = append(ts, x)
	}
	var counts struct {
		Created int `json:"created"`
		Skipped int `json:"skipped"`
		Failed  int `json:"failed"`
	}
	_ = json.Unmarshal(r.Counts, &counts)
	end := min(r.NextRow+importChunk, len(t.Rows))
	var news []newRec
	var scratch []cellErr
	for i := r.NextRow; i < end; i++ {
		set, ok := rowValues(ts, t.Rows[i], t.RowNums[i], &scratch)
		if !ok {
			counts.Skipped++
			continue
		}
		news = append(news, newRec{set: set})
	}
	if _, err := c.Tx.Exec(c.Context, "savepoint tables_import_chunk"); err != nil {
		return command.DB(err)
	}
	ids, err := insertRecords(c, tc, news)
	if err == nil {
		evs := make([]evRec, len(ids))
		for i, id := range ids {
			evs[i] = evRec{ID: id, Fields: sortedKeys(news[i].set)}
		}
		_, err = emitRecordEvents(c, tc, "created", evs)
	}
	if err != nil {
		if _, e := c.Tx.Exec(c.Context, "rollback to savepoint tables_import_chunk"); e != nil {
			return command.DB(e)
		}
		counts.Failed += len(t.Rows) - r.NextRow
		cj, _ := json.Marshal(counts)
		_, e := c.Tx.Exec(c.Context, `update tables.imports set status='failed', error=$3, counts=$4::jsonb, data='' where workspace_id=$1::uuid and id=$2::uuid`, c.WorkspaceID(), r.ID, errReason(err), string(cj))
		return command.DB(e)
	}
	counts.Created += len(ids)
	cj, _ := json.Marshal(counts)
	status := "running"
	if end >= len(t.Rows) {
		status = "done"
	}
	if _, err := c.Tx.Exec(c.Context, `update tables.imports set status=$3, next_row=$4, counts=$5::jsonb, data = case when $3='done' then ''::bytea else data end where workspace_id=$1::uuid and id=$2::uuid`,
		c.WorkspaceID(), r.ID, status, end, string(cj)); err != nil {
		return command.DB(err)
	}
	if status == "running" {
		return enqueueImport(c, r.ID)
	}
	return nil
}
