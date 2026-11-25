package tables

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func init() {
	handlers["table.get"] = tableGet
	handlers["table.create"] = tableCreate
	handlers["table.update"] = tableUpdate
	handlers["table.delete"] = tableDelete
	handlers["table.restore"] = tableRestore
}

func decode(raw json.RawMessage, in any) error {
	if err := json.Unmarshal(raw, in); err != nil {
		return command.ErrInvalid("invalid input")
	}
	return nil
}

func needID(what, s string) error {
	if !uuidRe.MatchString(s) {
		return command.ErrInvalid(what + " must be an id")
	}
	return nil
}

// need resolves the caller's role in a base and refuses below min. No role at all is "not found".
func need(c *command.Ctx, baseID string, min Role) error {
	r, err := baseRole(c, baseID)
	if err != nil {
		return err
	}
	return atLeast(r, min)
}

func atLeast(r, min Role) error {
	switch {
	case r == RoleNone:
		return command.ErrNotFound("base")
	case !r.AtLeast(min):
		return command.ErrForbidden(map[Role]string{
			RoleWrite:  "you need write access to this base",
			RoleManage: "this needs a space manager (workspace admin or space admin)",
		}[min])
	}
	return nil
}

func cleanName(what, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > max {
		return "", command.ErrInvalid(what + " name must be 1 to " + itoa(max) + " characters")
	}
	return s, nil
}

// emitStruct sends a value-free structure event: ids only, never names or values (17.3).
func emitStruct(c *command.Ctx, kind, baseID, tableID, fieldID string) error {
	p := map[string]string{"base_id": baseID}
	if tableID != "" {
		p["table_id"] = tableID
	}
	if fieldID != "" {
		p["field_id"] = fieldID
	}
	_, err := c.Emit(kind, nil, p)
	return err
}

func isConstraint(err error, name string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == name
}

type viewOut struct {
	ID       string          `json:"id"`
	TableID  string          `json:"table_id"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Config   json.RawMessage `json:"config"`
	Position int             `json:"position"`
}
type tableOut struct {
	ID             string    `json:"id"`
	BaseID         string    `json:"base_id"`
	Name           string    `json:"name"`
	Position       int       `json:"position"`
	PrimaryFieldID string    `json:"primary_field_id"`
	Fields         []Field   `json:"fields"`
	Views          []viewOut `json:"views"`
}

func loadTableOut(c *command.Ctx, id string) (tableOut, error) {
	var t tableOut
	var pf *string
	err := c.Tx.QueryRow(c.Context, `select id::text, base_id::text, name, position, primary_field_id::text from tables.tables
		where workspace_id=$1::uuid and id=$2::uuid and deleted_at is null`, c.WorkspaceID(), id).Scan(&t.ID, &t.BaseID, &t.Name, &t.Position, &pf)
	if err != nil {
		return t, command.DB(err)
	}
	if pf != nil {
		t.PrimaryFieldID = *pf
	}
	if t.Fields, err = loadFields(c, id); err != nil {
		return t, err
	}
	rows, err := c.Tx.Query(c.Context, `select id::text, table_id::text, name, type, config, position from tables.views
		where workspace_id=$1::uuid and table_id=$2::uuid and type in ('grid','kanban') order by position, name`, c.WorkspaceID(), id)
	if err != nil {
		return t, command.DB(err)
	}
	defer rows.Close()
	t.Views = []viewOut{}
	for rows.Next() {
		var v viewOut
		if err := rows.Scan(&v.ID, &v.TableID, &v.Name, &v.Type, &v.Config, &v.Position); err != nil {
			return t, command.DB(err)
		}
		t.Views = append(t.Views, v)
	}
	return t, command.DB(rows.Err())
}

func tableGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	t, err := loadTable(c, in.ID)
	if err != nil {
		return nil, command.ErrNotFound("table")
	}
	if err := need(c, t.BaseID, RoleRead); err != nil {
		return nil, err
	}
	return loadTableOut(c, in.ID)
}

type fieldNew struct {
	Name     string          `json:"name"`
	Type     FieldType       `json:"type"`
	Config   json.RawMessage `json:"config"`
	Position *int            `json:"position"`
}

func primaryOK(t FieldType) bool {
	return t == Text || t == Number || t == Phone || t == Email || t == Autonumber
}

// createTable inserts a table, its fields (the first is the primary one; none means a text field "Name") and, unless
// the caller brings its own grid views, a default grid view.
func createTable(c *command.Ctx, b Base, name string, fields []fieldNew, defaultView bool) (string, error) {
	plan, err := workspacePlan(c.Context, c.Tx, c.WorkspaceID())
	if err != nil {
		return "", err
	}
	var n int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.tables where workspace_id=$1::uuid and base_id=$2::uuid and deleted_at is null`, c.WorkspaceID(), b.ID).Scan(&n); err != nil {
		return "", command.DB(err)
	}
	if max := limitsFor(plan).TablesPerBase; n >= max {
		return "", command.ErrConflict("This base has reached " + itoa(max) + " tables on the " + planName(plan) + " plan")
	}
	if len(fields) == 0 {
		fields = []fieldNew{{Name: "Name", Type: Text}}
	}
	if len(fields) > fieldsPerTable {
		return "", command.ErrInvalid("a table has at most " + itoa(fieldsPerTable) + " fields")
	}
	if !primaryOK(fields[0].Type) {
		return "", command.ErrInvalid("the first field names each record, so it must be text, number, phone, email or autonumber")
	}
	var tid string
	if err := c.Tx.QueryRow(c.Context, `insert into tables.tables(workspace_id, base_id, name, position)
		values ($1::uuid,$2::uuid,$3,(select coalesce(max(position)+1,0) from tables.tables where base_id=$2::uuid and deleted_at is null)) returning id::text`,
		c.WorkspaceID(), b.ID, name).Scan(&tid); err != nil {
		return "", command.DB(err)
	}
	tbl := Table{ID: tid, WorkspaceID: c.WorkspaceID(), BaseID: b.ID, Name: name}
	for i, f := range fields {
		if f.Position == nil {
			p := i
			f.Position = &p
		}
		nf, err := insertField(c, tbl, f, false)
		if err != nil {
			return "", err
		}
		if i == 0 {
			if _, err := c.Tx.Exec(c.Context, `update tables.tables set primary_field_id=$2::uuid where id=$1::uuid`, tid, nf.ID); err != nil {
				return "", command.DB(err)
			}
		}
	}
	if defaultView {
		if err := insertView(c, tid, "Grid view", "grid", json.RawMessage(`{"v":1}`), 0); err != nil {
			return "", err
		}
	}
	return tid, nil
}

