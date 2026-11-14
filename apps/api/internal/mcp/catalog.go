package mcp

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/cnote/api/internal/command"
)

// rustCatalog is a snapshot of GET /api/commands on the Rust server: the commands Rust owns and what agents are told
// about them (description, tool group, confirmation, whether they are a direct MCP tool, input schema). Go serves the
// MCP protocol itself, so it needs the whole catalogue; Go-only commands add theirs from modules/mcpmeta.go.
// TestCatalogMatchesRust compares it with the running Rust server; regenerate with:
//
//	curl -s localhost:4000/api/commands > internal/mcp/catalog.json
//
//go:embed catalog.json
var rustCatalog []byte

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
		if err := json.Unmarshal(rustCatalog, &baseList); err != nil {
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

// catalog is every command agents can discover, sorted by name like Registry::list in Rust: Rust's commands plus the
// Go-only ones (commands with MCP metadata that Rust does not have).
func (s *Server) catalog() []entry {
	s.once.Do(func() {
		out := slices.Clone(rustEntries())
		known := map[string]bool{}
		for _, e := range out {
			known[e.Name] = true
		}
		for _, c := range s.Registry.List() {
			if c.Description == "" || known[c.Name] {
				continue
			}
			schema, err := command.Norm(c.Schema)
			if err != nil {
				continue
			}
			out = append(out, entry{Name: c.Name, Description: c.Description, Group: string(c.Group), Confirm: c.Confirm, Schema: schema})
		}
		slices.SortFunc(out, func(a, b entry) int { return strings.Compare(a.Name, b.Name) })
		s.cat = out
	})
	return s.cat
}

func toolName(command string) string { return strings.ReplaceAll(command, ".", "_") }

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
