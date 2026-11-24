package command

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

var allGroups = []Group{GroupRead, GroupWriteNotes, GroupWriteItems, GroupManageCRM, GroupManageInvoices, GroupSendMessages, GroupAdmin, GroupDestructive}

// Get returns a registered command by name.
func (r *Registry) Get(name string) (Command, bool) { c, ok := r.cmds[name]; return c, ok }

// Allowed reports whether the caller may run a command: role, scopes, finance access and the workspace's tool
// groups, through the same gate Registry.run uses. Nothing runs and no rate limit is counted.
func (c *Ctx) Allowed(name string) error {
	cmd, ok := c.reg.Get(name)
	if !ok {
		return ErrNotFound("command " + name)
	}
	return c.gate(cmd)
}

// AgentPrincipal derives the principal an AI employee runs as, entirely from the database: the agent must exist in
// this workspace and be enabled, onBehalfOf must be an active member, the role is min(member role, Admin), and the
// scopes are the agent's own intersected with the workspace's tool groups. Finance access follows the member
// (Ctx.FinanceAllowed reads it through UserID). Call it again whenever permissions must be current.
func AgentPrincipal(c *Ctx, agentID, onBehalfOf string) (Principal, error) {
	ws := c.WorkspaceID()
	var scopes []string
	var enabled bool
	err := c.Tx.QueryRow(c.Context, "select scopes, enabled from core.agents where id = $1::uuid and workspace_id = $2::uuid", agentID, ws).Scan(&scopes, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrNotFound("agent")
	}
	if err != nil {
		return Principal{}, DB(err)
	}
	if !enabled {
		return Principal{}, ErrForbidden("this AI employee is paused")
	}
	var role string
	err = c.Tx.QueryRow(c.Context,
		"select role from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid and deactivated_at is null", ws, onBehalfOf).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrForbidden("the person this agent acts for is not an active member of this workspace")
	}
	if err != nil {
		return Principal{}, DB(err)
	}
	granted := []string{}
	for _, g := range allGroups {
		if ok, err := c.workspaceAllowsGroup(g); err != nil {
			return Principal{}, err
		} else if ok && (slices.Contains(scopes, string(g)) || slices.Contains(scopes, "*")) {
			granted = append(granted, string(g))
		}
	}
	return Principal{Kind: KindAgent, AgentID: &agentID, UserID: &onBehalfOf, WorkspaceID: ws, Role: min(ParseRole(role), Admin), Scopes: granted}, nil
}

// CallConfirmed runs a Confirm command from an approved confirmation as the agent. The principal is re-derived from
// the database, so a person deactivated, an agent paused or a scope or tool group removed since the request was
// made refuses the replay. The confirmation must have been requested by this agent; the registry consumes it.
func (c *Ctx) CallConfirmed(p Principal, name, confirmationID string) (json.RawMessage, error) {
	if p.Kind != KindAgent || p.AgentID == nil || p.UserID == nil {
		return nil, ErrForbidden("only an AI employee's own confirmations are replayed this way")
	}
	cmd, ok := c.reg.Get(name)
	if !ok {
		return nil, ErrNotFound("command " + name)
	}
	if !cmd.Confirm {
		return nil, ErrInvalid(name + " does not take a confirmation")
	}
	fresh, err := AgentPrincipal(c, *p.AgentID, *p.UserID)
	if err != nil {
		return nil, err
	}
	var by string
	var stored []byte
	err = c.Tx.QueryRow(c.Context, "select requested_by, input from core.confirmations where id = $1::uuid", confirmationID).Scan(&by, &stored)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && by != *p.AgentID {
		return nil, ErrForbidden("confirmation missing, rejected or already used")
	}
	if err != nil {
		return nil, DB(err)
	}
	cc := &Ctx{Context: c.Context, Tx: c.Tx, Pool: c.Pool, Principal: fresh, reg: c.reg}
	return cc.reg.run(cc, name, stored, ExecOptions{ConfirmationID: &confirmationID})
}
