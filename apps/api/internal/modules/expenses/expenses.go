// Package expenses: employee expense claims and direct (company-paid) expenses, plus the profit and loss view.
// Go-only. An expense is a typed note ['expense'] plus a billing.expenses row, so the receipt photo (an attachment),
// comments and history hang on the note.
//
// Claim flow: draft -> submitted -> approved -> reimbursed, or submitted -> rejected (the claimant may fix and resubmit).
// Postings (no input tax credit is taken on claims; gst_minor is informational):
//
//	approve      Dr expense account / Cr 2150 employee reimbursements
//	reimburse    Dr 2150 / Cr bank
//	direct       Dr expense account / Cr bank (posted when recorded)
//
// A workspace approval rule on expense.approve or expense.reimburse can hold large amounts for a named person.
package expenses

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/cnote/api/internal/modules/notes"
)

const acctReimbursements = "2150"

// categories map to expense accounts, seeded per workspace on first use. 5100 is bad debt, so these start at 5200.
var categories = []struct{ Key, Label, Code string }{
	{"travel", "Travel and transport", "5200"},
	{"meals", "Meals and entertainment", "5210"},
	{"office", "Office supplies", "5220"},
	{"software", "Software and subscriptions", "5230"},
	{"communication", "Phone and internet", "5240"},
	{"repairs", "Repairs and maintenance", "5250"},
	{"other", "Other expenses", "5290"},
}

func Commands() []command.Command {
	wn, mi, rd := command.GroupWriteNotes, command.GroupManageInvoices, command.GroupRead
	confirm := func(c command.Command) command.Command { c.Confirm = true; return c }
	admin := func(c command.Command) command.Command { c.MinRole = command.Admin; return c }
	member := func(c command.Command) command.Command { c.MinRole = command.Member; return c }
	nc := command.NewCommand
	return []command.Command{
		nc("expense.create", wn, create),
		nc("expense.update", wn, update),
		nc("expense.submit", wn, submit),
		admin(nc("expense.approve", mi, approve)),
		admin(nc("expense.reject", mi, reject)),
		admin(confirm(nc("expense.reimburse", mi, reimburse))),
		confirm(nc("expense.record_direct", mi, recordDirect)),
		member(nc("expense.list", rd, list)),
		member(nc("expense.get", rd, get)),
		member(nc("expense.categories", rd, categoryList)),
		member(nc("report.profit_loss", rd, profitLoss)),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type in struct {
	ID          string  `json:"id"`
	SpaceID     string  `json:"space_id"`
	SpentOn     *string `json:"spent_on"`
	Category    *string `json:"category"`
	AccountCode *string `json:"account_code"`
	AmountMinor *int64  `json:"amount_minor"`
	GSTMinor    *int64  `json:"gst_minor"`
	VendorName  *string `json:"vendor_name"`
	Description *string `json:"description"`
	Method      *string `json:"method"`
	Reference   *string `json:"reference"`
	Reason      *string `json:"reason"`
	Status      *string `json:"status"`
	Kind        *string `json:"kind"`
	From        *string `json:"from"`
	To          *string `json:"to"`
	Limit       *int64  `json:"limit"`
}

func decode(raw json.RawMessage) (in, error) {
	var v in
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, command.ErrInvalid("invalid input")
	}
	for _, id := range []string{v.ID, v.SpaceID} {
		if id != "" && !uuidRe.MatchString(id) {
			return v, command.ErrInvalid("invalid id")
		}
	}
	for _, d := range []*string{v.SpentOn, v.From, v.To} {
		if d != nil {
			if _, err := time.Parse("2006-01-02", *d); err != nil {
				return v, command.ErrInvalid("dates must be YYYY-MM-DD")
			}
		}
	}
	for _, f := range []struct {
		p   *string
		max int
		n   string
	}{{v.VendorName, 200, "vendor_name"}, {v.Method, 40, "method"}, {v.Reference, 200, "reference"}, {v.Reason, 500, "reason"}, {v.Description, 2000, "description"}} {
		if f.p != nil && len(*f.p) > f.max {
			return v, command.ErrInvalid(f.n + " is too long")
		}
	}
	v.ID, v.SpaceID = strings.ToLower(v.ID), strings.ToLower(v.SpaceID)
	return v, nil
}

func nilStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

// ---------- rows ----------

type row struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Kind           string      `json:"kind"`
	Status         string      `json:"status"`
	SpentOn        string      `json:"spent_on"`
	Category       string      `json:"category"`
	CategoryLabel  string      `json:"category_label"`
	AccountCode    string      `json:"account_code"`
	AmountMinor    int64       `json:"amount_minor"`
	GSTMinor       int64       `json:"gst_minor"`
	VendorName     *string     `json:"vendor_name"`
	Description    string      `json:"description"`
	PaidByUserID   *string     `json:"paid_by_user_id"`
	PaidByName     *string     `json:"paid_by_name"`
	Method         string      `json:"method"`
	Reference      *string     `json:"reference"`
	SubmittedAt    *command.Ts `json:"submitted_at"`
	DecidedBy      *string     `json:"decided_by"`
	DecidedAt      *command.Ts `json:"decided_at"`
	DecisionReason *string     `json:"decision_reason"`
	ReimbursedAt   *command.Ts `json:"reimbursed_at"`
	SpaceID        string      `json:"space_id"`
	CreatedAt      command.Ts  `json:"created_at"`
}

