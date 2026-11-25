// Package importers ports the command surface of crates/cnote-importers: import.start, import.status, import.list.
// The import.run job (the actual upsert) stays in the Rust worker; Go enqueues it with the same payload.
// import.start counts records with the same parsers the import.run job uses (internal/importers), including the
// live Jira fetch. Only what serde would reject as a request (wrong field types, unknown source, malformed mapping)
// returns ErrFallback.
package importers

import (
	"encoding/json"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/importers"
	"github.com/cnote/api/internal/modules/notes"
)

func Commands() []command.Command {
	start := command.NewCommand("import.start", command.GroupWriteNotes, importStart)
	status := command.NewCommand("import.status", command.GroupRead, importStatus)
	list := command.NewCommand("import.list", command.GroupRead, importList)
	for _, c := range []*command.Command{&start, &status, &list} {
		c.MinRole = command.Admin
	}
	setup := command.Exists("setup.import_exists", "select 1 from importers.jobs where workspace_id = $1::uuid and kind <> 'notes' and status = 'done'")
	return append([]command.Command{start, status, list, setup}, dataCommands()...)
}

var sources = map[string]bool{"jira": true, "confluence": true, "notion": true, "trello": true, "csv": true}

var csvKeys = []string{"target", "id", "title", "description", "status", "priority", "item_type", "assignee_email", "labels", "parent", "url"}

func importStart(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	source, err := o.ReqStr("source")
	if err != nil {
		return nil, err
	}
	space, err := o.ReqUUID("space_id")
	if err != nil {
		return nil, err
	}
	content, err := o.OptStr("content")
	if err != nil {
		return nil, err
	}
	batch, err := o.OptInt64("batch_size")
	if err != nil {
		return nil, err
	}
	live, err := jiraLive(o)
	if err != nil {
		return nil, err
	}
	if !sources[source] || (batch != nil && (*batch < 0 || *batch > 1<<32-1)) {
		return nil, command.ErrFallback
	}
	mapping, err := csvMapping(o)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, space, true); err != nil {
		return nil, err
	}
	var text string
	switch {
	case live != nil && source == "jira":
		text, err = importers.FetchIssues(c.Context, importers.DefaultJira, live[0], live[1], live[2], live[3])
		if err != nil {
			return nil, command.ErrInvalid("Jira fetch failed: " + err.Error())
		}
	case content != nil:
		text = *content
	default:
		return nil, command.ErrInvalid("provide `content` (or `jira` for a live Jira import)")
	}
	stored := map[string]any{"content": text, "mapping": mapping, "batch_size": batch}
	if mapping == nil {
		stored["mapping"] = nil
	}
	b, err := command.Marshal(stored)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	records, err := importers.CountRecords(source, b)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	if records == 0 {
		return nil, command.ErrInvalid("nothing to import: the content has no records")
	}
	var id string
	if err := c.Tx.QueryRow(c.Context,
		"insert into importers.jobs (workspace_id, source, space_id, input, created_by) values ($1::uuid,$2,$3::uuid,$4,$5::uuid) returning id::text",
		c.WorkspaceID(), source, space, json.RawMessage(b), c.Principal.UserID).Scan(&id); err != nil {
		return nil, err
	}
	payload, _ := command.Marshal(map[string]string{"import_id": id})
	if _, err := c.Tx.Exec(c.Context, "insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,$2,$3,now())",
		c.WorkspaceID(), "import.run", json.RawMessage(payload)); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"id": id, "status": "queued", "records": records}, nil
}

// csvMapping is Option<CsvMapping>: title is required, every other field an optional string, unknown keys ignored.
func csvMapping(o notes.Obj) (map[string]*string, error) {
	if !o.Has("mapping") {
		return nil, nil
	}
	raw, err := o.OptValue("mapping")
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrFallback
	}
	out := map[string]*string{}
	for _, k := range csvKeys {
		out[k] = nil
		if v, ok := m[k]; ok && string(v) != "null" {
			var s string
			if json.Unmarshal(v, &s) != nil {
				return nil, command.ErrFallback
			}
			out[k] = &s
		}
	}
	if out["title"] == nil {
		return nil, command.ErrFallback
	}
	return out, nil
}

// jiraLive is Option<JiraLive>: base_url, email, api_token and jql, all required strings.
func jiraLive(o notes.Obj) (*[4]string, error) {
	raw, err := o.OptValue("jira")
	if err != nil || raw == nil || string(raw) == "null" {
		return nil, err
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrFallback
	}
	var out [4]string
	for i, k := range []string{"base_url", "email", "api_token", "jql"} {
		v, ok := m[k]
		if !ok || json.Unmarshal(v, &out[i]) != nil || string(v) == "null" {
			return nil, command.ErrFallback
		}
	}
	return &out, nil
}

func importStatus(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("id")
	if err != nil {
		return nil, err
	}
	if err := utc(c); err != nil {
		return nil, err
	}
	var raw []byte
	err = c.Tx.QueryRow(c.Context, "select (to_jsonb(j) - 'input')::text from importers.jobs j where id = $1::uuid and kind = 'notes'", id).Scan(&raw)
	if err != nil {
		if command.DB(err).(*command.Error).Code == command.NotFound {
			return nil, command.ErrNotFound("import")
		}
		return nil, err
	}
	return notes.NormValue(raw)
}

func importList(c *command.Ctx, input json.RawMessage) (any, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil || m == nil {
		return nil, command.ErrFallback
	}
	if err := utc(c); err != nil {
		return nil, err
	}
	var raw []byte
	if err := c.Tx.QueryRow(c.Context,
		`select (coalesce(jsonb_agg(to_jsonb(j) - 'input' order by j.created_at desc), '[]'::jsonb))::text
		 from (select * from importers.jobs where kind = 'notes' order by created_at desc limit 50) j`).Scan(&raw); err != nil {
		return nil, err
	}
	return notes.NormValue(raw)
}

// utc: Rust's sqlx sessions run in UTC and jsonb renders timestamps in the session time zone.
func utc(c *command.Ctx) error {
	_, err := c.Tx.Exec(c.Context, "set local timezone = 'UTC'")
	return err
}
