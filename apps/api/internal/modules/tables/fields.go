package tables

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func init() {
	handlers["field.create"] = fieldCreate
	handlers["field.update"] = fieldUpdate
	handlers["field.delete"] = fieldDelete
	handlers["field.restore"] = fieldRestore
}

const optionLimit = 200

// normalizeConfig validates a field's config and returns the JSON to store. old is the stored field on an update.
func normalizeConfig(c *command.Ctx, tbl Table, typ FieldType, raw json.RawMessage, old *Field) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, command.ErrInvalid("config must be an object")
	}
	var out any
	switch typ {
	case Number, Percent, Currency:
		cfg := NumberConfig{}
		if typ == Currency {
			cfg.Decimals = 2
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, command.ErrInvalid("decimals must be a whole number")
		}
		if (typ == Currency && cfg.Decimals != 0 && cfg.Decimals != 2) || cfg.Decimals < 0 || cfg.Decimals > 4 {
			return nil, command.ErrInvalid("decimals must be 0 to 4 (0 or 2 for currency)")
		}
		out = cfg
	case SingleSelect, MultiSelect:
		var cfg SelectConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, command.ErrInvalid("options must be a list of {id, name, color}")
		}
		opts, err := normalizeOptions(cfg.Options)
		if err != nil {
			return nil, err
		}
		out = SelectConfig{Options: opts}
	case User:
		var cfg UserConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, command.ErrInvalid("multiple must be true or false")
		}
		out = cfg
	case Link:
		var cfg LinkConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, command.ErrInvalid("invalid link config")
		}
		if old != nil { // the target and the inverse never change
			prev := cfgOf[LinkConfig](*old)
			cfg.TableID, cfg.InverseOf = prev.TableID, prev.InverseOf
		} else {
			cfg.InverseOf = ""
			if err := needID("config.table_id", cfg.TableID); err != nil {
				return nil, err
			}
			var n int
			if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.tables where workspace_id=$1::uuid and id=$2::uuid and base_id=$3::uuid and deleted_at is null`,
				c.WorkspaceID(), cfg.TableID, tbl.BaseID).Scan(&n); err != nil {
				return nil, command.DB(err)
			}
			if n == 0 {
				return nil, command.ErrInvalid("a link field can only point to a table in the same base")
			}
		}
		out = cfg
	case Autonumber:
		var cfg AutonumberConfig
		if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Prefix) > 20 {
			return nil, command.ErrInvalid("prefix is at most 20 characters")
		}
		out = cfg
	default:
		out = struct{}{}
	}
	b, err := json.Marshal(out)
	return b, err
}

// normalizeOptions trims names, checks they are unique, and gives new options the next free id (o1, o2...).
func normalizeOptions(in []Option) ([]Option, error) {
	if len(in) > optionLimit {
		return nil, command.ErrInvalid("at most " + itoa(optionLimit) + " options")
	}
	used, names, max := map[string]bool{}, map[string]bool{}, 0
	for _, o := range in {
		used[o.ID] = true
		if n, err := strconv.Atoi(strings.TrimPrefix(o.ID, "o")); err == nil && strings.HasPrefix(o.ID, "o") && n > max {
			max = n
		}
	}
	out := make([]Option, 0, len(in))
	seen := map[string]bool{}
	for _, o := range in {
		o.Name = strings.TrimSpace(o.Name)
		if o.Name == "" || len([]rune(o.Name)) > 100 {
			return nil, command.ErrInvalid("an option name must be 1 to 100 characters")
		}
		if k := strings.ToLower(o.Name); names[k] {
			return nil, command.ErrInvalid("two options are named " + o.Name)
		} else {
			names[k] = true
		}
		if o.ID == "" {
			for max++; used["o"+strconv.Itoa(max)]; max++ {
			}
			o.ID = "o" + strconv.Itoa(max)
			used[o.ID] = true
		} else if seen[o.ID] || len(o.ID) > 20 || strings.ContainsAny(o.ID, " ,\"") {
			return nil, command.ErrInvalid("option ids must be unique and simple")
		}
		seen[o.ID] = true
		if o.Color == "" {
			o.Color = "gray"
		} else if len(o.Color) > 20 {
			return nil, command.ErrInvalid("option color is at most 20 characters")
		}
		out = append(out, o)
	}
	return out, nil
}

func uniqueFieldName(c *command.Ctx, tableID, want string) (string, error) {
	for i := 1; ; i++ {
		name := want
		if i > 1 {
			name = fmt.Sprintf("%s (%d)", want, i)
		}
		var n int
		if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.fields where table_id=$1::uuid and deleted_at is null and lower(name)=lower($2)`, tableID, name).Scan(&n); err != nil {
			return "", command.DB(err)
		}
		if n == 0 {
			return name, nil
		}
	}
}

