package modules

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/cnote/api/internal/modules/notifications"
	"github.com/cnote/api/internal/modules/publiclinks"
)

// The B1 contract (docs-parity-b 6 and 13): group, minimum role, confirmation, secret and internal flags of every command,
// valid schemas, a description that says what comes back. Which of them are in the read-only AI scope follows from the
// group: only GroupRead is.
func TestB1CommandContract(t *testing.T) {
	type want struct {
		group                  command.Group
		min                    command.Role
		confirm, secret, intnl bool
	}
	R, W, S := command.GroupRead, command.GroupWriteNotes, command.GroupSendMessages
	G, M := command.Guest, command.Member
	table := map[string]want{
		"space.members":          {R, M, false, false, false},
		"space.member_set":       {W, M, true, false, false},
		"space.update":           {W, M, true, false, false},
		"space.archive":          {W, M, true, false, false},
		"page_link.get":          {R, M, false, false, false},
		"page_link.create":       {S, M, true, true, false},
		"page_link.revoke":       {W, M, false, false, false},
		"page_link.list":         {R, M, false, false, false},
		"page_link.revoke_space": {W, M, false, false, true},
		"notify_prefs.get":       {R, G, false, false, false},
		"notify_prefs.set":       {W, G, false, false, false},
		"task.mine":              {R, G, false, false, false},
		"comment.edit":           {W, G, false, false, false},
		"comment.delete":         {W, G, true, false, false},
		"comment.react":          {W, G, false, false, false},
	}
	by := map[string]command.Command{}
	for _, c := range Commands() {
		by[c.Name] = c
	}
	outputs := map[string]func(string) json.RawMessage{}
	for n := range table {
		switch {
		case strings.HasPrefix(n, "page_link."):
			outputs[n] = publiclinks.B1OutputSchema
		case strings.HasPrefix(n, "notify_prefs."):
			outputs[n] = notifications.B1OutputSchema
		default:
			outputs[n] = notes.B1OutputSchema
		}
	}
	for name, w := range table {
		c, ok := by[name]
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		if c.Group != w.group || c.MinRole != w.min || c.Confirm != w.confirm || c.ReturnsSecret != w.secret || c.Internal != w.intnl {
			t.Errorf("%s: group %v role %v confirm %v secret %v internal %v, want %+v", name, c.Group, c.MinRole, c.Confirm, c.ReturnsSecret, c.Internal, w)
		}
		for what, raw := range map[string]json.RawMessage{"input": c.Schema, "output": outputs[name](name)} {
			var v map[string]any
			if json.Unmarshal(raw, &v) != nil || (v["type"] == nil && v["anyOf"] == nil) {
				t.Errorf("%s: %s schema is not a JSON Schema: %s", name, what, raw)
			}
		}
		if !strings.Contains(c.Description, "Returns") || len(c.Description) < 80 {
			t.Errorf("%s: the description must say what comes back", name)
		}
		// Only the read group is in the read-only AI scope: a state-changing command must not be there.
		if w.group != R && c.Group == R {
			t.Errorf("%s must not be in the read-only AI scope", name)
		}
	}
	// A guest never runs a command that acts on a space, on other pages or on the outside world.
	for _, n := range []string{"space.members", "space.member_set", "space.update", "space.archive", "page_link.get", "page_link.create", "page_link.revoke", "page_link.list"} {
		if by[n].MinRole < command.Member {
			t.Errorf("%s: guests must not run it", n)
		}
	}
}
