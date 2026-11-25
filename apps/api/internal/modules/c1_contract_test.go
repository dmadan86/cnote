package modules

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

// C1 step 0: every command has a schema pair, a group (AI scope), a minimum role (guest rule) and an output description.
func TestC1CommandContract(t *testing.T) {
	cases := map[string]struct {
		group   command.Group
		role    command.Role
		confirm bool
	}{
		"note.tree":           {command.GroupRead, command.Guest, false},
		"note.cover_set":      {command.GroupWriteNotes, command.Guest, false},
		"note.purge":          {command.GroupDestructive, command.Member, true},
		"trash.retention_get": {command.GroupRead, command.Member, false},
		"trash.retention_set": {command.GroupAdmin, command.Admin, false},
	}
	by := map[string]command.Command{}
	for _, c := range Commands() {
		by[c.Name] = c
	}
	for name, want := range cases {
		c, ok := by[name]
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		if c.Group != want.group || c.MinRole != want.role || c.Confirm != want.confirm || c.ReturnsSecret || c.Internal {
			t.Errorf("%s: unexpected contract", name)
		}
		for kind, raw := range map[string]json.RawMessage{"input": c.Schema, "output": notes.C1OutputSchema(name)} {
			var v map[string]any
			if json.Unmarshal(raw, &v) != nil || v["type"] == nil {
				t.Errorf("%s: invalid %s schema: %s", name, kind, raw)
			}
		}
		if !strings.Contains(c.Description, "Returns") {
			t.Errorf("%s: missing output description", name)
		}
	}
	// purge and the retention change are never in the read-only AI scope; the tree is.
	if by["note.purge"].Group == command.GroupRead || by["trash.retention_set"].Group == command.GroupRead || by["note.tree"].Group != command.GroupRead {
		t.Error("AI scope")
	}
}