func nameTaken(err error, name string) error {
	if isConstraint(err, "fields_name_unique") {
		return command.ErrConflict("A field named " + name + " already exists in this table")
	}
	return command.DB(err)
}

// insertField adds one field. A link with inverse also adds the reverse field in the target table.
func insertField(c *command.Ctx, tbl Table, f fieldNew, inverse bool) (Field, error) {
	return insertFieldOf(c, tbl, f, inverse, "")
}

func insertFieldOf(c *command.Ctx, tbl Table, f fieldNew, inverse bool, inverseOf string) (Field, error) {
	name, err := cleanName("field", f.Name, 80)
	if err != nil {
		return Field{}, err
	}
	known := false
	for _, t := range AllFieldTypes {
		known = known || t == f.Type
	}
	if !known {
		return Field{}, command.ErrInvalid("unknown field type " + string(f.Type))
	}
	if f.Type == Attachment {
		return Field{}, command.ErrInvalid("attachment fields are not available yet")
	}
	cfg, err := normalizeConfig(c, tbl, f.Type, f.Config, nil)
	if err != nil {
		return Field{}, err
	}
	if inverseOf != "" {
		lc := cfgOf[LinkConfig](Field{Config: cfg})
		lc.InverseOf = inverseOf
		cfg, _ = json.Marshal(lc)
	}
	var n int
	var dup bool
	if err := c.Tx.QueryRow(c.Context, `select count(*), coalesce(bool_or(lower(name)=lower($3)),false) from tables.fields where workspace_id=$1::uuid and table_id=$2::uuid and deleted_at is null`,
		c.WorkspaceID(), tbl.ID, name).Scan(&n, &dup); err != nil {
		return Field{}, command.DB(err)
	}
	if dup { // the unique index still decides races
		return Field{}, nameTaken(&pgconn.PgError{Code: "23505", ConstraintName: "fields_name_unique"}, name)
	}
	if n >= fieldsPerTable {
		return Field{}, command.ErrConflict("A table can have at most " + itoa(fieldsPerTable) + " fields")
	}
	nf := Field{WorkspaceID: c.WorkspaceID(), TableID: tbl.ID, Name: name, Type: f.Type, Config: cfg}
	err = c.Tx.QueryRow(c.Context, `insert into tables.fields(workspace_id, table_id, name, type, config, position)
		values ($1::uuid,$2::uuid,$3,$4,$5,coalesce($6,(select coalesce(max(position)+1,0) from tables.fields where table_id=$2::uuid and deleted_at is null)))
		returning id::text, position, locked`, c.WorkspaceID(), tbl.ID, name, f.Type, cfg, f.Position).Scan(&nf.ID, &nf.Position, &nf.Locked)
	if err != nil {
		return Field{}, nameTaken(err, name)
	}
	if inverse && f.Type == Link {
		target := cfgOf[LinkConfig](nf).TableID
		rev, err := uniqueFieldName(c, target, tbl.Name)
		if err != nil {
			return Field{}, err
		}
		rc, _ := json.Marshal(LinkConfig{TableID: tbl.ID, Multiple: true})
		if _, err := insertFieldOf(c, Table{ID: target, BaseID: tbl.BaseID}, fieldNew{Name: rev, Type: Link, Config: rc}, false, nf.ID); err != nil {
			return Field{}, err
		}
	}
	return nf, nil
}

func fieldCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		fieldNew
		TableID string `json:"table_id"`
		Inverse bool
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("table_id", in.TableID); err != nil {
		return nil, err
	}
	tbl, err := loadTable(c, in.TableID)
	if err != nil {
		return nil, command.ErrNotFound("table")
	}
	if err := need(c, tbl.BaseID, RoleWrite); err != nil {
		return nil, err
	}
	f, err := insertField(c, tbl, in.fieldNew, in.Inverse)
	if err != nil {
		return nil, err
	}
	if err := emitStruct(c, "field.created", tbl.BaseID, tbl.ID, f.ID); err != nil {
		return nil, err
	}
	return f, nil
}

