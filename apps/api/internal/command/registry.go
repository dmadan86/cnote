// Package command is the Go port of crates/cnote-core/src/command.rs: every capability is a command run
// through one gate (role, scopes, rate limit, agent groups, idempotency, confirmation, usage).
// Porting a module = building []Command and passing it to NewRegistry.
package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/db"
)

type Group string

const (
	GroupRead           Group = "read"
	GroupWriteNotes     Group = "write_notes"
	GroupWriteItems     Group = "write_items"
	GroupManageCRM      Group = "manage_crm"
	GroupManageInvoices Group = "manage_invoices"
	GroupSendMessages   Group = "send_messages"
	GroupAdmin          Group = "admin"
	GroupDestructive    Group = "destructive"
)

// Groups enabled for agents/API keys when a workspace has not set settings.agent_tool_groups.
var defaultAgentGroups = []string{"read", "write_notes", "write_items", "manage_crm"}

type Ctx struct {
	Context   context.Context
	Tx        pgx.Tx
	Principal Principal
	reg       *Registry
}

func (c *Ctx) WorkspaceID() string { return c.Principal.WorkspaceID }

// FinanceAllowed is false for a non-admin person (or their keys and agents) whose finance_access flag is off.
func (c *Ctx) FinanceAllowed() (bool, error) {
	p := c.Principal
	if p.Kind == KindSystem || p.Role >= Admin {
		return true, nil
	}
	if p.UserID == nil { // a key or agent with no person behind it never sees money
		return false, nil
	}
	var ok bool
	err := c.Tx.QueryRow(c.Context, "select finance_access from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid",
		p.WorkspaceID, *p.UserID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return ok, DB(err)
}

// Handler receives the raw JSON input and returns a value that is marshaled as the response.
// Return ErrFallback to delegate the request to Rust.
type Handler func(c *Ctx, input json.RawMessage) (any, error)

type Command struct {
	Name          string
	Group         Group
	MinRole       Role // zero value Guest; use NewCommand for the Rust default (Read=Guest, else Member)
	Confirm       bool
	ReturnsSecret bool
	Handler       Handler
	Finance       bool       // money data: refused below Admin when the member's finance_access is off (core.memberships)
	Amount        AmountFunc // set: a workspace approval rule may hold this command for a person's decision
	// Description and Schema (JSON Schema of the input) are what agents see over MCP. Set for Go-only commands;
	// commands Rust also has are described by Rust. See modules/mcpmeta.go.
	Description string
	Schema      json.RawMessage
}

func NewCommand(name string, g Group, h Handler) Command {
	min := Member
	if g == GroupRead {
		min = Guest
	}
	return Command{Name: name, Group: g, MinRole: min, Handler: h}
}

type ExecOptions struct {
	IdempotencyKey *string
	ConfirmationID *string
	approved       bool // replay of an approved request: skip the approval gate
}

type Registry struct{ cmds map[string]Command }

func NewRegistry(cmds ...Command) *Registry {
	r := &Registry{cmds: map[string]Command{}}
	for _, c := range cmds {
		if _, dup := r.cmds[c.Name]; dup {
			panic("duplicate command " + c.Name)
		}
		r.cmds[c.Name] = c
	}
	return r
}

func (r *Registry) Has(name string) bool { _, ok := r.cmds[name]; return ok }

// Execute runs a command in its own tenant transaction and returns the serialized result.
func (r *Registry) Execute(ctx context.Context, pool *pgxpool.Pool, name string, input json.RawMessage, p Principal, o ExecOptions) ([]byte, error) {
	tx, err := db.BeginTenant(ctx, pool, p.WorkspaceID)
	if err != nil {
		return nil, DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c := &Ctx{Context: ctx, Tx: tx, Principal: p, reg: r}
	out, err := r.run(c, name, input, o)
	if err != nil {
		if p.IsMachine() {
			r.auditDenial(ctx, pool, p, name, err)
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, DB(err)
	}
	return out, nil
}

func (r *Registry) run(c *Ctx, name string, input json.RawMessage, o ExecOptions) ([]byte, error) {
	cmd, ok := r.cmds[name]
	if !ok {
		return nil, ErrNotFound("command " + name)
	}
	p := c.Principal
	if p.Role < cmd.MinRole {
		return nil, ErrForbidden(fmt.Sprintf("%s requires role %s", name, cmd.MinRole))
	}
	if p.Scopes != nil && !slices.ContainsFunc(p.Scopes, func(s string) bool { return s == "*" || s == string(cmd.Group) }) {
		return nil, ErrForbidden(fmt.Sprintf("scope %s not granted", cmd.Group))
	}
	if cmd.Finance {
		if ok, err := c.FinanceAllowed(); err != nil {
			return nil, err
		} else if !ok {
			return nil, ErrForbidden("finance access is turned off for you; ask an admin")
		}
	}
	if p.Kind != KindSystem {
		who := p.UserID
		if who == nil {
			who = p.AgentID
		}
		if err := checkRate(p.WorkspaceID + ":" + orNil(who)); err != nil {
			return nil, err
		}
	}
	if p.IsMachine() {
		var raw []byte
		if err := c.Tx.QueryRow(c.Context, "select settings from core.workspaces where id = $1::uuid", p.WorkspaceID).Scan(&raw); err != nil {
			return nil, DB(err)
		}
		var s struct {
			Groups *[]any `json:"agent_tool_groups"`
		}
		_ = json.Unmarshal(raw, &s)
		allowed := slices.Contains(defaultAgentGroups, string(cmd.Group))
		if s.Groups != nil {
			allowed = slices.Contains(*s.Groups, any(string(cmd.Group)))
		}
		if !allowed {
			return nil, ErrForbidden(fmt.Sprintf("tool group %s is disabled for agents in this workspace", cmd.Group))
		}
	}

	actor := fmt.Sprintf("%s:%s:%s", p.Kind, orNil(p.UserID), orNil(p.AgentID))
	var idem *string
	if !cmd.ReturnsSecret {
		idem = o.IdempotencyKey
	}
	if idem != nil {
		var command string
		var result []byte
		err := c.Tx.QueryRow(c.Context,
			"select command, result from core.idempotency where workspace_id = $1::uuid and actor = $2 and key = $3",
			p.WorkspaceID, actor, *idem).Scan(&command, &result)
		if err == nil {
			if command != name {
				return nil, ErrConflict("idempotency key reused for a different command")
			}
			n, err := Norm(result)
			return n, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, DB(err)
		}
	}

	if cmd.Amount != nil && !o.approved {
		if out, held, err := r.approvalGate(c, cmd, input); held || err != nil {
			return out, err
		}
	}

	if cmd.Confirm && p.IsMachine() {
		return r.confirm(c, cmd, input, o)
	}

	out, err := cmd.Handler(c, input)
	if err != nil {
		return nil, DB(err)
	}
	b, err := serialize(out)
	if err != nil {
		return nil, err
	}
	if idem != nil {
		if _, err := c.Tx.Exec(c.Context,
			"insert into core.idempotency (workspace_id, actor, key, command, result) values ($1::uuid,$2,$3,$4,$5)",
			p.WorkspaceID, actor, *idem, name, json.RawMessage(b)); err != nil {
			return nil, DB(err)
		}
	}
	if p.IsMachine() {
		if err := recordUsage(c, name); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// confirm: agents get a confirmation request a human must approve; the approved input is authoritative.
func (r *Registry) confirm(c *Ctx, cmd Command, input json.RawMessage, opts ExecOptions) ([]byte, error) {
	p, name := c.Principal, cmd.Name
	if opts.ConfirmationID == nil {
		by := ""
		if p.AgentID != nil {
			by = *p.AgentID
		} else if p.UserID != nil {
			by = *p.UserID
		}
		var id string
		if err := c.Tx.QueryRow(c.Context,
			"insert into core.confirmations (workspace_id, command, input, requested_by) values ($1::uuid,$2,$3,$4) returning id::text",
			p.WorkspaceID, name, input, by).Scan(&id); err != nil {
			return nil, DB(err)
		}
		// a map, not a struct: Rust builds a serde_json::Value, so keys come out sorted
		return Marshal(map[string]any{"confirmation_required": true, "confirmation_id": id,
			"message": name + " moves money or sends a message; a human must confirm it in C Note."})
	}
	var confirmed []byte
	err := c.Tx.QueryRow(c.Context,
		"update core.confirmations set status = 'expired' where id = $1::uuid and command = $2 and status = 'confirmed' returning input",
		*opts.ConfirmationID, name).Scan(&confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden("confirmation missing, rejected or already used")
	}
	if err != nil {
		return nil, DB(err)
	}
	out, err := cmd.Handler(c, confirmed)
	if err != nil {
		return nil, DB(err)
	}
	b, err := serialize(out)
	if err != nil {
		return nil, err
	}
	return b, recordUsage(c, name)
}

func recordUsage(c *Ctx, name string) error {
	meta, _ := Marshal(map[string]string{"command": name})
	_, err := c.Tx.Exec(c.Context,
		"insert into core.usage (workspace_id, kind, quantity, meta) values ($1::uuid,$2,$3,$4)",
		c.Principal.WorkspaceID, "mcp.call", 1, json.RawMessage(meta))
	return DB(err)
}

// serialize mirrors `serde_json::to_value(out)` followed by Json(): the handler output becomes a
// serde_json::Value first, so every object in a response has its keys sorted (struct field order is lost).
func serialize(out any) ([]byte, error) {
	b, err := Marshal(out)
	if err != nil {
		return nil, NewError(Invalid, err.Error())
	}
	n, err := Norm(b)
	if err != nil {
		return nil, NewError(Invalid, err.Error())
	}
	return n, nil
}

// List returns every command sorted by name (MCP discovery, metadata checks).
func (r *Registry) List() []Command {
	out := make([]Command, 0, len(r.cmds))
	for _, c := range r.cmds {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Command) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// auditDenial writes command.denied (SEC-014) for an agent or API key the gate refused. The caller's own transaction
// rolls back with the error, so this is a second one; it is best effort and capped at one event per actor and
// command per minute and 20 per actor per minute, so a probing loop cannot flood the log.
func (r *Registry) auditDenial(ctx context.Context, pool *pgxpool.Pool, p Principal, name string, cause error) {
	var ce *Error
	if !errors.As(cause, &ce) || ce.Code != Forbidden {
		return
	}
	ctx = context.WithoutCancel(ctx)
	tx, err := db.BeginTenant(ctx, pool, p.WorkspaceID)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, actorID, _ := p.Actor()
	caller := string(p.Kind)
	var same, all int
	if tx.QueryRow(ctx, `select count(*) filter (where payload->>'command' = $2 and payload->>'caller' = $3), count(*)
		from core.events where kind = 'command.denied' and actor_id is not distinct from $1 and created_at > now() - interval '1 minute'`,
		actorID, name, caller).Scan(&same, &all) != nil || same > 0 || all >= 20 {
		return
	}
	c := &Ctx{Context: ctx, Tx: tx, Principal: p, reg: r}
	if _, err := c.Emit("command.denied", nil, map[string]any{"command": name, "reason": ce.Message, "caller": caller}); err == nil {
		_ = tx.Commit(ctx)
	}
}
