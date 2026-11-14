// Package importers ports the command surface of crates/cnote-importers: import.start, import.status, import.list.
// The import.run job (the actual upsert) stays in the Rust worker; Go enqueues it with the same payload.
// import.start parses just enough to count records. Everything it cannot count exactly like Rust (Jira, HTML pages,
// a live Jira fetch, malformed JSON or CSV, BOM, lone CR) returns ErrFallback so Rust answers with its own text.
package importers

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"
	"unicode"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

func Commands() []command.Command {
	start := command.NewCommand("import.start", command.GroupWriteNotes, importStart)
	status := command.NewCommand("import.status", command.GroupRead, importStatus)
	list := command.NewCommand("import.list", command.GroupRead, importList)
	for _, c := range []*command.Command{&start, &status, &list} {
		c.MinRole = command.Admin
	}
	return []command.Command{start, status, list}
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
	if !sources[source] || o.Has("jira") || (batch != nil && (*batch < 0 || *batch > 1<<32-1)) {
		return nil, command.ErrFallback
	}
	mapping, err := csvMapping(o)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, space, true); err != nil {
		return nil, err
	}
	if content == nil {
		return nil, command.ErrInvalid("provide `content` (or `jira` for a live Jira import)")
	}
	records, err := count(source, *content, mapping)
	if err != nil {
		return nil, err
	}
	if records == 0 {
		return nil, command.ErrInvalid("nothing to import: the content has no records")
	}
	stored := map[string]any{"content": *content, "mapping": mapping, "batch_size": batch}
	if mapping == nil {
		stored["mapping"] = nil
	}
	b, err := command.Marshal(stored)
	if err != nil {
		return nil, command.ErrInvalid(err.Error())
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
			if json.Unmarshal(v, &s) != nil || strings.ContainsRune(s, '�') {
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

func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Other_Alphabetic, r)
}

func nonEmptyStr(v any) bool { s, ok := v.(string); return ok && s != "" }

func field(v any, k string) any { m, _ := v.(map[string]any); return m[k] }

func parseJSON(s string) (any, error) {
	depth, max := 0, 0
	for i := 0; i < len(s); i++ { // serde_json stops at depth 128; Go does not
		switch s[i] {
		case '[', '{':
			if depth++; depth > max {
				max = depth
			}
		case ']', '}':
			depth--
		}
	}
	var v any
	if max > 100 || json.Unmarshal([]byte(s), &v) != nil {
		return nil, command.ErrFallback
	}
	return v, nil
}

func count(source, content string, m map[string]*string) (int, error) {
	switch source {
	case "trello":
		b, err := parseJSON(content)
		if err != nil {
			return 0, err
		}
		cards, _ := field(b, "cards").([]any)
		n := 0
		for _, c := range cards {
			if nonEmptyStr(field(c, "id")) && nonEmptyStr(field(c, "name")) && field(c, "closed") != true {
				n++
			}
		}
		return n, nil
	case "notion", "confluence":
		return countPages(content)
	case "csv":
		return countCSV(content, m)
	}
	return 0, command.ErrFallback // jira
}

func countPages(content string) (int, error) {
	t := strings.TrimLeftFunc(content, unicode.IsSpace)
	switch {
	case strings.HasPrefix(t, "{") || strings.HasPrefix(t, "["):
		v, err := parseJSON(content)
		if err != nil {
			return 0, err
		}
		arr, ok := v.([]any)
		if !ok {
			if arr, ok = field(v, "pages").([]any); !ok {
				return 0, command.ErrFallback
			}
		}
		n := 0
		for _, p := range arr {
			if nonEmptyStr(field(p, "id")) && nonEmptyStr(field(p, "title")) {
				n++
			}
		}
		return n, nil
	case strings.HasPrefix(t, "<"):
		return 0, command.ErrFallback // HTML title extraction stays in Rust
	}
	title := "Untitled"
	for _, l := range strings.Split(t, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSuffix(l, "\r"), "# "); ok {
			title = strings.TrimSpace(rest)
			break
		}
	}
	if strings.IndexFunc(title, isAlnum) >= 0 { // slug(title) is the page id and must not be empty
		return 1, nil
	}
	return 0, nil
}

func countCSV(content string, m map[string]*string) (int, error) {
	if m == nil || strings.HasPrefix(content, "\uFEFF") || strings.Contains(strings.ReplaceAll(content, "\r\n", ""), "\r") {
		return 0, command.ErrFallback
	}
	if t := m["target"]; t != nil && *t != "items" && *t != "pages" {
		return 0, command.ErrFallback
	}
	r := csv.NewReader(strings.NewReader(content))
	r.FieldsPerRecord = -1
	headers, err := r.Read()
	if err != nil {
		return 0, command.ErrFallback
	}
	pos := func(name string) (int, bool) {
		for i, h := range headers {
			if h == name {
				return i, true
			}
		}
		return 0, false
	}
	for _, k := range csvKeys {
		if m[k] != nil {
			if _, ok := pos(*m[k]); !ok {
				return 0, command.ErrFallback // "mapped column ... is not in the CSV header"
			}
		}
	}
	tc, _ := pos(*m["title"])
	n := 0
	for {
		row, err := r.Read()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return 0, command.ErrFallback
		}
		if tc < len(row) && strings.TrimSpace(row[tc]) != "" {
			n++
		}
	}
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
	err = c.Tx.QueryRow(c.Context, "select (to_jsonb(j) - 'input')::text from importers.jobs j where id = $1::uuid", id).Scan(&raw)
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
		 from (select * from importers.jobs order by created_at desc limit 50) j`).Scan(&raw); err != nil {
		return nil, err
	}
	return notes.NormValue(raw)
}

// utc: Rust's sqlx sessions run in UTC and jsonb renders timestamps in the session time zone.
func utc(c *command.Ctx) error {
	_, err := c.Tx.Exec(c.Context, "set local timezone = 'UTC'")
	return err
}