func loadField(c *command.Ctx, id string, deleted bool) (Field, Table, error) {
	var f Field
	var t Table
	err := c.Tx.QueryRow(c.Context, `select f.id::text, f.workspace_id::text, f.table_id::text, f.name, f.type, f.config, f.position, f.locked, t.base_id::text, t.name
		from tables.fields f join tables.tables t on t.workspace_id=f.workspace_id and t.id=f.table_id
		where f.workspace_id=$1::uuid and f.id=$2::uuid and (f.deleted_at is not null)=$3 and t.deleted_at is null`, c.WorkspaceID(), id, deleted).
		Scan(&f.ID, &f.WorkspaceID, &f.TableID, &f.Name, &f.Type, &f.Config, &f.Position, &f.Locked, &t.BaseID, &t.Name)
	t.ID, t.WorkspaceID = f.TableID, f.WorkspaceID
	if errors.Is(err, pgx.ErrNoRows) {
		return f, t, command.ErrNotFound("field")
	}
	return f, t, command.DB(err)
}

func fieldUpdate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID       string
		Name     *string
		Config   json.RawMessage
		Position *int
		Locked   *bool
		Type     *FieldType
		Confirm  bool
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	f, tbl, err := loadField(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	role, err := baseRole(c, tbl.BaseID)
	if err != nil {
		return nil, err
	}
	if err := atLeast(role, RoleWrite); err != nil {
		return nil, err
	}
	manage := func(what string) error {
		if role.AtLeast(RoleManage) {
			return nil
		}
		return command.ErrForbidden(what + " needs a space manager (workspace admin or space admin)")
	}
	hasConfig := len(in.Config) > 0 && string(in.Config) != "null"
	if (in.Locked != nil && *in.Locked != f.Locked) || (f.Locked && hasConfig) {
		if err := manage("Locking a field, or changing a locked field's settings,"); err != nil {
			return nil, err
		}
	}
	if in.Type != nil && *in.Type != f.Type {
		if err := manage("Changing a field's type"); err != nil {
			return nil, err
		}
		prev, err := convertField(c, f, tbl, *in.Type, in.Config, in.Confirm)
		if err != nil || prev != nil {
			if prev != nil {
				return prev, nil
			}
			return nil, err
		}
		if err := emitStruct(c, "field.converted", tbl.BaseID, tbl.ID, f.ID); err != nil {
			return nil, err
		}
		f, _, err = loadField(c, in.ID, false)
		if err != nil {
			return nil, err
		}
	} else if hasConfig {
		cfg, err := normalizeConfig(c, tbl, f.Type, in.Config, &f)
		if err != nil {
			return nil, err
		}
		if err := checkConfigChange(c, f, cfg, role); err != nil {
			return nil, err
		}
		f.Config = cfg
		if _, err := c.Tx.Exec(c.Context, `update tables.fields set config=$2 where id=$1::uuid`, f.ID, cfg); err != nil {
			return nil, command.DB(err)
		}
	}
	name := f.Name
	if in.Name != nil {
		if name, err = cleanName("field", *in.Name, 80); err != nil {
			return nil, err
		}
	}
	nf := f
	err = c.Tx.QueryRow(c.Context, `update tables.fields set name=$2, position=coalesce($3,position), locked=coalesce($4,locked) where id=$1::uuid
		returning name, position, locked`, f.ID, name, in.Position, in.Locked).Scan(&nf.Name, &nf.Position, &nf.Locked)
	if err != nil {
		return nil, nameTaken(err, name)
	}
	if err := emitStruct(c, "field.updated", tbl.BaseID, tbl.ID, f.ID); err != nil {
		return nil, err
	}
	return nf, nil
}