const cols = `e.note_id::text, n.title, e.kind, e.status, e.spent_on::text, e.category, coalesce(a.name, e.category), e.account_code, e.amount_minor,
	e.gst_minor, e.vendor_name, n.content_md, e.paid_by_user_id::text, u.name, e.method, e.reference, e.submitted_at, e.decided_by::text,
	e.decided_at, e.decision_reason, e.reimbursed_at, n.space_id::text, e.created_at`

const from = ` from billing.expenses e join core.notes n on n.id = e.note_id left join core.users u on u.id = e.paid_by_user_id
	left join billing.accounts a on a.code = e.account_code `

func scan(r pgx.Row) (row, error) {
	var x row
	var sub, dec, reimb *time.Time
	var created time.Time
	err := r.Scan(&x.ID, &x.Title, &x.Kind, &x.Status, &x.SpentOn, &x.Category, &x.CategoryLabel, &x.AccountCode, &x.AmountMinor, &x.GSTMinor,
		&x.VendorName, &x.Description, &x.PaidByUserID, &x.PaidByName, &x.Method, &x.Reference, &sub, &x.DecidedBy, &dec, &x.DecisionReason,
		&reimb, &x.SpaceID, &created)
	x.SubmittedAt, x.DecidedAt, x.ReimbursedAt, x.CreatedAt = command.TsPtr(sub), command.TsPtr(dec), command.TsPtr(reimb), command.Ts(created)
	return x, err
}

