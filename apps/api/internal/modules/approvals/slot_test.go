package approvals

import (
	"testing"

	"github.com/cnote/api/internal/command"
)

func TestSlotForNamedSlotsAreTheirOwnersUntilEscalation(t *testing.T) {
	str := func(s string) *string { return &s }
	person := func(id string, r command.Role) command.Principal { return command.Principal{UserID: str(id), Role: r} }
	named := command.ApprovalStep{Approvers: []command.ApprovalApprover{{Kind: "user", UserID: "asha"}}}
	role := command.ApprovalStep{Approvers: []command.ApprovalApprover{{Kind: "role", Role: "owner"}}}
	mixed := command.ApprovalStep{Approvers: []command.ApprovalApprover{{Kind: "user", UserID: "asha"}, {Kind: "role", Role: "admin"}}}
	none := map[int]string{}
	for _, c := range []struct {
		name      string
		p         command.Principal
		st        command.ApprovalStep
		taken     map[int]string
		approver  *string
		escalated bool
		slot      int
		ok        bool
	}{
		{"named person fills it", person("asha", command.Member), named, none, nil, false, 0, true},
		{"admin cannot fill a named slot", person("ada", command.Admin), named, none, nil, false, 0, false},
		{"owner cannot fill a named slot", person("own", command.Owner), named, none, nil, false, 0, false},
		{"admin may after escalation", person("ada", command.Admin), named, none, str("fay"), true, 0, true},
		{"fallback may after escalation", person("fay", command.Member), named, none, str("fay"), true, 0, true},
		{"fallback may not before escalation", person("fay", command.Member), named, none, str("fay"), false, 0, false},
		{"other member never", person("otto", command.Member), named, none, str("fay"), true, 0, false},
		{"admin fills a role slot", person("ada", command.Admin), role, none, nil, false, 0, true},
		{"member cannot fill a role slot", person("otto", command.Member), role, none, nil, false, 0, false},
		{"admin skips the named slot for the role slot", person("ada", command.Admin), mixed, none, nil, false, 1, true},
		{"admin cannot fill a mixed step when the role slot is taken", person("ada", command.Admin), mixed, map[int]string{1: "own"}, nil, false, 0, false},
		{"requester never", person("req", command.Admin), role, none, nil, false, 0, false},
	} {
		req := "req"
		if c.name != "requester never" {
			req = "someone"
		}
		slot, ok := slotFor(c.p, req, c.st, c.taken, c.approver, c.escalated)
		if ok != c.ok || (ok && slot != c.slot) {
			t.Errorf("%s: got %d,%v want %d,%v", c.name, slot, ok, c.slot, c.ok)
		}
	}
}
