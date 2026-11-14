// Package approvals: rules that hold a command for a named person's decision, the inbox to decide them, and the
// reminder sweep. A request is a core.confirmations row of kind 'approval' (see command/approval.go for the gate).
package approvals

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// supported lists the commands a rule can hold, with the table that stores the amount of the document in `id`.
// Drafts keep the pre-tax subtotal in total_minor, so thresholds compare against that until the document is issued.
var supported = []struct{ name, label string }{
	{"quote.send", "Send a quote"},
	{"invoice.issue", "Issue an invoice"},
	{"bill.record", "Record a bill"},
	{"bill.pay", "Pay a bill"},
	{"invoice.write_off", "Write off an invoice"},
}

// amountFor is the Amount of a supported command; check = run the handler's page access check first.
func amountFor(name string, check bool) command.AmountFunc {
	switch name {
	case "quote.send":
		return totalOf("billing.quotations", check)
	case "invoice.issue":
		return totalOf("billing.invoices", check)
	case "bill.record":
		return totalOf("billing.purchase_bills", check)
	case "bill.pay":
		return paymentOf("billing.purchase_bills", "total_minor - paid_minor", check)
	case "invoice.write_off":
		return paymentOf("billing.invoices", "total_minor - paid_minor - credited_minor - written_off_minor", check)
	}
	return nil
}

// Wire gives the supported commands their Amount, so the registry gate can hold them.
func Wire(cmds []command.Command) []command.Command {
	for i := range cmds {
		cmds[i].Amount = amountFor(cmds[i].Name, true)
	}
	return cmds
}

func idOf(input json.RawMessage, key string) (string, map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil || m == nil {
		return "", nil, command.ErrInvalid("invalid input")
	}
	var id string
	if json.Unmarshal(m[key], &id) != nil || !uuidRe.MatchString(id) {
		return "", nil, command.ErrInvalid(key + " must be a uuid")
	}
	return strings.ToLower(id), m, nil
}

// Table and expressions are constants in this file, never caller input.
func totalOf(table string, check bool) command.AmountFunc {
	return func(c *command.Ctx, input json.RawMessage) (string, int64, error) {
		id, _, err := idOf(input, "id")
		if err != nil {
			return "", 0, err
		}
		if check {
			if _, err := access.AssertNoteAccess(c, id, true); err != nil {
				return "", 0, err
			}
		}
		var amount int64
		if err := c.Tx.QueryRow(c.Context, "select total_minor from "+table+" where note_id = $1::uuid", id).Scan(&amount); err != nil {
			return "", 0, command.DB(err)
		}
		return id, amount, nil
	}
}

// paymentOf: the amount_minor in the input, or the outstanding expression when the input has none.
func paymentOf(table, outstanding string, check bool) command.AmountFunc {
	return func(c *command.Ctx, input json.RawMessage) (string, int64, error) {
		id, m, err := idOf(input, "id")
		if err != nil {
			return "", 0, err
		}
		if check {
			if _, err := access.AssertNoteAccess(c, id, true); err != nil {
				return "", 0, err
			}
		}
		var amount int64
		if raw, ok := m["amount_minor"]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, &amount) != nil || amount < 0 {
				return "", 0, command.ErrInvalid("amount_minor must be a positive integer")
			}
			return id, amount, nil
		}
		if err := c.Tx.QueryRow(c.Context, "select "+outstanding+" from "+table+" where note_id = $1::uuid", id).Scan(&amount); err != nil {
			return "", 0, command.DB(err)
		}
		return id, amount, nil
	}
}

func Commands() []command.Command {
	adm := func(name string, h command.Handler) command.Command {
		c := command.NewCommand(name, command.GroupAdmin, h)
		c.MinRole = command.Admin
		return c
	}
	decide := command.NewCommand("approval.decide", command.GroupAdmin, decide)
	cmds := []command.Command{
		adm("approval.rule_set", ruleSet),
		command.NewCommand("approval.rule_list", command.GroupRead, ruleList),
		command.NewCommand("approval.list", command.GroupRead, list),
		decide,
		command.NewCommand("approval.cancel", command.GroupWriteNotes, cancel),
	}
	cmds[1].MinRole, cmds[2].MinRole = command.Member, command.Member
	return cmds
}

// human: approvals are decisions of people; an agent or API key never sets rules or decides.
func human(c *command.Ctx) (string, error) {
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return "", command.ErrForbidden("only a person can do this")
	}
	return *c.Principal.UserID, nil
}

func decodeObj(input json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil || m == nil {
		return nil, command.ErrInvalid("invalid input")
	}
	return m, nil
}

