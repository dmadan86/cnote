package reports

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

const maxTiles = 24

type tile struct {
	ReportID string `json:"report_id"`
	Size     string `json:"size"`
}

type dashboard struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Tiles      json.RawMessage `json:"tiles"`
	Period     json.RawMessage `json:"period"`
	Visibility string          `json:"visibility"`
	CreatedBy  string          `json:"created_by"`
	OwnerName  string          `json:"owner_name"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	CanEdit    bool            `json:"can_edit"`
}

const dashCols = `d.id::text, d.name, d.tiles, d.period, d.visibility, d.created_by::text, u.name, d.created_at, d.updated_at`

func scanDash(c *command.Ctx, row pgx.Row) (dashboard, error) {
	var d dashboard
	if err := row.Scan(&d.ID, &d.Name, &d.Tiles, &d.Period, &d.Visibility, &d.CreatedBy, &d.OwnerName, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return d, err
	}
	d.CanEdit = d.CreatedBy == uidOf(c) || (c.Principal.Role >= command.Admin && d.Visibility == "workspace")
	return d, nil
}

func loadDash(c *command.Ctx, id string) (dashboard, error) {
	if !isUUID(id) {
		return dashboard{}, command.ErrNotFound("dashboard")
	}
	d, err := scanDash(c, c.Tx.QueryRow(c.Context, `select `+dashCols+` from core.dashboards d join core.users u on u.id = d.created_by
		where d.id = $1::uuid and d.workspace_id = $2::uuid`, id, c.WorkspaceID()))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !visible(c, d.CreatedBy, d.Visibility)) {
		return dashboard{}, command.ErrNotFound("dashboard")
	}
	return d, command.DB(err)
}

// dashboardOut is a dashboard with each tile marked available (the caller can see its report) and named.
func dashboardOut(c *command.Ctx, d dashboard) (map[string]any, error) {
	var tiles []tile
	_ = json.Unmarshal(d.Tiles, &tiles)
	ids := make([]string, 0, len(tiles))
	for _, t := range tiles {
		ids = append(ids, t.ReportID)
	}
	names := map[string]string{}
	rows, err := c.Tx.Query(c.Context, `select id::text, name from core.reports where workspace_id = $1::uuid and id = any($2::text[]::uuid[])
		and (visibility = 'workspace' or created_by = $3::uuid)`, c.WorkspaceID(), ids, nilIfEmpty(uidOf(c)))
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, command.DB(err)
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	out := make([]map[string]any, 0, len(tiles))
	for _, t := range tiles {
		m := map[string]any{"report_id": t.ReportID, "size": t.Size, "available": false}
		if n, ok := names[t.ReportID]; ok {
			m["available"], m["name"] = true, n
		}
		out = append(out, m)
	}
	return map[string]any{"id": d.ID, "name": d.Name, "tiles": out, "period": d.Period, "visibility": d.Visibility, "created_by": d.CreatedBy,
		"owner_name": d.OwnerName, "created_at": d.CreatedAt, "updated_at": d.UpdatedAt, "can_edit": d.CanEdit}, nil
}

func dashboardGet(c *command.Ctx, input json.RawMessage) (any, error) {
	var in idIn
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	d, err := loadDash(c, in.ID)
	if err != nil {
		return nil, err
	}
	o, err := dashboardOut(c, d)
	return map[string]any{"dashboard": o}, err
}

func dashboardList(c *command.Ctx, input json.RawMessage) (any, error) {
	rows, err := c.Tx.Query(c.Context, `select `+dashCols+` from core.dashboards d join core.users u on u.id = d.created_by
		where d.workspace_id = $1::uuid and (d.visibility = 'workspace' or d.created_by = $2::uuid) order by d.updated_at desc, d.id`, c.WorkspaceID(), nilIfEmpty(uidOf(c)))
	if err != nil {
		return nil, command.DB(err)
	}
	var ds []dashboard
	for rows.Next() {
		d, err := scanDash(c, rows)
		if err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		ds = append(ds, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	out := []map[string]any{}
	for _, d := range ds {
		o, err := dashboardOut(c, d)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return map[string]any{"dashboards": out}, nil
}

func dashboardSave(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		Tiles      []tile  `json:"tiles"`
		Period     *Period `json:"period"`
		Visibility string  `json:"visibility"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	if err := needPerson(c); err != nil {
		return nil, err
	}
	name, err := checkName(in.Name)
	if err != nil {
		return nil, err
	}
	vis, err := checkVisibility(in.Visibility)
	if err != nil {
		return nil, err
	}
	if len(in.Tiles) > maxTiles {
		return nil, command.ErrInvalid("a dashboard holds at most 24 tiles")
	}
	var ids []string
	for _, t := range in.Tiles {
		if !slices.Contains([]string{"number", "half", "full"}, t.Size) {
			return nil, command.ErrInvalid("tile size is number, half or full")
		}
		if !isUUID(t.ReportID) {
			return nil, command.ErrInvalid("tile report_id must be a uuid")
		}
		if !slices.Contains(ids, t.ReportID) {
			ids = append(ids, t.ReportID)
		}
	}
	if len(ids) > 0 {
		var n int
		if err := c.Tx.QueryRow(c.Context, "select count(*) from core.reports where workspace_id = $1::uuid and id = any($2::text[]::uuid[]) and (visibility = 'workspace' or created_by = $3::uuid)", c.WorkspaceID(), ids, nilIfEmpty(uidOf(c))).Scan(&n); err != nil {
			return nil, command.DB(err)
		}
		if n != len(ids) {
			return nil, command.ErrInvalid("a tile points at a report that does not exist")
		}
	}
	var period []byte
	if in.Period != nil {
		env, err := buildEnv(c)
		if err != nil {
			return nil, err
		}
		if _, _, _, err := ResolvePeriod(*in.Period, env.FYStartMonth, env.Today); err != nil {
			return nil, command.ErrInvalid("period: " + err.Error())
		}
		period, _ = json.Marshal(in.Period)
	}
	tiles, _ := json.Marshal(append([]tile{}, in.Tiles...))
	id := in.ID
	if id == "" {
		err = c.Tx.QueryRow(c.Context, `insert into core.dashboards (workspace_id, name, tiles, period, visibility, created_by)
			values ($1::uuid, $2, $3, $4, $5, $6::uuid) returning id::text`, c.WorkspaceID(), name, tiles, period, vis, uidOf(c)).Scan(&id)
		if err != nil {
			return nil, command.DB(err)
		}
	} else {
		old, err := loadDash(c, id)
		if err != nil {
			return nil, err
		}
		if !old.CanEdit {
			return nil, command.ErrForbidden("only the owner or an admin can change this dashboard")
		}
		if _, err := c.Tx.Exec(c.Context, `update core.dashboards set name=$2, tiles=$3, period=$4, visibility=$5, updated_at=now() where id=$1::uuid`,
			id, name, tiles, period, vis); err != nil {
			return nil, command.DB(err)
		}
	}
	d, err := loadDash(c, id)
	if err != nil {
		return nil, err
	}
	o, err := dashboardOut(c, d)
	return map[string]any{"dashboard": o}, err
}

func dashboardDelete(c *command.Ctx, input json.RawMessage) (any, error) {
	var in idIn
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	d, err := loadDash(c, in.ID)
	if err != nil {
		return nil, err
	}
	if !d.CanEdit {
		return nil, command.ErrForbidden("only the owner or an admin can delete this dashboard")
	}
	if _, err := c.Tx.Exec(c.Context, "delete from core.dashboards where id = $1::uuid", d.ID); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"ok": true}, nil
}