func insertView(c *command.Ctx, tableID, name, typ string, cfg json.RawMessage, pos int) error {
	_, err := c.Tx.Exec(c.Context, `insert into tables.views(workspace_id, table_id, name, type, config, position, created_by)
		values ($1::uuid,$2::uuid,$3,$4,$5,$6,$7::uuid)`, c.WorkspaceID(), tableID, name, typ, cfg, pos, c.Principal.UserID)
	return command.DB(err)
}

func tableCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		BaseID string `json:"base_id"`
		Name   string
		Fields []fieldNew
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("base_id", in.BaseID); err != nil {
		return nil, err
	}
	name, err := cleanName("table", in.Name, 80)
	if err != nil {
		return nil, err
	}
	b, err := loadBase(c, in.BaseID)
	if err != nil {
		return nil, command.ErrNotFound("base")
	}
	if err := need(c, b.ID, RoleWrite); err != nil {
		return nil, err
	}
	for _, f := range in.Fields {
		if f.Type == Link {
			return nil, command.ErrInvalid("add link fields after the table exists, with field.create")
		}
	}
	tid, err := createTable(c, b, name, in.Fields, true)
	if err != nil {
		return nil, err
	}
	if err := emitStruct(c, "table.created", b.ID, tid, ""); err != nil {
		return nil, err
	}
	return loadTableOut(c, tid)
}

func tableUpdate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID       string
		Name     *string
		Position *int
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	t, err := loadTable(c, in.ID)
	if err != nil {
		return nil, command.ErrNotFound("table")
	}
	if err := need(c, t.BaseID, RoleWrite); err != nil {
		return nil, err
	}
	name := t.Name
	if in.Name != nil {
		if name, err = cleanName("table", *in.Name, 80); err != nil {
			return nil, err
		}
	}
	if _, err := c.Tx.Exec(c.Context, `update tables.tables set name=$2, position=coalesce($3,position) where id=$1::uuid`, t.ID, name, in.Position); err != nil {
		return nil, command.DB(err)
	}
	if err := emitStruct(c, "table.updated", t.BaseID, t.ID, ""); err != nil {
		return nil, err
	}
	return loadTableOut(c, t.ID)
}

func tableDelete(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	t, err := loadTable(c, in.ID)
	if err != nil {
		return nil, command.ErrNotFound("table")
	}
	if err := need(c, t.BaseID, RoleManage); err != nil {
		return nil, err
	}
	var by *string
	err = c.Tx.QueryRow(c.Context, `select t.name from tables.fields f join tables.tables t on t.workspace_id=f.workspace_id and t.id=f.table_id
		where f.workspace_id=$1::uuid and f.type='link' and f.deleted_at is null and t.deleted_at is null and t.id::text<>$2 and f.config->>'table_id'=$2
		limit 1`, c.WorkspaceID(), t.ID).Scan(&by)
	if err == nil {
		return nil, command.ErrConflict("The table " + *by + " has a link field to this table. Delete that field first")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, `update tables.tables set deleted_at=now() where id=$1::uuid`, t.ID); err != nil {
		return nil, command.DB(err)
	}
	// bases.record_count is the live records of live tables: a trashed table gives its records back (restore takes
	// them again; the purge then has nothing left to count)
	if err := bumpTableRecords(c, t.BaseID, t.ID, -1); err != nil {
		return nil, err
	}
	return map[string]any{}, emitStruct(c, "table.deleted", t.BaseID, t.ID, "")
}

func tableRestore(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	var baseID string
	err := c.Tx.QueryRow(c.Context, `select t.base_id::text from tables.tables t join tables.bases b on b.workspace_id=t.workspace_id and b.id=t.base_id
		where t.workspace_id=$1::uuid and t.id=$2::uuid and t.deleted_at is not null and b.deleted_at is null`, c.WorkspaceID(), in.ID).Scan(&baseID)
	if err != nil {
		return nil, command.ErrNotFound("table in the trash")
	}
	if err := need(c, baseID, RoleManage); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, `update tables.tables set deleted_at=null where id=$1::uuid`, in.ID); err != nil {
		return nil, command.DB(err)
	}
	if err := bumpTableRecords(c, baseID, in.ID, 1); err != nil { // refused when the base is at its cap
		return nil, err
	}
	return map[string]any{}, emitStruct(c, "table.restored", baseID, in.ID, "")
}

// bumpTableRecords adds (sign 1) or removes (sign -1) the live records of one table to the base's record_count.
func bumpTableRecords(c *command.Ctx, baseID, tableID string, sign int) error {
	var n int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and deleted_at is null`,
		c.WorkspaceID(), tableID).Scan(&n); err != nil {
		return command.DB(err)
	}
	if n == 0 {
		return nil
	}
	return bumpRecordCount(c, baseID, sign*n)
}
