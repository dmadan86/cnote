package command

import (
	"encoding/json"
	"errors"

	"github.com/cnote/api/internal/notify"
	"github.com/jackc/pgx/v5"
)

// AmountFunc tells the approval gate which document a command acts on and the amount (paise) at stake.
// It must run the same access check as the handler, so a caller never opens a request on a page they cannot touch.
type AmountFunc func(c *Ctx, input json.RawMessage) (noteID string, amount int64, err error)

// approvalGate runs before the handler of a command that has an Amount. A human who is not the owner and acts
// at or above the workspace rule's threshold does not execute: a pending approval is stored (kind 'approval') and
// the caller gets approval_required. approval.decide later replays the stored input as the requester (RunApproved).
// An agent or API key with a person behind it (UserID) is routed the same way, in that person's name; c.Emit records
// the agent as the actor and the agent run links the request through its step's confirmation_id.
func (r *Registry) approvalGate(c *Ctx, cmd Command, input json.RawMessage) ([]byte, bool, error) {
	p := c.Principal
	machine := p.Kind == KindAgent || p.Kind == KindAPIKey
	// A person who owns the workspace skips the gate; their agent is capped at admin, so it does not.
	if !machine && (p.Kind != KindUser || p.UserID == nil || p.Role >= Owner) {
		return nil, false, nil
	}
	var min int64
	var fallback *string
	var hours *int
	var ruleSteps []ApprovalStep
	err := c.Tx.QueryRow(c.Context,
		`select min_amount_minor, steps, fallback_user_id::text, escalate_after_hours
		   from core.approval_rules where command = $1 and enabled`, cmd.Name).Scan(&min, &ruleSteps, &fallback, &hours)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, DB(err)
	}
	noteID, amount, err := cmd.Amount(c, input)
	if err != nil {
		return nil, false, err // the same error the handler would give
	}
	if amount < min {
		return nil, false, nil
	}
	if machine && p.UserID == nil { // fail closed: no person to open the approval in the name of
		return nil, false, ErrForbidden("this amount needs a person's approval; ask a workspace member to make the request in C Note")
	}
	var byKind *string // a machine's request remembers who made it, so the replay is bounded by that machine
	if machine {
		k := string(p.Kind)
		byKind = &k
	}
	steps, err := snapshotSteps(c, ruleSteps, amount, *p.UserID)
	if err != nil {
		return nil, false, err
	}
	if len(steps) == 0 { // every step's own threshold is above this amount
		return nil, false, nil
	}
	var id string
	err = c.Tx.QueryRow(c.Context, `select id::text from core.confirmations where kind = 'approval' and status = 'pending'
		and command = $1 and note_id is not distinct from nullif($2,'')::uuid and requested_by = $3 and input = $4::jsonb`, cmd.Name, noteID, *p.UserID, input).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = c.Tx.QueryRow(c.Context,
			`insert into core.confirmations (workspace_id, command, input, requested_by, kind, note_id, amount_minor,
			   approver_user_id, fallback_user_id, escalate_at, steps, requested_by_agent, requested_by_kind)
			 values ($1::uuid,$2,$3,$4,'approval',nullif($5,'')::uuid,$6,$7::uuid,$8::uuid,
			   case when $9::int is null then null else now() + $9::int * interval '1 hour' end, $10::jsonb, $11::uuid, $12)
			 returning id::text`,
			p.WorkspaceID, cmd.Name, input, *p.UserID, noteID, amount, steps[0].FirstUser(), fallback, hours, steps, p.AgentID, byKind).Scan(&id)
		if err != nil {
			return nil, false, DB(err)
		}
		var np *string // a document without a note (a payroll run) has no page to attach to
		if noteID != "" {
			np = &noteID
		}
		if _, err := c.Emit("approval.requested", np, map[string]any{"approval_id": id, "command": cmd.Name, "amount_minor": amount, "agent_id": p.AgentID}); err != nil {
			return nil, false, err
		}
		if err := steps[0].Notify(c, np, *p.UserID); err != nil {
			return nil, false, err
		}
	} else if err != nil {
		return nil, false, DB(err)
	}
	out, err := Marshal(map[string]any{"approval_required": true, "confirmation_id": id, "amount_minor": amount,
		"message": cmd.Name + " needs approval; you will be notified when it is decided."})
	return out, true, err
}

// ApprovalStep is one link of a chain: the step is done when one approver (mode any) or every approver slot
// (mode all) has approved. An approver is a role (admin = admins and owners, owner = owners), a named member, or the
// requester's department head (resolved into a user when the request is opened).
type ApprovalStep struct {
	Mode           string             `json:"mode"`
	MinAmountMinor int64              `json:"min_amount_minor"`
	Approvers      []ApprovalApprover `json:"approvers"`
}

