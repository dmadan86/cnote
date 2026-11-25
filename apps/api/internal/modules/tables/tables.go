package tables

import (
	"encoding/json"
	"strings"

	"github.com/cnote/api/internal/command"
)

// The Phase 1a command contract (design sections 9 and 17). Every command has an input schema, an output schema
// (OutputSchema) and a description that also says what it returns, because MCP shows only description and input.
// Handlers are stubs until packages 1 and 2 replace them; swap the handler in `handlers` by name.
//
// Left out on purpose (1b): table_form.*, record.attach|detach|file, table_import.upload (an HTTP endpoint, 17.6),
// table_import.undo (no undo in 1a, 17.4).
//
// Roles: every command keeps NewCommand's workspace minimum (Guest for read, Member for the rest). The base role
// (baseRole) is checked in the handler: read, write (space editor, any member of an open space) or manage
// (workspace admin or space admin). "manage" is noted per command below.

// ---- schema builders ----

const (
	sID   = `{"type":"string","description":"UUID"}`
	sStr  = `{"type":"string"}`
	sInt  = `{"type":"integer"}`
	sBool = `{"type":"boolean"}`
	sTS   = `{"type":"string","description":"RFC 3339 timestamp"}`
	sAny  = `{}`
	sObj  = `{"type":"object"}`
)

func nullable(s string) string { return `{"anyOf":[` + s + `,{"type":"null"}]}` }
func arr(item string) string   { return `{"type":"array","items":` + item + `}` }
func arrMax(item string, max int) string {
	return `{"type":"array","maxItems":` + itoa(max) + `,"items":` + item + `}`
}
func described(s, d string) string { // adds a description to an object schema fragment
	b, _ := json.Marshal(d)
	return strings.Replace(s, "{", `{"description":`+string(b)+",", 1)
}
func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
func enum(vals ...string) string {
	b, _ := json.Marshal(vals)
	return `{"type":"string","enum":` + string(b) + `}`
}

// o builds an object schema from required names and alternating name, schema arguments.
func o(required []string, kv ...string) string {
	var p []string
	for i := 0; i < len(kv); i += 2 {
		p = append(p, `"`+kv[i]+`":`+kv[i+1])
	}
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return `{"type":"object","properties":{` + strings.Join(p, ",") + `},"required":` + string(req) + `}`
}

