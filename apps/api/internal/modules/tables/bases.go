package tables

import (
	"encoding/json"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

func init() {
	handlers["base.list"] = baseList
	handlers["base.get"] = baseGet
	handlers["base.create"] = baseCreate
	handlers["base.update"] = baseUpdate
	handlers["base.delete"] = baseDelete
	handlers["base.restore"] = baseRestore
	handlers["table_template.list"] = templateList
}

type baseOut struct {
	ID          string    `json:"id"`
	SpaceID     string    `json:"space_id"`
	Name        string    `json:"name"`
	Icon        *string   `json:"icon"`
	Template    *string   `json:"template"`
	RecordCount int       `json:"record_count"`
	TableCount  int       `json:"table_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const baseCols = `b.id::text, b.space_id::text, b.name, b.icon, b.template, b.record_count,
	(select count(*) from tables.tables t where t.workspace_id=b.workspace_id and t.base_id=b.id and t.deleted_at is null)::int, b.created_at, b.updated_at`

func (b *baseOut) dest() []any {
	return []any{&b.ID, &b.SpaceID, &b.Name, &b.Icon, &b.Template, &b.RecordCount, &b.TableCount, &b.CreatedAt, &b.UpdatedAt}
}

func loadBaseOut(c *command.Ctx, id string) (baseOut, error) {
	var b baseOut
	err := c.Tx.QueryRow(c.Context, `select `+baseCols+` from tables.bases b where b.workspace_id=$1::uuid and b.id=$2::uuid and b.deleted_at is null`,
		c.WorkspaceID(), id).Scan(b.dest()...)
	return b, command.DB(err)
}

func baseList(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		SpaceID *string `json:"space_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if in.SpaceID != nil {
		if err := needID("space_id", *in.SpaceID); err != nil {
			return nil, err
		}
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select `+baseCols+` from tables.bases b where b.workspace_id=$1::uuid and b.deleted_at is null
		and b.space_id = any($2::text[]::uuid[]) and ($3::uuid is null or b.space_id=$3::uuid) order by b.name, b.id`, c.WorkspaceID(), spaces, in.SpaceID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []baseOut{}
	for rows.Next() {
		var b baseOut
		if err := rows.Scan(b.dest()...); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, b)
	}
	return out, command.DB(rows.Err())
}

func baseGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	role, err := baseRole(c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := atLeast(role, RoleRead); err != nil {
		return nil, err
	}
	b, err := loadBaseOut(c, in.ID)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select id::text from tables.tables where workspace_id=$1::uuid and base_id=$2::uuid and deleted_at is null order by position, name, id`, c.WorkspaceID(), in.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	tbls := []tableOut{}
	for _, id := range ids {
		t, err := loadTableOut(c, id)
		if err != nil {
			return nil, err
		}
		tbls = append(tbls, t)
	}
	plan, err := workspacePlan(c.Context, c.Tx, c.WorkspaceID())
	if err != nil {
		return nil, err
	}
	l := limitsFor(plan)
	var attBytes int64
	if err := c.Tx.QueryRow(c.Context, `select attachment_bytes from tables.bases where workspace_id=$1::uuid and id=$2::uuid`, c.WorkspaceID(), b.ID).Scan(&attBytes); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{
		"base": b, "tables": tbls, "role": role,
		"limits": map[string]int64{"records_per_base": int64(l.RecordsPerBase), "tables_per_base": int64(l.TablesPerBase), "fields_per_table": fieldsPerTable,
			"records_per_table": recordsPerTable, "views_per_table": viewsPerTable, "attachment_bytes_per_base": l.AttachmentBytesPerBase},
		"usage": map[string]int64{"records": int64(b.RecordCount), "tables": int64(b.TableCount), "attachment_bytes": attBytes},
	}, nil
}

func baseCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		SpaceID  string `json:"space_id"`
		Name     string
		Template string
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("space_id", in.SpaceID); err != nil {
		return nil, err
	}
	name, err := cleanName("base", in.Name, 120)
	if err != nil {
		return nil, err
	}
	var tpl *template
	if in.Template != "" {
		if tpl, err = findTemplate(in.Template); err != nil {
			return nil, err
		}
	}
	role, err := spaceRole(c, in.SpaceID)
	if err != nil {
		return nil, err
	}
	if role == RoleNone {
		return nil, command.ErrNotFound("space")
	}
	if err := atLeast(role, RoleWrite); err != nil {
		return nil, err
	}
	plan, err := workspacePlan(c.Context, c.Tx, c.WorkspaceID())
	if err != nil {
		return nil, err
	}
	var n int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.bases where workspace_id=$1::uuid and deleted_at is null`, c.WorkspaceID()).Scan(&n); err != nil {
		return nil, command.DB(err)
	}
	if max := limitsFor(plan).BasesPerWorkspace; n >= max {
		return nil, command.ErrConflict("This workspace has reached " + itoa(max) + " bases on the " + planName(plan) + " plan")
	}
	var tplKey *string
	if tpl != nil {
		tplKey = &tpl.Key
	}
	var id string
	if err := c.Tx.QueryRow(c.Context, `insert into tables.bases(workspace_id, space_id, name, template, created_by) values ($1::uuid,$2::uuid,$3,$4,$5::uuid) returning id::text`,
		c.WorkspaceID(), in.SpaceID, name, tplKey, c.Principal.UserID).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	if tpl != nil {
		if err := instantiate(c, Base{ID: id, WorkspaceID: c.WorkspaceID(), SpaceID: in.SpaceID, Name: name}, tpl); err != nil {
			return nil, err
		}
	}
	if err := emitStruct(c, "base.created", id, "", ""); err != nil {
		return nil, err
	}
	return loadBaseOut(c, id)
}

func baseUpdate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID   string
		Name *string
		Icon *string
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	if err := need(c, in.ID, RoleManage); err != nil {
		return nil, err
	}
	var name *string
	if in.Name != nil {
		n, err := cleanName("base", *in.Name, 120)
		if err != nil {
			return nil, err
		}
		name = &n
	}
	if in.Icon != nil && len(*in.Icon) > 40 {
		return nil, command.ErrInvalid("icon is at most 40 characters")
	}
	// an empty icon clears it
	if _, err := c.Tx.Exec(c.Context, `update tables.bases set name=coalesce($2,name), icon=case when $3::text is null then icon else nullif($3,'') end, updated_at=now() where id=$1::uuid`,
		in.ID, name, in.Icon); err != nil {
		return nil, command.DB(err)
	}
	if err := emitStruct(c, "base.updated", in.ID, "", ""); err != nil {
		return nil, err
	}
	return loadBaseOut(c, in.ID)
}

func baseDelete(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	if err := need(c, in.ID, RoleManage); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, `update tables.bases set deleted_at=now() where id=$1::uuid`, in.ID); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{}, emitStruct(c, "base.deleted", in.ID, "", "")
}

func baseRestore(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	var space string
	if err := c.Tx.QueryRow(c.Context, `select space_id::text from tables.bases where workspace_id=$1::uuid and id=$2::uuid and deleted_at is not null`,
		c.WorkspaceID(), in.ID).Scan(&space); err != nil {
		return nil, command.ErrNotFound("base in the trash")
	}
	role, err := spaceRole(c, space)
	if err != nil {
		return nil, err
	}
	if err := atLeast(role, RoleManage); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, `update tables.bases set deleted_at=null where id=$1::uuid`, in.ID); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{}, emitStruct(c, "base.restored", in.ID, "", "")
}
