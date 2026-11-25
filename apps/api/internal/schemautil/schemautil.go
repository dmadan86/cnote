// Package schemautil builds the JSON Schemas and the command stubs of the docs-parity phases (A1's pattern, shared so
// a module other than notes can declare its commands the same way). A stub carries the full contract (input and output
// schema, description, group, minimum role, confirmation, secret) and answers "not implemented" until the work package
// that owns it registers the real handler from an init() in its own file: handlers["page_link.get"] = pageLinkGet.
package schemautil

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cnote/api/internal/command"
)

const (
	ID   = `{"type":"string","description":"UUID"}`
	Str  = `{"type":"string"}`
	Int  = `{"type":"integer"}`
	Bool = `{"type":"boolean"}`
	TS   = `{"type":"string","description":"RFC 3339 timestamp"}`
	Date = `{"type":"string","description":"YYYY-MM-DD"}`
)

// Obj is an object schema with the listed properties (key, schema pairs) and required names.
func Obj(required []string, kv ...string) string {
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

func Arr(item string) string   { return `{"type":"array","items":` + item + `}` }
func Nullable(s string) string { return `{"anyOf":[` + s + `,{"type":"null"}]}` }
func AnyOf(s ...string) string { return `{"anyOf":[` + strings.Join(s, ",") + `]}` }
func IntRange(lo, hi int) string {
	return fmt.Sprintf(`{"type":"integer","minimum":%d,"maximum":%d}`, lo, hi)
}
func Enum(vals ...string) string {
	b, _ := json.Marshal(vals)
	return `{"type":"string","enum":` + string(b) + `}`
}

// Spec is one command's contract.
type Spec struct {
	Name, Desc, In, Out string
	Group               command.Group
	MinRole             command.Role
	Confirm             bool // an agent or API key call waits for a person's approval
	Secret              bool // the result holds a secret that must not be stored or logged
	Internal            bool // callable only through Ctx.Call
}

func NotImplemented(name string) command.Handler {
	return func(*command.Ctx, json.RawMessage) (any, error) {
		return nil, command.NewError(command.Internal, name+" is not implemented yet")
	}
}

// Commands turns specs into commands, using the registered real handler where there is one.
func Commands(specs []Spec, handlers map[string]command.Handler) []command.Command {
	cmds := make([]command.Command, 0, len(specs))
	for _, s := range specs {
		h := handlers[s.Name]
		if h == nil {
			h = NotImplemented(s.Name)
		}
		c := command.NewCommand(s.Name, s.Group, h)
		c.Description, c.Schema = s.Desc, json.RawMessage(s.In)
		c.MinRole, c.Confirm, c.ReturnsSecret, c.Internal = s.MinRole, s.Confirm, s.Secret, s.Internal
		cmds = append(cmds, c)
	}
	return cmds
}

// OutputSchema is the JSON Schema of a command's result, for the web client types and the contract tests.
func OutputSchema(specs []Spec, name string) json.RawMessage {
	for _, s := range specs {
		if s.Name == name {
			return json.RawMessage(s.Out)
		}
	}
	return nil
}
