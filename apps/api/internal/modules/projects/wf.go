package projects

// Per-Space workflow configuration (ADR-007/ADR-014), port of wf.rs. No schemes: a Space copies its template's
// workflow into `spaces.config` and may edit it. Every change bumps a version and snapshots it in
// `projects.workflows`; items keep their `workflow_version` until migrated.

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

type SpaceWf struct {
	Template string
	Config   map[string]json.RawMessage
	Def      *Definition
	Version  int32
}

// SpaceWF loads the Space's current workflow (falling back to its template's) and version.
func SpaceWF(c *command.Ctx, spaceID string) (*SpaceWf, error) {
	var template string
	var cfgRaw []byte
	err := c.Tx.QueryRow(c.Context, "select template, config from core.spaces where id = $1::uuid", spaceID).Scan(&template, &cfgRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("space")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	var cfg map[string]json.RawMessage
	_ = json.Unmarshal(cfgRaw, &cfg)
	wf := &SpaceWf{Template: template, Config: cfg, Version: 1}
	if w, ok := cfg["workflow"]; ok {
		wf.Def, _ = DecodeDefinition(w, false)
	}
	if wf.Def == nil {
		wf.Def = Template(template)
	}
	if v, ok := cfg["workflow_version"]; ok {
		if n, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64); err == nil {
			wf.Version = int32(n)
		}
	}
	return wf, nil
}

// DefinitionFor is the definition an item at version follows (the current one unless the item lags behind).
func DefinitionFor(c *command.Ctx, spaceID string, wf *SpaceWf, version int32) (*Definition, error) {
	if version == wf.Version {
		return wf.Def, nil
	}
	var raw []byte
	err := c.Tx.QueryRow(c.Context,
		"select definition from projects.workflows where space_id = $1::uuid and type_name = 'item' and version = $2::int4", spaceID, version).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return wf.Def, nil
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if d, ok := DecodeDefinition(raw, false); ok {
		return d, nil
	}
	return wf.Def, nil
}

func snapshot(c *command.Ctx, spaceID string, version int32, def *Definition) error {
	b, err := command.Marshal(def)
	if err != nil {
		return command.ErrInvalid(err.Error())
	}
	_, err = c.Tx.Exec(c.Context,
		`insert into projects.workflows (workspace_id, space_id, type_name, version, definition)
         values ($1::uuid,$2::uuid,'item',$3::int4,$4::jsonb) on conflict do nothing`,
		c.WorkspaceID(), spaceID, version, json.RawMessage(b))
	return command.DB(err)
}

// setWorkflow publishes def as the Space's next workflow version. Returns (version, items migrated).
func setWorkflow(c *command.Ctx, spaceID string, def *Definition, migrate bool) (int32, int64, error) {
	if err := def.Validate(); err != nil {
		return 0, 0, command.ErrInvalid(err.Error())
	}
	cur, err := SpaceWF(c, spaceID)
	if err != nil {
		return 0, 0, err
	}
	if err := snapshot(c, spaceID, cur.Version, cur.Def); err != nil { // keep the version items may still be on
		return 0, 0, err
	}
	version := cur.Version + 1
	if err := snapshot(c, spaceID, version, def); err != nil {
		return 0, 0, err
	}
	patch, err := command.Marshal(map[string]any{"workflow": def, "workflow_version": version})
	if err != nil {
		return 0, 0, command.ErrInvalid(err.Error())
	}
	if _, err := c.Tx.Exec(c.Context, "update core.spaces set config = config || $2::jsonb where id = $1::uuid", spaceID, json.RawMessage(patch)); err != nil {
		return 0, 0, command.DB(err)
	}
	var migrated int64
	if migrate {
		// Only items whose status still exists move; the rest stay on their version.
		sts, _ := command.Marshal(def.Statuses)
		tag, err := c.Tx.Exec(c.Context,
			`update projects.items i set workflow_version = $2::int4, status = s.name, status_category = s.category
             from jsonb_to_recordset($3::jsonb) as s(name text, category text)
             where i.space_id = $1::uuid and lower(i.status) = lower(s.name)`, spaceID, version, json.RawMessage(sts))
		if err != nil {
			return 0, 0, command.DB(err)
		}
		migrated = tag.RowsAffected()
	}
	if _, err := c.Emit("workflow.updated", nil, map[string]any{"space_id": spaceID, "version": version, "migrated_items": migrated}); err != nil {
		return 0, 0, err
	}
	return version, migrated, nil
}

func decodeSpaceRef(input json.RawMessage) (jobj, string, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, "", err
	}
	id, err := o.reqUUID("space_id")
	return o, id, err
}

func workflowGet(c *command.Ctx, input json.RawMessage) (any, error) {
	_, id, err := decodeSpaceRef(input)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, id, false); err != nil {
		return nil, err
	}
	wf, err := SpaceWF(c, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"space_id": id, "template": wf.Template, "version": wf.Version, "definition": wf.Def}, nil
}

func workflowUpdate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, id, err := decodeSpaceRef(input)
	if err != nil {
		return nil, err
	}
	dv, ok := o["definition"]
	if !ok {
		return nil, command.ErrFallback
	}
	def, ok := DecodeDefinition(dv, true)
	if !ok {
		return nil, command.ErrFallback
	}
	migrate, err := o.boolDef("migrate_items", false)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, id, true); err != nil {
		return nil, err
	}
	version, migrated, err := setWorkflow(c, id, def, migrate)
	if err != nil {
		return nil, err
	}
	return map[string]any{"version": version, "migrated_items": migrated}, nil
}

func spaceDrift(c *command.Ctx, input json.RawMessage) (any, error) {
	_, id, err := decodeSpaceRef(input)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, id, false); err != nil {
		return nil, err
	}
	wf, err := SpaceWF(c, id)
	if err != nil {
		return nil, err
	}
	diff := wf.Def.Diff(Template(wf.Template))
	return map[string]any{"template": wf.Template, "version": wf.Version, "in_sync": len(diff) == 0, "differences": diff}, nil
}

func spaceRealign(c *command.Ctx, input json.RawMessage) (any, error) {
	o, id, err := decodeSpaceRef(input)
	if err != nil {
		return nil, err
	}
	migrate, err := o.boolDef("migrate_items", false)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, id, true); err != nil {
		return nil, err
	}
	wf, err := SpaceWF(c, id)
	if err != nil {
		return nil, err
	}
	tpl := Template(wf.Template)
	if wf.Def.Equal(tpl) {
		return map[string]any{"changed": false, "version": wf.Version}, nil
	}
	version, migrated, err := setWorkflow(c, id, tpl, migrate)
	if err != nil {
		return nil, err
	}
	return map[string]any{"changed": true, "version": version, "migrated_items": migrated}, nil
}