// load reads one expense the caller may see: an admin sees all; a member sees their own claims and, with finance
// access, the company's direct expenses. Anything else is "not found".
func load(c *command.Ctx, id string, lock bool) (row, error) {
	q := "select " + cols + from + "where e.note_id = $1::uuid and n.deleted_at is null "
	if lock {
		q += "for update of e"
	}
	r, err := scan(c.Tx.QueryRow(c.Context, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, command.ErrNotFound("expense")
	}
	if err != nil {
		return r, command.DB(err)
	}
	ok, err := visible(c, r)
	if err != nil {
		return r, err
	}
	if !ok {
		return row{}, command.ErrNotFound("expense")
	}
	return r, nil
}

func me(c *command.Ctx) (string, error) {
	if c.Principal.UserID == nil {
		return "", command.ErrForbidden("expenses belong to a person")
	}
	return *c.Principal.UserID, nil
}

func visible(c *command.Ctx, r row) (bool, error) {
	if c.Principal.Role >= command.Admin {
		return true, nil
	}
	if r.PaidByUserID != nil && c.Principal.UserID != nil && *r.PaidByUserID == *c.Principal.UserID {
		return true, nil
	}
	if r.Kind == "direct" {
		return c.FinanceAllowed()
	}
	return false, nil
}

func categoryOf(key string) (label, code string, ok bool) {
	for _, k := range categories {
		if k.Key == key {
			return k.Label, k.Code, true
		}
	}
	return "", "", false
}

func categoryList(c *command.Ctx, input json.RawMessage) (any, error) {
	out := []map[string]string{}
	for _, k := range categories {
		out = append(out, map[string]string{"key": k.Key, "label": k.Label, "account_code": k.Code})
	}
	return out, nil
}

func ensureAccount(c *command.Ctx, code, name, kind string) error {
	_, err := c.Tx.Exec(c.Context,
		"insert into billing.accounts (workspace_id, code, name, kind) values ($1::uuid,$2,$3,$4) on conflict do nothing", c.WorkspaceID(), code, name, kind)
	return command.DB(err)
}

// resolveAccount: the category's account (seeded on first use), or an existing expense account the caller names.
func resolveAccount(c *command.Ctx, category string, override *string) (string, error) {
	label, code, ok := categoryOf(category)
	if !ok {
		return "", command.ErrInvalid("unknown category " + category)
	}
	if override != nil && strings.TrimSpace(*override) != "" {
		var found bool
		if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from billing.accounts where code = $1 and kind = 'expense')", strings.TrimSpace(*override)).Scan(&found); err != nil {
			return "", command.DB(err)
		}
		if !found {
			return "", command.ErrInvalid(fmt.Sprintf("account %s is not an expense account", *override))
		}
		return strings.TrimSpace(*override), nil
	}
	if err := ensureAccount(c, code, label, "expense"); err != nil {
		return "", err
	}
	var kind string
	if err := c.Tx.QueryRow(c.Context, "select kind from billing.accounts where code = $1", code).Scan(&kind); err != nil {
		return "", command.DB(err)
	}
	if kind != "expense" {
		return "", command.ErrConflict(fmt.Sprintf("account %s already exists as a %s account", code, kind))
	}
	return code, nil
}

func titleOf(label string, amount int64, vendor *string) string {
	t := fmt.Sprintf("Expense: %s Rs %d", label, amount/100)
	if amount%100 != 0 {
		t = fmt.Sprintf("Expense: %s Rs %d.%02d", label, amount/100, amount%100)
	}
	if vendor != nil && strings.TrimSpace(*vendor) != "" {
		t += " at " + strings.TrimSpace(*vendor)
	}
	return t
}

func defaultSpace(c *command.Ctx) (string, error) {
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return "", err
	}
	rows, err := c.Tx.Query(c.Context, "select id::text from core.spaces where id = any($1::text[]::uuid[]) order by created_at, id", spaces)
	if err != nil {
		return "", command.DB(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", command.DB(err)
	}
	for _, id := range ids {
		if access.AssertSpaceAccess(c, id, true) == nil {
			return id, nil
		}
	}
	return "", command.ErrForbidden("you have no space to write in; ask an admin to add you to one")
}

func checkFields(amount, gst int64, spent time.Time) error {
	if amount <= 0 {
		return command.ErrInvalid("amount must be positive")
	}
	if gst < 0 || gst > amount {
		return command.ErrInvalid("gst_minor must be between 0 and the amount")
	}
	if spent.After(billing.TodayIST()) {
		return command.ErrInvalid("spent_on cannot be in the future")
	}
	return nil
}

// ---------- create and edit ----------

type draftFields struct {
	space, category, account string
	spent                    time.Time
	amount, gst              int64
	vendor, desc             *string
	method                   string
	reference                *string
}

