package reports

import (
	"encoding/json"
	"slices"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
)

// buildEnv describes the caller for the compiler: readable spaces, viewer, finance flag, enabled modules, FY start.
func buildEnv(c *command.Ctx) (Env, error) {
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select settings from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&raw); err != nil {
		return Env{}, command.DB(err)
	}
	var st struct {
		Hidden       []string `json:"hidden_modules"`
		FYStartMonth int      `json:"fy_start_month"`
	}
	_ = json.Unmarshal(raw, &st)
	if st.FYStartMonth < 1 || st.FYStartMonth > 12 {
		st.FYStartMonth = 4
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return Env{}, err
	}
	fin, err := c.FinanceAllowed()
	if err != nil {
		return Env{}, err
	}
	uid, admin := access.Viewer(c)
	return Env{
		Spaces: spaces, UserID: uid, Admin: admin, Role: int(c.Principal.Role), Finance: fin,
		ModuleOn:     func(k string) bool { return !slices.Contains(st.Hidden, k) },
		FYStartMonth: st.FYStartMonth, Today: billing.TodayIST(),
	}, nil
}
