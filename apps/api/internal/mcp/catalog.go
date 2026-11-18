package mcp

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/cnote/api/internal/command"
)

// RustCatalog is the frozen catalogue of the commands that came from the Rust server: what agents are told about
// them (description, tool group, confirmation, whether they are a direct MCP tool, input schema). It is now plain data
// maintained by hand in catalog.json; newer commands add theirs from modules/mcpmeta.go. Shared with the
// /api/commands handler.
//
//go:embed catalog.json
var RustCatalog []byte

type entry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Group       string          `json:"group"`
	Confirm     bool            `json:"confirm"`
	Core        bool            `json:"core"`
	Schema      json.RawMessage `json:"input_schema"`
}

var (
	baseOnce sync.Once
	baseList []entry
)

func rustEntries() []entry {
	baseOnce.Do(func() {
		if err := json.Unmarshal(RustCatalog, &baseList); err != nil {
			panic("mcp: bad catalog.json: " + err.Error())
		}
		for i := range baseList {
			n, err := command.Norm(baseList[i].Schema)
			if err != nil {
				panic("mcp: bad schema for " + baseList[i].Name)
			}
			baseList[i].Schema = n
		}
	})
	return baseList
}

// Entry is what agents are told about one command.
type Entry = entry

// Catalog is every command agents can discover, sorted by name like Registry::list in Rust: Rust's commands plus the
// Go-only ones (commands with MCP metadata that Rust does not have).
func Catalog(reg *command.Registry) []Entry {
	out := slices.Clone(rustEntries())
	known := map[string]bool{}
	for _, e := range out {
		known[e.Name] = true
	}
	for _, c := range reg.List() {
		if c.Description == "" || c.Internal || known[c.Name] {
			continue
		}
		schema, err := command.Norm(c.Schema)
		if err != nil {
			continue
		}
		out = append(out, entry{Name: c.Name, Description: c.Description, Group: string(c.Group), Confirm: c.Confirm, Schema: schema})
	}
	slices.SortFunc(out, func(a, b entry) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (s *Server) catalog() []entry {
	s.once.Do(func() { s.cat = Catalog(s.Registry) })
	return s.cat
}

// ToolName is the MCP tool name of a command ("invoice.issue" becomes "invoice_issue").
func ToolName(command string) string { return strings.ReplaceAll(command, ".", "_") }

func toolName(command string) string { return ToolName(command) }

// resolve accepts "invoice.issue" or "invoice_issue".
func (s *Server) resolve(name string) (entry, bool) {
	cat := s.catalog()
	for _, e := range cat {
		if e.Name == name {
			return e, true
		}
	}
	for _, e := range cat {
		if toolName(e.Name) == name {
			return e, true
		}
	}
	return entry{}, false
}

// InputSchema is the JSON Schema of a command's input: Rust's (catalog snapshot) when Rust has the command, else
// the Go-only command's own. nil when neither is known.
func InputSchema(reg *command.Registry, name string) json.RawMessage {
	for _, e := range rustEntries() {
		if e.Name == name {
			return e.Schema
		}
	}
	for _, c := range reg.List() {
		if c.Name == name {
			return c.Schema
		}
	}
	return nil
}
