package tables

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cnote/api/internal/command"
)

// Base templates (design 10) are data: templates/<key>.json, embedded. Format, with names where ids do not exist yet:
//
//	{key, name, description, tables: [{name, fields: [{name, type, config?}], views: [{name, type, config}], records: [{field name: value}]}]}
//
// The first field of a table is its primary field. A link field's config is {table: "<table name>", multiple, inverse?}.
// View configs name fields in sorts[].field, filters.rules[].field, group (a name, or {field}), kanban.stack,
// kanban.card_fields, order and hidden. Record values are stored values (select option ids, currency in paise, dates
// YYYY-MM-DD); a link value is a list of the target table's primary values. Computed fields carry no values.

//go:embed templates/*.json
var templateFS embed.FS

type tmField struct {
	Name   string          `json:"name"`
	Type   FieldType       `json:"type"`
	Config json.RawMessage `json:"config"`
}
type tmView struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}
type tmTable struct {
	Name    string           `json:"name"`
	Fields  []tmField        `json:"fields"`
	Views   []tmView         `json:"views"`
	Records []map[string]any `json:"records"`
}
type template struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Tables      []tmTable `json:"tables"`
}

func parseTemplate(b []byte) (*template, error) {
	var t template
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func allTemplates() ([]*template, error) {
	files, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, command.DB(err)
	}
	var out []*template
	for _, f := range files {
		b, _ := templateFS.ReadFile("templates/" + f.Name())
		t, err := parseTemplate(b)
		if err != nil {
			return nil, command.NewError(command.Internal, "template "+f.Name()+" is unreadable")
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func findTemplate(key string) (*template, error) {
	all, err := allTemplates()
	if err != nil {
		return nil, err
	}
	for _, t := range all {
		if t.Key == key {
			return t, nil
		}
	}
	return nil, command.ErrInvalid("unknown template " + key + " (see table_template.list)")
}

func templateList(c *command.Ctx, _ json.RawMessage) (any, error) {
	all, err := allTemplates()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, t := range all {
		names := []string{}
		for _, tb := range t.Tables {
			names = append(names, tb.Name)
		}
		out = append(out, map[string]any{"key": t.Key, "name": t.Name, "description": t.Description, "tables": names})
	}
	return out, nil
}

type pendingLink struct {
	table, recordID, field string
	targets                []any
}

// instantiate builds a template's tables, fields, views, sample records and links in the caller's transaction.
func instantiate(c *command.Ctx, b Base, t *template) error {
	bad := func(format string, a ...any) error {
		return command.ErrInvalid("template " + t.Key + ": " + fmt.Sprintf(format, a...))
	}
	tableIDs := map[string]string{}
	fields := map[string]map[string]Field{} // table name -> field name -> field
	for _, tb := range t.Tables {
		tableIDs[tb.Name] = ""
	}
	// pass 1: tables with their non-link fields, positioned as written
	for _, tb := range t.Tables {
		if len(tb.Fields) == 0 || tb.Fields[0].Type == Link {
			return bad("table %s needs a non-link first field", tb.Name)
		}
		var fs []fieldNew
		for i, f := range tb.Fields {
			if f.Type != Link {
				pos := i
				fs = append(fs, fieldNew{Name: f.Name, Type: f.Type, Config: f.Config, Position: &pos})
			}
		}
		hasGrid := false
		for _, v := range tb.Views {
			hasGrid = hasGrid || v.Type == "grid"
		}
		id, err := createTable(c, b, tb.Name, fs, !hasGrid)
		if err != nil {
			return err
		}
		tableIDs[tb.Name] = id
		if fields[tb.Name], err = fieldsByName(c, id); err != nil {
			return err
		}
	}
	// pass 2: link fields
	for _, tb := range t.Tables {
		tbl := Table{ID: tableIDs[tb.Name], WorkspaceID: c.WorkspaceID(), BaseID: b.ID, Name: tb.Name}
		for i, f := range tb.Fields {
			if f.Type != Link {
				continue
			}
			var lc struct {
				Table    string `json:"table"`
				Multiple bool   `json:"multiple"`
				Inverse  bool   `json:"inverse"`
			}
			if json.Unmarshal(f.Config, &lc) != nil || tableIDs[lc.Table] == "" {
				return bad("%s.%s links to an unknown table", tb.Name, f.Name)
			}
			cfg, _ := json.Marshal(LinkConfig{TableID: tableIDs[lc.Table], Multiple: lc.Multiple})
			pos := i
			nf, err := insertField(c, tbl, fieldNew{Name: f.Name, Type: Link, Config: cfg, Position: &pos}, lc.Inverse)
			if err != nil {
				return err
			}
			fields[tb.Name][f.Name] = nf
		}
	}
	// records, then links
	primary := map[string]map[string]string{} // table name -> primary value -> record id
	var links []pendingLink
	total := 0
	for _, tb := range t.Tables {
		tid, fs := tableIDs[tb.Name], fields[tb.Name]
		if len(tb.Records) == 0 {
			continue
		}
		if err := checkRecordLimit(c.Context, c.Tx, b.ID, tid, len(tb.Records)); err != nil {
			return err
		}
		primary[tb.Name] = map[string]string{}
		for i, rec := range tb.Records {
			data := map[string]any{}
			var pend []pendingLink
			for k, v := range rec {
				f, ok := fs[k]
				if !ok {
					return bad("%s has no field %s", tb.Name, k)
				}
				if f.Type == Link {
					list, _ := v.([]any)
					pend = append(pend, pendingLink{table: tb.Name, field: k, targets: list})
					continue
				}
				sv, err := Validate(f, v)
				if err != nil {
					return bad("%s record %d: %v", tb.Name, i+1, err)
				}
				if sv != nil {
					data[f.ID] = sv
				}
			}
			body, _ := json.Marshal(data)
			var rid string
			if err := c.Tx.QueryRow(c.Context, `insert into tables.records(workspace_id, table_id, seq, data, created_by, updated_by)
				values ($1::uuid,$2::uuid,$3,$4,$5::uuid,$5::uuid) returning id::text`, c.WorkspaceID(), tid, i+1, json.RawMessage(body), c.Principal.UserID).Scan(&rid); err != nil {
				return command.DB(err)
			}
			if pv, ok := rec[tb.Fields[0].Name]; ok {
				if _, dup := primary[tb.Name][fmt.Sprint(pv)]; !dup {
					primary[tb.Name][fmt.Sprint(pv)] = rid
				}
			}
			for _, p := range pend {
				p.recordID = rid
				links = append(links, p)
			}
		}
		total += len(tb.Records)
		if _, err := c.Tx.Exec(c.Context, `update tables.tables set next_seq=$2 where id=$1::uuid`, tid, len(tb.Records)+1); err != nil {
			return command.DB(err)
		}
		if _, err := c.Tx.Exec(c.Context, `update tables.bases set record_count=record_count+$2 where id=$1::uuid`, b.ID, len(tb.Records)); err != nil {
			return command.DB(err)
		}
	}
	for _, l := range links {
		f := fields[l.table][l.field]
		target := ""
		for _, tb := range t.Tables {
			if tableIDs[tb.Name] == cfgOf[LinkConfig](f).TableID {
				target = tb.Name
			}
		}
		for _, v := range l.targets {
			to, ok := primary[target][fmt.Sprint(v)]
			if !ok {
				return bad("%s.%s: no %v in %s", l.table, l.field, v, target)
			}
			if _, err := c.Tx.Exec(c.Context, `insert into tables.links(workspace_id, base_id, field_id, from_record, to_record)
				values ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid) on conflict do nothing`, c.WorkspaceID(), b.ID, f.ID, l.recordID, to); err != nil {
				return command.DB(err)
			}
		}
	}
	// views
	for _, tb := range t.Tables {
		ids := map[string]string{}
		for n, f := range fields[tb.Name] {
			ids[n] = f.ID
		}
		hasGrid := false
		for _, v := range tb.Views {
			hasGrid = hasGrid || v.Type == "grid"
		}
		for i, v := range tb.Views {
			if !hasGrid {
				i++ // the default grid view keeps position 0
			}
			if v.Type != "grid" && v.Type != "kanban" {
				return bad("%s view %s: type %s", tb.Name, v.Name, v.Type)
			}
			cfg := v.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			if _, ok := cfg["v"]; !ok {
				cfg["v"] = 1
			}
			if err := mapViewConfig(cfg, ids); err != nil {
				return bad("%s view %s: %v", tb.Name, v.Name, err)
			}
			body, _ := json.Marshal(cfg)
			if err := insertView(c, tableIDs[tb.Name], v.Name, v.Type, body, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func fieldsByName(c *command.Ctx, tableID string) (map[string]Field, error) {
	fs, err := loadFields(c, tableID)
	m := map[string]Field{}
	for _, f := range fs {
		m[f.Name] = f
	}
	return m, err
}

// mapViewConfig rewrites field names in a view config to field ids.
func mapViewConfig(cfg map[string]any, ids map[string]string) error {
	id := func(v any) (string, error) {
		s, _ := v.(string)
		if ids[s] == "" {
			return "", fmt.Errorf("no field named %q", s)
		}
		return ids[s], nil
	}
	var err error
	rewrite := func(m map[string]any, key string) { // m[key] is a field name
		if err == nil && m[key] != nil {
			m[key], err = id(m[key])
		}
	}
	list := func(m map[string]any, key string) {
		l, _ := m[key].([]any)
		for i := range l {
			if err == nil {
				l[i], err = id(l[i])
			}
		}
	}
	if f, ok := cfg["filters"].(map[string]any); ok {
		rs, _ := f["rules"].([]any)
		for _, r := range rs {
			if rm, ok := r.(map[string]any); ok {
				rewrite(rm, "field")
			}
		}
	}
	ss, _ := cfg["sorts"].([]any)
	for _, s := range ss {
		if sm, ok := s.(map[string]any); ok {
			rewrite(sm, "field")
		}
	}
	switch g := cfg["group"].(type) {
	case string:
		var gid string
		if gid, err = id(g); err == nil {
			cfg["group"] = map[string]any{"field": gid}
		}
	case map[string]any:
		rewrite(g, "field")
	}
	if k, ok := cfg["kanban"].(map[string]any); ok {
		rewrite(k, "stack")
		list(k, "card_fields")
	}
	list(cfg, "order")
	list(cfg, "hidden")
	if w, ok := cfg["widths"].(map[string]any); ok {
		nw := map[string]any{}
		for n, px := range w {
			var wid string
			if wid, err = id(n); err != nil {
				break
			}
			nw[wid] = px
		}
		cfg["widths"] = nw
	}
	return err
}
