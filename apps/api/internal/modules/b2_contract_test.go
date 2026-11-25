package modules

import (
	"encoding/json"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/docs"
	"github.com/cnote/api/internal/modules/importers"
	"github.com/cnote/api/internal/modules/notes"
	"strings"
	"testing"
)

func TestB2CommandContract(t *testing.T) {
	type contract struct {
		group  command.Group
		role   command.Role
		output func(string) json.RawMessage
	}
	cases := map[string]contract{
		"note.request_access": {command.GroupWriteNotes, command.Guest, notes.B2OutputSchema},
		"note.viewers":        {command.GroupRead, command.Guest, notes.B2OutputSchema},
		"doc.archive":         {command.GroupWriteNotes, command.Member, docs.B2OutputSchema},
		"import.docx":         {command.GroupWriteNotes, command.Member, importers.B2OutputSchema},
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
		if c.Group != want.group || c.MinRole != want.role || c.Confirm || c.ReturnsSecret || c.Internal {
			t.Errorf("%s: unexpected contract", name)
		}
		for kind, raw := range map[string]json.RawMessage{"input": c.Schema, "output": want.output(name)} {
			var v map[string]any
			if json.Unmarshal(raw, &v) != nil || v["type"] == nil {
				t.Errorf("%s: invalid %s schema: %s", name, kind, raw)
			}
		}
		if !strings.Contains(c.Description, "Returns") {
			t.Errorf("%s: missing output description", name)
		}
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(by["template.render"].Schema, &schema); err != nil || schema.Properties["answers"] == nil {
		t.Error("template.render must describe answers")
	}
	if by["template.render"].Group != command.GroupWriteNotes {
		t.Error("template.render must be outside read-only AI scope")
	}
}