var (
	sRole  = enum("read", "write", "manage")
	sField = o([]string{"id", "table_id", "name", "type", "config", "position", "locked"},
		"id", sID, "table_id", sID, "name", sStr, "type", enum(typeNames()...), "config", sObj, "position", sInt, "locked", sBool)
	sViewCfg = described(sObj, "Shared view settings: {v:1, filters:{match,rules}, sorts:[{field,desc}], group:{field}|null, order:[field ids], hidden:[field ids], widths:{field id: px}, row_height:'short'|'medium'|'tall', kanban:{stack: single_select field id, card_fields:[field ids]}}. At most 16 KB")
	sView    = o([]string{"id", "table_id", "name", "type", "config", "position"},
		"id", sID, "table_id", sID, "name", sStr, "type", enum("grid", "kanban"), "config", sViewCfg, "position", sInt)
	sTable = o([]string{"id", "base_id", "name", "position", "primary_field_id", "fields", "views"},
		"id", sID, "base_id", sID, "name", sStr, "position", sInt, "primary_field_id", sID, "fields", arr(sField), "views", arr(sView))
	sBase = o([]string{"id", "space_id", "name", "icon", "template", "record_count", "table_count", "created_at", "updated_at"},
		"id", sID, "space_id", sID, "name", sStr, "icon", nullable(sStr), "template", nullable(sStr), "record_count", sInt, "table_count", sInt,
		"created_at", sTS, "updated_at", sTS)
	sLinkChip = o([]string{"id", "title"}, "id", sID, "title", sStr)
	sLinks    = described(`{"type":"object","additionalProperties":`+arr(sLinkChip)+`}`, "Link field id to the linked records, newest links last")
	sRecord   = o([]string{"id", "table_id", "seq", "data", "created_at", "updated_at", "created_by", "updated_by"},
		"id", sID, "table_id", sID, "seq", sInt, "data", described(sObj, "Field id to stored value. An empty cell is absent, never null. Link and computed fields are not in data"),
		"created_at", sTS, "updated_at", sTS, "created_by", nullable(sID), "updated_by", nullable(sID))
	sRecordLinks = o([]string{"id", "table_id", "seq", "data", "created_at", "updated_at", "created_by", "updated_by", "links"},
		"id", sID, "table_id", sID, "seq", sInt, "data", sObj, "created_at", sTS, "updated_at", sTS, "created_by", nullable(sID), "updated_by", nullable(sID), "links", sLinks)
	sValues   = described(sObj, "Field id to value, as stored (see field types). Computed fields (autonumber, created/modified, link) are refused; a locked field needs manage")
	sLinkIn   = described(`{"type":"object","additionalProperties":`+arr(sID)+`}`, "Link field id to the full list of linked record ids; replaces that field's links")
	sEmpty    = o(nil)
	sCount    = o([]string{"count"}, "count", sInt)
	sBatchOut = o([]string{"count", "batch_id"}, "count", sInt, "batch_id", nullable(sID))
	sCellErr  = o([]string{"row", "col", "reason"}, "row", sInt, "col", sInt, "reason", sStr)
	sJob      = o([]string{"id", "status", "counts"}, "id", sID, "status", enum("queued", "running", "done", "failed"),
		"counts", o([]string{"created", "skipped", "failed"}, "created", sInt, "skipped", sInt, "failed", sInt), "error", nullable(sStr))
	sFieldNew = o([]string{"name", "type"}, "name", sStr, "type", enum(typeNames()...), "config", sObj)
)

func typeNames() []string {
	n := make([]string, len(AllFieldTypes))
	for i, t := range AllFieldTypes {
		n[i] = string(t)
	}
	return n
}

// ---- the command list ----

type spec struct {
	name, desc, in, out string
	group               command.Group
	confirm             bool
}

