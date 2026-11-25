package notes

import (
	"encoding/json"
	"testing"

	"github.com/cnote/api/internal/command"
)

// The stub contract: every A1 command is registered with valid JSON Schemas (input and output), a description
// that says what it returns, the right group and role, and answers "not implemented" until its package lands.
func TestA1CommandContract(t *testing.T) {
	byName := map[string]command.Command{}
	for _, c := range Commands() {
		byName[c.Name] = c
	}
	want := []string{"label.set", "label.list", "note.star", "note.watch", "note.viewed", "note.mine"}
	for _, n := range want {
		c, ok := byName[n]
		if !ok {
			t.Fatalf("%s is not registered", n)
		}
		for what, raw := range map[string]json.RawMessage{"input": c.Schema, "output": A1OutputSchema(n)} {
			var v map[string]any
			if json.Unmarshal(raw, &v) != nil || v["type"] == nil {
				t.Errorf("%s: %s schema is not a JSON Schema: %s", n, what, raw)
			}
		}
		if c.Description == "" || c.Handler == nil {
			t.Errorf("%s: missing description or handler", n)
		}
		if a1Handlers[n] == nil { // a landed handler needs a real Ctx; its own tests cover it
			if _, err := c.Handler(nil, json.RawMessage(`{}`)); err == nil {
				t.Errorf("%s: a stub must refuse", n)
			}
		}
	}
	if byName["note.star"].Group != command.GroupWriteNotes || byName["note.star"].MinRole != command.Guest {
		t.Error("note.star: write_notes group (not in the read-only AI scope), guests allowed")
	}
	if byName["label.set"].MinRole != command.Member || byName["label.list"].MinRole != command.Guest {
		t.Error("label.set needs a member, label.list is open to guests (it filters by page access)")
	}
}