func parseDraft(c *command.Ctx, v in) (draftFields, error) {
	d := draftFields{}
	if v.SpentOn == nil || v.Category == nil || v.AmountMinor == nil {
		return d, command.ErrInvalid("spent_on, category and amount_minor are required")
	}
	d.spent, _ = time.Parse("2006-01-02", *v.SpentOn)
	d.amount, d.category = *v.AmountMinor, strings.TrimSpace(*v.Category)
	if v.GSTMinor != nil {
		d.gst = *v.GSTMinor
	}
	if err := checkFields(d.amount, d.gst, d.spent); err != nil {
		return d, err
	}
	var err error
	if d.account, err = resolveAccount(c, d.category, v.AccountCode); err != nil {
		return d, err
	}
	d.vendor, d.desc, d.reference = trimmed(v.VendorName), v.Description, trimmed(v.Reference)
	d.method = "bank"
	if v.Method != nil && strings.TrimSpace(*v.Method) != "" {
		d.method = strings.TrimSpace(*v.Method)
	}
	d.space = v.SpaceID
	return d, nil
}

func insertExpense(c *command.Ctx, kind, status string, d draftFields, claimant *string) (string, error) {
	if d.space == "" {
		var err error
		if d.space, err = defaultSpace(c); err != nil {
			return "", err
		}
	}
	label, _, _ := categoryOf(d.category)
	note, err := notes.InsertNote(c, d.space, titleOf(label, d.amount, d.vendor), nil, d.desc, []string{"expense"})
	if err != nil {
		return "", err
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.expenses (note_id, workspace_id, kind, status, spent_on, category, account_code, amount_minor, gst_minor,
		   vendor_name, paid_by_user_id, method, reference)
		 values ($1::uuid,$2::uuid,$3,$4,$5::date,$6,$7,$8,$9,$10,$11::uuid,$12,$13)`,
		note.ID, c.WorkspaceID(), kind, status, d.spent.Format("2006-01-02"), d.category, d.account, d.amount, d.gst,
		nilStr(d.vendor), claimant, d.method, nilStr(d.reference)); err != nil {
		return "", command.DB(err)
	}
	if claimant != nil { // a claim is private to the claimant and the admins who decide it
		if _, err := c.Tx.Exec(c.Context, "update core.notes set restriction = 'view' where id = $1::uuid", note.ID); err != nil {
			return "", command.DB(err)
		}
		if _, err := c.Tx.Exec(c.Context,
			"insert into core.note_permissions (note_id, workspace_id, user_id, level, granted_by) values ($1::uuid,$2::uuid,$3::uuid,'edit',$3::uuid)",
			note.ID, c.WorkspaceID(), *claimant); err != nil {
			return "", command.DB(err)
		}
	}
	return note.ID, nil
}

func create(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	uid, err := me(c)
	if err != nil {
		return nil, err
	}
	d, err := parseDraft(c, v)
	if err != nil {
		return nil, err
	}
	id, err := insertExpense(c, "claim", "draft", d, &uid)
	if err != nil {
		return nil, err
	}
	if _, err := c.Emit("expense.created", &id, map[string]any{"amount_minor": d.amount, "category": d.category}); err != nil {
		return nil, err
	}
	return load(c, id, false)
}

// update edits a draft or rejected claim of the caller; a rejected claim goes back to draft for resubmission.
func update(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, command.ErrInvalid("id is required")
	}
	uid, err := me(c)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, v.ID, true); err != nil {
		return nil, err
	}
	r, err := load(c, v.ID, true)
	if err != nil {
		return nil, err
	}
	if r.Kind != "claim" || r.PaidByUserID == nil || *r.PaidByUserID != uid {
		return nil, command.ErrForbidden("only the claimant can edit a claim")
	}
	if r.Status != "draft" && r.Status != "rejected" {
		return nil, command.ErrConflict(fmt.Sprintf("claim is %s and cannot be edited", r.Status))
	}
	// merge over the stored values, then validate like a create
	merged := in{SpentOn: &r.SpentOn, Category: &r.Category, AmountMinor: &r.AmountMinor, GSTMinor: &r.GSTMinor, VendorName: r.VendorName,
		Description: &r.Description, Method: &r.Method, Reference: r.Reference}
	if v.SpentOn != nil {
		merged.SpentOn = v.SpentOn
	}
	if v.Category != nil {
		merged.Category = v.Category
	}
	if v.AmountMinor != nil {
		merged.AmountMinor = v.AmountMinor
	}
	if v.GSTMinor != nil {
		merged.GSTMinor = v.GSTMinor
	}
	if v.VendorName != nil {
		merged.VendorName = v.VendorName
	}
	if v.Description != nil {
		merged.Description = v.Description
	}
	if v.Method != nil {
		merged.Method = v.Method
	}
	if v.Reference != nil {
		merged.Reference = v.Reference
	}
	merged.AccountCode = v.AccountCode
	if v.AccountCode == nil && v.Category == nil { // keep a custom account unless the category changes
		merged.AccountCode = &r.AccountCode
	}
	d, err := parseDraft(c, merged)
	if err != nil {
		return nil, err
	}
	label, _, _ := categoryOf(d.category)
	if _, err := c.Tx.Exec(c.Context,
		`update billing.expenses set status = 'draft', spent_on = $2::date, category = $3, account_code = $4, amount_minor = $5, gst_minor = $6,
		   vendor_name = $7, method = $8, reference = $9, decided_by = null, decided_at = null, decision_reason = null where note_id = $1::uuid`,
		r.ID, d.spent.Format("2006-01-02"), d.category, d.account, d.amount, d.gst, nilStr(d.vendor), d.method, nilStr(d.reference)); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "update core.notes set title = $2, content_md = coalesce($3, content_md), updated_at = now(), version = version + 1 where id = $1::uuid",
		r.ID, titleOf(label, d.amount, d.vendor), v.Description); err != nil {
		return nil, command.DB(err)
	}
	return load(c, r.ID, false)
}

// ---------- decisions ----------

func lock(c *command.Ctx, input json.RawMessage, verb string, from ...string) (row, in, error) {
	v, err := decode(input)
	if err != nil {
		return row{}, v, err
	}
	if v.ID == "" {
		return row{}, v, command.ErrInvalid("id is required")
	}
	if _, err := access.AssertNoteAccess(c, v.ID, true); err != nil {
		return row{}, v, err
	}
	r, err := load(c, v.ID, true)
	if err != nil {
		return r, v, err
	}
	for _, s := range from {
		if r.Status == s {
			return r, v, nil
		}
	}
	return r, v, command.ErrConflict(fmt.Sprintf("expense is %s and cannot be %s", r.Status, verb))
}

func notify(c *command.Ctx, user *string, kind, id, title string) error {
	if user == nil || c.Principal.UserID == nil || *user == *c.Principal.UserID {
		return nil
	}
	_, err := c.Tx.Exec(c.Context,
		`insert into core.notifications (workspace_id, user_id, kind, note_id, actor_user_id, title) values ($1::uuid,$2::uuid,$3,$4::uuid,$5::uuid,$6)`,
		c.WorkspaceID(), *user, kind, id, *c.Principal.UserID, title)
	return command.DB(err)
}

func submit(c *command.Ctx, input json.RawMessage) (any, error) {
	r, _, err := lock(c, input, "submitted", "draft")
	if err != nil {
		return nil, err
	}
	uid, err := me(c)
	if err != nil {
		return nil, err
	}
	if r.Kind != "claim" || r.PaidByUserID == nil || *r.PaidByUserID != uid {
		return nil, command.ErrForbidden("only the claimant can submit a claim")
	}
	if _, err := c.Tx.Exec(c.Context, "update billing.expenses set status = 'submitted', submitted_at = now() where note_id = $1::uuid", r.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("expense.submitted", &r.ID, map[string]any{"amount_minor": r.AmountMinor, "category": r.Category}); err != nil {
		return nil, err
	}
	if err := command.NotifyApprovers(c, nil, nil, "approval_requested", &r.ID, uid); err != nil {
		return nil, err
	}
	return load(c, r.ID, false)
}

func approve(c *command.Ctx, input json.RawMessage) (any, error) {
	r, _, err := lock(c, input, "approved", "submitted")
	if err != nil {
		return nil, err
	}
	if c.Principal.UserID != nil && r.PaidByUserID != nil && *r.PaidByUserID == *c.Principal.UserID && c.Principal.Role < command.Owner {
		return nil, command.ErrForbidden("you cannot approve your own claim")
	}
	if err := ensureAccount(c, acctReimbursements, "Employee reimbursements", "liability"); err != nil {
		return nil, err
	}
	if err := billing.Post(c, "expense", r.ID, r.Title,
		[]billing.Leg{billing.L(r.AccountCode, r.AmountMinor, 0), billing.L(acctReimbursements, 0, r.AmountMinor)}); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update billing.expenses set status = 'approved', decided_by = $2::uuid, decided_at = now() where note_id = $1::uuid", r.ID, c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("expense.approved", &r.ID, map[string]any{"amount_minor": r.AmountMinor, "category": r.Category}); err != nil {
		return nil, err
	}
	if err := notify(c, r.PaidByUserID, "approval_decided", r.ID, "Approved: "+r.Title); err != nil {
		return nil, err
	}
	return load(c, r.ID, false)
}

func reject(c *command.Ctx, input json.RawMessage) (any, error) {
	r, v, err := lock(c, input, "rejected", "submitted")
	if err != nil {
		return nil, err
	}
	if v.Reason == nil || strings.TrimSpace(*v.Reason) == "" {
		return nil, command.ErrInvalid("a reason is required to reject")
	}
	if c.Principal.UserID != nil && r.PaidByUserID != nil && *r.PaidByUserID == *c.Principal.UserID && c.Principal.Role < command.Owner {
		return nil, command.ErrForbidden("you cannot decide your own claim")
	}
	why := strings.TrimSpace(*v.Reason)
	if _, err := c.Tx.Exec(c.Context,
		"update billing.expenses set status = 'rejected', decided_by = $2::uuid, decided_at = now(), decision_reason = $3 where note_id = $1::uuid",
		r.ID, c.Principal.UserID, why); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("expense.rejected", &r.ID, map[string]any{"amount_minor": r.AmountMinor, "reason": why}); err != nil {
		return nil, err
	}
	if err := notify(c, r.PaidByUserID, "approval_decided", r.ID, "Rejected: "+r.Title); err != nil {
		return nil, err
	}
	return load(c, r.ID, false)
}

func reimburse(c *command.Ctx, input json.RawMessage) (any, error) {
	r, v, err := lock(c, input, "reimbursed", "approved")
	if err != nil {
		return nil, err
	}
	method := "bank"
	if v.Method != nil && strings.TrimSpace(*v.Method) != "" {
		method = strings.TrimSpace(*v.Method)
	}
	if err := billing.Post(c, "expense_reimbursement", r.ID, "Reimbursed: "+r.Title,
		[]billing.Leg{billing.L(acctReimbursements, r.AmountMinor, 0), billing.L(billing.AcctBank, 0, r.AmountMinor)}); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		"update billing.expenses set status = 'reimbursed', reimbursed_at = now(), reimbursed_method = $2, reimbursed_reference = $3 where note_id = $1::uuid",
		r.ID, method, nilStr(trimmed(v.Reference))); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("expense.reimbursed", &r.ID, map[string]any{"amount_minor": r.AmountMinor, "method": method}); err != nil {
		return nil, err
	}
	if err := notify(c, r.PaidByUserID, "approval_decided", r.ID, "Reimbursed: "+r.Title); err != nil {
		return nil, err
	}
	return load(c, r.ID, false)
}

// recordDirect books a company-paid expense with no vendor bill: it posts at once.
func recordDirect(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	d, err := parseDraft(c, v)
	if err != nil {
		return nil, err
	}
	id, err := insertExpense(c, "direct", "posted", d, nil)
	if err != nil {
		return nil, err
	}
	r, err := load(c, id, false)
	if err != nil {
		return nil, err
	}
	if err := billing.Post(c, "expense", id, r.Title,
		[]billing.Leg{billing.L(d.account, d.amount, 0), billing.L(billing.AcctBank, 0, d.amount)}); err != nil {
		return nil, err
	}
	if _, err := c.Emit("expense.recorded", &id, map[string]any{"amount_minor": d.amount, "category": d.category}); err != nil {
		return nil, err
	}
	return r, nil
}

// ---------- read ----------

func get(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, command.ErrInvalid("id is required")
	}
	return load(c, v.ID, false)
}

// list: filters status ('open' = submitted or approved), kind, from/to on spent_on. Members see their own claims (and,
// with finance access, direct expenses); admins see everything.
func list(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	lim := int64(100)
	if v.Limit != nil {
		lim = min(max(*v.Limit, 1), 500)
	}
	fin, err := c.FinanceAllowed()
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		"select "+cols+from+`where n.deleted_at is null
		   and ($1 or e.paid_by_user_id = $2::uuid or (e.kind = 'direct' and $3))
		   and ($4::text is null or e.status = $4 or ($4 = 'open' and e.status in ('submitted','approved')))
		   and ($5::text is null or e.kind = $5) and ($6::date is null or e.spent_on >= $6::date) and ($7::date is null or e.spent_on <= $7::date)
		 order by e.spent_on desc, e.created_at desc, e.note_id limit $8`,
		c.Principal.Role >= command.Admin, c.Principal.UserID, fin, nilStr(v.Status), nilStr(v.Kind), nilStr(v.From), nilStr(v.To), lim)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []row{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}

// ---------- profit and loss ----------

type line struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	AmountMinor int64  `json:"amount_minor"`
}

// profitLoss reads the journal: revenue accounts (credit minus debit) against expense accounts (debit minus credit)
// for entries posted in the range (India time). It is on the posting date, so a claim counts when it is approved.
func profitLoss(c *command.Ctx, input json.RawMessage) (any, error) {
	v, err := decode(input)
	if err != nil {
		return nil, err
	}
	if v.From == nil || v.To == nil {
		return nil, command.ErrInvalid("from and to are required")
	}
	rows, err := c.Tx.Query(c.Context,
		`select a.code, a.name, a.kind, coalesce(sum(l.debit_minor), 0)::bigint, coalesce(sum(l.credit_minor), 0)::bigint
		   from billing.accounts a
		   join billing.journal_lines l on l.account_code = a.code
		   join billing.journal_entries e on e.id = l.entry_id
		  where a.kind in ('revenue','expense') and (e.posted_at at time zone 'Asia/Kolkata')::date between $1::date and $2::date
		  group by a.code, a.name, a.kind order by a.code`, *v.From, *v.To)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	income, expense := []line{}, []line{}
	var ti, te int64
	for rows.Next() {
		var code, name, kind string
		var d, cr int64
		if err := rows.Scan(&code, &name, &kind, &d, &cr); err != nil {
			return nil, command.DB(err)
		}
		if kind == "revenue" {
			income, ti = append(income, line{code, name, cr - d}), ti+cr-d
		} else {
			expense, te = append(expense, line{code, name, d - cr}), te+d-cr
		}
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"from": *v.From, "to": *v.To, "income": income, "expenses": expense,
		"total_income_minor": ti, "total_expenses_minor": te, "net_profit_minor": ti - te}, nil
}