// checkConfigChange refuses config edits that would leave stored values invalid, and strips removed options.
func checkConfigChange(c *command.Ctx, f Field, cfg json.RawMessage, role Role) error {
	switch f.Type {
	case SingleSelect, MultiSelect:
		kept := map[string]bool{}
		for _, o := range cfgOf[SelectConfig](Field{Config: cfg}).Options {
			kept[o.ID] = true
		}
		var gone []string
		for _, o := range cfgOf[SelectConfig](f).Options {
			if !kept[o.ID] {
				gone = append(gone, o.ID)
			}
		}
		if len(gone) == 0 {
			return nil
		}
		var used int
		if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and
			(case when jsonb_typeof(data->$3)='array' then data->$3 ?| $4::text[] else data->>$3 = any($4::text[]) end)`,
			c.WorkspaceID(), f.TableID, f.ID, gone).Scan(&used); err != nil {
			return command.DB(err)
		}
		if used == 0 {
			return nil
		}
		if !role.AtLeast(RoleManage) {
			return command.ErrForbidden("Removing an option that records use needs a space manager (workspace admin or space admin)")
		}
		_, err := c.Tx.Exec(c.Context, `update tables.records set data = case
			when jsonb_typeof(data->$3)<>'array' then data - $3
			when not exists (select 1 from jsonb_array_elements_text(data->$3) e where e <> all($4::text[])) then data - $3
			else jsonb_set(data, array[$3::text], (select jsonb_agg(e) from jsonb_array_elements_text(data->$3) e where e <> all($4::text[]))) end
			where workspace_id=$1::uuid and table_id=$2::uuid and (case when jsonb_typeof(data->$3)='array' then data->$3 ?| $4::text[] else data->>$3 = any($4::text[]) end)`,
			c.WorkspaceID(), f.TableID, f.ID, gone)
		return command.DB(err)
	case Link:
		if cfgOf[LinkConfig](f).Multiple && !cfgOf[LinkConfig](Field{Config: cfg}).Multiple {
			// links are stored under the forward field: an inverse field reads them the other way round
			fid, col := f.ID, "from_record"
			if inv := cfgOf[LinkConfig](f).InverseOf; inv != "" {
				fid, col = inv, "to_record"
			}
			var n int
			if err := c.Tx.QueryRow(c.Context, `select count(*) from (select 1 from tables.links where workspace_id=$1::uuid and field_id=$2::uuid group by `+col+` having count(*)>1) x`,
				c.WorkspaceID(), fid).Scan(&n); err != nil {
				return command.DB(err)
			}
			if n > 0 {
				return command.ErrConflict("Some records have more than one link in this field. Remove the extra links first")
			}
		}
	case User:
		if cfgOf[UserConfig](f).Multiple && !cfgOf[UserConfig](Field{Config: cfg}).Multiple {
			var n int
			if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.records where workspace_id=$1::uuid and table_id=$2::uuid and jsonb_array_length(data->$3)>1`,
				c.WorkspaceID(), f.TableID, f.ID).Scan(&n); err != nil {
				return command.DB(err)
			}
			if n > 0 {
				return command.ErrConflict("Some records have more than one person in this field. Remove the extras first")
			}
		}
	}
	return nil
}

// linkPair marks a link field and its generated inverse deleted or restored together.
const pairWhere = `workspace_id=$1::uuid and type='link' and (id::text=$2 or config->>'inverse_of'=$2 or id::text=(select config->>'inverse_of' from tables.fields where id::text=$2))`

func fieldDelete(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	f, tbl, err := loadField(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	if err := need(c, tbl.BaseID, RoleManage); err != nil {
		return nil, err
	}
	var primary bool
	if err := c.Tx.QueryRow(c.Context, `select primary_field_id=$2::uuid from tables.tables where id=$1::uuid`, tbl.ID, f.ID).Scan(&primary); err != nil {
		return nil, command.DB(err)
	}
	if primary {
		return nil, command.ErrInvalid("The primary field names each record and cannot be deleted")
	}
	q := `update tables.fields set deleted_at=now() where workspace_id=$1::uuid and id=$2::uuid`
	if f.Type == Link {
		q = `update tables.fields set deleted_at=now() where deleted_at is null and ` + pairWhere
	}
	if _, err := c.Tx.Exec(c.Context, q, c.WorkspaceID(), f.ID); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{}, emitStruct(c, "field.deleted", tbl.BaseID, tbl.ID, f.ID)
}

func fieldRestore(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	f, tbl, err := loadField(c, in.ID, true)
	if err != nil {
		return nil, command.ErrNotFound("field in the trash")
	}
	if err := need(c, tbl.BaseID, RoleManage); err != nil {
		return nil, err
	}
	q := `update tables.fields set deleted_at=null where workspace_id=$1::uuid and id=$2::uuid`
	if f.Type == Link {
		q = `update tables.fields set deleted_at=null where deleted_at is not null and ` + pairWhere
	}
	if _, err := c.Tx.Exec(c.Context, q, c.WorkspaceID(), f.ID); err != nil {
		return nil, nameTaken(err, f.Name)
	}
	return map[string]any{}, emitStruct(c, "field.restored", tbl.BaseID, tbl.ID, f.ID)
}