func optStr(m map[string]json.RawMessage, k string) (*string, error) {
	raw, ok := m[k]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return nil, command.ErrInvalid(k + " must be a string")
	}
	s = strings.TrimSpace(s)
	return &s, nil
}

func optUUID(m map[string]json.RawMessage, k string) (*string, error) {
	s, err := optStr(m, k)
	if err != nil || s == nil {
		return nil, err
	}
	if !uuidRe.MatchString(*s) {
		return nil, command.ErrInvalid(k + " must be a uuid")
	}
	l := strings.ToLower(*s)
	return &l, nil
}

func optInt(m map[string]json.RawMessage, k string) (*int64, error) {
	raw, ok := m[k]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var n int64
	if json.Unmarshal(raw, &n) != nil {
		return nil, command.ErrInvalid(k + " must be an integer")
	}
	return &n, nil
}

type rule struct {
	ID                 string  `json:"id"`
	Command            string  `json:"command"`
	MinAmountMinor     int64   `json:"min_amount_minor"`
	ApproverUserID     *string `json:"approver_user_id"`
	ApproverRole       *string `json:"approver_role"`
	FallbackUserID     *string `json:"fallback_user_id"`
	EscalateAfterHours *int    `json:"escalate_after_hours"`
	Enabled            bool    `json:"enabled"`
}

const ruleCols = `id::text, command, min_amount_minor, approver_user_id::text, approver_role, fallback_user_id::text, escalate_after_hours, enabled`

func scanRule(r pgx.Row) (rule, error) {
	var x rule
	err := r.Scan(&x.ID, &x.Command, &x.MinAmountMinor, &x.ApproverUserID, &x.ApproverRole, &x.FallbackUserID, &x.EscalateAfterHours, &x.Enabled)
	return x, err
}

func ruleSet(c *command.Ctx, input json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	m, err := decodeObj(input)
	if err != nil {
		return nil, err
	}
	name, err := optStr(m, "command")
	if err != nil {
		return nil, err
	}
	ok := false
	for _, s := range supported {
		ok = ok || (name != nil && s.name == *name)
	}
	if !ok {
		return nil, command.ErrInvalid("command does not support approvals")
	}
	min, err := optInt(m, "min_amount_minor")
	if err != nil {
		return nil, err
	}
	if min == nil {
		z := int64(0)
		min = &z
	}
	if *min < 0 {
		return nil, command.ErrInvalid("min_amount_minor must not be negative")
	}
	approver, err := optUUID(m, "approver_user_id")
	if err != nil {
		return nil, err
	}
	role, err := optStr(m, "approver_role")
	if err != nil {
		return nil, err
	}
	if role != nil && *role != "owner" && *role != "admin" {
		return nil, command.ErrInvalid("approver_role must be owner or admin")
	}
	if approver == nil && role == nil {
		a := "admin"
		role = &a
	}
	fallback, err := optUUID(m, "fallback_user_id")
	if err != nil {
		return nil, err
	}
	hours, err := optInt(m, "escalate_after_hours")
	if err != nil {
		return nil, err
	}
	if hours != nil && (*hours < 1 || *hours > 168) {
		return nil, command.ErrInvalid("escalate_after_hours must be between 1 and 168")
	}
	if hours != nil && fallback == nil {
		return nil, command.ErrInvalid("escalate_after_hours needs a fallback_user_id")
	}
	enabled := true
	if raw, has := m["enabled"]; has && json.Unmarshal(raw, &enabled) != nil {
		return nil, command.ErrInvalid("enabled must be a boolean")
	}
	for _, u := range []*string{approver, fallback} { // approvers must be members who can decide
		if u == nil {
			continue
		}
		var r string
		if err := c.Tx.QueryRow(c.Context, "select role from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid", c.WorkspaceID(), *u).Scan(&r); err != nil || command.ParseRole(r) < command.Member {
			return nil, command.ErrInvalid("approver and fallback must be members of this workspace")
		}
	}
	out, err := scanRule(c.Tx.QueryRow(c.Context,
		`insert into core.approval_rules (workspace_id, command, min_amount_minor, approver_user_id, approver_role, fallback_user_id, escalate_after_hours, enabled, created_by)
		 values ($1::uuid,$2,$3,$4::uuid,$5,$6::uuid,$7::int,$8,$9::uuid)
		 on conflict (workspace_id, command) do update set min_amount_minor = excluded.min_amount_minor, approver_user_id = excluded.approver_user_id,
		   approver_role = excluded.approver_role, fallback_user_id = excluded.fallback_user_id, escalate_after_hours = excluded.escalate_after_hours,
		   enabled = excluded.enabled returning `+ruleCols,
		c.WorkspaceID(), *name, *min, approver, role, fallback, hours, enabled, uid))
	if err != nil {
		return nil, command.DB(err)
	}
	_, err = c.Emit("approval.rule_set", nil, map[string]any{"command": *name, "min_amount_minor": *min, "enabled": enabled})
	return out, err
}