var specs = []spec{
	{"base.list", "List the bases the caller can read, optionally of one space. Returns an array of bases, each with table_count and record_count.",
		o(nil, "space_id", sID), arr(sBase), command.GroupRead, false},
	{"base.get", "Get a base with every table, each table's fields and views, the caller's role in it (read, write, manage) and the plan limits and usage. Returns {base, tables, role, limits, usage}.",
		o([]string{"id"}, "id", sID),
		o([]string{"base", "tables", "role", "limits", "usage"}, "base", sBase, "tables", arr(sTable), "role", sRole,
			"limits", o([]string{"records_per_base", "tables_per_base", "fields_per_table", "records_per_table", "views_per_table"},
				"records_per_base", sInt, "tables_per_base", sInt, "fields_per_table", sInt, "records_per_table", sInt, "views_per_table", sInt),
			"usage", o([]string{"records", "tables"}, "records", sInt, "tables", sInt)),
		command.GroupRead, false},
	{"base.create", "Create a base in a space, optionally from a template key (see table_template.list). Needs write access to the space. Returns the base.",
		o([]string{"space_id", "name"}, "space_id", sID, "name", sStr, "template", sStr), sBase, command.GroupAdmin, false},
	{"base.update", "Rename a base or change its icon. Needs manage on the base. Returns the base.",
		o([]string{"id"}, "id", sID, "name", sStr, "icon", sStr), sBase, command.GroupAdmin, false},
	{"base.delete", "Move a base to the trash; it can be restored for 30 days. Needs manage and confirmation. Returns {}.",
		o([]string{"id"}, "id", sID), sEmpty, command.GroupDestructive, true},
	{"base.restore", "Restore a base from the trash. Needs manage. Returns {}.",
		o([]string{"id"}, "id", sID), sEmpty, command.GroupAdmin, true},
	{"table_template.list", "List the built-in base templates usable as base.create template. Returns [{key, name, description, tables: [table names]}].",
		o(nil), arr(o([]string{"key", "name", "description", "tables"}, "key", sStr, "name", sStr, "description", sStr, "tables", arr(sStr))),
		command.GroupRead, false},
	{"table.get", "Get one table with its fields and views. Returns the table.",
		o([]string{"id"}, "id", sID), sTable, command.GroupRead, false},
	{"table.create", "Add a table to a base with an optional list of starting fields. The first field becomes the primary field (text, number, phone, email or autonumber); without fields a text primary field named Name is created. Also creates a default grid view. Needs write access. Returns the table.",
		o([]string{"base_id", "name"}, "base_id", sID, "name", sStr, "fields", arr(sFieldNew)), sTable, command.GroupAdmin, false},
	{"table.update", "Rename or reorder a table. Needs write access. Returns the table.",
		o([]string{"id"}, "id", sID, "name", sStr, "position", sInt), sTable, command.GroupAdmin, false},
	{"table.delete", "Move a table to the trash (restorable for 30 days). Refused while another table has a link field to it. Needs manage and confirmation. Returns {}.",
		o([]string{"id"}, "id", sID), sEmpty, command.GroupDestructive, true},
	{"table.restore", "Restore a table from the trash. Needs manage. Returns {}.",
		o([]string{"id"}, "id", sID), sEmpty, command.GroupAdmin, true},
	{"field.create", "Add a field. Types: text, long_text, number, percent, currency (integer paise), date (YYYY-MM-DD), checkbox, single_select, multi_select (config.options [{id,name,color}]), phone, email, url, user, link (config.table_id of a table in the same base; inverse true also creates the reverse field), autonumber (config.prefix), created_time, modified_time, created_by, modified_by. Needs write access. Returns the field.",
		o([]string{"table_id", "name", "type"}, "table_id", sID, "name", sStr, "type", enum(typeNames()...), "config", sObj, "position", sInt, "inverse", sBool),
		sField, command.GroupAdmin, false},
	{"field.update", "Rename a field, change its config, position or lock (locking and changing config of a locked field need manage). Passing a new type changes nothing and returns a preview {kept, changed, cleared, samples:[{seq,from,to}]} unless confirm is true, then converts. Only lossless conversions are allowed; any other type change is refused. Type changes need manage. Returns the field, or the preview.",
		o([]string{"id"}, "id", sID, "name", sStr, "config", sObj, "position", sInt, "locked", sBool, "type", enum(typeNames()...), "confirm", sBool),
		`{"anyOf":[` + sField + `,` + o([]string{"kept", "changed", "cleared", "samples"}, "kept", sInt, "changed", sInt, "cleared", sInt,
			"samples", arrMax(o([]string{"seq", "from", "to"}, "seq", sInt, "from", sAny, "to", sAny), 5)) + `]}`,
		command.GroupAdmin, false},
	{"field.delete", "Move a field to the trash (restorable for 30 days; its values and links are kept until the purge). The primary field cannot be deleted. Needs manage. Returns {}.",
		o([]string{"id"}, "id", sID), sEmpty, command.GroupAdmin, false},
	{"field.restore", "Restore a field from the trash. Needs manage. Returns {}.",
		o([]string{"id"}, "id", sID), sEmpty, command.GroupAdmin, false},
	{"record.list", "List records of a table with filters, sorts, grouping and search, using a view's settings when view_id is given. A filter is {match:'and'|'or', rules:[{field, op, value}]} with at most 10 rules; up to 3 sorts [{field, desc}]. Keyset pagination: pass the previous next_cursor; limit 1-200 (default 100). Returns {records, links: {record id: {link field id: [{id,title}]}}, groups?: [{value,count}], next_cursor: string|null, total}.",
		o([]string{"table_id"}, "table_id", sID, "view_id", sID, "filters", sObj, "sorts", arrMax(o([]string{"field"}, "field", sID, "desc", sBool), 3),
			"group", o([]string{"field"}, "field", sID), "search", sStr, "cursor", sStr, "limit", `{"type":"integer","minimum":1,"maximum":200}`),
		o([]string{"records", "links", "next_cursor", "total"}, "records", arr(sRecord), "links", sObj,
			"groups", arr(o([]string{"value", "count"}, "value", sAny, "count", sInt)), "next_cursor", nullable(sStr), "total", sInt),
		command.GroupRead, false},
	{"record.get", "Get one record with its link chips. Returns the record with links {link field id: [{id,title}]}.",
		o([]string{"id"}, "id", sID), sRecordLinks, command.GroupRead, false},
	{"record.create", "Create a record. values maps field id to value; omitted fields stay empty. links sets link fields. Needs write access. Returns the record.",
		o([]string{"table_id", "values"}, "table_id", sID, "values", sValues, "links", sLinkIn), sRecordLinks, command.GroupWriteItems, false},
	{"record.create_many", "Create up to 500 records in one transaction (all or nothing); the request body is at most 2 MB. Use this for imports from an agent. Above 100 records one record.batch event is emitted; fetch its ids with record.batch_list. Needs write access. Returns {count, ids, batch_id: string|null}.",
		o([]string{"table_id", "records"}, "table_id", sID, "records", arrMax(o([]string{"values"}, "values", sValues, "links", sLinkIn), 500)),
		o([]string{"count", "ids", "batch_id"}, "count", sInt, "ids", arr(sID), "batch_id", nullable(sID)), command.GroupWriteItems, false},
	{"record.update", "Update a record: values sets fields, clear lists field ids to empty, links replaces link fields. Needs write access. Returns the record.",
		o([]string{"id"}, "id", sID, "values", sValues, "clear", arr(sID), "links", sLinkIn), sRecordLinks, command.GroupWriteItems, false},
	{"record.bulk_update", "Update up to 1,000 records of one table in one transaction. Needs write access. Returns {updated}.",
		o([]string{"table_id", "updates"}, "table_id", sID, "updates", arrMax(o([]string{"id"}, "id", sID, "values", sValues, "clear", arr(sID)), 1000)),
		o([]string{"updated"}, "updated", sInt), command.GroupWriteItems, false},
	{"record.paste", "Apply a block of text cells (what a spreadsheet copies) onto fields: field_ids are the target columns in order and each row is {id?, cells: [string]}. Rows with an id update that record; rows without create one. Cells that cannot be converted, or that target locked or computed fields, are skipped and reported. At most 1,000 rows by 50 columns. Needs write access. Returns {updated, created, errors: [{row, col, reason}]} (first 50 errors).",
		o([]string{"table_id", "field_ids", "rows"}, "table_id", sID, "field_ids", arrMax(sID, 50),
			"rows", arrMax(o([]string{"cells"}, "id", sID, "cells", arrMax(sStr, 50)), 1000)),
		o([]string{"updated", "created", "errors"}, "updated", sInt, "created", sInt, "errors", arrMax(sCellErr, 50)), command.GroupWriteItems, false},
	{"record.delete", "Move up to 1,000 records to the trash (restorable for 30 days). All ids must be in bases the caller can write; otherwise nothing is deleted. Returns {count, batch_id: string|null}.",
		o([]string{"ids"}, "ids", arrMax(sID, 1000)), sBatchOut, command.GroupWriteItems, false},
	{"record.restore", "Restore up to 1,000 records from the trash. Same access rule as record.delete. Returns {count, batch_id: string|null}.",
		o([]string{"ids"}, "ids", arrMax(sID, 1000)), sBatchOut, command.GroupWriteItems, false},
	{"record.search", "Find records by their primary value, for a link picker. Returns [{id, title, preview: [two values]}], at most 20.",
		o([]string{"table_id", "q"}, "table_id", sID, "q", sStr, "limit", `{"type":"integer","minimum":1,"maximum":20}`),
		arr(o([]string{"id", "title", "preview"}, "id", sID, "title", sStr, "preview", arrMax(sStr, 2))), command.GroupRead, false},
	{"record.history", "The last 100 changes of a record, newest first. Returns [{id, actor_type, actor_id, at, change: {field id: [old, new]}}].",
		o([]string{"id"}, "id", sID),
		arrMax(o([]string{"id", "actor_type", "actor_id", "at", "change"}, "id", sInt, "actor_type", sStr, "actor_id", nullable(sID), "at", sTS, "change", sObj), 100),
		command.GroupRead, false},
	{"record.batch_list", "List the record ids behind a record.batch event (more than 100 records changed at once). Only records the caller may read are returned. Returns {op, table_id, ids}.",
		o([]string{"batch_id"}, "batch_id", sID),
		o([]string{"op", "table_id", "ids"}, "op", enum("created", "updated", "deleted", "restored"), "table_id", sID, "ids", arr(sID)), command.GroupRead, false},
	{"table_view.create", "Create a grid or kanban view of a table. Needs write access. Returns the view.",
		o([]string{"table_id", "name", "type"}, "table_id", sID, "name", sStr, "type", enum("grid", "kanban"), "config", sViewCfg), sView, command.GroupWriteItems, false},
	{"table_view.update", "Rename a view, change its shared settings or position. Needs write access. Returns the view.",
		o([]string{"id"}, "id", sID, "name", sStr, "config", sViewCfg, "position", sInt), sView, command.GroupWriteItems, false},
	{"table_view.delete", "Delete a view. The last grid view of a table cannot be deleted. Needs write access. Returns {}.",
		o([]string{"id"}, "id", sID), sEmpty, command.GroupWriteItems, false},
	{"table_import.preview", "Preview a CSV import. handle comes from the upload endpoint POST /api/tables/import (CSV, 10 MB, 5,000 rows). mapping optionally sets, per column index, an existing field_id, a new_field {name, type, config?} or skip. Returns {columns: [{index, header, samples, field_id|null, new_field|null}], ready, needs_fixing, errors: [{row, col, reason}]}. Needs write access to the base (writers add fields).",
		o([]string{"handle"}, "handle", sStr, "mapping", sObj),
		o([]string{"columns", "ready", "needs_fixing", "errors"}, "columns", arr(sObj), "ready", sInt, "needs_fixing", sInt, "errors", arrMax(sCellErr, 50)),
		command.GroupAdmin, false},
	{"table_import.run", "Import the ready rows of an uploaded CSV. Idempotent per handle: running the same handle again returns the same job. No undo; imported records can be selected and deleted. Needs write access to the base (writers add fields). Returns {id, status, counts: {created, skipped, failed}, error}.",
		o([]string{"handle"}, "handle", sStr, "mapping", sObj), sJob, command.GroupAdmin, false},
	{"table_import.status", "Status of an import job. Returns {id, status: queued|running|done|failed, counts, error}.",
		o([]string{"id"}, "id", sID), sJob, command.GroupRead, false},
}

// handlers is where packages 1 and 2 register real implementations, from an init() in their own files.
var handlers = map[string]command.Handler{}

func notImplemented(name string) command.Handler {
	return func(*command.Ctx, json.RawMessage) (any, error) {
		return nil, command.NewError(command.Internal, name+" is not implemented yet")
	}
}

func Commands() []command.Command {
	cmds := make([]command.Command, 0, len(specs))
	for _, s := range specs {
		h := handlers[s.name]
		if h == nil {
			h = notImplemented(s.name)
		}
		c := command.NewCommand(s.name, s.group, h)
		c.Description, c.Schema, c.Confirm = s.desc, json.RawMessage(s.in), s.confirm
		if s.name == "table_template.list" {
			c.MinRole = command.Member // design 9: any member
		}
		cmds = append(cmds, c)
	}
	return cmds
}

// OutputSchema is the JSON Schema of a command's result; apps/web/src/lib/tables.ts mirrors it.
func OutputSchema(name string) json.RawMessage {
	for _, s := range specs {
		if s.name == name {
			return json.RawMessage(s.out)
		}
	}
	return nil
}