type ApprovalApprover struct {
	Kind   string `json:"kind"`
	Role   string `json:"role,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

// Needed is how many approver slots must approve before the chain moves on.
func (s ApprovalStep) Needed() int {
	if s.Mode == "all" {
		return len(s.Approvers)
	}
	return 1
}

// FirstUser is the named person of the step, shown as "the approver"; nil when it is a role.
func (s ApprovalStep) FirstUser() *string {
	for _, a := range s.Approvers {
		if a.Kind == "user" {
			return &a.UserID
		}
	}
	return nil
}

// Notify tells whoever can decide this step, except actor.
func (s ApprovalStep) Notify(c *Ctx, noteID *string, actor string) error {
	for _, a := range s.Approvers {
		var u, r *string
		if a.Kind == "user" {
			u = &a.UserID
		} else {
			r = &a.Role
		}
		if err := NotifyApprovers(c, u, r, "approval_requested", noteID, actor); err != nil {
			return err
		}
	}
	return nil
}

// snapshotSteps keeps the steps whose threshold the amount reaches and resolves who each approver is now, so a later
// rule edit or department change never rewrites a request in flight. A department head who is missing, or is the
// requester (nobody approves their own request), falls back to the admins.
func snapshotSteps(c *Ctx, rule []ApprovalStep, amount int64, requester string) ([]ApprovalStep, error) {
	var head *string
	if err := c.Tx.QueryRow(c.Context,
		`select d.head_user_id::text from core.memberships m join core.departments d on d.id = m.department_id
		  where m.user_id = $1::uuid`, requester).Scan(&head); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, DB(err)
	}
	out := []ApprovalStep{}
	for _, st := range rule {
		if amount < st.MinAmountMinor {
			continue
		}
		step := ApprovalStep{Mode: st.Mode, MinAmountMinor: st.MinAmountMinor}
		for _, a := range st.Approvers {
			if a.Kind == "department_head" {
				a = ApprovalApprover{Kind: "user"}
				if head != nil {
					a.UserID = *head
				}
			}
			if a.Kind == "user" && (a.UserID == "" || a.UserID == requester) {
				a = ApprovalApprover{Kind: "role", Role: "admin"}
			}
			step.Approvers = append(step.Approvers, a)
		}
		out = append(out, step)
	}
	return out, nil
}

// NotifyApprovers notifies the named approver, or every owner/admin (owners only for role 'owner'), except actor.
// An owner is not excluded: their own agent opens requests in their name and the owner may decide them.
func NotifyApprovers(c *Ctx, approver, role *string, kind string, noteID *string, actor string) error {
	rows, err := c.Tx.Query(c.Context,
		`select m.user_id::text, coalesce((select title from core.notes where id = $2::uuid), '')
		   from core.memberships m
		  where m.workspace_id = $1::uuid and (m.user_id <> $3::uuid or m.role = 'owner')
		    and case when $4::uuid is not null then m.user_id = $4::uuid
		             when $5::text = 'owner' then m.role = 'owner' else m.role in ('owner','admin') end`,
		c.Principal.WorkspaceID, noteID, actor, approver, role)
	if err != nil {
		return DB(err)
	}
	var ns []notify.Notification
	for rows.Next() {
		n := notify.Notification{WorkspaceID: c.Principal.WorkspaceID, Kind: kind, NoteID: noteID, ActorUserID: &actor}
		if err := rows.Scan(&n.UserID, &n.Title); err != nil {
			rows.Close()
			return DB(err)
		}
		ns = append(ns, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return DB(err)
	}
	_, err = notify.Send(c.Context, c.Tx, ns...)
	return DB(err)
}

// ApprovedCtx is the context an approved request replays in: the requester as a person, or, when a machine made the
// request (agent set, or kind ApiKey for a key without an agent), that machine's principal rebuilt from the database now
// (an agent paused or removed, or a member deactivated, refuses). name must still be allowed to that principal.
func (c *Ctx) ApprovedCtx(requester, name string, agent *string, kind string) (*Ctx, error) {
	var role string
	if err := c.Tx.QueryRow(c.Context, `select role from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid and deactivated_at is null`,
		c.Principal.WorkspaceID, requester).Scan(&role); err != nil {
		return nil, ErrConflict("the requester is no longer a member")
	}
	sub := &Ctx{Context: c.Context, Tx: c.Tx, Pool: c.Pool, reg: c.reg,
		Principal: Principal{Kind: KindUser, UserID: &requester, WorkspaceID: c.Principal.WorkspaceID, Role: ParseRole(role)}}
	switch {
	case kind == string(KindAgent) && agent == nil:
		return nil, ErrForbidden("the AI employee that made this request was removed")
	case agent != nil:
		p, err := AgentPrincipal(sub, *agent, requester)
		if err != nil {
			return nil, err
		}
		sub.Principal = p
	case kind == string(KindAPIKey): // shortcut: a key's own scopes are not stored, so it replays with its owner's role capped at admin
		sub.Principal.Kind, sub.Principal.Role = KindAPIKey, min(sub.Principal.Role, Admin)
	default:
		return sub, nil
	}
	return sub, sub.Allowed(name)
}

// RunApproved executes a stored command as whoever asked for it (see ApprovedCtx), inside the caller's
// transaction. The approval gate is skipped (that is the decision being applied); every other gate runs.
func (c *Ctx) RunApproved(requester, name string, input json.RawMessage, agent *string, kind string) ([]byte, error) {
	sub, err := c.ApprovedCtx(requester, name, agent, kind)
	if err != nil {
		return nil, err
	}
	return c.reg.run(sub, name, input, ExecOptions{approved: true})
}
