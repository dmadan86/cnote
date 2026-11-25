package tables

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
)

func init() {
	handlers["table_view.create"] = viewCreate
	handlers["table_view.update"] = viewUpdate
	handlers["table_view.delete"] = viewDelete
}

// Views are shared by everyone who can open the base. Writers create, change and delete them (design 5, 17.1).
// Field ids inside a config are not checked: a view may name a field that was deleted, and readers ignore it.

func viewConfig(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{"v":1}`), nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, command.ErrInvalid("config must be an object")
	}
	if len(raw) > 16384 {
		return nil, command.ErrInvalid("the view settings are too large (16 KB at most)")
	}
	return raw, nil
}

func loadView(c *command.Ctx, id string) (viewOut, error) {
	var v viewOut
	err := c.Tx.QueryRow(c.Context, `select id::text, table_id::text, name, type, config, position from tables.views
		where workspace_id=$1::uuid and id=$2::uuid and type in ('grid','kanban')`, c.WorkspaceID(), id).Scan(&v.ID, &v.TableID, &v.Name, &v.Type, &v.Config, &v.Position)
	return v, command.DB(err)
}

func viewCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		TableID string          `json:"table_id"`
		Name    string          `json:"name"`
		Type    string          `json:"type"`
		Config  json.RawMessage `json:"config"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("table_id", in.TableID); err != nil {
		return nil, err
	}
	if in.Type != "grid" && in.Type != "kanban" {
		return nil, command.ErrInvalid("type must be grid or kanban")
	}
	name, err := cleanName("view", in.Name, 80)
	if err != nil {
		return nil, err
	}
	cfg, err := viewConfig(in.Config)
	if err != nil {
		return nil, err
	}
	tc, err := openTable(c, in.TableID, RoleWrite)
	if err != nil {
		return nil, err
	}
	var n int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.views where workspace_id=$1::uuid and table_id=$2::uuid`, c.WorkspaceID(), tc.Table.ID).Scan(&n); err != nil {
		return nil, command.DB(err)
	}
	if n >= viewsPerTable {
		return nil, command.ErrConflict("A table can have 20 views. Delete one first")
	}
	var v viewOut
	err = c.Tx.QueryRow(c.Context, `insert into tables.views(workspace_id, table_id, name, type, config, position, created_by)
		values ($1::uuid,$2::uuid,$3,$4,$5,(select coalesce(max(position)+1,0) from tables.views where table_id=$2::uuid),$6::uuid)
		returning id::text, table_id::text, name, type, config, position`, c.WorkspaceID(), tc.Table.ID, name, in.Type, cfg, c.Principal.UserID).
		Scan(&v.ID, &v.TableID, &v.Name, &v.Type, &v.Config, &v.Position)
	return v, command.DB(err)
}

func viewUpdate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID       string          `json:"id"`
		Name     *string         `json:"name"`
		Config   json.RawMessage `json:"config"`
		Position *int            `json:"position"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	v, err := loadView(c, in.ID)
	if err != nil {
		return nil, command.ErrNotFound("view")
	}
	if _, err := openTable(c, v.TableID, RoleWrite); err != nil {
		return nil, err
	}
	if in.Name != nil {
		if v.Name, err = cleanName("view", *in.Name, 80); err != nil {
			return nil, err
		}
	}
	if len(in.Config) > 0 {
		if v.Config, err = viewConfig(in.Config); err != nil {
			return nil, err
		}
	}
	if in.Position != nil {
		v.Position = max(*in.Position, 0)
	}
	_, err = c.Tx.Exec(c.Context, `update tables.views set name=$3, config=$4, position=$5 where workspace_id=$1::uuid and id=$2::uuid`,
		c.WorkspaceID(), v.ID, v.Name, v.Config, v.Position)
	return v, command.DB(err)
}

func viewDelete(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("id", in.ID); err != nil {
		return nil, err
	}
	v, err := loadView(c, in.ID)
	if err != nil {
		return nil, command.ErrNotFound("view")
	}
	if _, err := openTable(c, v.TableID, RoleWrite); err != nil {
		return nil, err
	}
	if v.Type == "grid" {
		var n int
		if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.views where workspace_id=$1::uuid and table_id=$2::uuid and type='grid'`, c.WorkspaceID(), v.TableID).Scan(&n); err != nil {
			return nil, command.DB(err)
		}
		if n <= 1 {
			return nil, command.ErrConflict("The last grid view of a table cannot be deleted")
		}
	}
	_, err = c.Tx.Exec(c.Context, `delete from tables.views where workspace_id=$1::uuid and id=$2::uuid`, c.WorkspaceID(), v.ID)
	return map[string]any{}, command.DB(err)
}
