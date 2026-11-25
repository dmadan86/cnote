package home

import (
	"encoding/json"
	"slices"

	"github.com/cnote/api/internal/command"
)

// setup.status feeds the "Get set up" guide on Home. It lives here because the guide is a Home widget and Home already
// aggregates other modules. It reads no foreign table: each module answers "do I have any?" through its own internal
// command (setup.<x>_exists, command.Exists), called with c.Call, so a module can move to its own service later.
// The company-profile row reads core.workspaces settings, which belong to the platform.

// SetupRow is one step of the guide. Href is an app path.
type SetupRow struct {
	Key  string `json:"key"`
	Done bool   `json:"done"`
	Href string `json:"href"`
}

func setupCommands() []command.Command {
	c := command.NewCommand("setup.status", command.GroupRead, setupStatus)
	c.MinRole = command.Admin
	return []command.Command{c}
}

func setupStatus(c *command.Ctx, input json.RawMessage) (any, error) {
	var o map[string]json.RawMessage
	if json.Unmarshal(input, &o) != nil || o == nil {
		return nil, command.ErrInvalid("invalid input")
	}
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select settings from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&raw); err != nil {
		return nil, command.DB(err)
	}
	var st struct {
		Hidden    []string `json:"hidden_modules"`
		LegalName string   `json:"legal_name"`
		State     string   `json:"state_code"`
	}
	_ = json.Unmarshal(raw, &st)
	on := func(k string) bool { return !slices.Contains(st.Hidden, k) }
	base := "/w/" + c.WorkspaceID()
	rows := []SetupRow{{"company", st.LegalName != "" && st.State != "", base + "/settings?tab=Company"}}
	has := func(cmd string) (bool, error) {
		out, err := c.Call(cmd, map[string]any{})
		if err != nil {
			return false, err
		}
		var r struct {
			Exists bool `json:"exists"`
		}
		return r.Exists, json.Unmarshal(out, &r)
	}
	for _, s := range []struct{ module, key, cmd, href string }{
		{"crm", "customer", "setup.customer_exists", "/crm?new=1"},
		{"billing", "invoice", "setup.invoice_exists", "/billing?new=1"},
		{"site", "website", "setup.site_exists", "/site"},
		{"tables", "table", "setup.table_exists", "/tables?new=1"},
		{"", "import", "setup.import_exists", "/import"},
	} {
		if s.module != "" && !on(s.module) {
			continue
		}
		done, err := has(s.cmd)
		if err != nil {
			return nil, err
		}
		rows = append(rows, SetupRow{s.key, done, base + s.href})
	}
	return rows, nil
}
