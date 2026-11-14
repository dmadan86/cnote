package command

import (
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// AmountFunc tells the approval gate which document a command acts on and the amount (paise) at stake.
// It must run the same access check as the handler, so a caller never opens a request on a page they cannot touch.
type AmountFunc func(c *Ctx, input json.RawMessage) (noteID string, amount int64, err error)

// approvalGate runs before the handler of a command that has an Amount. A human who is not the owner and acts
// at or above the workspace rule's threshold does not execute: a pending approval is stored (kind 'approval') and
// the caller gets approval_required. approval.decide later replays the stored input as the requester (RunApproved).
func (r *Registry) approvalGate(c *Ctx, cmd Command, input json.RawMessage) ([]byte, bool, error) {
	p := c.Principal
	if p.Kind != KindUser || p.UserID == nil || p.Role >= Owner {
		return nil, false, nil
	}
	var min int64
	var approver, fallback, role *string
	var hours *int
	err := c.Tx.QueryRow(c.Context,
		`select min_amount_minor, approver_user_id::text, approver_role, fallback_user_id::text, escalate_after_hours
		   from core.approval_rules where command = $1 and enabled`, cmd.Name).Scan(&min, &approver, &role, &fallback, &hours)
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
	var id string
	err = c.Tx.QueryRow(c.Context, `select id::text from core.confirmations where kind = 'approval' and status = 'pending'
		and command = $1 and note_id = $2::uuid and requested_by = $3 and input = $4::jsonb`, cmd.Name, noteID, *p.UserID, input).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = c.Tx.QueryRow(c.Context,
			`insert into core.confirmations (workspace_id, command, input, requested_by, kind, note_id, amount_minor,
			   approver_user_id, fallback_user_id, escalate_at)
			 values ($1::uuid,$2,$3,$4,'approval',$5::uuid,$6,$7::uuid,$8::uuid,
			   case when $9::int is null then null else now() + $9::int * interval '1 hour' end)
			 returning id::text`,
			p.WorkspaceID, cmd.Name, input, *p.UserID, noteID, amount, approver, fallback, hours).Scan(&id)
		if err != nil {
			return nil, false, DB(err)
		}
		if _, err := c.Emit("approval.requested", &noteID, map[string]any{"approval_id": id, "command": cmd.Name, "amount_minor": amount}); err != nil {
			return nil, false, err
		}
		if err := NotifyApprovers(c, approver, role, "approval_requested", &noteID, *p.UserID); err != nil {
			return nil, false, err
		}
	} else if err != nil {
		return nil, false, DB(err)
	}
	out, err := Marshal(map[string]any{"approval_required": true, "confirmation_id": id, "amount_minor": amount,
		"message": cmd.Name + " needs approval; you will be notified when it is decided."})
	return out, true, err
}

// NotifyApprovers notifies the named approver, or every owner/admin (owners only for role 'owner'), except actor.
func NotifyApprovers(c *Ctx, approver, role *string, kind string, noteID *string, actor string) error {
	_, err := c.Tx.Exec(c.Context,
		`insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title)
		 select $1::uuid, m.user_id, $2, $3::uuid, $4::uuid, coalesce((select title from core.notes where id = $3::uuid), '')
		   from core.memberships m
		  where m.workspace_id = $1::uuid and m.user_id <> $4::uuid
		    and case when $5::uuid is not null then m.user_id = $5::uuid
		             when $6::text = 'owner' then m.role = 'owner' else m.role in ('owner','admin') end`,
		c.Principal.WorkspaceID, kind, noteID, actor, approver, role)
	return DB(err)
}

// RunApproved executes a stored command as the person who asked for it, with their current role, inside the
// caller's transaction. The approval gate is skipped (that is the decision being applied); every other gate runs.
func (c *Ctx) RunApproved(requester, name string, input json.RawMessage) ([]byte, error) {
	var role string
	if err := c.Tx.QueryRow(c.Context, `select role from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid`,
		c.Principal.WorkspaceID, requester).Scan(&role); err != nil {
		return nil, ErrConflict("the requester is no longer a member")
	}
	sub := &Ctx{Context: c.Context, Tx: c.Tx, reg: c.reg,
		Principal: Principal{Kind: KindUser, UserID: &requester, WorkspaceID: c.Principal.WorkspaceID, Role: ParseRole(role)}}
	return c.reg.run(sub, name, input, ExecOptions{approved: true})
}
