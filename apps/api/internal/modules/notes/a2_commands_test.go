package notes

import (
	"encoding/json"
	"testing"

	"github.com/cnote/api/internal/command"
)

// The A2 stub contract (docs-parity 7.9): every lifecycle command is registered with valid schemas, a description, a
// member minimum (guests never change the tree) and the right group, and answers "not implemented" until L1 lands.
func TestA2CommandContract(t *testing.T) {
	byName := map[string]command.Command{}
	for _, c := range Commands() {
		byName[c.Name] = c
	}
	for _, s := range a2Specs {
		for what, raw := range map[string]json.RawMessage{"input": json.RawMessage(s.in), "output": A2OutputSchema(s.name)} {
			var v map[string]any
			if json.Unmarshal(raw, &v) != nil || v["type"] == nil {
				t.Errorf("%s: %s schema is not a JSON Schema: %s", s.name, what, raw)
			}
		}
		c, ok := byName[s.name]
		if !ok {
			t.Fatalf("%s is not registered", s.name)
		}
		if c.Group != s.group || c.MinRole != command.Member {
			t.Errorf("%s: group %s role %v, want %s and member", s.name, c.Group, c.MinRole, s.group)
		}
		if s.existing {
			continue
		}
		if c.Description == "" || c.Handler == nil {
			t.Errorf("%s: missing description or handler", s.name)
		}
		if a2Handlers[s.name] == nil {
			if _, err := c.Handler(nil, json.RawMessage(`{}`)); err == nil {
				t.Errorf("%s: a stub must refuse", s.name)
			}
		}
	}
	if d, in := A2Describe("note.delete"); d == "" || in == nil {
		t.Error("note.delete: description and input schema are L1's to apply")
	}
}