func ruleList(c *command.Ctx, input json.RawMessage) (any, error) {
	if _, err := decodeObj(input); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select "+ruleCols+" from core.approval_rules order by command")
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	rules := []rule{}
	for rows.Next() {
		x, err := scanRule(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		rules = append(rules, x)
	}
	cmds := []map[string]string{}
	for _, s := range supported {
		cmds = append(cmds, map[string]string{"command": s.name, "label": s.label})
	}
	return map[string]any{"rules": rules, "commands": cmds}, command.DB(rows.Err())
}

type request struct {
	ID            string      `json:"id"`
	Command       string      `json:"command"`
	Status        string      `json:"status"`
	AmountMinor   *int64      `json:"amount_minor"`
	NoteID        *string     `json:"note_id"`
	NoteTitle     *string     `json:"note_title"`
	RequesterID   string      `json:"requester_id"`
	RequesterName *string     `json:"requester_name"`
	ApproverID    *string     `json:"approver_user_id"`
	ApproverName  *string     `json:"approver_name"`
	Reason        *string     `json:"reason"`
	Escalated     bool        `json:"escalated"`
	CreatedAt     command.Ts  `json:"created_at"`
	DecidedAt     *command.Ts `json:"decided_at"`
	CanDecide     bool        `json:"can_decide"`
	CanCancel     bool        `json:"can_cancel"`
}

// canDecide: owner or admin always (never on their own request unless owner); the named approver too.
func canDecide(p command.Principal, requester string, approver *string) bool {
	uid := *p.UserID
	if requester == uid && p.Role < command.Owner {
		return false
	}
	return p.Role >= command.Admin || approver != nil && *approver == uid
}

func list(c *command.Ctx, input json.RawMessage) (any, error) {
	m, err := decodeObj(input)
	if err != nil {
		return nil, err
	}
	if c.Principal.UserID == nil {
		return nil, command.ErrForbidden("approvals belong to people")
	}
	status, err := optStr(m, "status")
	if err != nil {
		return nil, err
	}
	db := map[string]string{"approved": "confirmed"}
	if status != nil {
		if s, ok := db[*status]; ok {
			status = &s
		}
		if !strings.Contains(" pending confirmed rejected expired cancelled ", " "+*status+" ") {
			return nil, command.ErrInvalid("status must be pending, approved, rejected, expired or cancelled")
		}
	}
	mine := false
	if raw, has := m["mine"]; has && json.Unmarshal(raw, &mine) != nil {
		return nil, command.ErrInvalid("mine must be a boolean")
	}
	noteID, err := optUUID(m, "note_id")
	if err != nil {
		return nil, err
	}
	uid := *c.Principal.UserID
	rows, err := c.Tx.Query(c.Context,
		`select f.id::text, f.command, case f.status when 'confirmed' then 'approved' else f.status end, f.amount_minor, f.note_id::text, n.title,
		        f.requested_by, ru.name, f.approver_user_id::text, au.name, f.reason, f.escalated_at is not null, f.created_at, f.decided_at
		   from core.confirmations f
		   left join core.notes n on n.id = f.note_id
		   left join core.users ru on ru.id::text = f.requested_by
		   left join core.users au on au.id = f.approver_user_id
		  where f.kind = 'approval'
		    and ($1::text is null or f.status = $1::text) and ($2::uuid is null or f.note_id = $2::uuid)
		    and (($3 and f.requested_by = $4::text) or (not $3 and ($5 or f.requested_by = $4::text or f.approver_user_id::text = $4::text)))
		  order by (f.status = 'pending') desc, f.created_at desc limit 100`,
		status, noteID, mine, uid, c.Principal.Role >= command.Admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []request{}
	for rows.Next() {
		var r request
		var created time.Time
		var decided *time.Time
		if err := rows.Scan(&r.ID, &r.Command, &r.Status, &r.AmountMinor, &r.NoteID, &r.NoteTitle, &r.RequesterID, &r.RequesterName,
			&r.ApproverID, &r.ApproverName, &r.Reason, &r.Escalated, &created, &decided); err != nil {
			return nil, command.DB(err)
		}
		r.CreatedAt, r.DecidedAt = command.Ts(created), command.TsPtr(decided)
		r.CanDecide = r.Status == "pending" && c.Principal.Kind == command.KindUser && canDecide(c.Principal, r.RequesterID, r.ApproverID)
		r.CanCancel = r.Status == "pending" && r.RequesterID == uid
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}

type pending struct {
	id, cmd, requester string
	input              json.RawMessage
	note               *string
	amount             *int64
	approver           *string
}

func lockPending(c *command.Ctx, id string) (pending, error) {
	var p pending
	var status string
	err := c.Tx.QueryRow(c.Context,
		`select id::text, command, requested_by, input, note_id::text, amount_minor, approver_user_id::text, status
		   from core.confirmations where id = $1::uuid and kind = 'approval' for update`, id).Scan(
		&p.id, &p.cmd, &p.requester, &p.input, &p.note, &p.amount, &p.approver, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, command.ErrNotFound("approval")
	}
	if err != nil {
		return p, command.DB(err)
	}
	if status != "pending" {
		return p, command.ErrConflict("approval is already " + status)
	}
	return p, nil
}

// finish records the outcome, emits it on the document and tells the requester.
func finish(c *command.Ctx, p pending, status, reason, label string) error {
	if _, err := c.Tx.Exec(c.Context,
		"update core.confirmations set status = $2, confirmed_by = $3::uuid, decided_at = now(), reason = nullif($4,'') where id = $1::uuid",
		p.id, status, c.Principal.UserID, reason); err != nil {
		return command.DB(err)
	}
	if _, err := c.Emit("approval."+map[string]string{"confirmed": "decided", "rejected": "decided", "expired": "decided", "cancelled": "cancelled"}[status], p.note,
		map[string]any{"approval_id": p.id, "command": p.cmd, "status": status, "reason": reason}); err != nil {
		return err
	}
	if p.requester == *c.Principal.UserID {
		return nil
	}
	_, err := c.Tx.Exec(c.Context,
		`insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title)
		 values ($1::uuid, $2::uuid, 'approval_decided', $3::uuid, $4::uuid, $5 || ': ' || coalesce((select title from core.notes where id = $3::uuid), ''))`,
		c.WorkspaceID(), p.requester, p.note, c.Principal.UserID, label)
	return command.DB(err)
}

func decide(c *command.Ctx, input json.RawMessage) (any, error) {
	if _, err := human(c); err != nil {
		return nil, err
	}
	m, err := decodeObj(input)
	if err != nil {
		return nil, err
	}
	id, err := optUUID(m, "id")
	if err != nil || id == nil {
		return nil, command.ErrInvalid("id must be a uuid")
	}
	d, err := optStr(m, "decision")
	if err != nil || d == nil || (*d != "approve" && *d != "reject") {
		return nil, command.ErrInvalid("decision must be approve or reject")
	}
	reason, err := optStr(m, "reason")
	if err != nil {
		return nil, err
	}
	why := ""
	if reason != nil {
		why = *reason
	}
	if *d == "reject" && why == "" {
		return nil, command.ErrInvalid("a reason is required to reject")
	}
	p, err := lockPending(c, *id)
	if err != nil {
		return nil, err
	}
	if !canDecide(c.Principal, p.requester, p.approver) {
		return nil, command.ErrForbidden("you are not the approver of this request")
	}
	if *d == "reject" {
		return map[string]any{"status": "rejected"}, finish(c, p, "rejected", why, "Rejected")
	}
	// What was asked must still be what is approved: a draft edited after the request expires it.
	if f := amountFor(p.cmd, false); f != nil && p.amount != nil {
		if _, now, err := f(c, p.input); err != nil || now != *p.amount {
			if err := finish(c, p, "expired", "changed after the request; submit it again", "Expired"); err != nil {
				return nil, err
			}
			return map[string]any{"status": "expired", "stale": true}, nil
		}
	}
	if err := finish(c, p, "confirmed", why, "Approved"); err != nil {
		return nil, err
	}
	res, err := c.RunApproved(p.requester, p.cmd, p.input)
	if err != nil {
		return nil, err // rolls back: the request stays pending and the approver sees the reason
	}
	return map[string]any{"status": "approved", "result": json.RawMessage(res)}, nil
}

func cancel(c *command.Ctx, input json.RawMessage) (any, error) {
	uid, err := human(c)
	if err != nil {
		return nil, err
	}
	m, err := decodeObj(input)
	if err != nil {
		return nil, err
	}
	id, err := optUUID(m, "id")
	if err != nil || id == nil {
		return nil, command.ErrInvalid("id must be a uuid")
	}
	p, err := lockPending(c, *id)
	if err != nil {
		return nil, err
	}
	if p.requester != uid {
		return nil, command.ErrForbidden("only the requester can cancel")
	}
	return map[string]any{"status": "cancelled"}, finish(c, p, "cancelled", "", "Cancelled")
}
