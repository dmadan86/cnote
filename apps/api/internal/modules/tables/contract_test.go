package tables_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/tables"
)

// The Phase 1a command contract (design 9 and 17). Adding or removing a command is a decision: update this list.
var phase1a = map[string]command.Group{
	"base.list": command.GroupRead, "base.get": command.GroupRead, "base.create": command.GroupAdmin, "base.update": command.GroupAdmin,
	"base.delete": command.GroupDestructive, "base.restore": command.GroupAdmin, "table_template.list": command.GroupRead,
	"table.get": command.GroupRead, "table.create": command.GroupAdmin, "table.update": command.GroupAdmin,
	"table.delete": command.GroupDestructive, "table.restore": command.GroupAdmin,
	"field.create": command.GroupAdmin, "field.update": command.GroupAdmin, "field.delete": command.GroupAdmin, "field.restore": command.GroupAdmin,
	"record.list": command.GroupRead, "record.get": command.GroupRead, "record.create": command.GroupWriteItems,
	"record.create_many": command.GroupWriteItems, "record.update": command.GroupWriteItems, "record.bulk_update": command.GroupWriteItems,
	"record.paste": command.GroupWriteItems, "record.delete": command.GroupWriteItems, "record.restore": command.GroupWriteItems,
	"record.search": command.GroupRead, "record.history": command.GroupRead, "record.batch_list": command.GroupRead,
	"table_view.create": command.GroupWriteItems, "table_view.update": command.GroupWriteItems, "table_view.delete": command.GroupWriteItems,
	"table_import.preview": command.GroupAdmin, "table_import.run": command.GroupAdmin, "table_import.status": command.GroupRead,
}

func TestEveryTablesCommandIsRegisteredWithSchemas(t *testing.T) {
	reg := command.NewRegistry(modules.Commands()...) // panics on a duplicate name anywhere in the registry
	got := map[string]command.Command{}
	for _, c := range reg.List() {
		got[c.Name] = c
	}
	for name, group := range phase1a {
		c, ok := got[name]
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		if c.Group != group {
			t.Errorf("%s: group %s, want %s", name, c.Group, group)
		}
		if len(c.Description) < 40 {
			t.Errorf("%s: description too short for MCP", name)
		}
		for what, raw := range map[string]json.RawMessage{"input": c.Schema, "output": tables.OutputSchema(name)} {
			var s struct {
				Type       string         `json:"type"`
				Properties map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(raw, &s); err != nil || (what == "input" && (s.Type != "object" || s.Properties == nil)) || len(raw) == 0 {
				t.Errorf("%s: bad %s schema (%v)", name, what, err)
			}
		}
		if c.Handler == nil {
			t.Errorf("%s: no handler", name)
		}
		wantMin := command.Member
		if group == command.GroupRead && name != "table_template.list" {
			wantMin = command.Guest // a guest who is in the space may read; the base role decides the rest
		}
		if c.MinRole != wantMin {
			t.Errorf("%s: MinRole %s, want %s", name, c.MinRole, wantMin)
		}
	}
	// nothing in the table families beyond the contract: no forms, attachments, import upload or undo in 1a
	for name := range got {
		for _, p := range []string{"base.", "table.", "table_", "field.", "record."} {
			if strings.HasPrefix(name, p) {
				if _, ok := phase1a[name]; !ok {
					t.Errorf("%s is registered but not in the Phase 1a contract", name)
				}
			}
		}
	}
	if !strings.Contains(string(got["record.create_many"].Schema), `"maxItems":500`) {
		t.Error("record.create_many must cap at 500")
	}
	for _, name := range []string{"base.delete", "table.delete", "base.restore", "table.restore"} {
		if !got[name].Confirm {
			t.Errorf("%s needs confirmation", name)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	in := tables.Cursor{Sort: []any{"abc", nil, 1.5}, ID: "6f1c8f1e-0000-4000-8000-000000000001"}
	out, err := tables.DecodeCursor(in.Encode())
	if err != nil || out.ID != in.ID || len(out.Sort) != 3 || out.Sort[0] != "abc" || out.Sort[1] != nil || out.Sort[2] != 1.5 {
		t.Fatalf("%+v %v", out, err)
	}
	for _, bad := range []string{"", "!!", "e30"} {
		if _, err := tables.DecodeCursor(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
